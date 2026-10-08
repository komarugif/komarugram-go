# Platforms: the graphics stack, and Unix systems other than Linux

How Gio draws on each platform, and what building KomaruGram for other
Unix-like systems takes, from a FreeBSD build tried in October 2026; and
how it is built and run on Haiku, from the port of October 2026.

**What is checked stays Linux, Windows and macOS.** A native build for FreeBSD,
and likely OpenBSD, is possible and cheap to port, but nobody runs it;
Haiku runs the demo, with the steps below and patches kept out of
`go.mod`. Live checks, the tests and the cross-builds in `AGENTS.md` are
for Linux, Windows and macOS only. A
change need not be checked on these systems, and nothing here is a
promise that they work.

## How Gio draws

Gio's renderer (`third_party/gio/gpu/gpu.go`) is one for every platform;
under it are the graphics APIs of each:

| Platform | Windows | Graphics | Chosen by |
|---|---|---|---|
| Linux, Wayland | `app/os_wayland.go` | OpenGL ES 3 through EGL; Vulkan if that fails | `NewContext`: EGL first |
| Linux, X11 | `app/os_x11.go`, `os_x11_xi2.go` | OpenGL ES 3 through EGL | Vulkan is there but off: `vulkanBuggy = true` |
| Windows | `app/os_windows.go` | Direct3D 11; EGL (ANGLE's `libEGL.dll`) if that fails | priority: D3D11 1, EGL 2 |
| macOS | `app/os_macos.go` | Metal; OpenGL with the `nometal` tag | — |
| Haiku | `app/os_haiku.go`, `app/internal/haiku` | OpenGL 3.3 core through OSMesa (llvmpipe), into memory; the window shows the frames | the only one (see Haiku below) |
| Android, iOS, js | their own | GLES or Vulkan, Metal or GLES, WebGL | — |

- On Linux the client draws with OpenGL ES in practice. `libEGL` is
  linked; `libGLESv2.so.2` and `libvulkan.so.1` are loaded with `dlopen`
  at run time (`internal/gl/gl_unix.go`, `internal/vk/vulkan.go`).
- The blur of the overlays (`paint.PushBlur`) uses the existing blit
  shaders and adds none, so it works on every API.
- Render tests draw with `gpu/headless`, through EGL on Linux.
- The tags `nowayland`, `nox11`, `novulkan` and `noopengl` leave a backend
  out of the build.

## Gio on Unix systems other than Linux

- **FreeBSD**: X11 and Wayland, EGL and Vulkan. The cgo flags name
  FreeBSD's paths (`/usr/local/include`, `/usr/local/lib`) and link the
  libraries directly, without pkg-config.
- **OpenBSD**: X11 and EGL only, no Wayland or Vulkan.
- **NetBSD**: not supported by Gio's `app`; a build fails in
  `gioui.org/internal/gl`.
- Gio needs cgo on all of them: without it, `gioui.org/internal/vk` has no
  files to build.

## FreeBSD: built, not run

`cmd/messenger` was cross-built and linked for FreeBSD 14 on amd64 from
Linux: a dynamic ELF for FreeBSD 14.0 of 68 MB, needing only libraries of
the ports:

```
libwayland-egl.so.1  libwayland-client.so.0  libwayland-cursor.so.0
libX11.so.6  libxkbcommon.so.0  libxkbcommon-x11.so.0  libX11-xcb.so.1
libXcursor.so.1  libXfixes.so.3  libEGL.so.1  libthr.so.3  libc.so.7
```

`libGLESv2` (`mesa-libs`) and `libvulkan` are loaded at run time. It was
not run: the development machine is a VirtualBox guest without nested
virtualization, where FreeBSD with a desktop would run under emulation
alone. A real machine or a VM of FreeBSD is needed to try it.

### What stands in the way

1. **`pkg/deviceinfo/libc_linux_cgo.go`** is `//go:build cgo` and is built
   on every system with cgo: Go takes an OS from the last element of a
   file name only, and this one ends in `_cgo`. It includes glibc's
   `<features.h>`, which FreeBSD lacks; by the code macOS should fail the
   same way (not tried). The fix: `//go:build linux && cgo` and
   `linux && !cgo` for the two files, and a `libcVersion` for other
   systems. Not fixed yet; the build above replaced the files with
   `go build -overlay`.
2. **Vulkan's headers** are needed to build (`vulkan-headers` in the
   ports), or the `novulkan` tag.
