package git2

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenErrors(t *testing.T) {
	for _, dir := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if _, err := PlainOpen(dir); err != ErrRepositoryNotExists {
			t.Errorf("PlainOpen(%s) = %v", dir, err)
		}
	}
}

func TestOpenGitFile(t *testing.T) {
	f := newFixture(t)
	worktree, submodule := filepath.Join(t.TempDir(), "wt"), t.TempDir()
	f.git(f.dir, "worktree", "add", "-q", worktree, "feature")
	must(os.WriteFile(filepath.Join(submodule, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644))
	for _, dir := range []string{worktree, submodule} {
		if _, err := PlainOpen(dir); err != errGitFile {
			t.Errorf("PlainOpen(%s): %v", dir, err)
		}
		if _, err := Open(FromFS(os.DirFS(dir))); err != errGitFile {
			t.Errorf("Open(%s): %v", dir, err)
		}
	}
}

// A copy of a repository whose refs are all packed may lack refs/, which
// libgit2 requires. Both kinds of open supply an empty one.
func TestOpenWithoutRefsDir(t *testing.T) {
	f := newFixture(t)
	bare := f.bareClone()
	must(os.RemoveAll(filepath.Join(bare, "refs")))
	for _, r := range []*Repository{must1(PlainOpen(bare)), must1(Open(FromFS(os.DirFS(bare))))} {
		if h, err := r.Head(); err != nil || h.Hash().String() != f.git(f.dir, "rev-parse", "main") {
			t.Errorf("Head = %v, %v", h, err)
		}
		r.Close()
	}
}

func TestClosed(t *testing.T) {
	r := must1(PlainOpen(newFixture(t).dir))
	must(r.Close())
	if _, err := r.Head(); err != ErrClosed {
		t.Errorf("Head after Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// fault runs fn holding r's instance, as the API's methods do.
func fault(r *Repository, fn func(*instance)) (err error) {
	rt := must1(r.lock())
	defer r.unlock(&err)
	fn(rt)
	return nil
}

func TestFault(t *testing.T) {
	r := must1(PlainOpen(newFixture(t).dir))
	err := fault(r, func(*instance) { panic("boom") })
	var fe *FaultError
	if !errors.As(err, &fe) || !errors.Is(err, ErrFault) || fe.Value != "boom" {
		t.Fatalf("fault: %#v", err)
	}
	if !strings.Contains(string(fe.Stack), "TestFault") {
		t.Errorf("stack does not show the panic:\n%s", fe.Stack)
	}
	if _, err := r.Head(); err != error(fe) {
		t.Errorf("Head after fault: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close after fault: %v", err)
	}
}

// An access just past the module's memory faults, as one by the module
// traps, instead of reaching the rest of the reservation.
func TestHostAccessPastMemory(t *testing.T) {
	r := openFixture(t, newFixture(t).dir)
	err := fault(r, func(rt *instance) { rt.host.Xrandom_get(int32(len(rt.mem.buf)), 4) })
	if !errors.Is(err, ErrFault) {
		t.Errorf("random_get past the end of memory: %v", err)
	}
}

// Each page that growth adds is writable, and memory doesn't grow past its
// maximum.
func TestMemoryGrow(t *testing.T) {
	const maxPages = maxMemory >> 16
	m := newMemory(1 << 16)
	defer m.free()
	for _, delta := range []int64{1, 15, 256} {
		old := int64(len(m.buf) >> 16)
		if got := m.Grow(delta, maxPages); got != old {
			t.Fatalf("Grow(%d) = %d, want %d", delta, got, old)
		}
		m.buf[old<<16] = 1
		m.buf[len(m.buf)-1] = 1
	}
	if got := m.Grow(maxPages, maxPages); got != -1 {
		t.Errorf("Grow past the maximum = %d, want -1", got)
	}
}

// panicFS panics on every Open.
type panicFS struct{ FS }

func (panicFS) Open(name string) (FSFile, error) { panic("open " + name) }

func TestOpenFault(t *testing.T) {
	_, err := Open(panicFS{FromFS(os.DirFS(newFixture(t).gitDir()))})
	if fe := (*FaultError)(nil); !errors.As(err, &fe) || !errors.Is(err, ErrFault) {
		t.Fatalf("Open with a panicking FS: %v", err)
	}
}

func TestReferences(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		if head := must1(r.Head()); head.Name() != Main || head.Hash().String() != f.git(f.dir, "rev-parse", "HEAD") {
			t.Errorf("Head() = %v", head)
		}
		if sym, err := r.Reference(HEAD, false); err != nil || sym.Type() != SymbolicReference || sym.Target() != Main {
			t.Errorf("Reference(HEAD, false) = %v, %v", sym, err)
		}
		for _, name := range []ReferenceName{"refs/heads/nope", "refs/heads/main\x00junk"} {
			if _, err := r.Reference(name, true); err != ErrReferenceNotFound {
				t.Errorf("Reference(%q) = %v", name, err)
			}
		}
		for name, want := range map[string]error{"v1": nil, "nope": ErrTagNotFound, "v1\x00junk": ErrTagNotFound} {
			if _, err := r.Tag(name); err != want {
				t.Errorf("Tag(%q) = %v", name, err)
			}
		}

		// Clones carry only some of the fixture's refs, so compare the ones
		// present.
		got := map[string]string{}
		must(must1(r.References()).ForEach(func(ref *Reference) error {
			if ref.Type() == HashReference {
				got[string(ref.Name())] = ref.Hash().String()
			}
			return nil
		}))
		for _, l := range lines(f.git(f.dir, "for-each-ref", "--format=%(refname) %(objectname)")) {
			if name, id, _ := strings.Cut(l, " "); got[name] != "" && got[name] != id {
				t.Errorf("%s = %s, want %s", name, got[name], id)
			}
		}
		var tags []string
		must(must1(r.Tags()).ForEach(func(ref *Reference) error {
			tags = append(tags, ref.Name().Short())
			return nil
		}))
		if !slices.Equal(sorted(tags), []string{"v1", "v2", "v2-tree"}) || len(got) < 5 {
			t.Errorf("Tags = %v, references %v", tags, got)
		}
		if b := must1(r.Branches()); b.Len() != 2 {
			t.Errorf("Branches: %d", b.Len())
		}
	})
}

func TestResolveRevision(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		for rev, want := range map[string]string{
			"v2": f.git(f.dir, "rev-parse", "v2^{commit}"), // an annotated tag, peeled
			f.git(f.dir, "rev-parse", "--short=10", "HEAD~1"): f.git(f.dir, "rev-parse", "HEAD~1"),
			"nope": "", "HEAD^{tree}": "", "HEAD:README.md": "", "v2-tree": "", "main\x00junk": "",
		} {
			h, err := r.ResolveRevision(Revision(rev))
			if want == "" && err != ErrReferenceNotFound || want != "" && (err != nil || h.String() != want) {
				t.Errorf("ResolveRevision(%q) = %v, %v; want %q", rev, h, err, want)
			}
		}
	})
}

