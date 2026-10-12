/* Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
 * the GNU GPL v2 with a Linking Exception. See the module's LICENSE. */

/*
 * g2.c defines the functions that the libgit2 wasm module exports.
 *
 * Its only job is to adapt libgit2's ABI to a smaller one for the Go side.
 * Every use of libgit2's enums, structs and variadic functions stays in
 * this file, where the compiler checks it against libgit2's public headers.
 * The build compiles this file against those headers alone. The logic and
 * data structures live in Go. Walks and listings make one call per item,
 * which wasm2go makes as cheap as a Go method call.
 *
 * Conventions:
 *  - Functions return 0 or a negative libgit2 error code.
 *  - libgit2 objects are opaque handles to the host.
 *  - Outputs are written through pointers supplied by the host, which
 *    usually point into the exchange buffer (g2_buf). Object ids are 20
 *    bytes. Strings are an address and a length, valid until the object
 *    they belong to is freed.
 */

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "git2.h"

#ifdef __wasm__
#define EXPORT(name) __attribute__((export_name(#name)))
#else
#define EXPORT(name)
#endif

/* ---- exchange buffer ---------------------------------------------------- */

#define G2_BUF_SIZE (64 * 1024)
static unsigned char g2_buf[G2_BUF_SIZE] __attribute__((aligned(16)));

EXPORT(g2_buf) unsigned char *g2_buf_ptr(void) { return g2_buf; }
EXPORT(g2_buf_size) size_t g2_buf_size(void) { return G2_BUF_SIZE; }

EXPORT(g2_malloc) void *g2_malloc(size_t n) { return malloc(n); }
EXPORT(g2_free) void g2_free(void *p) { free(p); }

static void put_oid(unsigned char *out, const git_oid *id) { memcpy(out, id->id, GIT_OID_SHA1_SIZE); }

static void put_str(const char *s, const char **out, size_t *len)
{
	*out = s;
	*len = s ? strlen(s) : 0;
}

/* ---- errors ------------------------------------------------------------- */

EXPORT(g2_errmsg) const char *g2_errmsg(void)
{
	const git_error *e = git_error_last();
	return e && e->message ? e->message : "";
}

EXPORT(g2_errclass) int g2_errclass(void)
{
	const git_error *e = git_error_last();
	return e ? e->klass : 0;
}

/* ---- lifecycle ---------------------------------------------------------- */

/* The tunables come from the host. Each per-type limit is the largest
 * object of that type that libgit2's object cache keeps, and 0 caches none.
 * For mwindow_size, mwindow_limit and cache_max, 0 keeps libgit2's default.
 * An option libgit2 rejects fails the call, so a setting cannot silently go
 * missing. */
EXPORT(g2_init) int g2_init(size_t mwindow_size, size_t mwindow_limit, size_t cache_max,
	size_t commit_limit, size_t tree_limit, size_t tag_limit, size_t blob_limit, int verify)
{
	int err;

	if ((err = git_libgit2_init()) < 0)
		return err;

	/* Hash verification checks that each object read hashes to its id
	 * (libgit2's default), unless the host trusts the repository's files.
	 * WASI has no users, so ownership cannot be checked. */
	if ((err = git_libgit2_opts(GIT_OPT_ENABLE_STRICT_HASH_VERIFICATION, verify)) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_OWNER_VALIDATION, 0)) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_SEARCH_PATH, GIT_CONFIG_LEVEL_SYSTEM, "")) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_SEARCH_PATH, GIT_CONFIG_LEVEL_XDG, "")) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_SEARCH_PATH, GIT_CONFIG_LEVEL_GLOBAL, "")) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_SEARCH_PATH, GIT_CONFIG_LEVEL_PROGRAMDATA, "")) < 0 ||
	    (mwindow_size && (err = git_libgit2_opts(GIT_OPT_SET_MWINDOW_SIZE, mwindow_size)) < 0) ||
	    (mwindow_limit && (err = git_libgit2_opts(GIT_OPT_SET_MWINDOW_MAPPED_LIMIT, mwindow_limit)) < 0) ||
	    (cache_max && (err = git_libgit2_opts(GIT_OPT_SET_CACHE_MAX_SIZE, (ssize_t)cache_max)) < 0) ||
	    (err = git_libgit2_opts(GIT_OPT_SET_CACHE_OBJECT_LIMIT, GIT_OBJECT_COMMIT, commit_limit)) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_CACHE_OBJECT_LIMIT, GIT_OBJECT_TREE, tree_limit)) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_CACHE_OBJECT_LIMIT, GIT_OBJECT_TAG, tag_limit)) < 0 ||
	    (err = git_libgit2_opts(GIT_OPT_SET_CACHE_OBJECT_LIMIT, GIT_OBJECT_BLOB, blob_limit)) < 0)
		return err;
	return 0;
}

