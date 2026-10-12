package bench

import (
	"os/exec"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/msuozzo/go-git2"
)

// eachRepoT runs fn as a subtest for each selected repository.
func eachRepoT(t *testing.T, fn func(*testing.T, *Repo)) {
	repos, err := Selected()
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range repos {
		t.Run(repo.Size, func(t *testing.T) {
			built(t, repo)
			fn(t, repo)
		})
	}
}

func open(t testing.TB, repo *Repo) (*git.Repository, *git2.Repository) {
	t.Helper()
	gg, err := OpenGoGit(repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	// Parity tests verify objects, as git2 does by default. Benchmarks
	// use the benchmark options.
	var o *git2.OpenOptions
	if _, ok := t.(*testing.B); ok {
		o = Options()
	}
	gw, err := git2.PlainOpenWithOptions(repo.Path(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close() })
	return gg, gw
}

func same(t *testing.T, name string, want, got Result, wantErr, gotErr error) {
	t.Helper()
	if wantErr != nil || gotErr != nil {
		t.Fatalf("%s: go-git err=%v git2 err=%v", name, wantErr, gotErr)
	}
	if want.N != got.N || want.Sum != got.Sum || !reflect.DeepEqual(want.Out, got.Out) {
		t.Errorf("%s: mismatch\n go-git:  N=%d Sum=%d Out=%.300v\n git2: N=%d Sum=%d Out=%.300v",
			name, want.N, want.Sum, want.Out, got.N, got.Sum, got.Out)
	}
}

// headTrees returns the tree of HEAD's commit as each library reads it.
func headTrees(t testing.TB, gg *git.Repository, gw *git2.Repository) (*object.Tree, *git2.Tree) {
	t.Helper()
	head, err := gg.Head()
	if err != nil {
		t.Fatal(err)
	}
	c, err := gg.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tr, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	wc, err := gw.CommitObject(git2.Hash(head.Hash()))
	if err != nil {
		t.Fatal(err)
	}
	wt, err := wc.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return tr, wt
}

func TestParityWorkloads(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		for _, wl := range []struct {
			name  string
			gogit goGitFunc
			g2    git2Func
		}{
			{"HeadCommit", GoGitHeadCommit, Git2HeadCommit},
			{"LogHead", GoGitLogHead, Git2LogHead},
			{"TreeFiles", GoGitTreeFiles, Git2TreeFiles},
			{"FindTagMatch", GoGitFindTagMatch, Git2FindTagMatch},
			{"Grep", GoGitGrep, Git2Grep},
		} {
			w, we := wl.gogit(gg, repo)
			g, ge := wl.g2(gw, repo)
			same(t, wl.name, w, g, we, ge)
		}

		tr, wt := headTrees(t, gg, gw)
		w, we := GoGitFileContents(tr, repo)
		g, ge := Git2FileContents(wt, repo)
		same(t, "FileContents", w, g, we, ge)

		w, we = GoGitEntryWalk(gg, repo, 3000)
		g, ge = Git2EntryWalk(gw, repo, 3000)
		same(t, "EntryWalk", w, g, we, ge)
	})
}

// gitSelected returns, per git, the commits that the path filters select
// for git rev-list revs -- path. That is plain git log -- path without its
// pruning of side branches (see TestPathFilterMerges in the root package):
// the commits with at most one parent that git rev-list --full-history
// lists, and the merges it lists that differ from all of their parents.
func gitSelected(t *testing.T, repo *Repo, revs []string, path string) []string {
	t.Helper()
	git := func(args ...string) []string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo.Path()}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}
	list := func(extra ...string) []string {
		out := git(append(append([]string{"rev-list", "--full-history"}, extra...), append(revs, "--", path)...)...)
		if len(out) == 1 && out[0] == "" {
			return nil
		}
		return out
	}
	selected := list("--no-merges")
	merges := list("--merges")
	if len(merges) == 0 {
		return selected
	}
	// Without a path, --parents lists the merges' real parents.
	for _, line := range git(append([]string{"rev-list", "--no-walk=unsorted", "--parents"}, merges...)...) {
		f := strings.Fields(line)
		differs := true
		for _, p := range f[1:] {
			err := exec.Command("git", "-C", repo.Path(), "diff", "--quiet", p, f[0], "--", path).Run()
			if err == nil {
				differs = false
				break
			}
		}
		if differs {
			selected = append(selected, f[0])
		}
	}
	return selected
}