func TestCommitObject(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		for _, rev := range f.revList("--all") {
			c := must1(r.CommitObject(NewHash(rev)))
			var parents []string
			for _, p := range c.ParentHashes {
				parents = append(parents, p.String())
			}
			got := append([]string{c.TreeHash.String(), strings.Join(parents, " "),
				c.Author.Name, c.Author.Email, strconv.FormatInt(c.Author.When.Unix(), 10),
				c.Committer.Name, c.Committer.Email, strconv.FormatInt(c.Committer.When.Unix(), 10),
			}, lines(strings.TrimRight(c.Message, "\n"))...)
			want := lines(f.git(f.dir, "log", "-1", "--format=%T%n%P%n%an%n%ae%n%at%n%cn%n%ce%n%ct%n%B", rev))
			if _, off := c.Author.When.Zone(); !slices.Equal(got, want) || off != 2*3600 {
				t.Errorf("commit %s:\n got  %q\n want %q (zone %d)", rev, got, want, off)
			}
		}
		for _, h := range []Hash{ZeroHash, NewHash(f.git(f.dir, "rev-parse", "HEAD^{tree}"))} {
			if _, err := r.CommitObject(h); err != ErrObjectNotFound {
				t.Errorf("CommitObject(%s) = %v", h, err)
			}
		}
	})
}

