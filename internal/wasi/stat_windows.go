package wasi

import (
	"io/fs"
	"syscall"
)

// platformStat reports the access and creation times. Windows has no device
// or inode numbers here, so those stay zero, as in libgit2's own Windows
// port, which also takes the creation time as the change time.
func platformStat(fi fs.FileInfo) (sysStat, bool) {
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return sysStat{}, false
	}
	return sysStat{
		nlink: 1,
		atim:  uint64(d.LastAccessTime.Nanoseconds()),
		ctim:  uint64(d.CreationTime.Nanoseconds()),
	}, true
}
