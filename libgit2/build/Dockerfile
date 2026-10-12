# syntax=docker/dockerfile:1.7
# Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
# the GNU GPL v2 with a Linking Exception. See the module's LICENSE.
#
# Builder for the libgit2 module: libgit2 compiled for wasm32-wasip1 and
# translated to Go by wasm2go. build/regen.sh builds this image when it is
# missing and runs it with the module mounted at /work. Each run compiles
# everything from scratch and rewrites libgit2.go, libgit2.dat, meta.go,
# LICENSE and LICENSE.wasi-libc.
#
# Every input is pinned here, the downloads by SHA-256 and wasm2go by the Go
# checksum database, so the output depends only on this file and on the
# module's c directory. The base images are pinned by tag.
#
# To bump a tool, change its version and SHA-256 below. regen.sh tags the
# image with a hash of this file, so the next run rebuilds it.

# ---------- wasm2go, built with the Go toolchain that go.mod pins ----------
FROM --platform=linux/amd64 golang:1.27.1-alpine AS go

ARG WASM2GO_VERSION=v0.4.17
RUN CGO_ENABLED=0 go install -trimpath -ldflags='-s -w' \
	"github.com/ncruces/wasm2go@${WASM2GO_VERSION}"

# ---------- toolchains and sources ----------
FROM --platform=linux/amd64 debian:bookworm-slim AS fetch
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

ARG WASI_SDK_VERSION=34.0
ARG WASI_SDK_SHA256=b761e3a0721dbae9c09a0059e5fdb2bf917d1b4a8a7b430fb3b5aafb0984b2c4
RUN curl -fsSL -o /tmp/wasi-sdk.tgz "https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-${WASI_SDK_VERSION%.*}/wasi-sdk-${WASI_SDK_VERSION}-x86_64-linux.tar.gz"; \
	echo "${WASI_SDK_SHA256}  /tmp/wasi-sdk.tgz" | sha256sum -c -; \
	mkdir -p /build/tools/wasi-sdk; \
	tar xzf /tmp/wasi-sdk.tgz -C /build/tools/wasi-sdk --strip-components=1; \
	rm /tmp/wasi-sdk.tgz; \
	rm -rf /build/tools/wasi-sdk/share/{man,doc,misc} /build/tools/wasi-sdk/lib/{cmake,pkgconfig} \
		/build/tools/wasi-sdk/lib/liblldb.so* /build/tools/wasi-sdk/bin/lldb*

ARG BINARYEN_VERSION=133
ARG BINARYEN_SHA256=2dc9c7813f5375db93d96ead4b78222fcc3e2677bbb832297af4797782a37489
RUN curl -fsSL -o /tmp/binaryen.tgz "https://github.com/WebAssembly/binaryen/releases/download/version_${BINARYEN_VERSION}/binaryen-version_${BINARYEN_VERSION}-x86_64-linux.tar.gz"; \
	echo "${BINARYEN_SHA256}  /tmp/binaryen.tgz" | sha256sum -c -; \
	mkdir -p /build/tools/binaryen; \
	tar xzf /tmp/binaryen.tgz -C /build/tools/binaryen --strip-components=1; \
	rm /tmp/binaryen.tgz; \
	find /build/tools/binaryen/bin -type f ! -name wasm-opt ! -name wasm-dis -delete

# libgit2, with the one change carried to it. Its delta base cache, which
# keeps a pack's reconstructed delta bases for the deltas built on them,
# holds 16 MiB, while git's core.deltaBaseCacheLimit is 96 MiB. With git's
# limit, reading every blob at HEAD of google-cloud-python takes 40% less
# time. The cache is per pack, so a repository with several large packs can
# use up to this much for each. Drop the change once libgit2 matches the
# limit or makes it configurable. The build fails if the definition moves.
ARG LIBGIT2_VERSION=1.9.7
ARG LIBGIT2_SHA256=1a4fbe7589e814777ae76b64734ad80f4ecad22cd33a22682a2aaea4ae5375e7
RUN curl -fsSL -o /tmp/libgit2.tgz "https://github.com/libgit2/libgit2/archive/refs/tags/v${LIBGIT2_VERSION}.tar.gz"; \
	echo "${LIBGIT2_SHA256}  /tmp/libgit2.tgz" | sha256sum -c -; \
	mkdir -p /build/src; \
	tar xzf /tmp/libgit2.tgz -C /build/src; \
	rm /tmp/libgit2.tgz; \
	h=/build/src/libgit2-${LIBGIT2_VERSION}/src/libgit2/pack.h; \
	from='#define GIT_PACK_CACHE_MEMORY_LIMIT 16 * 1024 * 1024'; \
	to='#define GIT_PACK_CACHE_MEMORY_LIMIT 96 * 1024 * 1024'; \
	grep -qxF "$from" "$h" || { echo "$h no longer defines GIT_PACK_CACHE_MEMORY_LIMIT as expected" >&2; exit 1; }; \
	sed -i "s/^#define GIT_PACK_CACHE_MEMORY_LIMIT 16 \* 1024 \* 1024\$/$to/" "$h"; \
	grep -qxF "$to" "$h"

