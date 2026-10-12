//go:build unix && !darwin && !linux

package wasi

import "syscall"

func statTimes(st *syscall.Stat_t) (atim, ctim uint64) { return 0, 0 }