EXPORT(g2_open) int g2_open(const char *path, git_repository **out, git_odb **odb_out)
{
	int err;

	if ((err = git_repository_open_ext(out, path, GIT_REPOSITORY_OPEN_NO_SEARCH, NULL)) < 0)
		return err;
	if ((err = git_repository_odb(odb_out, *out)) < 0) {
		git_repository_free(*out);
		*out = NULL;
	}
	return err;
}

EXPORT(g2_close) void g2_close(git_repository *repo, git_odb *odb)
{
	git_odb_free(odb);
	git_repository_free(repo);
}

EXPORT(g2_is_shallow) int g2_is_shallow(git_repository *repo) { return git_repository_is_shallow(repo); }

/* The repository's git directory, or its common directory (they differ in
 * linked worktrees). */
EXPORT(g2_repository_path) const char *g2_repository_path(git_repository *repo, int common)
{
	return common ? git_repository_commondir(repo) : git_repository_path(repo);
}

/* ---- objects ------------------------------------------------------------ */

EXPORT(g2_odb_exists) int g2_odb_exists(git_odb *odb, const git_oid *id) { return git_odb_exists(odb, id); }

EXPORT(g2_odb_read_header) int g2_odb_read_header(git_odb *odb, const git_oid *id, size_t *len, int *type)
{
	git_object_t t;
	int err = git_odb_read_header(len, &t, odb, id);
	if (err == 0)
		*type = (int)t;
	return err;
}

/* Reads a raw object. The host copies the data, then calls
 * g2_odb_object_free. */
EXPORT(g2_odb_read) int g2_odb_read(git_odb *odb, const git_oid *id,
	git_odb_object **out, const void **data, size_t *len, int *type)
{
	int err = git_odb_read(out, odb, id);
	if (err < 0)
		return err;
	*data = git_odb_object_data(*out);
	*len = git_odb_object_size(*out);
	*type = (int)git_odb_object_type(*out);
	return 0;
}

EXPORT(g2_odb_object_free) void g2_odb_object_free(git_odb_object *obj) { git_odb_object_free(obj); }

EXPORT(g2_blob_lookup) int g2_blob_lookup(git_repository *repo, const git_oid *id,
	git_blob **out, const void **data, size_t *len)
{
	int err = git_blob_lookup(out, repo, id);
	if (err < 0)
		return err;
	*data = git_blob_rawcontent(*out);
	*len = (size_t)git_blob_rawsize(*out);
	return 0;
}

EXPORT(g2_blob_free) void g2_blob_free(git_blob *b) { git_blob_free(b); }

/*
 * Resolves a slash-separated path below a tree. Writes the entry's mode and
 * id. Returns GIT_ENOTFOUND if any component is missing.
 */
EXPORT(g2_tree_entry_bypath) int g2_tree_entry_bypath(git_repository *repo,
	const git_oid *tree_id, const char *path, uint32_t *mode, unsigned char *id)
{
	git_tree *tree;
	git_tree_entry *entry;
	int err;

	if ((err = git_tree_lookup(&tree, repo, tree_id)) < 0)
		return err;
	err = git_tree_entry_bypath(&entry, tree, path);
	git_tree_free(tree);
	if (err < 0)
		return err;
	*mode = git_tree_entry_filemode_raw(entry);
	put_oid(id, git_tree_entry_id(entry));
	git_tree_entry_free(entry);
	return 0;
}

