package git2

import (
	"encoding/binary"
	"strings"
	"sync/atomic"

	"github.com/msuozzo/go-git2/internal/wasi"
	"github.com/msuozzo/go-git2/libgit2"
)

// mount is where the repository's filesystem appears inside the module.
const mount = "/repo"

// libgit2 error codes (git2/errors.h).
const (
	gitENotFound  = -3
	gitEAmbiguous = -5
	gitEInvalid   = -12 // GIT_EINVALIDSPEC
	gitEPeel      = -19
	gitIterOver   = -31
)

// objCommit is GIT_OBJECT_COMMIT (git2/types.h).
const objCommit = 1

// Exchange buffer layout. The module writes results to the output slots,
// which are 8 bytes each, enough for a pointer or a size. An object id takes
// 20 bytes and spans three slots, so an id written at slot 6 or later would
// overwrite oidA. The arguments follow the slots: two object ids, then a
// NUL-terminated string.
const (
	slotBytes = 8
	slots     = 8
	oidA      = slots * slotBytes // first object id argument
	oidB      = oidA + 20         // second object id argument
	strArg    = oidB + 20         // NUL-terminated string argument
)

// Tunables passed to g2_init.
const (
	// wasm has no mmap, so libgit2's pack windows are heap copies, and
	// smaller windows waste less on random access. The mapped limit bounds
	// the copies held at once.
	mwindowSize  = 1 << 20
	mwindowLimit = 128 << 20

	// cacheMax bounds libgit2's object cache, like go-git's default object
	// LRU.
	cacheMax = 96 << 20

	// The largest object of each type the cache keeps. By default libgit2
	// caches only objects under 4 KiB, and no blobs. In a monorepo the
	// upper levels of the tree are larger than that and are read on every
	// path lookup, so cache trees and commits of any reasonable size. Also
	// cache small blobs, because manifests are read repeatedly when walking
	// history.
	cacheCommitLimit = 1 << 20
	cacheTreeLimit   = 4 << 20
	cacheTagLimit    = 1 << 20
	cacheBlobLimit   = 64 << 10
)

// maxMemory is the most linear memory a wasm32 module can address.
const maxMemory = 1 << 32

// memory is the module's linear memory. Where the platform allows, it is
// a reservation of the full address space (see reserve). Otherwise it
// lives on the Go heap and grows by doubling.
type memory struct {
	buf    []byte
	mapped bool
}

func newMemory(size int) memory {
	if b, ok := reserve(size); ok {
		return memory{buf: b, mapped: true}
	}
	return memory{buf: make([]byte, size, max(size, 16<<20))}
}

func (m *memory) free() {
	if m.mapped {
		release(m.buf)
	}
	m.buf = nil
}

func (m *memory) Slice() *[]byte { return &m.buf }

func (m *memory) Grow(delta, maxPages int64) int64 {
	old := int64(len(m.buf) >> 16)
	if delta == 0 {
		return old
	}
	n := old + delta
	if n > maxPages {
		return -1
	}
	size := int(n << 16)
	if size <= cap(m.buf) {
		if m.mapped && !commit(m.buf[len(m.buf):size]) {
			return -1
		}
		m.buf = m.buf[:size]
		return old
	}
	c := max(size, 2*cap(m.buf))
	if lim := int(maxPages << 16); c > lim {
		c = lim
	}
	nb := make([]byte, size, c)
	copy(nb, m.buf)
	m.buf = nb
	return old
}

// instance is one instantiation of the libgit2 module with its host imports.
// It is not safe for concurrent use.
type instance struct {
	mod  *libgit2.Module
	mem  memory
	host *wasi.Host
	buf  uint32 // address of the exchange buffer
	bufN uint32
}

func (rt *instance) Xmemory() libgit2.Memory { return &rt.mem }

