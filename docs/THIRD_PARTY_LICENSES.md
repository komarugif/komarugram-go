# Third-party licenses

What `messenger` and `kitchen` contain or load that others wrote, and under which licenses. The project's own license, and the two components it loads at run time under stronger terms, are in the README's "License" section.

A new dependency, an embedded file or a module downloaded at run time gets its row here when it is added (the dependency itself is the maintainer's choice; see AGENTS.md).

## Go modules

The Go modules linked into `messenger` and `kitchen`, as `go version -m` lists them for Linux and Windows builds:

| Component | License |
|---|---|
| Go (standard library and runtime) | BSD-3-Clause |
| [Gio](https://gioui.org) (`third_party/gio`, `gioui.org/shader`) | Unlicense OR MIT |
| [gio-mw](https://git.sr.ht/~schnwalter/gio-mw) (`third_party/gio-mw`) | Unlicense OR MIT |
| [gotd/td](https://github.com/gotd/td) | MIT |
| [gotd/ige](https://github.com/gotd/ige) | MIT |
| [gotd/log](https://github.com/gotd/log) | Apache-2.0 |
| [gotd/neo](https://github.com/gotd/neo) | BSD-3-Clause |
| [go-faster/errors](https://github.com/go-faster/errors), [go-faster/xor](https://github.com/go-faster/xor) | BSD-3-Clause |
| [go-faster/jx](https://github.com/go-faster/jx) | MIT |
| [wazero](https://github.com/tetratelabs/wazero) | Apache-2.0 |
| [ebitengine/oto](https://github.com/ebitengine/oto), [ebitengine/purego](https://github.com/ebitengine/purego) | Apache-2.0 |
| [jfreymuth/pulse](https://github.com/jfreymuth/pulse) | MIT |
| [ncruces/go-sqlite3](https://github.com/ncruces/go-sqlite3), [ncruces/julianday](https://github.com/ncruces/julianday) | MIT |
| [ncruces/go-sqlite3-wasm](https://github.com/ncruces/go-sqlite3-wasm) | MIT-0; SQLite inside it is public domain |
| [lukechampine.com/adiantum](https://github.com/lukechampine/adiantum) | MIT |
| [go-text/typesetting](https://github.com/go-text/typesetting) | Unlicense OR BSD-3-Clause |
| [dlclark/regexp2](https://github.com/dlclark/regexp2) | MIT |
| [godbus/dbus](https://github.com/godbus/dbus) | BSD-2-Clause |
| [google/go-tpm](https://github.com/google/go-tpm) | Apache-2.0 |
| [coder/websocket](https://github.com/coder/websocket) | ISC |
| [refraction-networking/utls](https://github.com/refraction-networking/utls) | BSD-3-Clause |
| [klauspost/compress](https://github.com/klauspost/compress) | BSD-3-Clause |
| [andybalholm/brotli](https://github.com/andybalholm/brotli) | MIT |
| [cespare/xxhash](https://github.com/cespare/xxhash) | MIT |
| [segmentio/asm](https://github.com/segmentio/asm) | MIT-0 |
| [cenkalti/backoff](https://github.com/cenkalti/backoff) | MIT |
| [srwiley/oksvg](https://github.com/srwiley/oksvg), [srwiley/rasterx](https://github.com/srwiley/rasterx) | BSD-3-Clause |
| [uber-go/zap](https://github.com/uber-go/zap), [multierr](https://github.com/uber-go/multierr), [atomic](https://github.com/uber-go/atomic) | MIT |
| [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) (`otel`, `otel/trace`) | Apache-2.0 |
| `golang.org/x/crypto`, `exp`, `exp/shiny`, `image`, `net`, `sync`, `sys`, `text` | BSD-3-Clause |
| [rsc.io/qr](https://github.com/rsc/qr) | BSD-3-Clause |
| [Go fonts](https://go.dev/blog/go-fonts) (`gioui.org/font/gofont`) | BSD-3-Clause |

## Embedded and downloaded files

| Component | License |
|---|---|
| `pkg/vp9/vpxdec.wasm`: [libvpx](https://github.com/webmproject/libvpx) with [SIMDe](https://github.com/simd-everywhere/simde) | BSD-3-Clause (libvpx), MIT (SIMDe) |
| `pkg/opus/opusdec.wasm`: [libopus](https://github.com/xiph/opus) (notice in `pkg/opus/COPYING.libopus`) | BSD-3-Clause |
| `pkg/drdec/drdec.wasm`: [dr_libs](https://github.com/mackron/dr_libs) (dr_mp3, dr_flac, dr_wav; `pkg/drdec/LICENSE.dr_libs`) | Unlicense OR MIT-0 |
| `pkg/lottie/tlottie.wasm`: [tlottie](https://github.com/dkaraush/tlottie) and the Rust standard library | MIT; MIT OR Apache-2.0 |
| WASI libc and compiler-rt inside the modules above ([wasi-sdk](https://github.com/WebAssembly/wasi-sdk)) | MIT, Apache-2.0 WITH LLVM-exception |
| `pkg/ratex/ratex.wasm.gz`: [RaTeX](https://github.com/erweixin/RaTeX), its Rust crates and the Rust standard library (built by `pkg/ratex/build`) | MIT; MIT OR Apache-2.0; Unlicense OR MIT; Zlib OR Apache-2.0 OR MIT |
| `pkg/ratex/fonts`: [KaTeX](https://github.com/KaTeX/KaTeX)'s fonts (`pkg/ratex/fonts/OFL.txt`, `NOTICE`) | SIL OFL 1.1 |
| `pkg/cmark/cmark.wasm.gz`: [cmark-gfm](https://github.com/desktop-app/cmark-gfm) (desktop-app's fork, built by `pkg/cmark/build`; `pkg/cmark/LICENSE.cmark-gfm`) | BSD-2-Clause, parts MIT |
| `pkg/prism/grammars.dat.gz`: [Prism.js](https://prismjs.com) 1.29.0's grammars, with `pkg/prism`, a port of [libprisma](https://github.com/desktop-app/libprisma)'s tokenizer (`pkg/prism/LICENSE.prism`) | MIT |
| `pkg/miniapp/assets/telegram-web-app.js`: Telegram's Mini App SDK | © Telegram, no license stated |
| `avcdec.wasm`, downloaded at run time: FFmpeg's H.264 decoder ([libavcodec-wasm](https://github.com/komarugif/libavcodec-wasm)) | LGPL-2.1-or-later |
| `aacdec.wasm`, downloaded at run time: the Fraunhofer FDK AAC decoder ([fdk-aac-wasm](https://github.com/komarugif/fdk-aac-wasm)) | Fraunhofer FDK AAC license, no patent grant |

The full texts are in each module's `LICENSE` file (`go env GOMODCACHE`) and in the repositories linked above.