func TestTagObject(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		tag := must1(r.TagObject(must1(r.Tag("v2")).Hash()))
		if tag.Name != "v2" || tag.TargetType != CommitObject || tag.Target.String() != f.git(f.dir, "rev-parse", "HEAD") ||
			tag.Message != "release v2\n\nnotes\n" || tag.Tagger.Name != "Cy Committer" {
			t.Errorf("TagObject(v2) = %+v", tag)
		}
		if c, err := tag.Commit(); err != nil || c.Hash != tag.Target {
			t.Errorf("Tag.Commit: %v", err)
		}
		if _, err := r.TagObject(must1(r.Tag("v1")).Hash()); err != ErrObjectNotFound {
			t.Errorf("TagObject(lightweight) = %v", err)
		}
		tt := must1(r.TagObject(must1(r.Tag("v2-tree")).Hash()))
		if tr, err := tt.Tree(); tt.TargetType != TreeObject || err != nil || tr.Hash != tt.Target {
			t.Errorf("tree tag: %+v, %v", tt, err)
		}
	})
}

// lsTree returns git ls-tree's entries for args as "mode hash path".
func (f *fixture) lsTree(args ...string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(f.git(f.dir, append([]string{"ls-tree", "-z"}, args...)...), "\x00"), "\x00") {
		meta, name, _ := strings.Cut(l, "\t")
		fs := strings.Fields(meta)
		out = append(out, fs[0]+" "+fs[2]+" "+name)
	}
	return out
}

func TestTree(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		c := must1(r.CommitObject(must1(r.Head()).Hash()))
		tree := must1(c.Tree())
		entry := func(m FileMode, h Hash, name string) string { return m.String()[1:] + " " + h.String() + " " + name }

		var entries, files, walked []string
		for _, e := range tree.Entries {
			entries = append(entries, entry(e.Mode, e.Hash, e.Name))
		}
		must(tree.Files().ForEach(func(file *File) error {
			files = append(files, entry(file.Mode, file.Hash, file.Name))
			return nil
		}))
		for w := NewTreeWalker(tree, true); ; {
			name, e, err := w.Next()
			if err == io.EOF {
				break
			}
			walked = append(walked, entry(e.Mode, must1(e.Hash, err), name))
		}
		for _, l := range []struct {
			name      string
			got, want []string
		}{
			{"Entries", entries, f.lsTree("HEAD")},
			{"Files", files, f.lsTree("-r", "HEAD")},
			{"TreeWalker", walked, f.lsTree("-r", "-t", "HEAD")},
		} {
			if !slices.Equal(l.got, l.want) {
				t.Errorf("%s:\n got  %q\n want %q", l.name, l.got, l.want)
			}
		}

		for _, tc := range []struct {
			path, method string
			mode         FileMode
			err          error
		}{
			{"README.md", "File", Regular, nil},
			{"src/sub/b.txt", "File", Regular, nil},
			{"dir with space/naïve.txt", "File", Regular, nil},
			{"link", "File", Symlink, nil},
			{"bin/run.sh", "FindEntry", Executable, nil},
			{"src/sub", "Tree", Dir, nil},
			{"src", "File", 0, ErrFileNotFound},
			{"src/nope/x", "File", 0, ErrFileNotFound},
			{"src/a.txt\x00junk", "File", 0, ErrFileNotFound},
			{"nope", "FindEntry", 0, ErrEntryNotFound},
			{"src/nope/x", "FindEntry", 0, ErrEntryNotFound},
			{"src/a.txt\x00junk", "FindEntry", 0, ErrEntryNotFound},
			{"README.md", "Tree", 0, ErrDirectoryNotFound},
			{"src/missing", "Tree", 0, ErrDirectoryNotFound},
		} {
			var mode FileMode
			var err error
			switch tc.method {
			case "File":
				var file *File
				if file, err = tree.File(tc.path); err == nil {
					mode = file.Mode
					s, want := must1(file.Contents()), f.git(f.dir, "cat-file", "-p", "HEAD:"+tc.path)
					if mode != Symlink {
						want += "\n"
					}
					rd := must1(file.Reader())
					if s != want || must1(file.Size()) != int64(len(s)) || string(must1(io.ReadAll(rd))) != s {
						t.Errorf("contents of %s = %q, want %q", tc.path, s, want)
					}
				}
			case "FindEntry":
				var e *TreeEntry
				if e, err = tree.FindEntry(tc.path); err == nil {
					mode = e.Mode
				}
			case "Tree":
				var sub *Tree
				if sub, err = tree.Tree(tc.path); err == nil && len(sub.Entries) == 1 {
					mode = Dir
				}
			}
			if mode != tc.mode || err != tc.err {
				t.Errorf("%s(%q) = %v, %v; want %v, %v", tc.method, tc.path, mode, err, tc.mode, tc.err)
			}
		}
		if file, err := c.File("src/a.txt"); err != nil || file.Name != "src/a.txt" {
			t.Errorf("Commit.File: %v", err)
		}
		if blob, err := r.BlobObject(NewHash(f.git(f.dir, "rev-parse", "HEAD:src/a.txt"))); err != nil || blob.Size != 8 {
			t.Errorf("BlobObject = %+v, %v", blob, err)
		}
	})
}

