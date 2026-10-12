//go:build !unix && !windows

package wasi

import "io/fs"

func platformStat(fs.FileInfo) (sysStat, bool) { return sysStat{}, false }
