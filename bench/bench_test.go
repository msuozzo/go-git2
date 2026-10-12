package bench

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"

	"github.com/msuozzo/go-git2"
)

// Benchmarks run once per repository that $GIT2_BENCH_REPO selects, with
// go-git and git2 below that: BenchmarkX/large/go-git and
// BenchmarkX/large/git2. Some also run the git CLI doing the same work, as
// BenchmarkX/large/git.
//
// Cold benchmarks open the repository on every iteration, as a tool does
// when it reads each fresh clone once, so they include each library's open
// cost and start with empty caches. Warm benchmarks reuse one open
// repository.

type (
	goGitFunc func(*git.Repository, *Repo) (Result, error)
	git2Func  func(*git2.Repository, *Repo) (Result, error)
)

// built skips tb unless repo has been built.
func built(tb testing.TB, repo *Repo) {
	tb.Helper()
	if _, err := os.Stat(repo.Path()); err != nil {
		tb.Skipf("%s is not built; run go run ./cmd/benchrepos %s", repo.Path(), repo.Size)
	}
}

// eachRepo runs fn as a sub-benchmark for each selected repository.
func eachRepo(b *testing.B, fn func(*testing.B, *Repo)) {
	repos, err := Selected()
	if err != nil {
		b.Fatal(err)
	}
	for _, repo := range repos {
		b.Run(repo.Size, func(b *testing.B) {
			built(b, repo)
			fn(b, repo)
		})
	}
}

func coldGoGit(b *testing.B, repo *Repo, fn goGitFunc) {
	b.ReportAllocs()
	for b.Loop() {
		r, err := OpenGoGit(repo.Path())
		if err != nil {
			b.Fatal(err)
		}
		if _, err := fn(r, repo); err != nil {
			b.Fatal(err)
		}
		closeGoGit(r)
	}
}

// closeGoGit releases go-git's storage, whose open files KeepDescriptors
// keeps, as git2's cold benchmarks close their repository.
func closeGoGit(r *git.Repository) {
	if c, ok := r.Storer.(io.Closer); ok {
		c.Close()
	}
}

func coldGit2(b *testing.B, repo *Repo, fn git2Func) {
	b.ReportAllocs()
	for b.Loop() {
		r, err := git2.PlainOpenWithOptions(repo.Path(), Options())
		if err != nil {
			b.Fatal(err)
		}
		if _, err := fn(r, repo); err != nil {
			b.Fatal(err)
		}
		r.Close()
	}
}

// coldGit runs the git CLI with args in repo on every iteration,
// including its process startup.
func coldGit(b *testing.B, repo *Repo, args ...string) {
	args = append([]string{"-C", repo.Path()}, args...)
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	for b.Loop() {
		cmd := exec.Command("git", args...)
		cmd.Env = env
		if out, err := cmd.Output(); err != nil {
			b.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func cold(b *testing.B, gogit goGitFunc, g2 git2Func) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		b.Run("go-git", func(b *testing.B) { coldGoGit(b, repo, gogit) })
		b.Run("git2", func(b *testing.B) { coldGit2(b, repo, g2) })
	})
}

func warm(b *testing.B, gogit goGitFunc, g2 git2Func) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		gg, gw := open(b, repo)
		b.Run("go-git", func(b *testing.B) {
			if _, err := gogit(gg, repo); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				gogit(gg, repo)
			}
		})
		b.Run("git2", func(b *testing.B) {
			if _, err := g2(gw, repo); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				g2(gw, repo)
			}
		})
	})
}

func BenchmarkOpenHeadCommit(b *testing.B) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		b.Run("go-git", func(b *testing.B) { coldGoGit(b, repo, GoGitHeadCommit) })
		b.Run("git2", func(b *testing.B) { coldGit2(b, repo, Git2HeadCommit) })
		b.Run("git", func(b *testing.B) { coldGit(b, repo, "cat-file", "commit", "HEAD") })
	})
}

func BenchmarkLogHead(b *testing.B) { cold(b, GoGitLogHead, Git2LogHead) }

func BenchmarkTreeFiles(b *testing.B) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		b.Run("go-git", func(b *testing.B) { coldGoGit(b, repo, GoGitTreeFiles) })
		b.Run("git2", func(b *testing.B) { coldGit2(b, repo, Git2TreeFiles) })
		b.Run("git", func(b *testing.B) { coldGit(b, repo, "ls-tree", "-r", "--name-only", "HEAD") })
	})
}

func BenchmarkFindTagMatch(b *testing.B) { cold(b, GoGitFindTagMatch, Git2FindTagMatch) }

func BenchmarkGrep(b *testing.B) { cold(b, GoGitGrep, Git2Grep) }

func BenchmarkEntryWalk(b *testing.B) {
	cold(b,
		func(r *git.Repository, repo *Repo) (Result, error) { return GoGitEntryWalk(r, repo, 3000) },
		func(r *git2.Repository, repo *Repo) (Result, error) { return Git2EntryWalk(r, repo, 3000) })
}