// Tree.Tree reports errors other than a missing directory as themselves.
func TestTreeTreeErrors(t *testing.T) {
	f := newFixture(t)
	r := must1(PlainOpen(f.gitDir()))
	tr := must1(must1(r.CommitObject(NewHash(f.git(f.dir, "rev-parse", "HEAD")))).Tree())
	r.Close()
	if _, err := tr.Tree("src/sub"); err != ErrClosed {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}

// Each row's expected commits come from git: git rev-list, or pathLog for
// the path filters.
func TestLog(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		rev := func(revs ...string) (out []string) {
			for _, s := range revs {
				out = append(out, f.git(f.dir, "rev-parse", s))
			}
			return out
		}
		when := must1(r.CommitObject(NewHash(rev("HEAD~2")[0]))).Committer.When
		after, until := when.Add(500*time.Millisecond), when.Add(2*time.Hour)
		unix := func(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
		type row struct {
			name    string
			o       LogOptions
			want    []string
			ordered bool
		}
		rows := []row{
			{"pre-order", LogOptions{}, rev("HEAD", "HEAD^", "HEAD^^", "HEAD^^^", "HEAD^2", "HEAD^2^"), true},
			{"committer time", LogOptions{Order: LogOrderCommitterTime}, f.revList("--date-order", "HEAD"), true},
			{"from", LogOptions{From: NewHash(rev("feature~1")[0])}, f.revList("feature~1"), true},
			{"since and until", LogOptions{Since: &when, Until: &until}, f.revList("--since="+unix(when), "--until="+unix(until), "HEAD"), false},
			{"fractional since", LogOptions{Since: &after}, f.revList("--since="+unix(when.Add(time.Second)), "HEAD"), false},
			{"committer time since and until", LogOptions{Order: LogOrderCommitterTime, Since: &when, Until: &until}, f.revList("--since="+unix(when), "--until="+unix(until), "HEAD"), true},
			{"committer time since, all", LogOptions{Order: LogOrderCommitterTime, All: true, Since: &when}, f.revList("--since="+unix(when), "--all"), true},
			{"prefix", LogOptions{PathFilter: func(s string) bool { return strings.HasPrefix(s, "src/") }}, f.pathLog(f.dir, []string{"HEAD"}, "src"), false},
			{"NUL in FileName", LogOptions{FileName: ptr("src/a.txt\x00junk")}, nil, false},
		}
		for _, all := range []bool{false, true} {
			base := map[bool][]string{false: {"HEAD"}, true: {"--all"}}[all]
			for _, order := range []LogOrder{LogOrderDefault, LogOrderCommitterTime} {
				o := LogOptions{All: all, Order: order}
				rows = append(rows, row{fmt.Sprint(o), o, f.revList(base...), false})
				for _, p := range []string{"src/a.txt", "src/sub/b.txt", "feature.txt", "README.md", "src", "missing"} {
					want := f.pathLog(f.dir, base, p)
					o.FileName, o.PathFilter = &p, nil
					rows = append(rows, row{fmt.Sprintf("%v FileName %s", all, p), o, want, false})
					o.FileName, o.PathFilter = nil, func(s string) bool { return s == p || strings.HasPrefix(s, p+"/") }
					rows = append(rows, row{fmt.Sprintf("%v PathFilter %s", all, p), o, want, false})
				}
			}
		}
		for _, tc := range rows {
			got, want := ids(r.Log(&tc.o)), tc.want
			if !tc.ordered {
				got, want = sorted(got), sorted(want)
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s:\n got  %v\n want %v", tc.name, got, want)
			}
		}

		n := 0
		must(must1(r.Log(&LogOptions{})).ForEach(func(*Commit) error { n++; return ErrStop }))
		var ps []string
		merge := must1(r.CommitObject(NewHash(rev("HEAD")[0])))
		must(merge.Parents().ForEach(func(p *Commit) error { ps = append(ps, p.Hash.String()); return nil }))
		if n != 1 || !slices.Equal(ps, rev("HEAD^1", "HEAD^2")) || merge.NumParents() != 2 {
			t.Errorf("ErrStop after %d commits, Parents = %v", n, ps)
		}
	})
}

