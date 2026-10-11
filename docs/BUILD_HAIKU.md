# Building KomaruGram Go on Haiku

## Quick setup script

From a clone of this repository on Haiku, [cmd/haiku-setup.sh](../cmd/haiku-setup.sh)
can install the packages and build the patched Go automatically:

```sh
sh cmd/haiku-setup.sh
. "$HOME/config/settings/profile"
```

Then continue with **step 3**, building the messenger. The script installs
`git`, `golang` and `ffmpeg6_tools`, shallow-clones the `golang-1.27-haiku`
branch into `~/go-haiku` if absent, builds it with the bootstrap settings
below, checks the result and adds it to the profile's `PATH` once. It tries
the bootstrap up to three times, as Haiku's packaged Go can fail even with
those settings; after that it stops and refers to the recovery steps below.
A compatible existing installation is reused; sources are not pulled or
reset automatically, and local changes are rejected. When all required
system packages are present, pkgman skips refreshing repository metadata.
To prepare a fresh installation elsewhere, use `--go-dir "$HOME/go-haiku-new"`; `--no-profile`
leaves the profile unchanged. The script prints the export needed by the
current shell too: a child script cannot change its parent's environment,
and SSH commands do not read the Terminal profile automatically.

The complete manual procedure follows.

These steps build the messenger on Haiku itself, from a Terminal, with
nothing but Haiku and its packages. They took about 20 minutes on Haiku R1/beta6 in a virtual machine of
4 cores and 6 GB (October 2026), and need about 4.5 GB of free memory and
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
takes about 5 minutes and about 4.5 GB of memory; the peak varies with
Go and the build target, so close what you can meanwhile. Later builds
take seconds to a few minutes, as much as changed.

`haiku-build` already builds one package at a time (`-p 1`) and uses
`GOGC=50` to collect garbage sooner, unless it is already set. For less
memory, use `-low-memory`: it defaults to `GOMAXPROCS=1` and
`GOMEMLIMIT=1792MiB`, preserves explicit environment values, and disables
inlining only in `github.com/gotd/td/tg`. It applies to `-test` too:

```sh
go run ./cmd/haiku-build -low-memory -o ~/apps/KomaruGram
go run ./cmd/haiku-build -low-memory -test
```

Leave memory for Haiku and the native build tools. `GOMEMLIMIT` does not
cap the entire build or guarantee that a large package fits: it trades
more garbage collection for lower memory use. Disabling inlining may reduce
the application's runtime performance. See [Building with less
memory](BUILD_LOW_MEMORY.md) for details and equivalent manual build flags.

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

## Running the tests

```sh
cd ~/komarugram-go
go run ./cmd/haiku-build -test
```

`-test` runs `go test` instead of building the messenger, with the same
patched modules and flags, over `./internal/... ./cmd/... ./pkg/...`. The
four libraries are built into the work directory, and the tests are told
to load them from there. What follows a `--` goes to `go test` in place of
the packages:

```sh
go run ./cmd/haiku-build -test -- -run TestPlayback -v ./pkg/audio
```

The first run builds every package's tests, which takes long and as much
memory as the build. The tests of `pkg/h264` and `pkg/aac` need
`KOMARUGRAM_AVCDEC` and `KOMARUGRAM_AACDEC`, as on other systems, and are
skipped without them.

Offscreen rendering uses an OSMesa OpenGL context, without a window.
On Haiku R1/beta6 in VirtualBox (2026-10-11), Gio's headless tests and the
six UI tests that previously failed with `headless: no available GPU
backends` pass with this backend. The full test run passes too (unaffected
packages may use Go's test cache), and `cmd/render-all` produces 173 PNGs.

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
