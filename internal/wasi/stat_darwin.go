package wasi

import "syscall"

func statTimes(st *syscall.Stat_t) (atim, ctim uint64) {
	return uint64(syscall.TimespecToNsec(st.Atimespec)), uint64(syscall.TimespecToNsec(st.Ctimespec))
}
