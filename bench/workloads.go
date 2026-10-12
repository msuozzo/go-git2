package bench

import (
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/msuozzo/go-git2"
)

// OpenGoGit opens the repository the way oss-rebuild's gitx.NewInMemoryStorer
// configures storage, but over the OS filesystem.
func OpenGoGit(path string) (*git.Repository, error) {
	s := filesystem.NewStorageWithOptions(osfs.New(path), cache.NewObjectLRUDefault(),
		filesystem.Options{KeepDescriptors: true, ExclusiveAccess: true})
	return git.Open(s, nil)
}

// Options are the git2 open options the benchmarks use. Like go-git, they
// skip hash verification, as callers may for repositories git just cloned.
// Set GIT2_BENCH_VERIFY=1 to measure the verifying default instead.
func Options() *git2.OpenOptions {
	return &git2.OpenOptions{SkipHashVerification: os.Getenv("GIT2_BENCH_VERIFY") != "1"}
}

// Result summarizes a workload's output so implementations can be compared.
type Result struct {
	N   int
	Sum int64
	Out []string
}

// ---- go-git -------------------------------------------------------------

func GoGitHeadCommit(r *git.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	return Result{N: 1, Sum: c.Committer.When.Unix(), Out: []string{c.Hash.String()}}, nil
}

func GoGitLogHead(r *git.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	it, err := r.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = it.ForEach(func(c *object.Commit) error {
		res.N++
		res.Sum += c.Committer.When.Unix() + int64(len(c.Message)) + int64(len(c.Author.Name))
		return nil
	})
	return res, err
}

func GoGitTreeFiles(r *git.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	t, err := c.Tree()
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = t.Files().ForEach(func(f *object.File) error {
		res.N++
		res.Sum += int64(len(f.Name))
		return nil
	})
	return res, err
}

func GoGitFileContents(t *object.Tree, repo *Repo) (Result, error) {
	var res Result
	for _, p := range repo.Manifests {
		f, err := t.File(p)
		if err != nil {
			return res, err
		}
		s, err := f.Contents()
		if err != nil {
			return res, err
		}
		res.N++
		res.Sum += int64(len(s))
	}
	return res, nil
}

// GoGitFindTagMatch follows rebuild.FindTagMatch: scan every tag name,
// then peel the best match through its tag object.
func GoGitFindTagMatch(r *git.Repository, repo *Repo) (Result, error) {
	tags, err := r.Tags()
	if err != nil {
		return Result{}, err
	}
	var best string
	n := 0
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		n++
		name := ref.Name().Short()
		if strings.HasPrefix(name, repo.TagPrefix) && strings.HasSuffix(name, repo.TagVersion) {
			best = name
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	ref, err := r.Tag(best)
	if err != nil {
		return Result{}, err
	}
	h := ref.Hash()
	if t, err := r.TagObject(h); err == nil {
		h = t.Target
	}
	return Result{N: n, Out: []string{best, h.String()}}, nil
}

func GoGitLog(r *git.Repository, all bool, order git.LogOrder, filter func(string) bool) (Result, error) {
	o := &git.LogOptions{All: all, Order: order, PathFilter: filter}
	if !all {
		head, err := r.Head()
		if err != nil {
			return Result{}, err
		}
		o.From = head.Hash()
	}
	it, err := r.Log(o)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = it.ForEach(func(c *object.Commit) error {
		res.N++
		res.Out = append(res.Out, c.Hash.String())
		return nil
	})
	return res, err
}

func goGitVersionAt(c *object.Commit, repo *Repo) (string, error) {
	t, err := c.Tree()
	if err != nil {
		return "", err
	}
	f, err := t.File(repo.VersionFile)
	if err != nil {
		return "", err
	}
	s, err := f.Contents()
	if err != nil {
		return "", err
	}
	if m := repo.VersionRE.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	return "", nil
}

// GoGitVersionHistory follows npm/cratesio pkgJSONSearch: walk commits
// that touch a manifest, read the version at each and at its parents, and
// keep commits where the version changed.
func GoGitVersionHistory(r *git.Repository, repo *Repo, all bool) (Result, error) {
	o := &git.LogOptions{All: all, Order: git.LogOrderCommitterTime,
		PathFilter: func(p string) bool { return p == repo.VersionFile }}
	if !all {
		head, err := r.Head()
		if err != nil {
			return Result{}, err
		}
		o.From = head.Hash()
	}
	it, err := r.Log(o)
	if err != nil {
		return Result{}, err
	}
	versions := map[string]string{}
	err = it.ForEach(func(c *object.Commit) error {
		v, err := goGitVersionAt(c, repo)
		if err != nil || v == "" {
			return nil
		}
		changed := true
		c.Parents().ForEach(func(p *object.Commit) error {
			if pv, err := goGitVersionAt(p, repo); err == nil && pv == v {
				changed = false
			}
			return nil
		})
		if changed {
			if _, ok := versions[v]; !ok {
				versions[v] = c.Hash.String()
			}
		}
		return nil
	})
	return versionResult(versions), err
}

func versionResult(versions map[string]string) Result {
	var res Result
	for v, h := range versions {
		res.Out = append(res.Out, v+" "+h)
	}
	sort.Strings(res.Out)
	res.N = len(res.Out)
	return res
}

func GoGitGrep(r *git.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	rs, err := r.Grep(&git.GrepOptions{
		CommitHash: head.Hash(),
		PathSpecs:  []*regexp.Regexp{repo.GrepPath},
		Patterns:   []*regexp.Regexp{repo.GrepPattern},
	})
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, g := range rs {
		res.Out = append(res.Out, g.FileName)
	}
	res.N = len(rs)
	return res, nil
}

func GoGitEntryWalk(r *git.Repository, repo *Repo, limit int) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	it, err := r.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return Result{}, err
	}
	var res Result
	var last plumbing.Hash
	err = it.ForEach(func(c *object.Commit) error {
		if res.N == limit {
			return storer.ErrStop
		}
		res.N++
		t, err := c.Tree()
		if err != nil {
			return err
		}
		e, err := t.FindEntry(repo.VersionFile)
		if err != nil || e.Hash == last {
			return nil
		}
		last = e.Hash
		b, err := r.BlobObject(e.Hash)
		if err != nil {
			return err
		}
		rd, err := b.Reader()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rd)
		rd.Close()
		res.Sum += int64(len(data))
		return err
	})
	return res, err
}

