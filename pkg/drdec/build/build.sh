#!/bin/sh
# Builds drdec.wasm.gz: dr_libs's MP3, FLAC and WAV decoders as a WebAssembly
# reactor module.
#
# Needs clang with a wasm32 backend, wasm-ld, and the WASI sysroot plus the
# wasm32 compiler-rt builtins from https://github.com/WebAssembly/wasi-sdk
# (see ../../vp9/build/build.sh), and dr_libs's headers
# (https://github.com/mackron/dr_libs, dfe8377, 2026-09-01: dr_mp3 v0.7.4,
# dr_flac v0.13.4, dr_wav v0.14.6):
#
#   WASI_SYSROOT=/path/to/wasi-sysroot RESOURCE_DIR=/path/to/resource-dir \
#     DR_LIBS=/path/to/dr_libs ./build.sh
#
# The file is read through the host.read import, a range at a time.
#
# dr_libs is in the public domain (Unlicense), or MIT No Attribution, so the
# module is embedded in the binary.
set -eu

: "${CLANG:=clang-22}"
: "${WASI_SYSROOT:?set WASI_SYSROOT}"
: "${RESOURCE_DIR:?set RESOURCE_DIR}"
: "${DR_LIBS:?set DR_LIBS to dr_libs}"

here=$(cd "$(dirname "$0")" && pwd)

"$CLANG" --target=wasm32-wasip1 --sysroot="$WASI_SYSROOT" -resource-dir "$RESOURCE_DIR" \
	-O2 -msimd128 -mexec-model=reactor -Wl,--strip-all \
	-I"$DR_LIBS" "$here/drshim.c" \
	-o "$here/drdec.wasm"
# Embedded gzipped, without a name or a time, so that the same build
# gives the same bytes.
gzip -9 -n -c "$here/drdec.wasm" >"$here/../drdec.wasm.gz"
rm "$here/drdec.wasm"

echo "built $(cd "$here/.." && pwd)/drdec.wasm.gz"
