# Building with limited memory

[Русская версия](BUILD_LOW_MEMORY_RU.md).

Allow about 4.5 GB of RAM for the first build, mainly for the generated `github.com/gotd/td/tg` package. This is an estimate: the peak depends on the Go version, target and parallelism. Close unnecessary applications. Later builds reuse Go's cache and are usually much faster.

On Linux/macOS (POSIX shell), build one package at a time, limit compiler concurrency and collect garbage sooner:

```sh
GOMAXPROCS=1 GOGC=50 GOMEMLIMIT=2GiB go build -p 1 -ldflags="-s -w" -buildvcs=false -o komarugram ./cmd/messenger
```

On Windows (PowerShell), the same settings also work with `gogio`; `GOFLAGS` passes `-p=1` to its Go commands:

```powershell
$env:GOMAXPROCS = "1"
$env:GOGC = "50"
$env:GOMEMLIMIT = "2GiB"
$env:GOFLAGS = "-p=1"
gogio -ldflags="-s -w" -icon=./assets/logo_round.png -target=windows -o komarugram.exe ./cmd/messenger
```

`-p 1` limits parallel build commands; `GOMAXPROCS=1` also reduces concurrency within each Go process. `GOGC=50` trades more frequent garbage collection for less memory. `GOMEMLIMIT=2GiB` is an example soft limit for each Go process, not a cap on the entire build or its RSS: leave memory for the OS, native tools and other applications. A package's live data may exceed the limit; lowering it further can make compilation much slower without preventing an out-of-memory failure. These options trade build speed for lower memory use; swap can help with remaining peaks.

If `gotd/td/tg` still does not fit, add `-gcflags=github.com/gotd/td/tg=-l` to `go build` (or to `GOFLAGS` when using `gogio`). This disables inlining only in that dependency and may reduce runtime performance. Windows cross-builds with this flag completed in a Linux VM under a 2.25 GiB memory limit; see [the validation details](RICH_TEXT.md#focused-validation).

On Haiku, `haiku-build` supports the same `-low-memory` mode (including with `-test`): it uses `-p 1`, defaults to `GOGC=50`, `GOMAXPROCS=1`, `GOMEMLIMIT=1792MiB`, and disables inlining only in gotd. Explicit environment limits are preserved:

```sh
go run ./cmd/haiku-build -low-memory -o ~/apps/KomaruGram
```