func ptr[T any](v T) *T { return &v }

// Log reports a From that is missing, or not a commit, in either order.
func TestLogBadFrom(t *testing.T) {
	f := newFixture(t)
	r := openFixture(t, f.gitDir())
	for _, from := range []Hash{{0xde, 0xad}, NewHash(f.git(f.dir, "rev-parse", "HEAD^{tree}")), NewHash(f.git(f.dir, "rev-parse", "v2"))} {
		for _, order := range []LogOrder{LogOrderDefault, LogOrderCommitterTime} {
			if _, err := r.Log(&LogOptions{From: from, Order: order}); err != ErrObjectNotFound {
				t.Errorf("Log(From: %s, Order: %d) = %v, want ErrObjectNotFound", from, order, err)
			}
		}
	}
}

// A walk longer than a batch (logBatch) agrees with git, across batches
// and with NextHash and Next mixed, and Close ends it.
func TestLongWalk(t *testing.T) {
	f := emptyFixture(t)
	var spec strings.Builder
	for i := range 500 {
		fmt.Fprintf(&spec, "m%d %s=%d\n", i, map[bool]string{true: "f", false: "g"}[i%2 == 0], i)
		if i == 200 {
			for j := range 100 {
				fmt.Fprintf(&spec, "s%d @side %s %s=s%d\n", j, map[bool]string{true: "<m200", false: ""}[j == 0], map[bool]string{true: "f", false: "h"}[j%5 == 0], j)
			}
		}
	}
	spec.WriteString("M <m499,s99 f=merged h=merged\n")
	f.history(spec.String())
	r := openFixture(t, f.dir)
	for _, order := range []LogOrder{LogOrderDefault, LogOrderCommitterTime} {
		all := ids(r.Log(&LogOptions{Order: order}))
		if want := f.revList("--date-order", "HEAD"); len(all) != 601 || !slices.Equal(sorted(all), sorted(want)) ||
			order == LogOrderCommitterTime && !slices.Equal(all, want) {
			t.Errorf("order %d: %d commits, want %d", order, len(all), len(want))
		}
		got := ids(r.Log(&LogOptions{Order: order, FileName: ptr("f")}))
		if want := f.pathLog(f.dir, []string{"HEAD"}, "f"); len(want) <= logBatch || !slices.Equal(sorted(got), sorted(want)) {
			t.Errorf("order %d, f: %d commits, want %d", order, len(got), len(want))
		}

		it := must1(r.Log(&LogOptions{Order: order}))
		var mixed []string
		for i := range 300 {
			if i%2 == 0 {
				mixed = append(mixed, must1(it.NextHash()).String())
			} else {
				mixed = append(mixed, must1(it.Next()).Hash.String())
			}
		}
		it.Close()
		if _, err := it.Next(); !slices.Equal(mixed, all[:300]) || err != io.EOF {
			t.Errorf("order %d: mixed NextHash and Next differ, or Next after Close = %v", order, err)
		}
	}
}

// The path filters select what git log -- path does, except that they never
// prune side branches. When the side branch made the same change as main,
// its commit is selected too.
func TestPathFilterMerges(t *testing.T) {
	for _, tc := range []struct{ spec, want string }{
		// The merge takes the side's change. git log -- f also gives A C.
		{"A f=1\nB g=x\nC @side <A f=2\nM <B,C f=2 g=x", "A C"},
		// Both sides make the same change. git log -- f gives A B, pruning
		// the side branch, and the filters also select C.
		{"A f=1\nB f=2 g=x\nC @side <A f=2\nM <B,C f=2 g=x", "A B C"},
	} {
		f := emptyFixture(t)
		c := f.history(tc.spec)
		r := openFixture(t, f.dir)
		var want []string
		for _, n := range strings.Fields(tc.want) {
			want = append(want, c[n])
		}
		byName := ids(r.Log(&LogOptions{FileName: ptr("f")}))
		byFilter := ids(r.Log(&LogOptions{PathFilter: func(p string) bool { return p == "f" }}))
		if !slices.Equal(sorted(byName), sorted(want)) || !slices.Equal(sorted(byFilter), sorted(want)) {
			t.Errorf("%q: FileName %v, PathFilter %v; want %v", tc.want, byName, byFilter, want)
		}
	}
}

