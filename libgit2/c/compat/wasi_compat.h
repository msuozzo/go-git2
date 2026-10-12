/* Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
 * the GNU GPL v2 with a Linking Exception. See the module's LICENSE. */

/* Included in every libgit2 translation unit with -include. It declares
 * the POSIX process and user identity functions that wasi-libc omits,
 * which stubs.c defines. */
#ifndef G2_WASI_COMPAT_H
#define G2_WASI_COMPAT_H

#include <sys/types.h>

uid_t getuid(void);
uid_t geteuid(void);
gid_t getgid(void);
pid_t getpid(void);
pid_t getppid(void);
pid_t getpgid(pid_t);
pid_t getsid(pid_t);

/* Present in wasi-libc but only declared for unmodified upstream musl. */
void qsort_r(void *, size_t, size_t, int (*)(const void *, const void *, void *), void *);

#endif