/* Looks up a tree, writing it and its number of entries. */
EXPORT(g2_tree_lookup) int g2_tree_lookup(git_repository *repo, const git_oid *id, git_tree **out, size_t *count)
{
	int err = git_tree_lookup(out, repo, id);
	if (err < 0)
		return err;
	*count = git_tree_entrycount(*out);
	return 0;
}

/* Describes entry i of a tree: its raw mode, id and name. */
EXPORT(g2_tree_entry) int g2_tree_entry(const git_tree *tree, size_t i,
	uint32_t *mode, unsigned char *id, const char **name, size_t *len)
{
	const git_tree_entry *e = git_tree_entry_byindex(tree, i);
	if (!e)
		return GIT_ENOTFOUND;
	*mode = git_tree_entry_filemode_raw(e);
	put_oid(id, git_tree_entry_id(e));
	put_str(git_tree_entry_name(e), name, len);
	return 0;
}

EXPORT(g2_tree_free) void g2_tree_free(git_tree *t) { git_tree_free(t); }

/* ---- references --------------------------------------------------------- */

/* Looks up one reference, optionally resolving a symbolic reference to the
 * direct reference it points at. */
EXPORT(g2_ref_lookup) int g2_ref_lookup(git_repository *repo, const char *name, int resolve, git_reference **out)
{
	git_reference *ref, *res;
	int err;

	if ((err = git_reference_lookup(&ref, repo, name)) < 0)
		return err;
	if (resolve && git_reference_type(ref) == GIT_REFERENCE_SYMBOLIC) {
		err = git_reference_resolve(&res, ref);
		git_reference_free(ref);
		if (err < 0)
			return err;
		ref = res;
	}
	*out = ref;
	return 0;
}

/* Starts listing references matching glob (all when NULL). */
EXPORT(g2_ref_iter_new) int g2_ref_iter_new(git_repository *repo, const char *glob, git_reference_iterator **out)
{
	return glob ? git_reference_iterator_glob_new(out, repo, glob)
		    : git_reference_iterator_new(out, repo);
}

/* Writes the next reference, or returns GIT_ITEROVER at the end. */
EXPORT(g2_ref_next) int g2_ref_next(git_reference_iterator *it, git_reference **out)
{
	return git_reference_next(out, it);
}

EXPORT(g2_ref_iter_free) void g2_ref_iter_free(git_reference_iterator *it) { git_reference_iterator_free(it); }

/*
 * Describes a reference: whether it is symbolic (1) or direct (0), its
 * name, and its target. The target is a name if the reference is symbolic,
 * and an id if it is direct.
 */
EXPORT(g2_ref_info) void g2_ref_info(const git_reference *ref, int *symbolic,
	const char **name, size_t *name_len, unsigned char *id, const char **target, size_t *target_len)
{
	put_str(git_reference_name(ref), name, name_len);
	*symbolic = git_reference_type(ref) == GIT_REFERENCE_SYMBOLIC;
	if (*symbolic) {
		put_str(git_reference_symbolic_target(ref), target, target_len);
		memset(id, 0, GIT_OID_SHA1_SIZE);
	} else {
		put_str(NULL, target, target_len);
		put_oid(id, git_reference_target(ref));
	}
}

EXPORT(g2_ref_free) void g2_ref_free(git_reference *ref) { git_reference_free(ref); }

EXPORT(g2_revparse) int g2_revparse(git_repository *repo, const char *spec, unsigned char *out)
{
	git_object *obj;
	int err = git_revparse_single(&obj, repo, spec);
	if (err < 0)
		return err;
	put_oid(out, git_object_id(obj));
	git_object_free(obj);
	return 0;
}

/* Peels an object to the given type (GIT_OBJECT_ANY peels tags fully). */
EXPORT(g2_peel) int g2_peel(git_repository *repo, const git_oid *id, int type, unsigned char *out)
{
	git_object *obj, *peeled;
	int err;

	if ((err = git_object_lookup(&obj, repo, id, GIT_OBJECT_ANY)) < 0)
		return err;
	err = git_object_peel(&peeled, obj, (git_object_t)type);
	git_object_free(obj);
	if (err < 0)
		return err;
	put_oid(out, git_object_id(peeled));
	git_object_free(peeled);
	return 0;
}
