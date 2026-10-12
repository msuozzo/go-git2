//go:build unix && !git2_heapmem

package git2

import (
	"syscall"
)

// reserve maps the module's whole 4 GiB address space up front. Pages are
// committed only when touched, so growth never copies. The memory is
// outside the Go heap, so it adds nothing to GC work or allocation counts,
// and it is returned to the OS on Close.
func reserve(size int) ([]byte, bool) {
	b, err := syscall.Mmap(-1, 0, maxMemory, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE|mapNoReserve)
	if err != nil {
		return nil, false
	}
	return b[:size], true
}

// commit does nothing, since pages are committed when they're touched.
func commit([]byte) bool { return true }

func release(b []byte) { syscall.Munmap(b[:cap(b)]) }
