package bench

import (
	"io"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/msuozzo/go-git2"
)

// Workloads modeled on other read-heavy git consumers.
// Each names the code it follows.

// version parses name as prefix followed by a plain X.Y.Z version.
func version(name, prefix string) ([3]int, bool) {
	var v [3]int
	s, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// semverMax accumulates TagSemver's result: the commits tags resolve to, and
// the highest version.
type semverMax struct {
	prefix string
	res    Result
	best   [3]int
	name   string
}

func (m *semverMax) add(tag string, when time.Time) {
	m.res.N++
	m.res.Sum += when.Unix()
	if v, ok := version(tag, m.prefix); ok && (m.name == "" || v[0] > m.best[0] ||
		v[0] == m.best[0] && (v[1] > m.best[1] || v[1] == m.best[1] && v[2] > m.best[2])) {
		m.best, m.name = v, tag
	}
}

func (m *semverMax) result() Result {
	m.res.Out = []string{m.name}
	return m.res
}

// GoGitTagSemver follows Flux's cloneSemVer (fluxcd/pkg git/gogit/clone.go),
// as do updatecli and go-semantic-release. It resolves every tag to its
// commit, reads the commit's time, then picks the highest version.
func GoGitTagSemver(r *git.Repository, repo *Repo) (Result, error) {
	tags, err := r.Tags()
	if err != nil {
		return Result{}, err
	}
	m := semverMax{prefix: repo.TagPrefix}
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		h, err := r.ResolveRevision(plumbing.Revision(ref.Name()))
		if err != nil {
			return nil // not a commit
		}
		c, err := r.CommitObject(*h)
		if err != nil {
			return err
		}
		m.add(ref.Name().Short(), c.Committer.When)
		return nil
	})
	return m.result(), err
}

// GoGitBlobsAtHead follows zoekt's index build (sourcegraph/zoekt
// gitindex/index.go). It walks the tree at HEAD and reads every unique
// blob.
func GoGitBlobsAtHead(r *git.Repository, repo *Repo) (Result, error) {
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
	seen := map[plumbing.Hash]bool{}
	var res Result
	err = t.Files().ForEach(func(f *object.File) error {
		if seen[f.Hash] {
			return nil
		}
		seen[f.Hash] = true
		rd, err := f.Reader()
		if err != nil {
			return err
		}
		n, err := io.Copy(io.Discard, rd)
		rd.Close()
		res.N++
		res.Sum += n
		return err
	})
	return res, err
}

// GoGitRecentPathLog follows osv.dev's gitter (`log --since -- paths`) and
// go-git#1659. It lists the commits on any branch, within RecentWindow of
// HEAD, that touch the package's Dir, newest first.
func GoGitRecentPathLog(r *git.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	since := c.Committer.When.Add(-repo.RecentWindow)
	it, err := r.Log(&git.LogOptions{All: true, Order: git.LogOrderCommitterTime, Since: &since, PathFilter: repo.inDir})
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

// GoGitRecentLog lists the commits from HEAD within RecentWindow of HEAD,
// newest first, as git log --since does.
func GoGitRecentLog(r *git.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	since := c.Committer.When.Add(-repo.RecentWindow)
	it, err := r.Log(&git.LogOptions{From: head.Hash(), Order: git.LogOrderCommitterTime, Since: &since})
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

// inDir reports whether p is below the repository's Dir.
func (repo *Repo) inDir(p string) bool { return strings.HasPrefix(p, repo.Dir+"/") }

// GoGitCommitGraph follows osv.dev's commit graph (google/osv.dev
// go/cmd/gitter) and git-sizer. It lists every commit reachable from any
// reference, with its parents.
func GoGitCommitGraph(r *git.Repository, repo *Repo) (Result, error) {
	it, err := r.Log(&git.LogOptions{All: true})
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = it.ForEach(func(c *object.Commit) error {
		res.N++
		res.Sum += int64(len(c.ParentHashes))
		return nil
	})
	return res, err
}

// ---- git2 ------------------------------------------------------------

func Git2TagSemver(r *git2.Repository, repo *Repo) (Result, error) {
	tags, err := r.Tags()
	if err != nil {
		return Result{}, err
	}
	m := semverMax{prefix: repo.TagPrefix}
	err = tags.ForEach(func(ref *git2.Reference) error {
		h, err := r.ResolveRevision(git2.Revision(ref.Name()))
		if err != nil {
			return nil // not a commit
		}
		c, err := r.CommitObject(*h)
		if err != nil {
			return err
		}
		m.add(ref.Name().Short(), c.Committer.When)
		return nil
	})
	return m.result(), err
}

func Git2BlobsAtHead(r *git2.Repository, repo *Repo) (Result, error) {
	t, err := git2HeadTree(r)
	if err != nil {
		return Result{}, err
	}
	seen := map[git2.Hash]bool{}
	var res Result
	var buf []byte
	err = t.Files().ForEach(func(f *git2.File) error {
		if seen[f.Hash] {
			return nil
		}
		seen[f.Hash] = true
		buf, err = f.Bytes(buf[:0])
		res.N++
		res.Sum += int64(len(buf))
		return err
	})
	return res, err
}

func git2HeadTree(r *git2.Repository) (*git2.Tree, error) {
	head, err := r.Head()
	if err != nil {
		return nil, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}
	return c.Tree()
}

func Git2RecentLog(r *git2.Repository, repo *Repo) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	since := c.Committer.When.Add(-repo.RecentWindow)
	it, err := r.Log(&git2.LogOptions{From: head.Hash(), Order: git2.LogOrderCommitterTime, Since: &since})
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

// Git2RecentPathLog selects the commits by name if byName, with FileName set
// to Dir, and otherwise with the same PathFilter as go-git.
func Git2RecentPathLog(r *git2.Repository, repo *Repo, byName bool) (Result, error) {
	head, err := r.Head()
	if err != nil {
		return Result{}, err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return Result{}, err
	}
	since := c.Committer.When.Add(-repo.RecentWindow)
	o := &git2.LogOptions{All: true, Order: git2.LogOrderCommitterTime, Since: &since, PathFilter: repo.inDir}
	if byName {
		o.FileName, o.PathFilter = &repo.Dir, nil
	}
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

func Git2CommitGraph(r *git2.Repository, repo *Repo) (Result, error) {
	it, err := r.Log(&git2.LogOptions{All: true})
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = it.ForEach(func(c *git2.Commit) error {
		res.N++
		res.Sum += int64(len(c.ParentHashes))
		return nil
	})
	return res, err
}
