/* Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
 * the GNU GPL v2 with a Linking Exception. See the module's LICENSE. */

/* Definitions for symbols that libgit2 references but that mean nothing in
 * this build. Under WASI there is no network stack, no TLS, and no process
 * or user identity. */
#include <sys/types.h>
#include <errno.h>
#include <netdb.h>

int git_socket_stream_global_init(void) { return 0; }
int git_openssl_stream_global_init(void) { return 0; }
int git_mbedtls_stream_global_init(void) { return 0; }

uid_t getuid(void) { return 0; }
uid_t geteuid(void) { return 0; }
gid_t getgid(void) { return 0; }
pid_t getpid(void) { return 1; }
pid_t getppid(void) { return 0; }
pid_t getpgid(pid_t pid) { (void)pid; return 1; }
pid_t getsid(pid_t pid) { (void)pid; return 1; }

int getaddrinfo(const char *node, const char *service,
	const struct addrinfo *hints, struct addrinfo **res)
{
	(void)node; (void)service; (void)hints;
	*res = 0;
	return EAI_FAIL;
}
void freeaddrinfo(struct addrinfo *ai) { (void)ai; }
const char *gai_strerror(int code) { (void)code; return "network unavailable"; }

/* Socket timeouts, set through git_libgit2_opts. Without sockets they are
 * unused. */
int git_socket_stream__connect_timeout = 0;
int git_socket_stream__timeout = 0;

/* libgit2's bundled xdiff is LGPL, unlike the rest of libgit2, so it is not
 * compiled. These are the only xdiff entry points that libgit2 references.
 * Content diffs and merges reach them, and g2.c uses neither. */
#include "xdiff.h"

int xdl_diff(mmfile_t *mf1, mmfile_t *mf2, xpparam_t const *xpp,
	xdemitconf_t const *xecfg, xdemitcb_t *ecb)
{
	(void)mf1; (void)mf2; (void)xpp; (void)xecfg; (void)ecb;
	return -1;
}

int xdl_merge(mmfile_t *orig, mmfile_t *mf1, mmfile_t *mf2,
	xmparam_t const *xmp, mmbuffer_t *result)
{
	(void)orig; (void)mf1; (void)mf2; (void)xmp; (void)result;
	return -1;
}