# The license notices of wasi-libc as built into wasi-sdk, and of the code it
# includes that this build links: musl and cloudlibc. dlmalloc is CC0, and
# fts and emmalloc are not linked.
ARG WASI_LIBC_COMMIT=2e6fb9d8ee0cdf9e431fbcabe8af3115de000a13
ARG WASI_LIBC_LICENSE_SHA256=687ac601344f1c5a61676ab28ba23e3cb9ca9fe08c0080ab41c26b5582217537
RUN { \
		echo "wasi-libc (https://github.com/WebAssembly/wasi-libc), as built into"; \
		echo "wasi-sdk ${WASI_SDK_VERSION%.*}: commit ${WASI_LIBC_COMMIT}."; \
		for f in LICENSE LICENSE-MIT libc-top-half/musl/COPYRIGHT libc-bottom-half/cloudlibc/LICENSE; do \
			printf '\n----------------------------------------------------------------------\n\n%s:\n\n' "$f"; \
			curl -fsSL "https://raw.githubusercontent.com/WebAssembly/wasi-libc/${WASI_LIBC_COMMIT}/$f"; \
		done; \
	} > /build/src/LICENSE.wasi-libc; \
	echo "${WASI_LIBC_LICENSE_SHA256}  /build/src/LICENSE.wasi-libc" | sha256sum -c -

# libgit2's license notices, without ntlmclient's, since that code isn't
# compiled. Those include RSA's notice for MD4, which pkg.go.dev doesn't
# accept, so it would hide this module's documentation. The checksum fails
# the build when a libgit2 upgrade changes the notices, so they get
# reviewed.
ARG LIBGIT2_LICENSE_SHA256=eecd31a31e22f14d6f92d5d82327dd596fac6fd5377cb1a23037295fe18cc4be
RUN sed '/^The bundled ntlmclient code /,/^The bundled llhttp dependency /{/^The bundled llhttp dependency /!d}' \
		"/build/src/libgit2-${LIBGIT2_VERSION}/COPYING" > /build/src/LICENSE.libgit2; \
	echo "${LIBGIT2_LICENSE_SHA256}  /build/src/LICENSE.libgit2" | sha256sum -c -

# ---------- the builder ----------
# Built on the fetch stage, so the toolchains are stored once.
FROM fetch

COPY --from=go /go/bin/wasm2go /usr/local/bin/wasm2go

# The order of the sources, and so of the linked code, follows the locale's
# collation.
ENV LC_ALL=C

ARG LIBGIT2_VERSION=1.9.7
ENV LIBGIT2_VERSION=${LIBGIT2_VERSION}

COPY --chmod=0755 <<'EOF' /usr/local/bin/regen
#!/usr/bin/env bash
# Rebuilds the libgit2 module mounted at /work, from scratch.
#
# It compiles in a fresh directory laid out as the paths in the output
# expect: libgit2 under src/ (assertion messages name its files by these
# relative paths), the toolchains under tools/, and a copy of the module's
# c/. GIT2_KEEP_NAMES=1 keeps C function names in the generated Go, for
# profiling. Committed output is built without it.
set -euo pipefail

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
ln -s /build/src "$work/src"
ln -s /build/tools "$work/tools"
cp -R /work/c "$work/c"
mkdir "$work/obj" "$work/out"
cd "$work"

L=src/libgit2-$LIBGIT2_VERSION
CC=tools/wasi-sdk/bin/clang
AR=tools/wasi-sdk/bin/llvm-ar

