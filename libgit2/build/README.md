# Building the libgit2 module

`go generate ./...` in the module root runs `regen.sh`, which produces the
module's `libgit2.go`, `libgit2.dat` and `meta.go`, along with the licenses
of the code in them, `LICENSE` and `LICENSE.wasi-libc`. It needs Docker.
`regen.sh` builds the image defined by `Dockerfile` when it is missing and
runs it with the module mounted. The build works in four steps:

1. The image holds the pinned inputs: wasi-sdk 34, binaryen 133, wasm2go,
   libgit2 1.9.7 and the license notices of libgit2 and of the wasi-libc in
   wasi-sdk. The downloads and the license notices are checked against
   their SHA-256 sums. One change is applied to libgit2: its delta base
   cache holds git's 96MiB instead of 16MiB. The image build fails if
   libgit2's definition changes, so an upgrade cannot silently drop the
   change.
2. Each run compiles libgit2 and its bundled zlib for `wasm32-wasip1`
   against wasi-libc, with network transports, TLS, SSH and threads left
   out.
3. It compiles `c/g2.c`, the export surface, against libgit2's public
   headers only, and links it, keeping only what it reaches.
4. It optimizes with `wasm-opt -O3` and translates with
   `wasm2go -embed -unsafe`.

The output is deterministic. CI regenerates it and checks that it matches
the committed files. The image is tagged with a hash of `Dockerfile`, so a
change to a pin rebuilds it on the next run, and `regen.sh --image-only`
rebuilds it without regenerating.

| file, from the module root | |
| --- | --- |
| `build/Dockerfile` | the pinned toolchains and sources, and the build script the container runs |
| `build/regen.sh` | builds the image if needed and runs it |
| `c/g2.c` | the exported functions, which adapt libgit2's public API to a small ABI of handles, scalars and 20-byte ids for objects, tree entries and references. Walks, listings and parsing are in Go, in the root module. |
| `c/stubs.c`, `c/compat/` | definitions for code that never runs, covering what wasi-libc lacks (user and process identity, name resolution) and xdiff's entry points |
| `c/gen/` | the configuration headers libgit2's CMake would generate |

`GIT2_KEEP_NAMES=1` keeps C function names in the generated Go, for
profiling. Committed output is built without it.

## Licenses

The generated code is a translation of libgit2 and of wasi-libc. libgit2 is
licensed under the GPLv2 with the linking exception (see `LICENSE`, which
also covers the bundled zlib and SHA-1 collision detection). wasi-libc is
used under its MIT option, with musl under MIT, cloudlibc under
BSD-2-Clause and dlmalloc under CC0 (see `LICENSE.wasi-libc`). libgit2's
bundled xdiff (LGPL) and PCRE are not compiled. `c/stubs.c` stands in for
xdiff's two entry points, which only content diffs and merges reach.

`LICENSE` is libgit2's `COPYING` without the notices of its bundled
ntlmclient, which is not compiled either. One of them, RSA's notice for
MD4, is not a license pkg.go.dev accepts, so it would hide the module's
documentation. The file is named `LICENSE` because the go command adds the
repository's MIT `LICENSE` to a module in a subdirectory that has none.

The files in this directory are distributed under libgit2's terms, as is
the rest of the module.
