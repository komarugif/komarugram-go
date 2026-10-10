# Building KomaruGram Go on Haiku

These steps build the messenger on Haiku itself, from a Terminal, with
nothing but Haiku and its packages. They took about 20 minutes on Haiku R1/beta6 in a virtual machine of
4 cores and 6 GB (October 2026), and need about 5 GB of free memory and
3 GB of disk.

Upstream Go does not support Haiku, so the messenger is built with
[go-haiku](https://github.com/komarugif/go-haiku), a Go that does; Haiku's
own `golang` package is only used to build it. Five Go modules the
messenger uses need small changes for Haiku, which are kept in
[`docs/haiku`](haiku) and applied by `cmd/haiku-build`.

How the port works, what was checked on Haiku and how to build from Linux
instead is in [PLATFORMS.md](PLATFORMS.md), under "Haiku".

## 1. Packages

```sh
pkgman install golang ffmpeg6_tools
```

`golang` builds go-haiku. `ffmpeg6_tools` plays video and records voice
messages; the messenger runs without it, with fewer features.
Optionally, `pkgman install mpv` or `vlc` plays video in a window of its
own, and `pkgman install firefox` opens Mini Apps.

## 2. Go for Haiku

```sh
cd ~
git clone --depth 1 -b golang-1.27-haiku https://github.com/komarugif/go-haiku
cd go-haiku/src
GOMAXPROCS=1 GODEBUG=asyncpreemptoff=1 GOROOT_BOOTSTRAP=/boot/system/develop/lib/go ./make.bash
```

This takes about 10 minutes. The two variables are needed: the `golang`
package has the bugs of Go's runtime on Haiku that go-haiku fixes, and its
signals corrupt memory now and then, which breaks the build with `not the
start of an archive file` or `exit status 255`; with one thread and no
preemption by signals it built. If it fails all the same, start it again
from clean:

```sh
cd ~/go-haiku && rm -rf pkg bin && cd src
GOMAXPROCS=1 GODEBUG=asyncpreemptoff=1 GOROOT_BOOTSTRAP=/boot/system/develop/lib/go ./make.bash
```

The Go it builds no longer needs them. Then put it first on the `PATH`, for this
Terminal and the ones opened later:

```sh
echo 'export PATH=$HOME/go-haiku/bin:$PATH' >> ~/config/settings/profile
export PATH=$HOME/go-haiku/bin:$PATH
go version
```

`go version` should say `go1.27.1 haiku/amd64`.

## 3. The messenger

```sh
cd ~
git clone https://github.com/komarugif/komarugram-go
cd komarugram-go
go run ./cmd/haiku-build -o ~/apps/KomaruGram
```

`haiku-build` downloads the modules, applies the changes for Haiku to
copies of them, builds the messenger and the four libraries it loads from
beside it, and gives the program its icon and signature. The first build
takes about 5 minutes and up to 4.5 GB of memory; close what you can
meanwhile. Later builds take seconds to a few minutes, as
much as changed.

## 4. Running it

Open `~/apps/KomaruGram/messenger` from Tracker, or from a Terminal:

```sh
~/apps/KomaruGram/messenger
```

`messenger -demo` runs on made-up data, without an account. Settings and
accounts are kept in `~/config/settings/komarugram-go`.

To have it in the Deskbar's menu:

```sh
ln -s ~/apps/KomaruGram/messenger ~/config/settings/deskbar/menu/KomaruGram
```

## Updating

```sh
cd ~/komarugram-go
git pull
go run ./cmd/haiku-build -o ~/apps/KomaruGram
```

Quit the messenger first, from its icon in the Deskbar.

## When something goes wrong

- `this Go ... has no Haiku files of golang.org/x/sys`: the `go` that ran
  is Haiku's `golang` package, not go-haiku; check the `PATH` (step 2).
- The build stops with `signal: killed`, or Haiku stops answering: it ran
  out of memory. Close other programs and run the same command again; what
  was built is kept.
- `apply ... .patch` fails: a module of another version than the patch's
  was downloaded, which happens when the messenger's `go.mod` moved on and
  the patches did not. Tell us in an issue.
- A crashed program is held by Haiku's debugger behind a dialog, and looks
  hung: choose to save a report, and attach it to an issue.
