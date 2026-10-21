#!/bin/sh
# Builds vpxdec.wasm.zst: libvpx's VP9 decoder as a WebAssembly reactor module;
# KIND=encoder builds vpxenc.wasm.zst, its VP9 encoder, instead.
#
# Needs zstd, which compresses the module, and
# clang with a wasm32 backend, wasm-ld, and the WASI sysroot plus the
# wasm32 compiler-rt builtins from https://github.com/WebAssembly/wasi-sdk
# (the "wasi-sysroot" and "libclang_rt" assets of a release are enough; the
# full SDK works too), and the headers of SIMDe
# (https://github.com/simd-everywhere/simde, v0.8.2).
#
#   WASI_SYSROOT=/path/to/wasi-sysroot RESOURCE_DIR=/path/to/resource-dir \
#     LIBVPX=/path/to/libvpx-checkout SIMDE=/path/to/simde [KIND=encoder] ./build.sh
#
# The modules embedded were built from libvpx v1.17.0 (the encoder; the
# decoder's version was not recorded) and SIMDe v0.8.2, with wasi-sdk 34's
# clang 22 and sysroot.
#
# RESOURCE_DIR is a directory holding lib/wasm32-unknown-wasip1/libclang_rt.builtins.a
# and an include symlink to the compiler's own headers.
#
# libvpx has no WebAssembly SIMD of its own. It is configured for arm64, so
# that its decoder takes its NEON intrinsics, and arm_neon.h here turns them
# into WebAssembly SIMD128 through SIMDe: about three times as fast as the
# plain C build, with the same frames bit for bit.
set -eu

: "${KIND:=decoder}"
case "$KIND" in
  decoder) codec="--enable-vp9-decoder --disable-vp9-encoder" shim=vpxshim.c name=vpxdec ;;
  encoder) codec="--disable-vp9-decoder --enable-vp9-encoder" shim=vpxencshim.c name=vpxenc ;;
  *) echo "KIND is decoder or encoder" >&2; exit 1 ;;
esac

: "${CLANG:=clang-22}"
: "${AR:=llvm-ar-22}"
: "${RANLIB:=llvm-ranlib-22}"
: "${NM:=llvm-nm-22}"
: "${WASI_SYSROOT:?set WASI_SYSROOT}"
: "${RESOURCE_DIR:?set RESOURCE_DIR}"
: "${LIBVPX:?set LIBVPX to a libvpx checkout}"
: "${SIMDE:?set SIMDE to a SIMDe checkout}"

here=$(cd "$(dirname "$0")" && pwd)
target="--target=wasm32-wasip1 --sysroot=$WASI_SYSROOT -resource-dir $RESOURCE_DIR"
simd="-msimd128 -I$SIMDE"

# libvpx is configured for arm64, but WebAssembly's pointers are 32 bits: the
# encoder's NEON diamond search keeps addresses in vectors of 64-bit lanes on
# AArch64, which read outside the module's memory (speed 6, real time). It
# takes its 32-bit Arm way here, which SIMDe runs as well.
sed -i '/UINTPTR_MAX/!s/^#if VPX_ARCH_AARCH64/#if VPX_ARCH_AARCH64 \&\& UINTPTR_MAX > 0xffffffffu/' \
  "$LIBVPX/vp9/encoder/arm/neon/vp9_diamond_search_sad_neon.c"

build="$LIBVPX/build-wasm-$KIND"
mkdir -p "$build"
cd "$build"

# configure enables NEON when the compiler takes -march=armv8-a, which wasm32
# does not; the wrappers drop -march and pass the rest on.
for cc in cc c++; do
  real="$CLANG"
  [ "$cc" = c++ ] && real="${CLANG%clang*}clang++${CLANG#*clang}"
  cat >"$cc" <<EOF
#!/bin/sh
for arg; do
  shift
  case "\$arg" in -march=*) ;; *) set -- "\$@" "\$arg" ;; esac
done
exec "$real" "\$@"
EOF
  chmod +x "$cc"
done

# libvpx reaches for setjmp to bail out of a broken frame; see setjmp.h here.
CC="$PWD/cc" CXX="$PWD/c++" AR="$AR" RANLIB="$RANLIB" NM="$NM" LD="$PWD/cc" \
CFLAGS="$target -I$here $simd -O3 -fno-strict-aliasing" CXXFLAGS="$target -I$here $simd -O3 -fno-exceptions" LDFLAGS="$target" \
../configure --target=arm64-linux-gcc \
  --disable-neon-dotprod --disable-neon-i8mm --disable-sve --disable-sve2 \
  --disable-multithread --disable-runtime-cpu-detect \
  $codec --disable-vp8 \
  --disable-examples --disable-tools --disable-docs --disable-unit-tests \
  --disable-webm-io --disable-libyuv --disable-postproc \
  --enable-static --disable-shared
grep -q '^#define HAVE_NEON 1' vpx_config.h || { echo "NEON was not enabled" >&2; exit 1; }
make -j"$(nproc)"

# The shim is the only surface the host can reach.
"$CLANG" $target $simd -O3 -mexec-model=reactor \
  -I"$LIBVPX" -I"$build" -I"$here" \
  "$here/$shim" "$build/libvpx.a" \
  -o "$here/$name.wasm"
# Embedded zstd-compressed at its highest level, from the file, so that
# the frame tells its size (pkg/unpack checks it); zstd writes no name or
# time, and the same build gives the same bytes.
zstd --ultra -22 -q -f "$here/$name.wasm" -o "$here/../$name.wasm.zst"
rm "$here/$name.wasm"

echo "built $(cd "$here/.." && pwd)/$name.wasm.zst"