// ---- git2 ------------------------------------------------------------

func Git2HeadCommit(r *git2.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	return Result{N: 1, Sum: c.Committer.When.Unix(), Out: []string{c.Hash.String()}}, nil
}

func Git2LogHead(r *git2.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	it, err := r.Log(&git2.LogOptions{From: head.Hash()})
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = it.ForEach(func(c *git2.Commit) error {
		res.N++
		res.Sum += c.Committer.When.Unix() + int64(len(c.Message)) + int64(len(c.Author.Name))
		return nil
	})
	return res, err
}

func Git2TreeFiles(r *git2.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	t, err := c.Tree()
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = t.Files().ForEach(func(f *git2.File) error {
		res.N++
		res.Sum += int64(len(f.Name))
		return nil
	})
	return res, err
}

func Git2FileContents(t *git2.Tree, repo *Repo) (Result, error) {
	var res Result
	for _, p := range repo.Manifests {
		f, err := t.File(p)
		if err != nil {
			return res, err
		}
		s, err := f.Contents()
		if err != nil {
			return res, err
		}
		res.N++
		res.Sum += int64(len(s))
	}
	return res, nil
}

func Git2FindTagMatch(r *git2.Repository, repo *Repo) (Result, error) {
	tags, err := r.Tags()
	if err != nil {
		return Result{}, err
	}
	var best string
	n := 0
	err = tags.ForEach(func(ref *git2.Reference) error {
		n++
		name := ref.Name().Short()
		if strings.HasPrefix(name, repo.TagPrefix) && strings.HasSuffix(name, repo.TagVersion) {
			best = name
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	ref, err := r.Tag(best)
	if err != nil {
		return Result{}, err
	}
	h := ref.Hash()
	if t, err := r.TagObject(h); err == nil {
		h = t.Target
	}
	return Result{N: n, Out: []string{best, h.String()}}, nil
}

func Git2Log(r *git2.Repository, all bool, order git2.LogOrder, filter func(string) bool, fileName *string) (Result, error) {
	o := &git2.LogOptions{All: all, Order: order, PathFilter: filter, FileName: fileName}
	it, err := r.Log(o)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = it.ForEach(func(c *git2.Commit) error {
		res.N++
		res.Out = append(res.Out, c.Hash.String())
		return nil
	})
	return res, err
}

func git2VersionAt(c *git2.Commit, repo *Repo, buf []byte) (string, []byte, error) {
	f, err := c.File(repo.VersionFile)
	if err != nil {
		return "", buf, err
	}
	buf, err = f.Bytes(buf[:0])
	if err != nil {
		return "", buf, err
	}
	if m := repo.VersionRE.FindSubmatch(buf); m != nil {
		return string(m[1]), buf, nil
	}
	return "", buf, nil
}

func Git2VersionHistory(r *git2.Repository, repo *Repo, all bool) (Result, error) {
	fn := repo.VersionFile
	it, err := r.Log(&git2.LogOptions{All: all, Order: git2.LogOrderCommitterTime, FileName: &fn})
	if err != nil {
		return Result{}, err
	}
	versions := map[string]string{}
	var buf []byte
	err = it.ForEach(func(c *git2.Commit) error {
		var v string
		v, buf, err = git2VersionAt(c, repo, buf)
		if err != nil || v == "" {
			return nil
		}
		changed := true
		c.Parents().ForEach(func(p *git2.Commit) error {
			var pv string
			if pv, buf, err = git2VersionAt(p, repo, buf); err == nil && pv == v {
				changed = false
			}
			return nil
		})
		if changed {
			if _, ok := versions[v]; !ok {
				versions[v] = c.Hash.String()
			}
		}
		return nil
	})
	return versionResult(versions), err
}

func Git2Grep(r *git2.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	rs, err := r.Grep(&git2.GrepOptions{
		CommitHash: head.Hash(),
		PathSpecs:  []*regexp.Regexp{repo.GrepPath},
		Patterns:   []*regexp.Regexp{repo.GrepPattern},
	})
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, g := range rs {
		res.Out = append(res.Out, g.FileName)
	}
	res.N = len(rs)
	return res, nil
}

func Git2EntryWalk(r *git2.Repository, repo *Repo, limit int) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	it, err := r.Log(&git2.LogOptions{From: head.Hash()})
	if err != nil {
		return Result{}, err
	}
	var res Result
	var last git2.Hash
	err = it.ForEach(func(c *git2.Commit) error {
		if res.N == limit {
			return git2.ErrStop
		}
		res.N++
		t, err := c.Tree()
		if err != nil {
			return err
		}
		e, err := t.FindEntry(repo.VersionFile)
		if err != nil || e.Hash == last {
			return nil
		}
		last = e.Hash
		b, err := r.BlobObject(e.Hash)
		if err != nil {
			return err
		}
		rd, err := b.Reader()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rd)
		rd.Close()
		res.Sum += int64(len(data))
		return err
	})
	return res, err
}