// newInstance instantiates the module over fsys. verify makes libgit2 check
// that each object it reads hashes to its id. A fault while starting the
// module returns a *FaultError and releases the instance.
func newInstance(fsys wasi.FS, verify bool) (_ *instance, err error) {
	linkLicenses()
	rt := &instance{mem: newMemory(libgit2.MinPages << 16)}
	rt.host = wasi.New(&rt.mem, fsys, mount)
	liveInstances.Add(1)
	defer func() {
		if p := recover(); p != nil {
			rt.close()
			err = newFault(p)
		}
	}()
	rt.mod = libgit2.New(rt, rt.host)
	rt.mod.X_initialize()
	rt.buf = uint32(rt.mod.Xg2_buf())
	rt.bufN = uint32(rt.mod.Xg2_buf_size())
	if code := rt.mod.Xg2_init(mwindowSize, mwindowLimit, cacheMax,
		cacheCommitLimit, cacheTreeLimit, cacheTagLimit, cacheBlobLimit, b2i(verify)); code < 0 {
		err := rt.error(code)
		rt.close()
		return nil, err
	}
	return rt, nil
}

// liveInstances counts the instances not yet closed, for tests.
var liveInstances atomic.Int64

func (rt *instance) close() {
	liveInstances.Add(-1)
	rt.host.Close()
	rt.mod = nil
	rt.mem.free()
}

// m returns linear memory, with its capacity cut to its length so that an
// access past the module's memory panics, as one by the module traps,
// instead of reaching the rest of the reservation. Don't hold the result
// across a module call, because growth may move it.
func (rt *instance) m() []byte {
	b := rt.mem.buf
	return b[:len(b):len(b)]
}

// slot returns the address of output slot i.
func (rt *instance) slot(i int) int32 { return int32(rt.buf + uint32(i*slotBytes)) }

func (rt *instance) u32(addr int32) uint32 {
	return binary.LittleEndian.Uint32(rt.m()[uint32(addr):])
}

func (rt *instance) slotU32(i int) uint32 { return rt.u32(rt.slot(i)) }

// putOID writes h at the given exchange buffer offset and returns its address.
func (rt *instance) putOID(off uint32, h Hash) int32 {
	copy(rt.m()[rt.buf+off:], h[:])
	return int32(rt.buf + off)
}

// hasNUL reports whether s contains a NUL byte. C reads a string only up to
// its first NUL, so callers reject such names instead of truncating.
func hasNUL(s string) bool { return strings.IndexByte(s, 0) >= 0 }

// putStr writes s as a NUL-terminated string into the exchange buffer, or
// into the module's heap when it does not fit. It returns the address and
// whether the caller must free it.
func (rt *instance) putStr(s string) (addr int32, heap bool) {
	if uint32(len(s))+1 <= rt.bufN-strArg {
		o := rt.buf + strArg
		m := rt.m()
		copy(m[o:], s)
		m[o+uint32(len(s))] = 0
		return int32(o), false
	}
	p := rt.mod.Xg2_malloc(int32(len(s) + 1))
	if p == 0 {
		panic("git2: module out of memory")
	}
	m := rt.m()
	copy(m[uint32(p):], s)
	m[uint32(p)+uint32(len(s))] = 0
	return p, true
}

func (rt *instance) freeStr(addr int32, heap bool) {
	if heap {
		rt.mod.Xg2_free(addr)
	}
}

func (rt *instance) hashAt(addr uint32) (h Hash) {
	copy(h[:], rt.m()[addr:addr+20])
	return h
}

// bytes returns a view of linear memory. The view is invalidated by any
// subsequent module call.
func (rt *instance) bytes(ptr, n uint32) []byte { return rt.m()[ptr : ptr+n] }

func (rt *instance) cstring(addr uint32) string {
	m := rt.m()[addr:]
	for i, c := range m {
		if c == 0 {
			return string(m[:i])
		}
	}
	return string(m)
}

// error converts a libgit2 error code into an *Error carrying its message.
func (rt *instance) error(code int32) error {
	return &Error{
		Code:    int(code),
		Class:   int(rt.mod.Xg2_errclass()),
		Message: rt.cstring(uint32(rt.mod.Xg2_errmsg())),
	}
}
