/* Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
 * the GNU GPL v2 with a Linking Exception. See the module's LICENSE. */

/* Minimal pwd.h for wasm32-wasip1, which has no user database.
 * getuid and geteuid report uid 0, and getpwuid_r finds no entry, so
 * libgit2's home-directory lookup falls through to $HOME. The host leaves
 * $HOME unset, so no global or XDG config is read. */
#ifndef G2_PWD_H
#define G2_PWD_H

#include <sys/types.h>

struct passwd {
	char *pw_name;
	char *pw_passwd;
	uid_t pw_uid;
	gid_t pw_gid;
	char *pw_gecos;
	char *pw_dir;
	char *pw_shell;
};

static inline int getpwuid_r(uid_t uid, struct passwd *pwd, char *buf,
	size_t buflen, struct passwd **result)
{
	(void)uid; (void)pwd; (void)buf; (void)buflen;
	*result = 0;
	return 0;
}

#endif