// TestParityLogFilter checks path-filtered history against git, with
// gitSelected. go-git is not a reference here. It diffs each commit against
// the next one in iteration order instead of against its parents, so jumps
// between branches show up as spurious changes. In google-cloud-python,
// those jumps come from history imported by monorepo migrations.
func TestParityLogFilter(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		filter := func(p string) bool { return p == repo.VersionFile }
		g, err := Git2Log(gw, false, git2.LogOrderDefault, filter, nil)
		if err != nil {
			t.Fatal(err)
		}
		g2, err := Git2Log(gw, false, git2.LogOrderDefault, nil, &repo.VersionFile)
		if err != nil {
			t.Fatal(err)
		}
		same(t, "PathFilter vs FileName", g, g2, nil, nil)

		want := gitSelected(t, repo, []string{"HEAD"}, repo.VersionFile)
		got := append([]string(nil), g.Out...)
		sort.Strings(want)
		sort.Strings(got)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("git: %d commits, git2: %d", len(want), len(got))
		}

		w, err := GoGitLog(gg, false, git.LogOrderDefault, filter)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d commits touch %s (go-git reports %d)", g.N, repo.VersionFile, w.N)
	})
}

func TestParityVersionHistory(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		w, we := GoGitVersionHistory(gg, repo, false)
		g, ge := Git2VersionHistory(gw, repo, false)
		same(t, "VersionHistory", w, g, we, ge)
		t.Logf("%d versions", g.N)
	})
}

// TestParityVersionHistoryAll runs the version-map search of OSS Rebuild as
// it is written, over every reference in committer-time order. go-git takes
// minutes on the large repository.
//
// go-git's results are not a reference here. Its path filter misses commits
// whose successor in iteration order is not a parent, and its All iteration
// is not in committer-time order, so it both drops versions and attributes
// some to older commits on side branches. Instead, check that git2
// finds every version go-git does, and that git agrees each reported
// commit introduces its version.
func TestParityVersionHistoryAll(t *testing.T) {
	if testing.Short() {
		t.Skip("go-git takes minutes")
	}
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		g, err := Git2VersionHistory(gw, repo, true)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, line := range g.Out {
			v, c := splitVersion(line)
			found[v] = true
			if got := gitVersion(repo, c); got != v {
				t.Errorf("%s: git says version %q, git2 %q", c, got, v)
			}
			parents, _ := exec.Command("git", "-C", repo.Path(), "log", "-1", "--format=%P", c).Output()
			for _, p := range strings.Fields(string(parents)) {
				if gitVersion(repo, p) == v {
					t.Errorf("%s: parent %s already has version %s", c, p, v)
				}
			}
		}
		w, err := GoGitVersionHistory(gg, repo, true)
		if err != nil {
			t.Fatal(err)
		}
		missed := 0
		for _, line := range w.Out {
			if v, _ := splitVersion(line); !found[v] {
				t.Errorf("go-git found version %s, git2 did not", v)
			}
		}
		for v := range found {
			if !slices.ContainsFunc(w.Out, func(l string) bool { return strings.HasPrefix(l, v+" ") }) {
				missed++
			}
		}
		t.Logf("git2: %d versions, go-git: %d (misses %d)", g.N, w.N, missed)
	})
}

// splitVersion splits a VersionHistory result line into the version, which
// may contain spaces, and the commit that introduced it.
func splitVersion(line string) (version, commit string) {
	i := strings.LastIndexByte(line, ' ')
	return line[:i], line[i+1:]
}

