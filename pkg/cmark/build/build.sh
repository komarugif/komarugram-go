#!/bin/sh
# Builds cmark.wasm.gz: cmark-gfm, the CommonMark and GitHub Flavored
# Markdown parser, from desktop-app's fork that Telegram Desktop builds
# (https://github.com/desktop-app/cmark-gfm, branch tdesktop, d7d4a24,
# 2026-08-14: cmark-gfm 0.29.0.gfm.13), behind cmshim.c, as a WebAssembly
# reactor module.
#
# Needs clang with a wasm32 backend, wasm-ld, and the WASI sysroot plus the
# wasm32 compiler-rt builtins from https://github.com/WebAssembly/wasi-sdk
# (see ../../vp9/build/build.sh), and the fork's source:
#
#   WASI_SYSROOT=/path/to/wasi-sysroot RESOURCE_DIR=/path/to/resource-dir \
#     CMARK=/path/to/cmark-gfm ./build.sh
#
# include/ holds the headers CMake would generate.
#
# cmark-gfm is under the BSD-2-Clause license, with parts under the MIT
# license (../LICENSE.cmark-gfm), so the module is embedded in the binary.
set -eu

: "${CLANG:=clang-22}"
: "${WASI_SYSROOT:?set WASI_SYSROOT}"
: "${RESOURCE_DIR:?set RESOURCE_DIR}"
: "${CMARK:?set CMARK to cmark-gfm}"

here=$(cd "$(dirname "$0")" && pwd)
sources=$(ls "$CMARK"/src/*.c "$CMARK"/extensions/*.c | grep -v '/main\.c$')

# shellcheck disable=SC2086
"$CLANG" --target=wasm32-wasip1 --sysroot="$WASI_SYSROOT" -resource-dir "$RESOURCE_DIR" \
	-O2 -mexec-model=reactor -Wl,--strip-all -Wl,--gc-sections \
	-ffunction-sections -fdata-sections -DCMARK_GFM_STATIC_DEFINE -DCMARK_GFM_EXTENSIONS_STATIC_DEFINE \
	-I"$here/include" -I"$CMARK/src" -I"$CMARK/extensions" \
	$sources "$here/cmshim.c" \
	-o "$here/cmark.wasm"
gzip -9 -n -c "$here/cmark.wasm" >"$here/../cmark.wasm.gz"
rm "$here/cmark.wasm"
echo "built $(cd "$here/.." && pwd)/cmark.wasm.gz"
