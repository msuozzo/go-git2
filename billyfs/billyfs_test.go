package billyfs_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"

	"github.com/msuozzo/go-git2"
	"github.com/msuozzo/go-git2/billyfs"
)

func must1[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestMemfs reads a packed bare repository copied into a memfs.
func TestMemfs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, step := range []struct{ stdin, args string }{
		{"", "init -q --bare -b main"},
		{"commit refs/heads/main\ncommitter A <a@b> 0 +0000\ndata 4\none\nM 100644 inline f.txt\ndata 6\nhello\n", "fast-import --quiet"},
		{"", "repack -adq"},
	} {
		cmd := exec.Command("git", strings.Fields(step.args)...)
		cmd.Dir, cmd.Stdin, cmd.Env = dir, strings.NewReader(step.stdin), append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", step.args, err, out)
		}
	}
	mfs := memfs.New()
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return util.WriteFile(mfs, must1(filepath.Rel(dir, p)), must1(os.ReadFile(p)), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	r := must1(git2.Open(billyfs.New(mfs)))
	defer r.Close()
	c := must1(r.CommitObject(must1(r.Head()).Hash()))
	if s := must1(must1(c.File("f.txt")).Contents()); s != "hello\n" || c.Message != "one\n" {
		t.Errorf("contents %q, message %q", s, c.Message)
	}
}
