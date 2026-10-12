package bench

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/msuozzo/go-git2"
	"github.com/msuozzo/go-git2/billyfs"
)

var (
	memMu    sync.Mutex
	memRepos = map[*Repo]billy.Filesystem{}
)

// memRepo returns repo copied into a memfs, copying it on first use.
func memRepo(repo *Repo) (billy.Filesystem, error) {
	memMu.Lock()
	defer memMu.Unlock()
	if mfs, ok := memRepos[repo]; ok {
		return mfs, nil
	}
	mfs := memfs.New()
	src := repo.Path()
	// Like oss-rebuild's billyx.CopyFS, which keeps empty directories.
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return mfs.MkdirAll(rel, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := mfs.Create(rel)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return out.Close()
	})
	if err != nil {
		return nil, err
	}
	memRepos[repo] = mfs
	return mfs, nil
}

// OpenGoGitMemfs opens a repository in a memfs as oss-rebuild's
// gitx.NewInMemoryStorer holds clones during inference.
func OpenGoGitMemfs(mfs billy.Filesystem) (*git.Repository, error) {
	s := filesystem.NewStorageWithOptions(mfs, cache.NewObjectLRUDefault(),
		filesystem.Options{KeepDescriptors: true, ExclusiveAccess: true})
	return git.Open(s, nil)
}

func coldMemfs(b *testing.B, gogit goGitFunc, g2 git2Func) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		mfs, err := memRepo(repo)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("go-git", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := OpenGoGitMemfs(mfs)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := gogit(r, repo); err != nil {
					b.Fatal(err)
				}
				closeGoGit(r)
			}
		})
		b.Run("git2", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := git2.OpenWithOptions(billyfs.New(mfs), Options())
				if err != nil {
					b.Fatal(err)
				}
				if _, err := g2(r, repo); err != nil {
					b.Fatal(err)
				}
				r.Close()
			}
		})
	})
}

func BenchmarkMemfsOpenHeadCommit(b *testing.B) { coldMemfs(b, GoGitHeadCommit, Git2HeadCommit) }
func BenchmarkMemfsLogHead(b *testing.B)        { coldMemfs(b, GoGitLogHead, Git2LogHead) }
func BenchmarkMemfsTreeFiles(b *testing.B)      { coldMemfs(b, GoGitTreeFiles, Git2TreeFiles) }
func BenchmarkMemfsFindTagMatch(b *testing.B)   { coldMemfs(b, GoGitFindTagMatch, Git2FindTagMatch) }
func BenchmarkMemfsGrep(b *testing.B)           { coldMemfs(b, GoGitGrep, Git2Grep) }

func BenchmarkMemfsEntryWalk(b *testing.B) {
	coldMemfs(b,
		func(r *git.Repository, repo *Repo) (Result, error) { return GoGitEntryWalk(r, repo, 3000) },
		func(r *git2.Repository, repo *Repo) (Result, error) { return Git2EntryWalk(r, repo, 3000) })
}

func TestParityMemfs(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		_, gw := open(t, repo)
		mfs, err := memRepo(repo)
		if err != nil {
			t.Fatal(err)
		}
		mg, err := OpenGoGitMemfs(mfs)
		if err != nil {
			t.Fatal(err)
		}
		mw, err := git2.OpenWithOptions(billyfs.New(mfs), Options())
		if err != nil {
			t.Fatal(err)
		}
		defer mw.Close()
		w, we := GoGitLogHead(mg, repo)
		g, ge := Git2LogHead(mw, repo)
		same(t, "memfs LogHead", w, g, we, ge)
		g2, ge2 := Git2LogHead(gw, repo)
		same(t, "memfs vs osfs", g, g2, ge, ge2)
		w, we = GoGitFindTagMatch(mg, repo)
		g, ge = Git2FindTagMatch(mw, repo)
		same(t, "memfs FindTagMatch", w, g, we, ge)
	})
}
