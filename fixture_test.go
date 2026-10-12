package git2

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// must panics on an error that only a broken test setup would cause.
func must(err error) {
	if err != nil {
		panic(err)
	}
}

func must1[T any](v T, err error) T {
	must(err)
	return v
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// ids returns the ids of the commits it yields.
func ids(it *CommitIter, err error) []string {
	out, err := idsUntil(it, err)
	must(err)
	return out
}

// idsUntil returns the ids it yields before an error, and the error.
func idsUntil(it *CommitIter, err error) ([]string, error) {
	var out []string
	if err != nil {
		return nil, err
	}
	return out, it.ForEach(func(c *Commit) error {
		out = append(out, c.Hash.String())
		return nil
	})
}

// fixture is a repository built with the git CLI, which also serves as the
// oracle for expected results.
type fixture struct {
	t    testing.TB
	dir  string // working tree
	tick int
}

func gitAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// git runs git in dir and returns its trimmed output.
func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	date := fmt.Sprintf("%d +0200", 1700000000+f.tick*3600)
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
		"-c", "init.defaultBranch=main", "-c", "advice.graftFileDeprecated=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ada Author", "GIT_AUTHOR_EMAIL=ada@example.com",
		"GIT_COMMITTER_NAME=Cy Committer", "GIT_COMMITTER_EMAIL=cy@example.com",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// revList returns git rev-list's output for args.
func (f *fixture) revList(args ...string) []string {
	return lines(f.git(f.dir, append([]string{"rev-list"}, args...)...))
}

// pathLog returns, per git, the commits that the path filters select on path
// from revs, in the repository at dir: git log -- path without its pruning
// of side branches. Those are the commits with at most one parent that
// git rev-list --full-history lists, and the merges it lists that differ
// from all of their parents.
func (f *fixture) pathLog(dir string, revs []string, path string) []string {
	list := func(kind string) []string {
		return lines(f.git(dir, append(append([]string{"rev-list", "--full-history", kind}, revs...), "--", path)...))
	}
	out := list("--no-merges")
	for _, m := range list("--merges") {
		same := func(p string) bool {
			return exec.Command("git", "-C", dir, "diff", "--quiet", p, m, "--", path).Run() == nil
		}
		if !slices.ContainsFunc(strings.Fields(f.git(dir, "rev-parse", m+"^@")), same) {
			out = append(out, m)
		}
	}
	return out
}

// emptyFixture returns a fixture with an empty repository.
func emptyFixture(t testing.TB) *fixture {
	gitAvailable(t)
	f := &fixture{t: t, dir: t.TempDir()}
	f.git(f.dir, "init", "-q")
	return f
}

// history writes commits with one git fast-import and returns their ids by
// name. Each line of spec is a commit:
//
//	name [@branch] [<parent[,parent...]] op...
//
// A commit goes on main unless @branch names another branch, and continues
// the branch unless <parents names its parents. Each op changes a path:
// path=text writes text and a newline, path*=text does so with the
// executable bit, path@=target writes a symbolic link, path#=hex a gitlink,
// and -path deletes. A token may be double-quoted, Go-style, in parts. Each
// commit is an hour after the last, at +0200.
func (f *fixture) history(spec string) map[string]string {
	f.t.Helper()
	var in strings.Builder
	marks := map[string]int{}
	for _, line := range lines(strings.TrimSpace(spec)) {
		tok := tokens(line)
		name, branch, ops := tok[0], "main", tok[1:]
		var parents []string
		for len(ops) > 0 && (ops[0][0] == '@' || ops[0][0] == '<') {
			if ops[0][0] == '@' {
				branch = ops[0][1:]
			} else {
				parents = strings.Split(ops[0][1:], ",")
			}
			ops = ops[1:]
		}
		marks[name] = len(marks) + 1
		f.tick++
		when := fmt.Sprintf("%d +0200", 1700000000+f.tick*3600)
		fmt.Fprintf(&in, "commit refs/heads/%s\nmark :%d\nauthor Ada Author <ada@example.com> %s\n"+
			"committer Cy Committer <cy@example.com> %s\ndata %d\n%s\n", branch, marks[name], when, when, len(name), name)
		for i, p := range parents {
			fmt.Fprintf(&in, "%s :%d\n", map[bool]string{true: "from", false: "merge"}[i == 0], marks[p])
		}
		for _, op := range ops {
			if path, ok := strings.CutPrefix(op, "-"); ok {
				fmt.Fprintf(&in, "D %s\n", path)
				continue
			}
			path, text, _ := strings.Cut(op, "=")
			mode, data := "100644", text+"\n"
			switch path[len(path)-1] {
			case '*':
				mode = "100755"
			case '@':
				mode, data = "120000", text
			case '#':
				fmt.Fprintf(&in, "M 160000 %s %s\n", text, path[:len(path)-1])
				continue
			}
			path = strings.TrimRight(path, "*@")
			fmt.Fprintf(&in, "M %s inline %s\ndata %d\n%s\n", mode, path, len(data), data)
		}
	}
	marksFile := filepath.Join(f.t.TempDir(), "marks")
	cmd := exec.Command("git", "fast-import", "--quiet", "--date-format=raw", "--export-marks="+marksFile)
	cmd.Dir, cmd.Stdin = f.dir, strings.NewReader(in.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git fast-import: %v\n%s", err, out)
	}
	byMark := map[string]string{}
	for _, l := range lines(strings.TrimSpace(string(must1(os.ReadFile(marksFile))))) {
		m, id, _ := strings.Cut(l, " ")
		byMark[m] = id
	}
	out := map[string]string{}
	for name, m := range marks {
		out[name] = byMark[":"+strconv.Itoa(m)]
	}
	return out
}

// tokens splits s at spaces outside double-quoted parts, unquoting them.
func tokens(s string) []string {
	var out []string
	var cur strings.Builder
	inTok := false
	for len(s) > 0 {
		switch {
		case s[0] == '"':
			q := must1(strconv.QuotedPrefix(s))
			cur.WriteString(must1(strconv.Unquote(q)))
			s, inTok = s[len(q):], true
		case unicode.IsSpace(rune(s[0])):
			if inTok {
				out, inTok = append(out, cur.String()), false
				cur.Reset()
			}
			s = s[1:]
		default:
			cur.WriteByte(s[0])
			s, inTok = s[1:], true
		}
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out
}

// newFixture builds, with a lightweight tag v1 at c2 and annotated tags v2
// (c6) and v2-tree (a tree):
//
//	c1 -- c2 -- c4 ------ c6 (main)
//	        \            /
//	         c3 -- c5 --+ (feature)
func newFixture(t testing.TB) *fixture {
	f := emptyFixture(t)
	c := f.history(`
		c1 README.md="# fixture" src/a.txt=alpha "src/sub/b.txt=beta\nversion = \"1.0\"" bin/run.sh*="#!/bin/sh\necho hi" link@=README.md "dir with space/naïve.txt=unicode"
		c2 src/a.txt="alpha 2"
		c3 @feature <c2 feature.txt=feature "src/sub/b.txt=beta\nversion = \"2.0\""
		c4 README.md="# fixture\n\nmore"
		c5 @feature feature.txt="feature 2"
		c6 <c4,c5 feature.txt="feature 2" "src/sub/b.txt=beta\nversion = \"2.0\""
	`)
	f.git(f.dir, "reset", "-q", "--hard", "main")
	f.git(f.dir, "tag", "v1", c["c2"])
	f.tick++
	f.git(f.dir, "tag", "-a", "-m", "release v2\n\nnotes", "v2", c["c6"])
	f.git(f.dir, "tag", "-a", "-m", "a tree", "v2-tree", c["c6"]+"^{tree}")
	return f
}

// gitDir returns the repository's .git directory.
func (f *fixture) gitDir() string { return filepath.Join(f.dir, ".git") }

// pack repacks all objects and refs, the way clones and gc leave them.
func (f *fixture) pack() {
	f.git(f.dir, "repack", "-adq")
	f.git(f.dir, "pack-refs", "--all")
}

// bareClone returns a bare clone, packed like a fresh clone.
func (f *fixture) bareClone(extra ...string) string {
	dst := filepath.Join(f.t.TempDir(), "clone.git")
	f.git(f.dir, append([]string{"clone", "-q", "--bare"}, append(extra, "file://"+f.dir, dst)...)...)
	return dst
}

func openFixture(t testing.TB, path string) *Repository {
	r := must1(PlainOpen(path))
	t.Cleanup(func() { r.Close() })
	return r
}

// layouts runs fn against the fixture in several storage layouts.
func layouts(t *testing.T, fn func(t *testing.T, f *fixture, r *Repository)) {
	f := newFixture(t)
	t.Run("loose", func(t *testing.T) { fn(t, f, openFixture(t, f.dir)) })
	bare := f.bareClone()
	t.Run("bare-packed", func(t *testing.T) { fn(t, f, openFixture(t, bare)) })
	t.Run("iofs", func(t *testing.T) {
		r := must1(Open(FromFS(os.DirFS(bare))))
		defer r.Close()
		fn(t, f, r)
	})
}
