/* Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
 * the GNU GPL v2 with a Linking Exception. See the module's LICENSE. */

/* Minimal netdb.h for wasm32-wasip1, which has no name resolution.
 * libgit2's network transports reference these, but this build never does
 * network I/O. See stubs.c. */
#ifndef G2_NETDB_H
#define G2_NETDB_H

#include <sys/socket.h>

struct addrinfo {
	int ai_flags;
	int ai_family;
	int ai_socktype;
	int ai_protocol;
	socklen_t ai_addrlen;
	struct sockaddr *ai_addr;
	char *ai_canonname;
	struct addrinfo *ai_next;
};

#define EAI_FAIL (-4)

int getaddrinfo(const char *, const char *, const struct addrinfo *, struct addrinfo **);
void freeaddrinfo(struct addrinfo *);
const char *gai_strerror(int);

#endif
