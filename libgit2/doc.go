// Package libgit2 is libgit2 compiled to WebAssembly and translated to Go by
// wasm2go. The functions it exports are defined in c/g2.c.
//
// It is the low-level half of github.com/msuozzo/go-git2, which provides
// the git API and the host side of the module: its WASI preview1 imports
// and its linear memory. build/regen.sh produces libgit2.go, libgit2.dat,
// meta.go, LICENSE and LICENSE.wasi-libc. See build/README.md.
//
// This module, including g2.c, is distributed under libgit2's license, the
// GNU GPL v2 with a linking exception (see LICENSE). The module also
// contains wasi-libc (see LICENSE.wasi-libc).
//
// Versions encode the libgit2 release in the patch number, as
// go-sqlite3-wasm does for SQLite. For example, v0.N.10907 is build N of
// libgit2 1.9.7.
package libgit2
