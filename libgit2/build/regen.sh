#!/usr/bin/env bash
# Copyright (C) 2026 Matthew Suozzo. Distributed under libgit2's terms,
# the GNU GPL v2 with a Linking Exception. See the module's LICENSE.

# Regenerates the libgit2 module in a container. It builds the builder
# image from build/Dockerfile when the image is missing, then runs it with
# the module mounted at /work, which rewrites libgit2.go, libgit2.dat,
# meta.go, LICENSE and LICENSE.wasi-libc.
#
# Usage:
#   build/regen.sh               # build the image if needed, regenerate
#   build/regen.sh --image-only  # (re)build the image only
#
# `go generate` in the module root runs it. The image is tagged with a hash
# of the Dockerfile, so editing it rebuilds the image on the next run.
# GIT2_KEEP_NAMES=1 keeps C function names in the output, for profiling.
set -euo pipefail
cd -P -- "$(dirname -- "$0")/.."

image=go-git2-builder:$(cksum build/Dockerfile | cut -d' ' -f1)
if [[ ${1:-} == --image-only ]] || ! docker image inspect "$image" >/dev/null 2>&1; then
	docker build --platform linux/amd64 -t "$image" build
fi
if [[ ${1:-} == --image-only ]]; then
	exit 0
fi
exec docker run --rm --platform linux/amd64 -v "$PWD:/work" -u "$(id -u):$(id -g)" \
	${GIT2_KEEP_NAMES:+-e GIT2_KEEP_NAMES=1} "$image"
