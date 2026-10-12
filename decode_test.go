package git2

import (
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// object writes content as an object of type typ without git's checks, so
// that malformed objects can be stored, and returns its id.
func (f *fixture) object(typ, content string) Hash {
	cmd := exec.Command("git", "hash-object", "-t", typ, "-w", "--literally", "--stdin")
	cmd.Dir, cmd.Stdin = f.dir, strings.NewReader(content)
	return NewHash(strings.TrimSpace(string(must1(cmd.Output()))))
}

// treeEntry formats one raw tree entry.
func treeEntry(mode, name string, id Hash) string { return mode + " " + name + "\x00" + string(id[:]) }

// git, libgit2 and go-git all report non-canonical modes in their canonical
// form.
func TestTreeModes(t *testing.T) {
	f := newFixture(t)
	blob := NewHash(f.git(f.dir, "rev-parse", "HEAD:README.md"))
	sub := f.object("tree", treeEntry("100664", "c.txt", blob))
	tree := f.object("tree", treeEntry("100664", "a.txt", blob)+treeEntry("100775", "b.sh", blob)+treeEntry("40000", "d", sub))
	want := map[string]FileMode{}
	for _, l := range lines(f.git(f.dir, "ls-tree", "-r", "-t", tree.String())) {
		meta, name, _ := strings.Cut(l, "\t")
		want[name] = FileMode(must1(strconv.ParseUint(strings.Fields(meta)[0], 8, 32)))
	}
	if want["a.txt"] != Regular || want["b.sh"] != Executable || want["d/c.txt"] != Regular {
		t.Fatalf("git ls-tree: %v", want)
	}
	tr := must1(openFixture(t, f.dir).TreeObject(tree))
	got := map[string]FileMode{}
	for _, e := range tr.Entries {
		got[e.Name] = e.Mode
	}
	must(tr.Files().ForEach(func(fl *File) error {
		if fl.Mode != want[fl.Name] {
			t.Errorf("Files: %s is %v, want %v", fl.Name, fl.Mode, want[fl.Name])
		}
		return nil
	}))
	for w := NewTreeWalker(tr, true); ; {
		name, e, err := w.Next()
		if err == io.EOF {
			break
		}
		got[name] = must1(e, err).Mode
	}
	for name, m := range want {
		e, err := tr.FindEntry(name)
		if got[name] != m || err != nil || e.Mode != m {
			t.Errorf("%s: Entries and TreeWalker %v, FindEntry %v %v; want %v", name, got[name], e, err, m)
		}
		if fl, err := tr.File(name); m != Dir && (err != nil || fl.Mode != m) {
			t.Errorf("File(%s) = %v, %v; want mode %v", name, fl, err, m)
		}
	}
}

func TestMalformedObjects(t *testing.T) {
	f := newFixture(t)
	head, tree := f.git(f.dir, "rev-parse", "HEAD"), f.git(f.dir, "rev-parse", "HEAD^{tree}")
	blob := NewHash(f.git(f.dir, "rev-parse", "HEAD:README.md"))
	sig := "A U Thor <a@example.com> 1700000000 +0000"
	badParent := "tree " + tree + "\nparent " + strings.Repeat("z", 40) + "\n\nmsg\n"
	r := openFixture(t, f.dir)
	for _, tc := range []struct {
		typ, content string
		want         error
	}{
		{"commit", "", ErrMalformedCommit},
		{"commit", "author " + sig + "\ncommitter " + sig + "\n\nmsg\n", ErrMalformedCommit}, // no tree
		{"commit", "parent " + head + "\ntree " + tree + "\n\nmsg\n", ErrMalformedCommit},
		{"commit", "tree " + tree[:39] + "\n\nmsg\n", ErrMalformedCommit},
		{"commit", "tree " + tree + "0\n\nmsg\n", ErrMalformedCommit},
		{"commit", badParent, ErrMalformedCommit},
		{"tree", "10064x a\x00" + string(blob[:]), ErrMalformedTree},
		{"tree", "100644", ErrMalformedTree},
		{"tree", "100644 a", ErrMalformedTree},
		{"tree", "100644 \x00" + string(blob[:]), ErrMalformedTree},
		{"tree", "100644 a\x00" + string(blob[:19]), ErrMalformedTree},
		{"tree", treeEntry("100644", "a", blob) + "100644 b", ErrMalformedTree},
		{"tag", "", ErrMalformedTag},
		{"tag", "type commit\nobject " + head + "\ntag t\n\nmsg\n", ErrMalformedTag},
		{"tag", "object " + head + "\n", ErrMalformedTag},
		{"tag", "object " + head + "\ntype commit\n\nmsg\n", ErrMalformedTag},
		{"tag", "object " + head[:39] + "\ntype commit\ntag t\n\nmsg\n", ErrMalformedTag},
		{"tag", "object " + head + "\ntype commit\ntagger " + sig + "\n\nmsg\n", ErrMalformedTag},
		{"tag", "object " + head + "\ntype nope\ntag t\n\nmsg\n", ErrInvalidType},
	} {
		id := f.object(tc.typ, tc.content)
		var err error
		switch tc.typ {
		case "commit":
			_, err = r.CommitObject(id)
		case "tree":
			_, err = r.TreeObject(id)
		default:
			_, err = r.TagObject(id)
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s %q: %v, want %v", tc.typ, tc.content, err, tc.want)
		}
	}
	// The walk itself reaches a bad parent only when it visits it, but the
	// commit that names it fails to decode.
	if _, err := idsUntil(r.Log(&LogOptions{From: f.object("commit", badParent)})); !errors.Is(err, ErrMalformedCommit) {
		t.Errorf("Log over a bad parent line: %v", err)
	}
}

// git and go-git take the signature from the last line that starts one, in
// any of the formats they recognize.
func TestTagSignature(t *testing.T) {
	f := newFixture(t)
	r := openFixture(t, f.dir)
	hdr := "object " + f.git(f.dir, "rev-parse", "HEAD") + "\ntype commit\ntag t\ntagger A <a@example.com> 1700000000 +0000\n\n"
	ssh := "-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"
	pgp := "-----BEGIN PGP SIGNATURE-----\niQEz\n-----END PGP SIGNATURE-----\n"
	for _, tc := range []struct{ msg, sig string }{
		{"release\n", ssh},
		{"release\n", pgp},
		{"release\n" + pgp, ssh},
		{"quoting -----BEGIN SSH SIGNATURE----- inline\n", ""},
		{"no signature\n", ""},
	} {
		if tag := must1(r.TagObject(f.object("tag", hdr+tc.msg+tc.sig))); tag.Message != tc.msg || tag.PGPSignature != tc.sig {
			t.Errorf("message %q, signature %q; want %q, %q", tag.Message, tag.PGPSignature, tc.msg, tc.sig)
		}
	}
}

// Only the author and committer lines in their places count, as in go-git
// and libgit2, and the walk reads the committer time from the same line.
func TestCommitSignaturePositions(t *testing.T) {
	f := newFixture(t)
	r := openFixture(t, f.dir)
	commit := func(hdr ...string) Hash {
		return f.object("commit", "tree "+f.git(f.dir, "rev-parse", "HEAD^{tree}")+"\n"+strings.Join(hdr, "\n")+"\n\nmsg\n")
	}
	first, second := "First <first@example.com> 1700000000 +0000", "Second <second@example.com> 1800000000 +0000"
	id := commit("author "+first, "committer "+first, "committer "+second, "author "+second)
	since := time.Unix(1750000000, 0)
	if c := must1(r.CommitObject(id)); c.Author.Name != "First" || c.Committer.Name != "First" {
		t.Errorf("author %q, committer %q; want the first of each", c.Author.Name, c.Committer.Name)
	}
	if got := ids(r.Log(&LogOptions{From: id, Since: &since})); len(got) != 0 {
		t.Errorf("Since after the committer time selected %v", got)
	}
	// A second author takes the committer's place, so there is none.
	if c := must1(r.CommitObject(commit("author "+first, "author "+second, "committer "+second))); c.Author.Name != "First" || c.Committer != (Signature{}) {
		t.Errorf("author %q, committer %+v; want First and none", c.Author.Name, c.Committer)
	}
}

// Multi-line headers keep their continuation lines without the leading
// space, as in go-git, and a header without a value is an empty line.
func TestCommitMultilineHeaders(t *testing.T) {
	f := newFixture(t)
	r := openFixture(t, f.dir)
	tree := "tree " + f.git(f.dir, "rev-parse", "HEAD^{tree}") + "\n"
	for _, tc := range []struct{ hdr, sig, mergetag string }{
		{"gpgsig a\n b\n c\n", "a\nb\nc\n", ""},
		{"gpgsig a\nencoding x\n", "a\n", ""},
		{"gpgsig\n", "\n", ""},
		{"gpgsig a\n b\ngpgsig c\n", "a\nb\nc\n", ""},
		{"mergetag a\n b\nmergetag c\n", "", "a\nb\n"},
		{"mergetag\n x\n", "", "\nx\n"},
	} {
		c := must1(r.CommitObject(f.object("commit", tree+tc.hdr+"\nmsg\n")))
		if c.PGPSignature != tc.sig || c.MergeTag != tc.mergetag || c.Message != "msg\n" {
			t.Errorf("%q: signature %q, mergetag %q, message %q", tc.hdr, c.PGPSignature, c.MergeTag, c.Message)
		}
	}
}

func TestSignatureDecode(t *testing.T) {
	at := time.Unix(1700000000, 0)
	zone := func(off int) time.Time { return at.In(time.FixedZone("", off)) }
	for _, tc := range []struct {
		in, name, mail string
		when           time.Time
	}{
		{"A U Thor <a@example.com> 1700000000 +0130", "A U Thor", "a@example.com", zone(5400)},
		{"A <a> 1700000000 -0030", "A", "a", zone(-1800)},
		{"A <a> 1700000000 -0130", "A", "a", zone(-5400)},
		{"A <a> +1700000000 +0000", "A", "a", zone(0)},
		{"  A  <a> 1700000000 +0000", "A", "a", zone(0)},
		{"A <a> 1700000000 +0a00", "A", "a", at.In(time.UTC)},
		{"A <a> 1700000000 +01", "A", "a", at.In(time.UTC)},
		{"A <a> 1700000000", "A", "a", at.In(time.UTC)},
		{"A <a> 99999999999999999999 +0000", "A", "a", time.Time{}},
		{"A <a> 17x +0000", "A", "a", time.Time{}},
		{"A <a>", "A", "a", time.Time{}},
		{"no email", "", "", time.Time{}},
		{"A >a< 1700000000 +0000", "", "", time.Time{}},
	} {
		var s Signature
		s.decode(tc.in)
		_, gotOff := s.When.Zone()
		_, wantOff := tc.when.Zone()
		if s.Name != tc.name || s.Email != tc.mail || !s.When.Equal(tc.when) || gotOff != wantOff ||
			(tc.when.Location() == time.UTC) != (s.When.Location() == time.UTC) {
			t.Errorf("%q: got %q %q %v; want %q %q %v", tc.in, s.Name, s.Email, s.When, tc.name, tc.mail, tc.when)
		}
	}
}

func TestCommitString(t *testing.T) {
	c := &Commit{Hash: NewHash(strings.Repeat("ab", 20)), Message: "subject\n\nbody\n",
		Author: Signature{Name: "A", Email: "a@example.com", When: time.Unix(0, 0).UTC()}}
	want := "commit " + hex.EncodeToString(c.Hash[:]) + "\nAuthor: A <a@example.com>\n" +
		"Date:   Thu Jan 01 00:00:00 1970 +0000\n\n    subject\n\n    body\n\n"
	if got := c.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// IsRegular, ToOSFileMode's error and IsHash follow go-git.
func TestGoGitHelpers(t *testing.T) {
	for m, want := range map[FileMode]bool{Regular: true, Deprecated: true, Executable: false, Symlink: false, Dir: false, Submodule: false, Empty: false} {
		if m.IsRegular() != want {
			t.Errorf("%v.IsRegular() = %v", m, !want)
		}
	}
	if _, err := FileMode(0100600).ToOSFileMode(); err == nil || err.Error() != "malformed mode (0100600)" {
		t.Errorf("ToOSFileMode of an unknown mode: %v", err)
	}
	a := strings.Repeat("a", 39)
	for s, want := range map[string]bool{strings.Repeat("ab", 20): true, strings.Repeat("AB", 20): true, a: false, a + "aa": false, a + "g": false, "": false} {
		if IsHash(s) != want {
			t.Errorf("IsHash(%q) = %v", s, !want)
		}
	}
}
