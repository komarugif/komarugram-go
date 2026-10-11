# Building an AppImage

[Русская версия](BUILD_APPIMAGE_RU.md).

`cmd/appimage-build` builds the Linux messenger and packages it with a
square 256×256 icon, `.DirIcon`, a desktop entry, a launcher and license
notices. It needs the same Go, C compiler and development libraries as a
normal Linux build, plus [appimagetool](https://github.com/AppImage/appimagetool).
No new Go modules are required.

## appimagetool

The command uses `-appimagetool` or `APPIMAGETOOL` when set, then looks for
`appimagetool` on `PATH`. Otherwise it downloads the official
[continuous release](https://github.com/AppImage/appimagetool/releases/tag/continuous)
for the **build machine's** CPU (x86_64 or aarch64). The complete executable
is stored in `$XDG_CACHE_HOME/komarugram-go/appimage-build` (normally
`~/.cache/komarugram-go/appimage-build`). Later builds reuse it without
checking for updates. Remove the cached tool to download a newer release.
Failed or interrupted downloads never replace the cached executable.

For a specific tool version or offline use, pass a local executable:
`-appimagetool ~/tools/appimagetool.AppImage`. An invalid explicit path is
reported as an error rather than triggering a download.

The packager is run with `APPIMAGE_EXTRACT_AND_RUN=1`, so packaging does
not require FUSE. `appimagetool` normally downloads the AppImage runtime;
`-runtime /absolute/path/to/runtime` uses a local one instead. For repeatable
offline packaging, keep both the tool and runtime versions you have checked.

## Build

From the repository:

```sh
go run ./cmd/appimage-build -o /tmp/KomaruGram.AppImage
```

For a machine with less memory:

```sh
GOMAXPROCS=1 GOGC=50 GOMEMLIMIT=1792MiB go run ./cmd/appimage-build -low-memory -o /tmp/KomaruGram.AppImage
```

The command builds one package at a time and defaults to `GOGC=50`.
`-low-memory` also defaults to `GOMAXPROCS=1` and `GOMEMLIMIT=1792MiB`,
without replacing values already set in the environment, and disables
inlining only in `github.com/gotd/td/tg`. This may reduce the application's
runtime performance; the memory limit is soft. See
[Building with limited memory](BUILD_LOW_MEMORY.md).
SquashFS compression uses one worker too.

`-tags nowayland` or `-tags novulkan` passes build tags to Go.
`-keep-work` keeps and prints the temporary AppDir for inspection;
otherwise it is removed, even after a failed build. A failed build or
packaging leaves an existing output AppImage untouched. Without `-o`, the
output is `KomaruGram-x86_64.AppImage` or `KomaruGram-aarch64.AppImage` in
the system's temporary directory. Use `-h` for all options.

## Run and limitations

```sh
/tmp/KomaruGram.AppImage -demo
```

The launcher preserves arguments and the caller's working directory.
Normal launches use the user's usual settings and accounts. `-demo` uses
made-up data and reaches no Telegram account. The existing installer
recognizes `APPIMAGE` and does not offer self-installation.

This is a small AppImage using the system's graphics libraries and libc;
it does not bundle EGL, X11, Wayland, graphics drivers, FFmpeg or browsers.
Build on the oldest glibc distribution you intend to support and test on
the others. A glibc binary built on Mint does not become compatible with
Alpine/musl just by packaging it. This command does not provide that
compatibility or a fully self-contained distribution.

The icon is inside the file. File managers show it when an AppImage
thumbnailer is available; menu integration still needs a desktop entry
installed by an external integration tool. See the
[AppDir specification](https://docs.appimage.org/reference/appdir.html).

Checked on Linux Mint 22.3, x86_64, XFCE/X11 in VirtualBox with
appimagetool build 313 (`854e19e`) and type2-runtime `8f39b89`.
ARM64 and other desktops have not been live-tested.
