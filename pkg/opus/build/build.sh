#!/bin/sh
# Builds opusdec.wasm.gz: libopus's decoder as a WebAssembly reactor module.
#
# Needs clang with a wasm32 backend, wasm-ld, and the WASI sysroot plus the
# wasm32 compiler-rt builtins from https://github.com/WebAssembly/wasi-sdk
# (see ../../vp9/build/build.sh), and libopus's sources
# (https://github.com/xiph/opus, v1.6.1):
#
#   WASI_SYSROOT=/path/to/wasi-sysroot RESOURCE_DIR=/path/to/resource-dir \
#     OPUS=/path/to/opus ./build.sh
#
# libopus is BSD-licensed, so the module is embedded in the binary. Its
# sources are compiled as they are, float build, without the optional
# neural network parts (deep PLC, DRED, OSCE); the linker keeps what the
# decoder uses.
set -eu

: "${CLANG:=clang-22}"
: "${WASI_SYSROOT:?set WASI_SYSROOT}"
: "${RESOURCE_DIR:?set RESOURCE_DIR}"
: "${OPUS:?set OPUS to libopus's sources}"

here=$(cd "$(dirname "$0")" && pwd)
cd "$OPUS"

# The source lists of libopus's own makefiles.
sources() {
	for var in "$@"; do
		awk -v v="$var" '
			$1 == v && $2 == "=" { on = 1; next }
			on { line = $0; sub(/\\$/, "", line); n = split(line, f); for (i = 1; i <= n; i++) print f[i]; if ($0 !~ /\\$/) on = 0 }
		' opus_sources.mk celt_sources.mk silk_sources.mk
	done
}

"$CLANG" --target=wasm32-wasip1 --sysroot="$WASI_SYSROOT" -resource-dir "$RESOURCE_DIR" \
	-O2 -msimd128 -mexec-model=reactor -Wl,--strip-all \
	-DOPUS_BUILD -DVAR_ARRAYS -DHAVE_LRINT -DHAVE_LRINTF \
	-Iinclude -Icelt -Isilk -Isilk/float -Isrc \
	$(sources OPUS_SOURCES OPUS_SOURCES_FLOAT CELT_SOURCES SILK_SOURCES SILK_SOURCES_FLOAT) \
	"$here/opusshim.c" \
	-o "$here/opusdec.wasm"
# Embedded gzipped, without a name or a time, so that the same build
# gives the same bytes.
gzip -9 -n -c "$here/opusdec.wasm" >"$here/../opusdec.wasm.gz"
rm "$here/opusdec.wasm"

echo "built $(cd "$here/.." && pwd)/opusdec.wasm.gz"
