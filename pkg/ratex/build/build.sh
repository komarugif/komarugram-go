#!/bin/sh
# Builds ratex.wasm.gz: RaTeX's parser and layout (https://github.com/erweixin/RaTeX,
# MIT, at the commit Cargo.toml pins) behind the reactor of src/lib.rs, for
# wasm32-unknown-unknown, so that the module imports nothing at all.
#
# Needs Rust with the wasm32-unknown-unknown target (rustup target add
# wasm32-unknown-unknown) and the network, for the crates Cargo.lock pins:
#
#   ./build.sh
#
# CARGO_TARGET_DIR, when set, is where Cargo builds; by default a target
# directory beside this script, which git ignores.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
: "${CARGO_TARGET_DIR:=$here/target}"
export CARGO_TARGET_DIR

cd "$here"
cargo build --locked --release --target wasm32-unknown-unknown
gzip -9 -n -c "$CARGO_TARGET_DIR/wasm32-unknown-unknown/release/komarugram_ratex.wasm" >"$here/../ratex.wasm.gz"