// Path filters on changes of kind and mode agree with git.
func TestPathFilterEdges(t *testing.T) {
	f := emptyFixture(t)
	gitlink := func(b byte) string { return strings.Repeat(string(b), 40) }
	f.history(`
		A d=1 x=1 r=old q=moved
		B -d d/e=2
		C x*=1
		D s#=` + gitlink('a') + `
		E -d/e d=3 s#=` + gitlink('b') + `
		F -r -s
		G -q q2=moved
	`)
	r := openFixture(t, f.dir)
	for _, p := range []string{"d", "d/e", "x", "s", "r", "q", "q2"} {
		want := f.pathLog(f.dir, []string{"HEAD"}, p)
		byName := ids(r.Log(&LogOptions{FileName: &p}))
		byFilter := ids(r.Log(&LogOptions{PathFilter: func(s string) bool { return s == p || strings.HasPrefix(s, p+"/") }}))
		if !slices.Equal(sorted(byName), sorted(want)) || !slices.Equal(sorted(byFilter), sorted(want)) {
			t.Errorf("%s: FileName %v, PathFilter %v; want %v", p, byName, byFilter, want)
		}
	}
}

// PathFilter's argument stays valid after the call, as go-git's does.
func TestPathFilterKeepsPaths(t *testing.T) {
	r := openFixture(t, newFixture(t).dir)
	var kept, copies []string
	ids(r.Log(&LogOptions{All: true, PathFilter: func(p string) bool {
		kept, copies = append(kept, p), append(copies, strings.Clone(p))
		return false
	}}))
	if len(kept) < 2 || !slices.Equal(kept, copies) {
		t.Errorf("paths changed after the calls:\n kept   %q\n copies %q", kept, copies)
	}
}

// As in go-git, a commit must pass both FileName and PathFilter.
func TestFileNameAndPathFilter(t *testing.T) {
	r := openFixture(t, newFixture(t).dir)
	want := ids(r.Log(&LogOptions{FileName: ptr("README.md")}))
	none := ids(r.Log(&LogOptions{FileName: ptr("README.md"), PathFilter: func(string) bool { return false }}))
	both := ids(r.Log(&LogOptions{FileName: ptr("README.md"), PathFilter: func(string) bool { return true }}))
	if len(want) == 0 || len(none) != 0 || !slices.Equal(both, want) {
		t.Errorf("FileName alone %v, with a PathFilter rejecting all %v, accepting all %v", want, none, both)
	}
}

// Grafts cut and reparent history, in both orders and with a filter.
func TestGrafts(t *testing.T) {
	f := newFixture(t)
	head := f.git(f.dir, "rev-parse", "HEAD")
	grafts := f.git(f.dir, "rev-parse", "HEAD^1") + "\n" + // cut main below c6's first parent
		f.git(f.dir, "rev-parse", "feature") + " " + f.git(f.dir, "rev-parse", "v1~1") + "\n" // reparent c5 on c1
	must(os.WriteFile(filepath.Join(f.gitDir(), "info", "grafts"), []byte(grafts), 0o644))
	r := openFixture(t, f.dir)
	for _, order := range []LogOrder{LogOrderDefault, LogOrderCommitterTime} {
		for _, p := range []*string{nil, ptr("src/a.txt"), ptr("feature.txt")} {
			want := f.revList(head)
			if p != nil {
				want = f.pathLog(f.dir, []string{head}, *p)
			}
			if got := ids(r.Log(&LogOptions{Order: order, FileName: p})); !slices.Equal(sorted(got), sorted(want)) {
				t.Errorf("order %d, %v: %v, want %v", order, p, got, want)
			}
		}
	}
}

