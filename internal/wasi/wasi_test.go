package wasi

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// mem is linear memory with capacity to spare, like the module's
// reservation.
type mem struct{ b []byte }

func (m *mem) Slice() *[]byte { return &m.b }

func must1[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// newHost returns a host over fsys, or over a directory holding an empty
// objects/ when fsys is nil.
func newHost(t *testing.T, fsys FS) (*Host, *mem) {
	if fsys == nil {
		dir := t.TempDir()
		if err := os.Mkdir(dir+"/objects", 0o755); err != nil {
			t.Fatal(err)
		}
		root := must1(os.OpenRoot(dir))
		t.Cleanup(func() { root.Close() })
		fsys = DirFS{Root: root}
	}
	m := &mem{b: make([]byte, 4096, 1<<20)}
	return New(m, fsys, "/repo"), m
}

// put writes s to memory at 1000 and returns its address and length.
func (m *mem) put(s string) (int32, int32) { return 1000, int32(copy(m.b[1000:], s)) }

// open opens path on the preopen and returns its fd.
func open(t *testing.T, h *Host, m *mem, path string) int32 {
	ptr, n := m.put(path)
	if got := h.Xpath_open(preopenFD, 0, ptr, n, 0, 0, 0, 0, 2000); got != errnoSuccess {
		t.Fatalf("path_open(%s) = %d", path, got)
	}
	return int32(binary.LittleEndian.Uint32(m.b[2000:]))
}

func panics(f func()) (p bool) {
	defer func() { p = recover() != nil }()
	f()
	return false
}

func TestAccessPastMemoryPanics(t *testing.T) {
	h, m := newHost(t, nil)
	end := int32(len(m.b))
	if !panics(func() { h.Xrandom_get(end, 4) }) || !panics(func() { h.Xclock_time_get(0, 0, end-4) }) ||
		h.Xrandom_get(end-4, 4) != errnoSuccess {
		t.Error("accesses past the end of memory did not panic, or one up to the end failed")
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

func (r recordFS) Open(name string) (File, error)         { r.check(name); return r.FS.Open(name) }
func (r recordFS) Stat(name string) (fs.FileInfo, error)  { r.check(name); return r.FS.Stat(name) }
func (r recordFS) Lstat(name string) (fs.FileInfo, error) { r.check(name); return r.FS.Lstat(name) }
func (r recordFS) Readlink(name string) (string, error)   { r.check(name); return r.FS.Readlink(name) }

func TestResolve(t *testing.T) {
	plain, m := newHost(t, nil)
	h := New(m, recordFS{plain.fs, t}, "/repo")
	objects := open(t, h, m, "objects")
	for _, tc := range []struct {
		dir         int32
		path, want  string
		errno, stat int32
	}{
		{preopenFD, "objects/../../../x", "", errnoNotcapable, errnoNotcapable},
		{preopenFD, "../x", "", errnoNotcapable, errnoNotcapable},
		{preopenFD, "a/../b", "", errnoNotcapable, errnoNotcapable},
		{preopenFD, "a/b/..", "", errnoNotcapable, errnoNotcapable},
		{objects, "../../x", "", errnoNotcapable, errnoNotcapable},
		{objects, "info", "objects/info", errnoSuccess, errnoNoent},
		// Empty and "." elements are dropped.
		{preopenFD, "a//b", "a/b", errnoSuccess, errnoNoent},
		{preopenFD, "objects/./x", "objects/x", errnoSuccess, errnoNoent},
		{preopenFD, "./objects", "objects", errnoSuccess, errnoSuccess},
		{preopenFD, "a/./", "a", errnoSuccess, errnoNoent},
		{preopenFD, "./.", ".", errnoSuccess, errnoSuccess},
	} {
		ptr, n := m.put(tc.path)
		if got, errno := h.resolve(tc.dir, ptr, n); got != tc.want || errno != tc.errno {
			t.Errorf("resolve(%d, %q) = %q, %d; want %q, %d", tc.dir, tc.path, got, errno, tc.want, tc.errno)
		}
		if got := h.Xpath_filestat_get(tc.dir, 0, ptr, n, 2000); got != tc.stat {
			t.Errorf("path_filestat_get(%d, %q) = %d, want %d", tc.dir, tc.path, got, tc.stat)
		}
	}
}

// mapFS serves fstest.MapFS's file info, which has no modification times.
type mapFS struct{ fstest.MapFS }

func (f mapFS) Open(name string) (File, error)         { return nil, fs.ErrNotExist }
func (f mapFS) Stat(name string) (fs.FileInfo, error)  { return fs.Stat(f.MapFS, name) }
func (f mapFS) Lstat(name string) (fs.FileInfo, error) { return fs.Stat(f.MapFS, name) }
func (f mapFS) Readlink(string) (string, error)        { return "", errors.ErrUnsupported }

func TestEdgeCases(t *testing.T) {
	h, m := newHost(t, mapFS{fstest.MapFS{"HEAD": {Data: []byte("ref: refs/heads/main\n")}}})
	ptr, n := m.put("HEAD")
	if got := h.Xpath_filestat_get(preopenFD, 0, ptr, n, 2000); got != errnoSuccess {
		t.Fatalf("path_filestat_get(HEAD) = %d", got)
	}
	for off := 40; off <= 56; off += 8 { // atim, mtim, ctim
		if v := binary.LittleEndian.Uint64(m.b[2000+off:]); v != 0 {
			t.Errorf("a zero time is reported as %d at offset %d", v, off)
		}
	}
	for id := int32(-1); id <= 4; id++ {
		if got, ok := h.Xclock_time_get(id, 0, 1000), id >= 0 && id <= 3; (got == errnoSuccess) != ok || !ok && got != errnoInval {
			t.Errorf("clock_time_get(%d) = %d", id, got)
		}
	}
	if got := (&ExitError{3}).Error(); got != "wasi: proc_exit(3) called" {
		t.Errorf("ExitError.Error() = %q", got)
	}
	d, dm := newHost(t, nil)
	if got := d.Xfd_readdir(open(t, d, dm, "objects"), 2100, 256, -1, 2000); got != errnoInval {
		t.Errorf("fd_readdir with cookie -1 = %d, want EINVAL", got)
	}
}