// gitVersion returns the version in the repository's VersionFile at
// commit c, per git.
func gitVersion(repo *Repo, c string) string {
	out, err := exec.Command("git", "-C", repo.Path(), "show", c+":"+repo.VersionFile).Output()
	if err != nil {
		return ""
	}
	if m := repo.VersionRE.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

func sig(s object.Signature) [4]any {
	_, off := s.When.Zone()
	return [4]any{s.Name, s.Email, s.When.Unix(), off}
}

func wsig(s git2.Signature) [4]any {
	_, off := s.When.Zone()
	return [4]any{s.Name, s.Email, s.When.Unix(), off}
}

// TestParityCommits compares every field of the first commits from HEAD,
// read both directly and through a history walk.
func TestParityCommits(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		head, _ := gg.Head()
		it, _ := gg.Log(&git.LogOptions{From: head.Hash()})
		wit, err := gw.Log(&git2.LogOptions{From: git2.Hash(head.Hash())})
		if err != nil {
			t.Fatal(err)
		}
		defer wit.Close()
		check := func(c *object.Commit, wc *git2.Commit) {
			t.Helper()
			var parents []plumbing.Hash
			for _, p := range wc.ParentHashes {
				parents = append(parents, plumbing.Hash(p))
			}
			if plumbing.Hash(wc.Hash) != c.Hash || wc.Message != c.Message ||
				plumbing.Hash(wc.TreeHash) != c.TreeHash ||
				!reflect.DeepEqual(parents, c.ParentHashes) ||
				wsig(wc.Author) != sig(c.Author) || wsig(wc.Committer) != sig(c.Committer) ||
				wc.PGPSignature != c.PGPSignature || wc.MergeTag != c.MergeTag ||
				wc.Encoding != string(c.Encoding) {
				t.Fatalf("commit %s differs:\n go-git:  %+v\n git2: %+v", c.Hash, c, wc)
			}
		}
		n := 0
		it.ForEach(func(c *object.Commit) error {
			if n++; n > 5000 {
				return storer.ErrStop
			}
			wc, err := gw.CommitObject(git2.Hash(c.Hash))
			if err != nil {
				t.Fatal(err)
			}
			check(c, wc)
			wc, err = wit.Next()
			if err != nil {
				t.Fatal(err)
			}
			check(c, wc)
			return nil
		})
	})
}

func TestParityRefs(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		collect := func(f func(func(name, target string, h [20]byte))) []string {
			var out []string
			f(func(name, target string, h [20]byte) {
				out = append(out, name+" "+target+" "+plumbing.Hash(h).String())
			})
			sort.Strings(out)
			return out
		}
		want := collect(func(add func(string, string, [20]byte)) {
			it, _ := gg.References()
			it.ForEach(func(r *plumbing.Reference) error {
				add(string(r.Name()), string(r.Target()), r.Hash())
				return nil
			})
		})
		got := collect(func(add func(string, string, [20]byte)) {
			it, _ := gw.References()
			it.ForEach(func(r *git2.Reference) error {
				add(string(r.Name()), string(r.Target()), r.Hash())
				return nil
			})
		})
		if !reflect.DeepEqual(want, got) {
			t.Errorf("references differ: go-git %d, git2 %d", len(want), len(got))
			for i := 0; i < len(want) && i < len(got); i++ {
				if want[i] != got[i] {
					t.Errorf("first difference: %q vs %q", want[i], got[i])
					break
				}
			}
		}

		// Annotated tags.
		it, _ := gg.TagObjects()
		n := 0
		it.ForEach(func(tag *object.Tag) error {
			n++
			wt, err := gw.TagObject(git2.Hash(tag.Hash))
			if err != nil {
				t.Fatalf("tag %s: %v", tag.Hash, err)
			}
			if wt.Name != tag.Name || plumbing.Hash(wt.Target) != tag.Target ||
				int8(wt.TargetType) != int8(tag.TargetType) || wt.Message != tag.Message ||
				wt.PGPSignature != tag.PGPSignature || wsig(wt.Tagger) != sig(tag.Tagger) {
				t.Errorf("tag %s differs:\n go-git:  %+v\n git2: %+v", tag.Hash, tag, wt)
			}
			return nil
		})
		t.Logf("%d refs, %d annotated tags", len(want), n)

		for _, rev := range []string{"HEAD", "HEAD~100", repo.Branch, "refs/heads/" + repo.Branch,
			repo.TagPrefix + repo.TagVersion} {
			wh, werr := gg.ResolveRevision(plumbing.Revision(rev))
			gh, gerr := gw.ResolveRevision(git2.Revision(rev))
			if (werr == nil) != (gerr == nil) || (werr == nil && *wh != plumbing.Hash(*gh)) {
				t.Errorf("ResolveRevision(%q): go-git %v %v, git2 %v %v", rev, wh, werr, gh, gerr)
			}
		}
	})
}