// BenchmarkLogPathFilter also measures git2 with FileName, which selects
// the same commits without diffing trees. git runs with --full-history,
// since git2 does not prune side branches either.
func BenchmarkLogPathFilter(b *testing.B) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		filter := func(p string) bool { return p == repo.VersionFile }
		b.Run("go-git", func(b *testing.B) {
			coldGoGit(b, repo, func(r *git.Repository, _ *Repo) (Result, error) {
				return GoGitLog(r, false, git.LogOrderDefault, filter)
			})
		})
		b.Run("git2", func(b *testing.B) {
			coldGit2(b, repo, func(r *git2.Repository, _ *Repo) (Result, error) {
				return Git2Log(r, false, git2.LogOrderDefault, filter, nil)
			})
		})
		b.Run("git2-FileName", func(b *testing.B) {
			coldGit2(b, repo, func(r *git2.Repository, _ *Repo) (Result, error) {
				return Git2Log(r, false, git2.LogOrderDefault, nil, &repo.VersionFile)
			})
		})
		b.Run("git", func(b *testing.B) {
			coldGit(b, repo, "rev-list", "--full-history", "HEAD", "--", repo.VersionFile)
		})
	})
}

func BenchmarkVersionHistory(b *testing.B) {
	cold(b,
		func(r *git.Repository, repo *Repo) (Result, error) { return GoGitVersionHistory(r, repo, false) },
		func(r *git2.Repository, repo *Repo) (Result, error) { return Git2VersionHistory(r, repo, false) })
}

// BenchmarkVersionHistoryAll is the version-map search of OSS Rebuild as it
// is written: every reference, in committer-time order.
func BenchmarkVersionHistoryAll(b *testing.B) {
	cold(b,
		func(r *git.Repository, repo *Repo) (Result, error) { return GoGitVersionHistory(r, repo, true) },
		func(r *git2.Repository, repo *Repo) (Result, error) { return Git2VersionHistory(r, repo, true) })
}

func BenchmarkTagSemver(b *testing.B) { cold(b, GoGitTagSemver, Git2TagSemver) }

func BenchmarkBlobsAtHead(b *testing.B) { cold(b, GoGitBlobsAtHead, Git2BlobsAtHead) }

// BenchmarkRecentPathLog also measures git2 with FileName set to the
// directory, which go-git supports only for single files. git runs with
// --full-history.
func BenchmarkRecentPathLog(b *testing.B) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		b.Run("go-git", func(b *testing.B) { coldGoGit(b, repo, GoGitRecentPathLog) })
		for _, byName := range []bool{false, true} {
			name := map[bool]string{false: "git2", true: "git2-FileName"}[byName]
			b.Run(name, func(b *testing.B) {
				coldGit2(b, repo, func(r *git2.Repository, repo *Repo) (Result, error) {
					return Git2RecentPathLog(r, repo, byName)
				})
			})
		}
		b.Run("git", func(b *testing.B) {
			coldGit(b, repo, "rev-list", "--all", "--full-history", "--max-age="+recentCutoff(b, repo), "--", repo.Dir)
		})
	})
}

// BenchmarkRecentLog lists the commits from HEAD of the last RecentWindow.
// go-git filters a walk of the whole history by time, while git2 and git
// stop at the cutoff.
func BenchmarkRecentLog(b *testing.B) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		b.Run("go-git", func(b *testing.B) { coldGoGit(b, repo, GoGitRecentLog) })
		b.Run("git2", func(b *testing.B) { coldGit2(b, repo, Git2RecentLog) })
		b.Run("git", func(b *testing.B) { coldGit(b, repo, "rev-list", "--max-age="+recentCutoff(b, repo), "HEAD") })
	})
}

// recentCutoff returns the Unix time RecentWindow before HEAD's commit,
// computed before the timer starts.
func recentCutoff(b *testing.B, repo *Repo) string {
	out, err := exec.Command("git", "-C", repo.Path(), "log", "-1", "--format=%ct", "HEAD").Output()
	if err != nil {
		b.Fatal(err)
	}
	head, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		b.Fatal(err)
	}
	return strconv.FormatInt(head-int64(repo.RecentWindow/time.Second), 10)
}

func BenchmarkCommitGraph(b *testing.B) { cold(b, GoGitCommitGraph, Git2CommitGraph) }

func BenchmarkFileContentsWarm(b *testing.B) {
	eachRepo(b, func(b *testing.B, repo *Repo) {
		gg, gw := open(b, repo)
		tr, wt := headTrees(b, gg, gw)
		b.Run("go-git", func(b *testing.B) {
			if _, err := GoGitFileContents(tr, repo); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				GoGitFileContents(tr, repo)
			}
		})
		b.Run("git2", func(b *testing.B) {
			if _, err := Git2FileContents(wt, repo); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				Git2FileContents(wt, repo)
			}
		})
	})
}

func BenchmarkHeadCommitWarm(b *testing.B) { warm(b, GoGitHeadCommit, Git2HeadCommit) }

func BenchmarkLogHeadWarm(b *testing.B) { warm(b, GoGitLogHead, Git2LogHead) }
