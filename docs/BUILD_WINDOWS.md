# Building Windows executables

[Русская версия](BUILD_WINDOWS_RU.md).

From the repository, run:

```sh
go run ./cmd/windows-build -o /tmp/KomaruGram.exe
```

In Windows PowerShell, for example:

```powershell
go run ./cmd/windows-build -o "$env:TEMP\KomaruGram.exe"
```

The Go command combines the README's `go install gioui.org/cmd/gogio@latest`
and `gogio -target=windows ...` steps. It packages the application's icon,
manifest and version resource. It works on Windows and can cross-build from
Linux or macOS. No C cross-compiler is needed: the Windows client is built
with `CGO_ENABLED=0`. No project Go modules are added or changed.

`gogio` is taken from `-gogio`, `GOGIO` or `PATH`. If absent, the command
installs it with `go install` into a host-specific directory under the user's
cache (`komarugram-go/windows-build`), without changing the global `GOBIN`.
Later builds reuse it. `-gogio-version v0.10.0` pins the version installed
when the tool is absent; the default is `latest`. Remove that cache directory
to reinstall the latest version, or supply a local tool explicitly.

For limited memory, add `-low-memory`:

```powershell
go run ./cmd/windows-build -low-memory -o "$env:TEMP\KomaruGram.exe"
```

The command always sets `-p=1` through `GOFLAGS` and defaults to `GOGC=50`.
`-low-memory` also defaults to `GOMAXPROCS=1`, `GOMEMLIMIT=1792MiB`, and disables
inlining only in `github.com/gotd/td/tg`. Existing GC and memory variables are
preserved. This trades compilation speed and possibly application performance
for less memory; see [Building with limited memory](BUILD_LOW_MEMORY.md).

`-arch amd64` and `-arch arm64` select the Windows architecture. The default
is amd64, or arm64 when the build host is ARM64. Without `-o`, the result
is `KomaruGram.exe` in the system temporary directory. Use `-h` for all flags.

Sources are copied into a temporary module, retaining local module
replacements and the `komarugram/` prefix needed by internal imports. Existing
`.syso` files are excluded, and gogio writes its resources there; the original
sources and resources are left untouched. Temporary files are cleaned up.
The previous output executable is replaced only after a successful build.
Close a running copy before replacing it on Windows.

Windows 7 requires [go-legacy-win7](BUILD_WINDOWS7.md) on `PATH` for this
command, gogio's installation, and compilation. The wrapper does not add
Windows 7 compatibility to the official Go toolchain.

Checked on Linux Mint 22.3, x86_64: automatic installation of gogio v0.10.3,
cross-builds for Windows amd64 and arm64 with icon, manifest and version
resources, and low-memory build flags. Native Windows/macOS execution of
the wrapper and the produced EXEs has not been checked in this session.