func TestParityTrees(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)
		tr, wt := headTrees(t, gg, gw)

		// Every subtree under Dir, entry by entry.
		walker := object.NewTreeWalker(tr, true, nil)
		defer walker.Close()
		checked := 0
		for {
			name, e, err := walker.Next()
			if err != nil {
				break
			}
			if e.Mode.IsFile() || !strings.HasPrefix(name, repo.Dir) {
				continue
			}
			sub, _ := tr.Tree(name)
			wsub, err := wt.Tree(name)
			if err != nil || len(sub.Entries) != len(wsub.Entries) {
				t.Fatalf("tree %s: %v (%d vs %d entries)", name, err, len(sub.Entries), len(wsub.Entries))
			}
			for i, e := range sub.Entries {
				we := wsub.Entries[i]
				if e.Name != we.Name || uint32(e.Mode) != uint32(we.Mode) || e.Hash != plumbing.Hash(we.Hash) {
					t.Fatalf("tree %s entry %d: %+v vs %+v", name, i, e, we)
				}
			}
			checked++
		}

		// The recursive walker yields the same sequence.
		walker = object.NewTreeWalker(tr, true, nil)
		ww := git2.NewTreeWalker(wt, true)
		n := 0
		for {
			name, e, err := walker.Next()
			wname, we, werr := ww.Next()
			if (err == nil) != (werr == nil) {
				t.Fatalf("walker end differs at %d: %v vs %v", n, err, werr)
			}
			if err != nil {
				break
			}
			if name != wname || e.Name != we.Name || uint32(e.Mode) != uint32(we.Mode) || e.Hash != plumbing.Hash(we.Hash) {
				t.Fatalf("walker entry %d: %s %+v vs %s %+v", n, name, e, wname, we)
			}
			n++
		}
		t.Logf("%d subtrees compared, %d walker entries", checked, n)

		// Missing paths.
		if _, err := wt.File(repo.Dir + "/nope.py"); err != git2.ErrFileNotFound {
			t.Errorf("File(missing) = %v", err)
		}
		if _, err := wt.File(repo.Dir); err != git2.ErrFileNotFound {
			t.Errorf("File(dir) = %v", err)
		}
		if _, err := gw.CommitObject(git2.NewHash("")); err != git2.ErrObjectNotFound {
			t.Errorf("CommitObject(zero) = %v", err)
		}
	})
}

func TestParityRealWorld(t *testing.T) {
	eachRepoT(t, func(t *testing.T, repo *Repo) {
		gg, gw := open(t, repo)

		w, we := GoGitTagSemver(gg, repo)
		g, ge := Git2TagSemver(gw, repo)
		same(t, "TagSemver", w, g, we, ge)
		t.Logf("TagSemver: %d tags resolve to commits, highest %s", g.N, g.Out)

		w, we = GoGitBlobsAtHead(gg, repo)
		g, ge = Git2BlobsAtHead(gw, repo)
		same(t, "BlobsAtHead", w, g, we, ge)

		// go-git's Log{All} misses commits reachable only from some
		// references (2,445 of 70,808 in google-cloud-python), so compare
		// with git.
		g, ge = Git2CommitGraph(gw, repo)
		if ge != nil {
			t.Fatal(ge)
		}
		if out, err := exec.Command("git", "-C", repo.Path(), "rev-list", "--all", "--count").Output(); err == nil {
			if want := strings.TrimSpace(string(out)); want != strconv.Itoa(g.N) {
				t.Errorf("CommitGraph: git rev-list --all counts %s commits, git2 %d", want, g.N)
			}
		}
		w, _ = GoGitCommitGraph(gg, repo)
		t.Logf("CommitGraph: %d commits (go-git reports %d)", g.N, w.N)

		// go-git's path filter diverges from git's (see TestParityLogFilter),
		// so compare with git.
		head, _ := gw.Head()
		c, _ := gw.CommitObject(head.Hash())
		since := c.Committer.When.Add(-repo.RecentWindow).Unix()
		want := gitSelected(t, repo, []string{"--all", "--max-age=" + strconv.FormatInt(since, 10)}, repo.Dir)
		sort.Strings(want)
		for _, byName := range []bool{false, true} {
			g, ge = Git2RecentPathLog(gw, repo, byName)
			if ge != nil {
				t.Fatal(ge)
			}
			got := append([]string(nil), g.Out...)
			sort.Strings(got)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("RecentPathLog (by name %v): git: %d commits, git2: %d", byName, len(want), len(got))
			}
		}
		g, ge = Git2RecentLog(gw, repo)
		if ge != nil {
			t.Fatal(ge)
		}
		if out, err := exec.Command("git", "-C", repo.Path(), "rev-list", "--max-age="+strconv.FormatInt(since, 10), "HEAD").Output(); err != nil {
			t.Fatal(err)
		} else if want := strings.Fields(string(out)); !reflect.DeepEqual(want, g.Out) {
			t.Errorf("RecentLog: git rev-list: %d commits, git2: %d", len(want), g.N)
		}
		w, _ = GoGitRecentLog(gg, repo)
		t.Logf("RecentLog: %d commits (go-git reports %d)", g.N, w.N)
		w, _ = GoGitRecentPathLog(gg, repo)
		t.Logf("RecentPathLog: %d commits (go-git reports %d)", g.N, w.N)
	})
}
