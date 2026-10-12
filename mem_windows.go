//go:build windows && !git2_heapmem

package git2

import (
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	virtualAlloc = kernel32.NewProc("VirtualAlloc")
	virtualFree  = kernel32.NewProc("VirtualFree")
)

// VirtualAlloc and VirtualFree flags (winnt.h).
const (
	memCommit    = 0x1000
	memReserve   = 0x2000
	memRelease   = 0x8000
	pageNoAccess = 0x01
)

// reserve sets aside the module's whole 4 GiB address space up front and
// commits the first size bytes. Windows doesn't commit pages when they're
// touched, so Grow commits each new range. The memory is outside the Go
// heap, so it adds nothing to GC work or allocation counts, and it is
// returned to the OS on Close.
//
// The standard library has no Windows call that returns mapped memory as
// a slice, so this file uses unsafe to make one.
func reserve(size int) ([]byte, bool) {
	p, _, _ := virtualAlloc.Call(0, maxMemory, memReserve, pageNoAccess)
	if p == 0 {
		return nil, false
	}
	// vet flags a uintptr converted to a pointer, since it could be a Go
	// pointer the GC has lost track of. p is outside the Go heap, so read
	// it as a pointer instead.
	b := unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), maxMemory)
	if !commit(b[:size]) {
		release(b)
		return nil, false
	}
	return b[:size], true
}

// commit makes b readable and writable.
func commit(b []byte) bool {
	p, _, _ := virtualAlloc.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		memCommit, syscall.PAGE_READWRITE)
	return p != 0
}

func release(b []byte) { virtualFree.Call(uintptr(unsafe.Pointer(&b[0])), 0, memRelease) }
