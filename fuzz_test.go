package git2

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Fuzz targets for the Go code that reads repository data, such as the
// history walk and the object decoders. They run through the public API, so
// they also exercise libgit2 as compiled for wasm32. Failing inputs belong
// in testdata/fuzz. Errors are expected, but a recovered panic (ErrFault)
// is a bug, and so is a crash. Run them with:
//
//	go test -run '^$' -fuzz '^FuzzLog$' -fuzztime 10m -fuzzminimizetime 0
//
// Without -fuzzminimizetime 0, minimizing each new input dominates the run.

// bareSkeleton returns an empty bare repository whose main branch is unborn.
func bareSkeleton(t testing.TB) string {
	dir := t.TempDir()
	must(os.MkdirAll(filepath.Join(dir, "objects/pack"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "refs/heads"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\tbare = true\n\trepositoryformatversion = 0\n"), 0o644))
	return dir
}

// setMain points dir's main branch at h.
func setMain(dir string, h Hash) {
	must(os.WriteFile(filepath.Join(dir, "refs/heads/main"), hex.AppendEncode(nil, h[:]), 0o644))
}

func noFault(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, ErrFault) {
		t.Fatal(err)
	}
}

// putLoose writes a loose object, under id if it is not nil, and returns
// its id.
func putLoose(dir, typ string, body []byte, id *Hash) Hash {
	raw := append(fmt.Appendf(nil, "%s %d\x00", typ, len(body)), body...)
	h := Hash(sha1.Sum(raw))
	if id != nil {
		h = *id
	}
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(raw)
	w.Close()
	hx := hex.EncodeToString(h[:])
	must(os.MkdirAll(filepath.Join(dir, "objects", hx[:2]), 0o755))
	must(os.WriteFile(filepath.Join(dir, "objects", hx[:2], hx[2:]), z.Bytes(), 0o644))
	return h
}

// FuzzLog walks history over loose objects built from the input. The input
// is a control byte followed by records.
//
// In the control byte, bit 0 selects committer-time order, bits 1-2 select
// no filter, FileName or PathFilter, and bit 3 skips hash verification.
//
// Each record is a type byte (mod 3: commit, tree, blob), a u16 length and
// a body. In commit bodies, "@k" is replaced by the hex id of object k. In
// tree bodies, 0xFE followed by k is replaced by k's raw id. The last commit
// is main.
func FuzzLog(f *testing.F) {
	rec := func(typ byte, body string) []byte {
		return append(binary.LittleEndian.AppendUint16([]byte{typ}, uint16(len(body))), body...)
	}
	// The seed has blob 0, trees 1 (b.txt: 0), 2 (e: 1) and 3 (d: 2),
	// commit 4 at tree 3, and commit 5 at tree 1 with parent 4.
	seed := slices.Concat(
		rec(2, "hello\n"),
		rec(1, "100644 b.txt\x00\xfe\x00"),
		rec(1, "40000 e\x00\xfe\x01"),
		rec(1, "40000 d\x00\xfe\x02"),
		rec(0, "tree @\x03\nauthor A <a> 1 +0000\ncommitter A <a> 1 +0000\n\nc1\n"),
		rec(0, "tree @\x01\nparent @\x04\nauthor A <a> 2 +0000\ncommitter A <a> 2 +0000\n\nc2\n"),
	)
	for ctl := range byte(16) {
		f.Add(append([]byte{ctl}, seed...))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 1 {
			return
		}
		ctl, data := data[0], data[1:]
		dir := bareSkeleton(t)
		var ids []Hash
		var types []string
		for len(data) >= 3 && len(ids) < 64 {
			typ := []string{"commit", "tree", "blob"}[data[0]%3]
			n := min(int(binary.LittleEndian.Uint16(data[1:])), len(data)-3)
			data = data[3:]
			var body []byte
			for j := 0; j < n; j++ {
				c := data[j]
				switch {
				case len(ids) > 0 && typ == "commit" && c == '@' && j+1 < n:
					j++
					id := ids[int(data[j])%len(ids)]
					body = hex.AppendEncode(body, id[:])
				case len(ids) > 0 && typ == "tree" && c == 0xFE && j+1 < n:
					j++
					id := ids[int(data[j])%len(ids)]
					body = append(body, id[:]...)
				default:
					body = append(body, c)
				}
			}
			ids = append(ids, putLoose(dir, typ, body, nil))
			types = append(types, typ)
			data = data[n:]
		}
		head := -1
		for k := len(ids) - 1; k >= 0 && head < 0; k-- {
			if types[k] == "commit" {
				head = k
			}
		}
		if head >= 0 {
			setMain(dir, ids[head])
		}
		r, err := PlainOpenWithOptions(dir, &OpenOptions{SkipHashVerification: ctl&8 != 0})
		if err != nil {
			noFault(t, err)
			return
		}
		defer r.Close()
		for k, id := range ids {
			if types[k] != "tree" {
				continue
			}
			tr, err := r.TreeObject(id)
			noFault(t, err)
			if err == nil {
				noFault(t, tr.Files().ForEach(func(*File) error { return nil }))
				_, err = tr.File("d/e/b.txt")
				noFault(t, err)
			}
		}
		if head < 0 {
			return
		}
		o := &LogOptions{From: ids[head]}
		if ctl&1 != 0 {
			o.Order = LogOrderCommitterTime
		}
		switch ctl >> 1 & 3 {
		case 1:
			name := "d/e/b.txt"
			o.FileName = &name
		case 2:
			o.PathFilter = func(p string) bool { return len(p)&1 == 1 }
		}
		it, err := r.Log(o)
		if err != nil {
			noFault(t, err)
			return
		}
		for range 10000 {
			if _, err := it.Next(); err != nil {
				noFault(t, err)
				break
			}
		}
	})
}

// A tree stored under an id that its contents refer to contains itself.
// Verification rejects it. Without verification, walks stop at the depth
// limit instead of recursing until the stack runs out.
func TestSelfContainingTree(t *testing.T) {
	dir := bareSkeleton(t)
	self := Hash{0xaa, 0xbb}
	putLoose(dir, "tree", append([]byte("40000 d\x00"), self[:]...), &self)
	commit := putLoose(dir, "commit", fmt.Appendf(nil, "tree %s\nauthor A <a> 1 +0000\ncommitter A <a> 1 +0000\n\nc\n", self), nil)
	setMain(dir, commit)

	if _, err := openFixture(t, dir).TreeObject(self); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("verifying: TreeObject: got %v, want a hash mismatch", err)
	}
	r := must1(PlainOpenWithOptions(dir, &OpenOptions{SkipHashVerification: true}))
	defer r.Close()
	if err := must1(r.TreeObject(self)).Files().ForEach(func(*File) error { return nil }); err == nil || errors.Is(err, ErrFault) {
		t.Errorf("Files: got %v, want a depth error", err)
	}
	if _, err := idsUntil(r.Log(&LogOptions{From: commit, PathFilter: func(string) bool { return false }})); err == nil || errors.Is(err, ErrFault) {
		t.Errorf("Log: got %v, want a depth error", err)
	}
}