func TestShallow(t *testing.T) {
	f := newFixture(t)
	f.pack()
	dir := f.bareClone("--depth", "2")
	r := openFixture(t, dir)
	if ok, _ := r.IsShallow(); !ok {
		t.Error("IsShallow = false")
	}
	for _, o := range []LogOptions{{}, {Order: LogOrderCommitterTime}, {FileName: ptr("src/a.txt")},
		{PathFilter: func(s string) bool { return s == "feature.txt" }}} {
		want := lines(f.git(dir, "rev-list", "HEAD"))
		if o.FileName != nil {
			want = f.pathLog(dir, []string{"HEAD"}, "src/a.txt")
		} else if o.PathFilter != nil {
			want = f.pathLog(dir, []string{"HEAD"}, "feature.txt")
		}
		if got := ids(r.Log(&o)); !slices.Equal(sorted(got), sorted(want)) {
			t.Errorf("shallow Log(%+v) = %v, want %v", o, got, want)
		}
	}
}

// failShallow fails to stat the shallow file.
type failShallow struct{ FS }

func (f failShallow) Lstat(name string) (fs.FileInfo, error) {
	if path.Base(name) == "shallow" {
		return nil, fs.ErrPermission
	}
	return f.FS.Lstat(name)
}

func TestIsShallowError(t *testing.T) {
	r := must1(Open(failShallow{FromFS(os.DirFS(newFixture(t).bareClone()))}))
	defer r.Close()
	if ok, err := r.IsShallow(); err == nil {
		t.Errorf("IsShallow = %v, nil; want an error", ok)
	}
}

// In committer-time order, as in git, the walk ends at the first commit
// older than Since. A commit made with a clock behind hides its ancestors,
// even newer ones, which the default order still finds.
func TestSinceClockSkew(t *testing.T) {
	f := emptyFixture(t)
	tree := f.object("tree", "")
	commit := func(at int, parent string) Hash {
		if parent != "" {
			parent = "parent " + parent + "\n"
		}
		return f.object("commit", fmt.Sprintf("tree %s\n%sauthor A <a> %d +0000\ncommitter A <a> %d +0000\n\nc\n", tree, parent, at, at))
	}
	c1 := commit(1700000300, "")
	c2 := commit(1700000100, c1.String())
	c3 := commit(1700000400, c2.String())
	r := openFixture(t, f.dir)
	since := time.Unix(1700000200, 0)
	for order, want := range map[LogOrder][]string{
		LogOrderDefault:       {c3.String(), c1.String()},
		LogOrderCommitterTime: f.revList("--since=1700000200", c3.String()),
	} {
		if got := ids(r.Log(&LogOptions{From: c3, Order: order, Since: &since})); !slices.Equal(got, want) {
			t.Errorf("order %d: %v, want %v", order, got, want)
		}
	}
}

// A walk reaching a missing commit returns the commits before it, then an
// error that is not a fault. In committer-time order, as in git, a commit is
// returned only once its parents are read, so c2 is not.
func TestLogMissingCommit(t *testing.T) {
	dir := bareSkeleton(t)
	empty := putLoose(dir, "tree", nil, nil)
	c1 := Hash{0xc1}
	c2 := putLoose(dir, "commit", fmt.Appendf(nil, "tree %s\nparent %s\nauthor A <a> 2 +0000\ncommitter A <a> 2 +0000\n\nc2\n", empty, c1), nil)
	c3 := putLoose(dir, "commit", fmt.Appendf(nil, "tree %s\nparent %s\nauthor A <a> 3 +0000\ncommitter A <a> 3 +0000\n\nc3\n", empty, c2), nil)
	r := openFixture(t, dir)
	for order, want := range map[LogOrder][]string{
		LogOrderDefault:       {c3.String(), c2.String()},
		LogOrderCommitterTime: {c3.String()},
	} {
		got, err := idsUntil(r.Log(&LogOptions{From: c3, Order: order}))
		if !slices.Equal(got, want) || err == nil || errors.Is(err, ErrFault) {
			t.Errorf("order %d: %v, %v", order, got, err)
		}
	}
}

