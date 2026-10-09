# Building KomaruGram Go on Windows 7

These steps build the messenger on Windows 7 itself, from the Command
Prompt. They were checked on Windows 7 SP1 x64 in a virtual machine of 4
cores and 6 GB (October 2026): the first build took about 4 minutes,
later ones well under one. They need about 1.6 GB of disk: Go, the
modules and the build cache.

Go has not run on Windows 7 since Go 1.21, so the messenger is built with
[go-legacy-win7](https://github.com/thongtech/go-legacy-win7), a Go with
Windows 7's support kept in. Nothing else is patched: the messenger's own
code handles what Windows 7 lacks. What it handles, and what was checked
on Windows 7, is in [PLATFORMS.md](PLATFORMS.md), under "Windows 7".

Where it is built does not matter, the toolchain does: built with
go-legacy-win7 on Linux (`GOOS=windows`), the messenger runs on Windows 7
too, and built with the official Go it does not.

## 1. Git

Install [Git for Windows 2.46.2](https://github.com/git-for-windows/git/releases/tag/v2.46.2.windows.1),
the last version for Windows 7: later installers refuse to run on it.
The default options will do.

## 2. Go for Windows 7

Download `go-legacy-win7-1.27.1-1.windows_amd64.zip` (or the latest
release's) from
[go-legacy-win7's releases](https://github.com/thongtech/go-legacy-win7/releases),
open it in Explorer and extract it to `C:\`, which makes
`C:\go-legacy-win7`.

Put its `bin` folder first on the user's `Path`: Control Panel → System →
Advanced system settings → Environment Variables, and in the user's
variables add or change `Path` to

```
C:\go-legacy-win7\bin;%USERPROFILE%\go\bin
```

then open a new Command Prompt and check it:

```bat
go version
go env -w GOTOOLCHAIN=local
```

`go version` should say `go1.27.1 windows/amd64`. `GOTOOLCHAIN=local`
keeps Go from downloading the official toolchain when a module asks for a
newer Go: that one would not run on Windows 7.

## 3. The messenger

```bat
cd %USERPROFILE%
git clone https://github.com/komarugif/komarugram-go
cd komarugram-go
go install gioui.org/cmd/gogio@latest
gogio -ldflags="-s -w" -icon=assets\logo_round.png -target=windows -arch=amd64 -o KomaruGram.exe ./cmd/messenger
```

`gogio` builds the program with its icon and its manifest. The first build
downloads the modules and takes a few minutes; later ones take under a
minute.

## 4. Running it

Open `KomaruGram.exe` from Explorer. At the first start it offers to
install itself: into `%LocalAppData%\Programs\KomaruGram` by default, with
shortcuts and an entry in the list of programs, from which it is
uninstalled; it can also run as it is. `KomaruGram.exe -demo` runs on
made-up data, without an account. Settings and accounts are kept in
`%AppData%\komarugram-go`.

For video, install [VLC](https://www.videolan.org/vlc/) and
[FFmpeg](https://www.gyan.dev/ffmpeg/builds/) (the essentials build;
point the settings at its `ffmpeg.exe` under External integrations).
mpv's builds need Windows 8.1, and the messenger does not offer it on
Windows 7. Mini Apps open in [Supermium](https://github.com/win32ss/supermium),
a Chromium that runs on Windows 7.

## Updating

```bat
cd %USERPROFILE%\komarugram-go
git pull
gogio -ldflags="-s -w" -icon=assets\logo_round.png -target=windows -arch=amd64 -o KomaruGram.exe ./cmd/messenger
```

Quit the messenger first, from its icon in the tray: Windows cannot
replace a running program. An installed copy is updated by running the
new `KomaruGram.exe` and installing it into the same folder.

## When something goes wrong

- `go` crashes at once with `Exception 0xc0000005` at `PC=0x0`: the `go`
  that ran is the official one, not go-legacy-win7; check the `Path`
  (step 2) with `where go`.
- `go: downloading go1.2x...` then a crash: Go fetched the official
  toolchain; run `go env -w GOTOOLCHAIN=local` and build again.
- The window stays black or blank: in a virtual machine without 3D, the
  messenger draws with WARP, Direct3D's software renderer, on its own
  when the driver fails; `GIO_D3D11_WARP=1` forces it.
- Tell us in an issue, with the crash report from
  `%LocalAppData%\komarugram-go\crashes`.
