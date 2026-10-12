# Releasing

The repository holds three published modules, released independently in the
style of [go-sqlite3](https://github.com/ncruces/go-sqlite3) and
[go-sqlite3-wasm](https://github.com/ncruces/go-sqlite3-wasm):

| module | contents | changes when |
| --- | --- | --- |
| `github.com/msuozzo/go-git2/libgit2` (`libgit2/`) | the C export surface, the build, and the generated Go | libgit2, wasm2go, the toolchain or the export surface change |
| `github.com/msuozzo/go-git2` (root) | the Go API and the WASI host | the API changes |
| `github.com/msuozzo/go-git2/billyfs` (`billyfs/`) | the go-billy adapter | `git2.FS` or go-billy changes |

`bench/` is a fourth, unpublished module.

## Versions of the libgit2 module

Versions have the form `v0.N.LLMMPP`. `LLMMPP` is the libgit2 release (1.9.7
is `10907`), and `N` counts builds. A libgit2 upgrade with the same export
surface changes only the patch number, for example from `v0.3.10907` to
`v0.3.10908`. A change to the export surface, the build or wasm2go
increments `N`. Tags include the module directory, as in
`libgit2/v0.1.10907`.

## Steps

1. Change pins in `libgit2/build/Dockerfile` (libgit2, wasi-sdk, binaryen,
   wasm2go and the Go image that builds it), or the export surface in
   `libgit2/c/`. A wasi-sdk upgrade also moves `WASI_LIBC_COMMIT` to
   that release's `src/wasi-libc` submodule commit, and the checksum of its
   license notices. A libgit2 upgrade that changes its `COPYING` also moves
   `LIBGIT2_LICENSE_SHA256`, once the change is reviewed.
2. Regenerate: `cd libgit2 && go generate ./...`, which needs Docker. CI
   regenerates and fails if the result differs from what is committed.
3. Tag the libgit2 module with `libgit2/v0.N.LLMMPP` and push the tag.
4. Require that version in the root module with
   `GOWORK=off go get github.com/msuozzo/go-git2/libgit2@v0.N.LLMMPP`,
   which also records its checksums in `go.sum`.
5. Tag the root module. Users get the new libgit2 module only through a
   root release that requires it.
6. Release billyfs only when it changes. If it needs a new root release,
   require it from `billyfs/` with
   `GOWORK=off go get github.com/msuozzo/go-git2@vX.Y.Z`, then tag
   `billyfs/vX.Y.Z`.

Local development uses `go.work`, which overrides the requirements between
the modules.
