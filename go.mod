module komarugram

go 1.27.1

require (
	gio-mw v0.0.0-20260221053317-be0445f63b48
	gioui.org v0.10.2
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/coder/websocket v1.8.15
	github.com/dlclark/regexp2 v1.12.0
	github.com/ebitengine/oto/v3 v3.5.1
	github.com/go-faster/errors v0.8.0
	github.com/go-text/typesetting v0.3.4
	github.com/godbus/dbus/v5 v5.2.2
	github.com/google/go-tpm v0.9.8
	github.com/gotd/td v0.162.0
	github.com/ncruces/go-sqlite3 v0.35.5
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c
	github.com/srwiley/rasterx v0.0.0-20210519020934-456a8d69b780
	github.com/tetratelabs/wazero v1.12.0
	go.uber.org/multierr v1.11.0
	go.uber.org/zap v1.28.0
	golang.org/x/crypto v0.57.0
	golang.org/x/exp v0.0.0-20250711185948-6ae5c78190dc
	golang.org/x/exp/shiny v0.0.0-20260611194520-c48552f49976
	golang.org/x/image v0.42.0
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
)

require (
	gioui.org/shader v1.0.9 // indirect
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/ebitengine/purego v0.11.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-faster/jx v1.2.0 // indirect
	github.com/go-faster/xor v1.0.0 // indirect
	github.com/go-faster/yaml v0.4.6 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gotd/ige v0.3.0 // indirect
	github.com/gotd/log v0.1.0 // indirect
	github.com/gotd/neo v0.1.5 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/ncruces/go-sqlite3-wasm/v6 v6.2.35304 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/ogen-go/ogen v1.23.0 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/yuin/goldmark v1.8.5 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	lukechampine.com/adiantum v1.1.1 // indirect
	rsc.io/qr v0.2.0 // indirect
)

replace gio-mw => ./third_party/gio-mw

// Local XKB fix: retain non-Latin key events and resolve shortcut keys.
replace gioui.org => ./third_party/gio