// Files skips a gitlink, while the walker and FindEntry report it.
func TestSubmoduleEntry(t *testing.T) {
	f := emptyFixture(t)
	f.history("A f=1 sub#=" + strings.Repeat("a", 40))
	tree := must1(must1(openFixture(t, f.dir).CommitObject(NewHash(f.git(f.dir, "rev-parse", "HEAD")))).Tree())
	var files, walked []string
	must(tree.Files().ForEach(func(file *File) error { files = append(files, file.Name); return nil }))
	for w := NewTreeWalker(tree, true); ; {
		name, e, err := w.Next()
		if err == io.EOF {
			break
		}
		walked = append(walked, name+" "+must1(e.Mode, err).String())
	}
	_, ferr := tree.File("sub")
	e, eerr := tree.FindEntry("sub")
	if !slices.Equal(files, []string{"f"}) || !slices.Equal(walked, []string{"f 0100644", "sub 0160000"}) ||
		ferr != ErrFileNotFound || eerr != nil || e.Mode != Submodule {
		t.Errorf("files %v, walked %v, File %v, FindEntry %v %v", files, walked, ferr, e, eerr)
	}
}

func TestGrep(t *testing.T) {
	layouts(t, func(t *testing.T, f *fixture, r *Repository) {
		head := f.git(f.dir, "rev-parse", "HEAD")
		res := must1(r.Grep(&GrepOptions{
			Patterns:  []*regexp.Regexp{regexp.MustCompile(`version = "`)},
			PathSpecs: []*regexp.Regexp{regexp.MustCompile(`\.txt$`)},
		}))
		if want := []GrepResult{{FileName: "src/sub/b.txt", LineNumber: 2, Content: `version = "2.0"`, TreeName: head}}; !slices.Equal(res, want) {
			t.Errorf("Grep = %+v", res)
		}
		res = must1(r.Grep(&GrepOptions{ReferenceName: "refs/tags/v1", Patterns: []*regexp.Regexp{regexp.MustCompile(`^alpha`)}}))
		if len(res) != 1 || res[0].FileName != "src/a.txt" || res[0].TreeName != "refs/tags/v1" {
			t.Errorf("Grep at v1 = %+v", res)
		}
		if _, err := r.Grep(&GrepOptions{CommitHash: NewHash(head), ReferenceName: Main}); err != ErrHashOrReference {
			t.Errorf("ambiguous Grep: %v", err)
		}
	})
}

func TestConcurrentUse(t *testing.T) {
	r := openFixture(t, newFixture(t).dir)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				if err := must1(r.Log(&LogOptions{})).ForEach(func(c *Commit) error {
					_, err := c.File("README.md")
					return err
				}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

// A Repository that is never closed releases its instance once collected.
func TestUnclosedRepositoryIsReleased(t *testing.T) {
	f := newFixture(t)
	base := liveInstances.Load()
	for range 20 {
		must1(must1(PlainOpen(f.dir)).Head())
	}
	for deadline := time.Now().Add(10 * time.Second); liveInstances.Load() != base; {
		if time.Now().After(deadline) {
			t.Fatalf("%d instances still live", liveInstances.Load()-base)
		}
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}

func TestComputeHash(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"README.md", "src/a.txt"} {
		if got, want := ComputeHash(BlobObject, must1(os.ReadFile(filepath.Join(f.dir, p)))).String(), f.git(f.dir, "hash-object", p); got != want {
			t.Errorf("ComputeHash(%s) = %s, want %s", p, got, want)
		}
	}
}

// recordFS fails the test if a name with ".." reaches it.
type recordFS struct {
	FS
	t *testing.T
}

func (r recordFS) check(name string) {
	if strings.Contains(name, "..") {
		r.t.Errorf("FS saw %q", name)
	}
}

func (r recordFS) Open(name string) (FSFile, error)       { r.check(name); return r.FS.Open(name) }
func (r recordFS) Stat(name string) (fs.FileInfo, error)  { r.check(name); return r.FS.Stat(name) }
func (r recordFS) Lstat(name string) (fs.FileInfo, error) { r.check(name); return r.FS.Lstat(name) }
func (r recordFS) Readlink(name string) (string, error)   { r.check(name); return r.FS.Readlink(name) }

// An alternates file can name a directory outside the repository, but the
// FS never sees a name that leaves its root.
func TestAlternatesOutside(t *testing.T) {
	f := newFixture(t)
	bare := f.bareClone()
	must(os.WriteFile(filepath.Join(bare, "objects/info/alternates"), []byte("../../../outside/objects\n"), 0o644))
	r := must1(Open(recordFS{FromFS(os.DirFS(bare)), t}))
	defer r.Close()
	if _, err := r.CommitObject(NewHash(f.git(f.dir, "rev-parse", "HEAD"))); err != nil {
		t.Error(err)
	}
}