// git reads a commit's tree from its first line only, and its parents from
// the lines right after it. Later tree and parent lines are ignored.
func TestCommitHeaderOrder(t *testing.T) {
	f := emptyFixture(t)
	dir := bareSkeleton(t)
	blob := putLoose(dir, "blob", []byte("x\n"), nil)
	withX := putLoose(dir, "tree", append([]byte("100644 x\x00"), blob[:]...), nil)
	empty := putLoose(dir, "tree", nil, nil)
	c1 := putLoose(dir, "commit", fmt.Appendf(nil, "tree %s\nauthor A <a> 1 +0000\ncommitter A <a> 1 +0000\n\nc1\n", empty), nil)
	c2 := putLoose(dir, "commit", fmt.Appendf(nil,
		"tree %s\nparent %s\ntree %s\nauthor A <a> 2 +0000\nparent %s\ncommitter A <a> 2 +0000\n\nc2\n",
		withX, c1, empty, withX), nil)
	setMain(dir, c2)

	r := openFixture(t, dir)
	if c := must1(r.CommitObject(c2)); c.TreeHash != withX || !slices.Equal(c.ParentHashes, []Hash{c1}) {
		t.Errorf("tree %s, parents %v; want %s, [%s]", c.TreeHash, c.ParentHashes, withX, c1)
	}
	if got, want := ids(r.Log(&LogOptions{From: c2, FileName: ptr("x")})), lines(f.git(dir, "rev-list", "main", "--", "x")); !slices.Equal(got, want) {
		t.Errorf("Log -- x: got %q, want %q (git rev-list)", got, want)
	}
}
