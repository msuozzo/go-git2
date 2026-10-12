//go:build unix

package wasi

import (
	"io/fs"
	"syscall"
)

func platformStat(fi fs.FileInfo) (sysStat, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return sysStat{}, false
	}
	atim, ctim := statTimes(st)
	return sysStat{dev: uint64(st.Dev), ino: uint64(st.Ino), nlink: uint64(st.Nlink), atim: atim, ctim: ctim}, true
}