3. **Memory**: compiling `github.com/gotd/td/tg` for a new target peaks at
   4.8 GB. On a machine with 6 GB, build it alone first
   (`go build -p 1 github.com/gotd/td/tg`), then the client.

### What works differently there

Read from the code, not tried:

| What | On FreeBSD |
|---|---|
| Tray | SNI over D-Bus, as on Linux (`internal/tray/tray_sni.go` names FreeBSD) |
| Sound (`oto`) | PulseAudio's protocol in pure Go, else ALSA loaded with `purego`: needs PulseAudio or PipeWire running, or `alsa-lib` with its OSS plugin |
| SQLite, the wasm sandbox | pure Go (`wazero`), as everywhere |
| TPM | `go-tpm` opens `/dev/tpmrm0`, and `/dev/tpm0` when that does not exist. FreeBSD's TPM 2.0 driver makes `/dev/tpm0` only (`TPM_CDEV_NAME` in [`sys/dev/tpm/tpm20.h`](https://github.com/freebsd/freebsd-src/blob/releng/14.3/sys/dev/tpm/tpm20.h)), without a resource manager. The diagnosis of access (`security/access_linux.go`) is Linux's only |
| Voice messages | not available: `pkg/voice/input_other.go` returns an error. FreeBSD's ffmpeg has `-f oss` and usually `-f pulse`; an `input_freebsd.go` would be a few lines |
| Dark theme, power saving | stubs (`appearance_other.go`, `powersave_other.go`), though the freedesktop portal and D-Bus are there on FreeBSD too |
| Giving memory back | no `malloc_trim` (glibc's) |

### How it was built

From Linux, with [zig](https://ziglang.org/download/) 0.16 as the C
compiler (it carries FreeBSD's libc headers and stubs) and the headers and
libraries of FreeBSD's packages, from
[pkg.freebsd.org](https://pkg.freebsd.org/FreeBSD:14:amd64/latest/):
`libX11`, `xorgproto`, `libxkbcommon`, `libXcursor`, `libXfixes`,
`libXrender`, `libxcb`, `libXau`, `libXdmcp`, `libXext`, `wayland`,
`libglvnd`, `libffi`, `libxml2`, `libepoll-shim` and `vulkan-headers`.
Their `.pkg` files are tar archives in zstd; the package catalog,
`packagesite.pkg`, gives the path of each (`repopath`). Unpacked into
`$ROOT`:

```sh
cat > cc <<EOF
#!/bin/sh
exec /path/to/zig cc -target x86_64-freebsd "\$@"
EOF
chmod +x cc
CGO_ENABLED=1 GOOS=freebsd GOARCH=amd64 CC=$PWD/cc \
  CGO_CFLAGS="-I$ROOT/usr/local/include" \
  CGO_LDFLAGS="-L$ROOT/usr/local/lib" \
  go build -p 2 -o messenger-freebsd ./cmd/messenger
```

zig, the packages and the build take about 1.5 GB of disk with the Go
build cache for the new target. On FreeBSD itself it is the ordinary
build, with the packages above and Go installed.

## OpenBSD

Not tried: a check of the build ran out of disk. Gio gives it X11 and EGL;
the tray falls back to `tray_other.go` (SNI is built for Linux and FreeBSD
only), and the `deviceinfo` file above stands in the way as on FreeBSD.

## Haiku

Built and run in October 2026: `messenger -demo` runs on Haiku R1/beta6
(`hrev59866+79`, x86_64), drawn in software. The development machine ran
it in a VirtualBox VM with 4 cores and 6 GB, cross-built from Linux. The
maintainer looked at it live on 2026-10-08: quick, for a VM. Nothing of
a real account was tried.

Haiku is not a Unix by descent, but it has a good POSIX layer (libc,
threads, sockets, `mmap`), which is why Go and ffmpeg run there. What is
its own is the GUI: the `app_server` and the Be API in C++, with no X11 or
Wayland server. Gio had no backend for it; the one here is new
(`third_party/gio/app/os_haiku.go`, `gl_haiku.go`, `app/internal/haiku`).

### Building

Everything is built on Linux; Haiku only runs the program. Compiling
`github.com/gotd/td/tg` for a new target peaks at 4.8 GB, which Haiku's
own `go` needs too, and the `go` command on Haiku crashed and stalled now
and then.

1. **Go.** [komarugif/go-haiku](https://github.com/komarugif/go-haiku),
   branch `golang-1.27-haiku`, built on Linux with `src/make.bash`
   (2 minutes): Go 1.27.1 (the version `go.mod` needs) with the Haiku port
   of Jérôme Duval, [korli/go](https://github.com/korli/go), brought onto
   it, and fixes found running the client, each a commit (its `HAIKU.md`
   lists them). The ones that showed:
   - the runtime's `_SS_DISABLE` was Solaris's 4, not Haiku's 2: with cgo,
     the runtime asked `sigaltstack` whether C had set a signal stack, read
     Haiku's "none" as "one", and ran signal handlers on goroutines'
     stacks. Programs died at random with `unexpected return pc`,
     `traceback did not unwind completely` or `morestack on gsignal`;
   - the process ended with `exit`, not `_exit` as on Solaris: `exit` ran
     the C++ destructors of every library (libbe, OSMesa's LLVM) while the
     windows' threads still ran, and closing two windows at once ended in
     `Segmentation violation` five times out of five;
   - sockets were made nonblocking with `SOCK_NONBLOCK` in `socket()`,
     which Haiku does not honor in `accept` (below): the client's
     single-instance socket held a thread in `accept`, and the process never
     ended after its windows closed.

   [Quad4-Software/go-haiku](https://github.com/Quad4-Software/go-haiku),
   another Go 1.27.1 from korli's port, whose commits are an LLM agent's,
   has the first two, makes sockets as above, and turns cgo off; the
   client was first built with it.
2. **cgo** works (`CGO_ENABLED=1`), with Go's internal linker only
   (`-ldflags=-linkmode=internal`): Haiku links programs as shared objects
   (gcc's spec passes `-shared`), and the external linker fails on Go's
   local-exec TLS (`R_X86_64_TPOFF32 against runtime.tlsg`); the fork has
   no `-buildmode=pie`. The C compiler is clang (22 here) with
   `--target=x86_64-unknown-haiku -fuse-ld=lld --sysroot=$ROOT`, its Haiku
   driver finding everything under `$ROOT/boot/system/develop`: copy
   Haiku's `/boot/system/develop/headers` and `/boot/system/develop/lib`
   there (with `tar h`, the libraries are links), and from
   `/boot/system/develop/tools/lib/gcc/x86_64-unknown-haiku/13.3.0` the
   `crtbegin*.o`, `crtend*.o`, `libgcc*`, `include` and `include-fixed`;
   add `/boot/system/lib/libgcc_s.so.1` and `libstdc++.so.6.0.32` to
   `develop/lib`, and link `$ROOT/boot/system/lib` to `develop/lib`.
3. **Modules**, patched outside the repository and taken in with a
   `go.work` beside it, not with `go.mod`:
   - `golang.org/x/sys` v0.48.0, which knows no Haiku: copy the `*_haiku*`
     files of the toolchain's own copy, `src/cmd/vendor/golang.org/x/sys/unix`,
     into `unix`, and apply
     [`haiku/x-sys-v0.48.0.patch`](haiku/x-sys-v0.48.0.patch) (`haiku`
     in 15 files' build tags, as go-haiku has them, and `Mprotect`, which
     wazero needs and go-haiku's copy lacks).
   - `github.com/tetratelabs/wazero` v1.12.0,
     [`haiku/wazero-v1.12.0.patch`](haiku/wazero-v1.12.0.patch): Haiku's
     `Stat_t.Ino` is an `int64`; and the compiler for `haiku/amd64`, which
     needs only `mmap` and `mprotect`. Without it wazero interprets, and
     the history cache and the decoders run many times slower.
   - `gioui.org/shader` v1.0.9,
     [`haiku/gioui-shader-v1.0.9.patch`](haiku/gioui-shader-v1.0.9.patch):
     it fills in the GLSL 1.50 sources, which Gio's desktop OpenGL uses,
     on macOS only; on Haiku they were empty.
   - `github.com/go-text/typesetting` v0.3.4,
     [`haiku/go-text-typesetting-v0.3.4.patch`](haiku/go-text-typesetting-v0.3.4.patch):
     `fontscan` knows no font directories of Haiku and fails, so Gio found
     no system font and drew everything with the Go fonts it carries: no
     CJK, Thai or most symbols, and not Haiku's own Noto Sans. The patch
     names the four of `finddir` (`B_SYSTEM_FONTS_DIRECTORY`, the user's
     and the non-packaged ones).

   ```
   go 1.27.1

   use /path/to/komarugram-go

   replace golang.org/x/sys => /path/to/x-sys
   replace github.com/tetratelabs/wazero => /path/to/wazero
   replace gioui.org/shader => /path/to/shader
   replace github.com/go-text/typesetting => /path/to/typesetting
   ```
4. **Tags.** `sqlite3_flock`: without a tag `go-sqlite3` knows no file
   locks on Haiku and fails every lock, `disk I/O error`
   (`SQLITE_IOERR_LOCK`). `sqlite3_dotlk` works too, but leaves lock files
   behind a crashed process.
5. **The client:**

   ```sh
   GOWORK=/path/to/go.work GOOS=haiku GOARCH=amd64 CGO_ENABLED=1 \
     CC="clang --target=x86_64-unknown-haiku --sysroot=$ROOT -fuse-ld=lld" \
     /path/to/go-haiku/bin/go build -ldflags=-linkmode=internal \
     -tags sqlite3_flock -o messenger ./cmd/messenger
   ```
6. **libgiohaiku.so**, the C++ half of Gio's driver, which the program
   loads from beside itself or from `lib` beside it (or from
   `GIO_HAIKU_LIB`):

   ```sh
   go run third_party/gio/app/internal/haiku/build.go \
     -cxx "clang++ --target=x86_64-unknown-haiku --sysroot=$ROOT -fuse-ld=lld" \
     -o libgiohaiku.so
   ```

   On Haiku itself, `go run build.go -o libgiohaiku.so` uses g++.

Copy `messenger`, `libgiohaiku.so` and, for `-demo`, `assets` to Haiku.
Then, on Haiku, give `messenger` its resources: its signature, flags,
version and icon, from `cmd/messenger/messenger.rdef`:

```sh
rc -o messenger.rsrc messenger.rdef
xres -o messenger messenger.rsrc
```

The icon is `assets/logo_round.hvif`, made from `assets/logo_round.svg` by
`icon2icon` of the package `hvif_tools`; to make it anew, put the new
file's bytes into the rdef's `vector_icon` (`xxd -p -c 32`, each line a `$"…"`).

ffmpeg is the package `ffmpeg6_tools` (`pkgman install ffmpeg6_tools`);
`ffmpeg6` is its libraries only.

### Gio's Haiku driver

- **Two halves.** Go's internal linker takes C, not C++: classes come in
  COMDAT groups (`unrecognized symbol in section ".group"`), and C++
  exceptions abort. The Be API's half is a library of its own,
  `libgiohaiku.so` (`giohaiku.cpp`, its C interface in `giohaiku.h`),
  which `os_haiku.go` opens with `dlopen` and calls through pointers;
  nothing of it is needed to build. `GH_ABI` must match.
- **Threads.** A `BApplication` runs in a thread of its own. Each window
  is a `BWindow` whose thread puts what happens into a queue;
  `gh_window_next_event` hands it to the window's goroutine, which waits
  on it as other drivers wait on their event sources.
- **Drawing.** With OSMesa, Mesa's llvmpipe drawing into memory, on the
  window's goroutine (core profile 3.3, BGRA, rows from the top); the
  finished frame is copied into a `BBitmap`, which the window's thread
  draws when told. GL's functions are `libOSMesa.so.8`'s. Not the others:
  Haiku's EGL does not initialize (`EGL_NOT_INITIALIZED`), and a
  `BGLView`, drawn into from another thread, locks the window from it:
  the window's thread waited on the drawing, so a window that animated
  took no input at all. One message to show a frame waits at a time:
  one a frame piled up in the window's queue, and input waited behind
  them for seconds.
- **Colors.** OSMesa's framebuffer has no sRGB encoding, which Gio's
  desktop OpenGL takes for granted. Gio draws into an `SRGB8_ALPHA8`
  texture, and `Present` blits it to the framebuffer without decoding
  it. The context is `Shared`, so Gio reads the state anew each frame:
  otherwise it took `GL_FRAMEBUFFER_SRGB` for on after the blit turned it
  off, and every frame but the first was too dark. It keeps a vertex
  array bound, which the core profile needs.
- **The signature.** `BApplication` takes the signature the program's
  file carries in its resources, and Gio's `application/x-vnd.<ID>`
  (`application/x-vnd.messenger`) only when it has none: a
  `BApplication` of a signature other than its file's is told of on the
  terminal, and the roster keeps the file's icon and flags under the
  file's signature.
- **Frames** come only when Gio asks, and none while the window is
  minimized.
- **Keys.** Haiku's Command (Alt on most keyboards) and Control are both
  Gio's Ctrl, as in Qt's Haiku port; Option, the Windows key, is Alt.
  Keys are named by their codes, with the US layout's letter for
  shortcuts in other layouts. `BWindow`'s own Command+W, X, C, V and A
  are removed, for Gio to have them; Command+Q asks every window to
  close.
- **Pointer.** The first click on an inactive window reaches Gio
  (`B_WILL_ACCEPT_FIRST_CLICK`). Haiku's wheel message has no position:
  the wheel scrolls at the pointer's last, a notch 100 pixels, as on
  Wayland and Windows.
- **The window** keeps within the screen, less the Deskbar when it lies
  across it, and is centered there; the client asked for 1200×760 and
  got 1014×713 on a screen of 1024×768.
- **The clipboard** is `be_clipboard`'s `text/plain` and other MIME types;
  input methods give their confirmed text only.
- `GIO_HAIKU_TRACE=1` prints the events the driver gives Gio and frames
  slower than 30 ms. With `GIO_HAIKU_INPUT` naming a file, each window
  looks for it ten times a second, runs the commands in it (`move X Y`,
  `down X Y`, `up X Y`, `click X Y`, `wheel DX DY`, `key TEXT`) as the
  `app_server`'s messages would come, and deletes it: input for testing
  when no one can click.

### Checked on Haiku

From 2026-10-07 to 2026-10-08, in the VM above:

| What | Result |
|---|---|
| Tests, built on Linux and run there | `historycache` 11/11 and `securedb` 2/2 (with `sqlite3_flock`); `sandbox`, `aac`, `cmark`, `drdec`, `h264`, `lottie`, `opus`, `ratex` all pass with wazero's compiler; `vp9` passes, but its 250 ms limit for a frame was missed while the VM stalled |
| `messenger -demo` | the chat list and chats draw, colors right; clicks, scrolling with the wheel, hover, resizing, minimizing; 8 starts of a Gio test program and 3 of the client without a crash |
| Speed | first frame 1.3 s, then 30–50 ms a frame at 1014×713 with `LP_NUM_THREADS=3`; a chat opened about 2.5 s after the click on an older install, which stalled often |
| Resources | the icon of the rdef shows for the program (seen by the maintainer, 2026-10-08); with the file's signature taken, the demo prints nothing on the terminal |
| Mini Apps | in Firefox 157 (`pkgman install firefox`), over WebDriver BiDi: the demo's app opens from the bot's menu button without the browser's toolbars, with its init data and theme, and Firefox closes with the client (2026-10-08). Clicks inside Firefox were not tried: `GIO_HAIKU_INPUT` reaches Gio's windows only |
| Links | opened with Haiku's `open`, as on macOS, in the browser the system names (WebPositive here): a bot's URL button, after the confirmation (2026-10-08) |
| System fonts | Noto Sans for text and Noto Sans Mono for code, from `/boot/system/data/fonts`, with the patch of `go-text/typesetting` above |
| Choosing files | Haiku's own panel, `filepanel` (in the system): opening a file for the attachment menu leads to the box for sending it; saving prints its path the same way (2026-10-08). The Open button was pressed by the maintainer once and then by `~/haiku-tools/tests/sendrefs`, which sends the panel's messages |
| Editing | typing, Backspace, the arrows and Delete in an editor of a Gio test program |

`LP_NUM_THREADS` limits llvmpipe's threads (one a core otherwise): with
all four busy, Haiku stopped answering over SSH while a first frame was
drawn.

### Not done

| What | On Haiku |
|---|---|
| Sound (`oto`) | no backend for the Media Kit; `oto` falls to PulseAudio's protocol, which Haiku has no server for |
| Voice messages | ffmpeg's only input device there is `lavfi`: no microphone; recording would be the Media Kit's |
| Tray, notifications | the Deskbar's replicants and Haiku's `BNotification`, not written; SNI over D-Bus does not apply |
| TPM | none; how the key is kept without one was not tried |
| Emoji | no emoji font on the system, nor in HaikuPorts: the boxes go once an emoji pack is chosen in Settings → Appearance (Apple, downloaded from Telegram Desktop's repository; checked 2026-10-08) |
| Other windows | the photo viewer's transparent window, drag and drop: not tried |
| The clipboard, shortcuts | written, not tried |
| 3D acceleration | none in practice: the drivers for AMD and Intel set modes only, and an accelerated one for NVIDIA Turing and Ampere is an alpha of January 2026 ([OSnews](https://www.osnews.com/story/144097/haiku-gets-accelerated-nvidia-graphics-driver/)) |

### Choosing files

The client asks the system's own chooser for files (`chooseFiles`,
`chooseStickerArchive` in `internal/messenger/ui`); on Haiku that is
`filepanel`, a command of the system that shows a `BFilePanel` and prints the
paths chosen. It ends with status 1 after a choice as well as after Cancel: the
panel sends `B_CANCEL` as it closes, after the files. So on Haiku what it
printed is the choice, whatever its status. It has no filter by type: every
file shows, where Linux's choosers show only images for "Photo or video".

### Browsers for Mini Apps

Haiku has no Chromium-based browser, so Mini Apps and the browser player run in
Firefox, which HaikuPorts builds with its WebDriver BiDi (`pkg/miniapp/bidi.go`).
It prints `Error parsing B_ARGV_RECEIVED message` on the terminal at every
start, which changes nothing. A Firefox started on a profile while another
Firefox on it is still quitting fails: the bridge waits for the browser to exit.

Ladybird would not do, as of October 2026: it has no WebDriver BiDi and no
DevTools protocol of Chromium's, only WebDriver classic, through a server of
its own (`Services/WebDriver`) that starts the browser itself — no events and
no script that runs before the page's, so the page's half of the transport
would be a queue the client polls. HaikuPorts' `ladybird` is a build of July
2022.

### Things met on the way

- A program that crashes on Haiku is not killed: the debug server holds
  it behind a dialog, and it looks hung. `~/config/settings/system/debug_server/settings`
  can make chosen programs end with a report on the Desktop instead:
  `executable_actions { *.test report  messenger report }`.
- In the VM, the AHCI disk timed out under load (`ahci: ExecuteAtaRequest
  port 0: device timeout` in `/var/log/syslog`): everything stalled for
  minutes, and files written just before a hard reset came back as
  zeros. VirtualBox's host I/O cache helped; the VM was then reinstalled
  on a disk of a fixed size.
- A socket made with `SOCK_NONBLOCK` in `socket()` reports `O_NONBLOCK`
  on Haiku R1/beta6, but `accept` on it blocks; set by `fcntl`, the flag
  works (a C test, 2026-10-08). The Go fork above makes sockets so.
- Haiku's `ps` puts a command's arguments in its first column: the team's
  ID is `$(NF-3)`, not `$2`.
- `hey` drives a window by scripting:
  `hey messenger set Minimize of Window 0 to true`.