WASMFLAGS=(
	--target=wasm32-wasip1 --sysroot=tools/wasi-sdk/share/wasi-sysroot -O2 -std=gnu99 -g0
	-mmutable-globals -mmultivalue -mnontrapping-fptoint -msign-ext
	-mreference-types -mbulk-memory -mextended-const
)
CFLAGS=(
	"${WASMFLAGS[@]}"
	-DNDEBUG -DNO_MMAP -DNO_READDIR_R -DGIT_DEPRECATE_HARD -D_GNU_SOURCE
	-D_WASI_EMULATED_SIGNAL -D_WASI_EMULATED_PROCESS_CLOCKS
	-include c/compat/wasi_compat.h
	-Ic/gen -Ic/compat -I$L/include -I$L/src/util -I$L/src/libgit2
	-I$L/deps/zlib -I$L/deps/xdiff -I$L/deps/llhttp -I$L/src/util/hash/sha1dc
	-DNO_GZIP -DSTDC
	-DSHA1DC_NO_STANDARD_INCLUDES=1
	-DSHA1DC_CUSTOM_INCLUDE_SHA1_C='"git2_util.h"'
	-DSHA1DC_CUSTOM_INCLUDE_UBC_CHECK_C='"git2_util.h"'
	-Wno-everything
)
# g2.c sees only libgit2's public headers, and must compile without
# warnings.
G2CFLAGS=("${WASMFLAGS[@]}" -DNDEBUG -DGIT_DEPRECATE_HARD -I$L/include -Wall -Werror)

sources() {
	ls $L/src/util/*.c $L/src/util/unix/realpath.c \
		$L/src/util/hash/collisiondetect.c $L/src/util/hash/builtin.c \
		$L/src/util/hash/sha1dc/*.c $L/src/util/hash/rfc6234/*.c \
		$L/src/util/allocators/stdalloc.c $L/src/util/allocators/failalloc.c \
		$L/src/libgit2/*.c $L/src/libgit2/streams/registry.c $L/src/libgit2/streams/tls.c \
		$L/src/libgit2/transports/*.c $L/deps/zlib/*.c \
		c/stubs.c
}

LDSTRIP=(-Wl,--strip-all) OPTNAMES=() OPTSTRIP=(--strip-debug)
if [[ -n ${GIT2_KEEP_NAMES:-} ]]; then
	LDSTRIP=() OPTNAMES=(-g) OPTSTRIP=()
fi

# Compile libgit2 and its bundled dependencies, then the export surface,
# and link the two.
printf '%s\n' "${CFLAGS[@]}" | sed 's/"/\\"/g' > out/cflags.rsp
sources | xargs -P "$(nproc)" -n 1 sh -c \
	'exec '$CC' @out/cflags.rsp -c "$1" -o obj/$(echo "$1" | tr / _).o' _
$AR rcs out/libgit2.a $(sources | sed "s|/|_|g; s|^|obj/|; s|\$|.o|")
$CC "${G2CFLAGS[@]}" -c -o out/g2.o c/g2.c
$CC "${WASMFLAGS[@]}" -o out/g2.linked out/g2.o out/libgit2.a \
	-mexec-model=reactor \
	-Wl,--import-memory \
	-Wl,--stack-first -Wl,-z,stack-size=1048576 \
	-Wl,--max-memory=4294967296 \
	-Wl,--gc-sections \
	${LDSTRIP[@]+"${LDSTRIP[@]}"}

tools/binaryen/bin/wasm-opt ${OPTNAMES[@]+"${OPTNAMES[@]}"} out/g2.linked -o out/g2.wasm \
	--low-memory-unused --converge -O3 \
	--enable-mutable-globals --enable-multivalue \
	--enable-nontrapping-float-to-int --enable-sign-ext \
	--enable-reference-types --enable-bulk-memory \
	--enable-extended-const \
	${OPTSTRIP[@]+"${OPTSTRIP[@]}"} --strip-producers

wasm2go -embed -unsafe -pkg libgit2 -o /work/libgit2.go out/g2.wasm

# Record the module's minimum memory so the host can size it up front.
pages=$(tools/binaryen/bin/wasm-dis out/g2.wasm | sed -n 's/.*(import "env" "memory" (memory [^ ]* \([0-9]*\) .*/\1/p' | head -1)
cat > /work/meta.go <<GO
// Code generated by build/regen.sh. DO NOT EDIT.

package libgit2

// Version of libgit2 compiled into this module.
const Version = "$LIBGIT2_VERSION"

// MinPages is the initial size of linear memory, in 64 KiB pages.
const MinPages = $pages
GO

# The generated code is a translation of libgit2, its bundled dependencies
# and wasi-libc, so ship their licenses alongside it. libgit2's goes in
# LICENSE, because the go command adds the repository's MIT LICENSE to a
# module in a subdirectory that has none.
cp src/LICENSE.libgit2 /work/LICENSE
cp src/LICENSE.wasi-libc /work/LICENSE.wasi-libc
EOF

ENTRYPOINT ["/usr/local/bin/regen"]
