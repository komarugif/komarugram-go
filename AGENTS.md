# AGENTS.md

Start here. This file is for AI agents working in this repository: what to
read, what to run, and what not to break. `README.md` is the human
documentation of the same things at length; read its sections only when a
task needs them.

## What this is

A Telegram desktop client in Go on Gio (module `komarugram`), a replacement for
Telegram Desktop (tdesktop) that works offline from its local cache, plus
`kitchen`, a demo of widgets. The maintainer writes in Russian: answer in
Russian; code, comments and docs are in English.

| Path | What |
|---|---|
| `cmd/messenger` | Entry point; `account_host.go` runs accounts, windows and their stores |
| `cmd/render-all` | Renders every screen of the render tests into one directory |
| `cmd/haiku-build` | Builds the messenger on Haiku itself, patching the modules that need it (`docs/BUILD_HAIKU.md`) |
| `cmd/emoji-pack` | Makes the catalog of emoji packs built into the client (`official`: Telegram Desktop's emoji sets, pinned to commits), and catalogs of a directory |
| `internal/messenger/ui` | All messenger UI. Components: `docs/UI_COMPONENTS.md` |
| `internal/messenger/tgstore` | `model.Store` backed by Telegram (gotd): chats, history, search, updates |
| `internal/messenger/historycache` | Per-account SQLite cache: messages (JSON), media, layouts, FTS5 search index |
| `internal/messenger/{fonts,emojipacks}` | Font files of the user's own; emoji packs of a catalog, installed beside the settings |
| `internal/messenger/account` | Registry, sign-in, tdata import, one client per auth key |
| `internal/messenger/model` | Data types and the interfaces the UI reads |
| `internal/messenger/{security,securedb}` | TPM-sealed key, encrypted SQLite (Adiantum VFS) |
| `internal/messenger/mockstore` | Demo store for `-demo` |
| `pkg/*` | Reusable parts: `dcpool` (download connections), `tdata`, media decoders |
| `third_party/gio`, `third_party/gio-mw` | Forks, wired with `replace`; changes to Gio go in `third_party/gio/LOCAL_CHANGES.md` |
| `tdesktop`, `ayugram`, `materialgram`, `telegram-android` | Not in the repository, and ignored by git: local clones of Telegram Desktop, two of its forks and Telegram for Android, made when needed (see "Telegram sources") |

## Read before

- What to do next: `docs/PLAN.md` — porting features of AyuGram and materialgram,
  with the rules for it (behavior, not code: they are GPLv3), and the
  technical debts.
- UI work: `docs/UI_COMPONENTS.md`. Reuse `surface`, `tabRow`, `folderChip`,
  `modal`… instead of new widgets; check the result by looking at it.
- Telegram behavior (errors, limits, flows): how Telegram Desktop and the
  forks do it, in `*/Telegram/SourceFiles`, widgets in `*/Telegram/lib_ui`,
  and https://core.telegram.org/api. Clone what you need first, without
  asking: see "Telegram sources". Look at both forks for a feature
  to port: it may be one fork's own, as AyuGram's snapshots are.
  Telegram for Android is the second reference, for what Telegram Desktop
  lacks or does differently (`TMessagesProj/src/main/java/org/telegram`;
  its strings in `TMessagesProj/src/main/res/values/strings.xml`).
  Texts shown to the user map to tdesktop's `lng_…` keys in
  `internal/messenger/localization` (`TelegramKeys`).
- Performance or memory: `docs/PROFILING.md`; README "Pitfalls".

## Commands

```sh
go build -o /dev/null ./cmd/messenger          # never leave binaries in the tree
go vet ./internal/... ./cmd/... ./pkg/...
go test ./internal/... ./cmd/... ./pkg/...
gofmt -l internal cmd pkg
go vet gioui.org/app                           # the Gio fork, by its import path
go run ./cmd/messenger -demo                   # no account needed
go run ./cmd/render-all [dir]                  # every render test, into one directory
```

Commands here are for a POSIX shell; in PowerShell, set a variable with
`$env:NAME = "value"` before the command.

These cross-builds must keep working:

```sh
go build -tags nowayland ./cmd/messenger       # also nox11, novulkan
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/messenger ./cmd/kitchen
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/messenger
```

Render tests save PNGs when their variable is set, and are skipped
otherwise, so `go test` never runs them: `COMPOSER_PNG` (with
`COMPOSER_VIEW`), `COMPOSER_MOTION_PNG`, `SETTINGS_PNG` (with
`SETTINGS_SECTION`), `ACCOUNTS_PNG_DIR`, `SESSION_PNG_DIR`, `SESSIONS_PNG`, `FORUM_PNG`,
`STICKER_SET_PNG_DIR`, `MENU_PNG`, `REACTED_PNG`, `VIEWER_PNG` (with
`VIEWER_ZOOM`), `PLAYER_PNG`, `COMMENTS_PNG`, `UNWRAPPED_PNG`,
`SERVICE_PNG`, `JUMP_PNG_DIR`, `PINNED_PNG`, `CHAT_SEARCH_PNG`, `SHOT_PNG`,
`SAVED_EMPTY_PNG`, `SHARED_PNG`, `TOAST_PNG_DIR`, `AUDIO_PNG_DIR`,
`FRAME_PNG_DIR` (the window's own frame, in `internal/appwindow`) (see
`docs/UI_COMPONENTS.md`). Run the one of the screen you changed, and look
at the PNG. `KOMARUGRAM_FONT`, `KOMARUGRAM_FONT_EXTRA`,
`KOMARUGRAM_FONT_MONO` and `KOMARUGRAM_FONT_EMOJI` name a font file for the
text, for what it lacks, for code and for emoji, over the settings: in the
client and in the render tests alike (`internal/messenger/fonts`).
`KOMARUGRAM_EMOJI_SET` names the directory of an emoji pack to draw emoji
with, there too, and `KOMARUGRAM_EMOJI_PACKS` a catalog of packs for the
settings to offer in place of the one built in, a directory or a URL
(`cmd/emoji-pack` makes both). `go run ./cmd/render-all [dir]` renders all of them, every
variant, into one directory (about a minute; `komarugram-renders` in the
system's temporary directory by default; `-only composer` for some of
them). Nothing compares them with references: the project is in active
development, and the renders are for looking at.

When a change reaches much of the UI at once (a shared component such as
`surface`, `modal`, `toast` or `pill`, the theme, typography, spacing,
the history's layout), run `cmd/render-all`, look over the PNGs of the
places it touches, and remind the maintainer to look them over too.

`pkg/h264`'s test needs `KOMARUGRAM_AVCDEC` set to the absolute path of an
`avcdec.wasm` ([libavcodec-wasm](https://github.com/komarugif/libavcodec-wasm)),
and `pkg/aac`'s and the M4A voice message's `KOMARUGRAM_AACDEC`, of an
`aacdec.wasm` ([fdk-aac-wasm](https://github.com/komarugif/fdk-aac-wasm)): these
modules are not in this repository, and the client fetches them
(`internal/messenger/wasmmodule`).

## Rules

- **The README files in the root (`README.md`, `README_RU.md`) change only
  with the maintainer's consent.** Propose the text instead; when a change
  is agreed, make it in both languages.
- **Third-party licenses are in `docs/THIRD_PARTY_LICENSES.md`**, not in
  the READMEs: a dependency, embedded file or downloaded module gets its
  row there when it comes in.
- **Dependencies are the maintainer's choice.** Do not add a Go module, a
  library or a tool the build or the app needs on your own. Propose the
  options with their trade-offs — for a decoder of media from strangers,
  a C library compiled to wasm and run in `pkg/sandbox` beside a pure-Go
  one, saying which runs outside the sandbox — and wait for the choice. A
  direction agreed ("a small audio library") still leaves the library to
  choose.
- **Root causes, not workarounds.** Measure before and after; say what was
  verified and what was not.
- **Live-test on the operating system you work on.** The project is
  developed on Linux, Windows and macOS alike: keep code and tools portable (Go,
  not shell, for tools in `cmd`), and say which systems a change touches
  and was not checked on, for someone who uses them. macOS is
  supported as well.
- **Memory is a feature.** The app must give memory back to the OS (hidden
  windows drop their GPU context, `malloc_trim` after a window closes). When
  RSS grows but the Go heap does not, look at the native side.
- **Binary size.** Compare stripped builds (`-ldflags='-s -w'`) after adding
  a dependency. Nothing may call `reflect.Type.Method`/`MethodByName` (godbus
  `Export` did, and doubled the binary through gotd's `tg`).
- **One connection per auth key, main sessions only on the main DC.** Two
  clients on one key, or parallel sessions to the main DC's non-media
  addresses, make Telegram kill the key with `AUTH_KEY_DUPLICATED`. `dcpool`
  sends downloads from the home DC to its media-only addresses for this.
- **Accounts on a developer's machine are their real ones.** Read-only calls are fine for
  testing; never send, edit or delete. Don't search public posts: each search
  spends one of the day's free searches.
- **Found messages are not saved to history.** The cache keeps each chat's
  history without gaps; single messages from search would break that.
- **SQLite payloads are JSON BLOBs:** `json_extract(CAST(payload AS TEXT), …)`.
  FTS5 is registered for every connection in `historycache` (`AutoExtension`);
  its triggers need it.
- **A new test must fail without the fix.** Break the code, run, restore.

## Gio in short

- Handle input at the top of `Layout` (or in `Update`), before drawing what it
  changes; otherwise nothing redraws until the next event.
- Zero `gtx.Constraints.Min` before content that should take its own size.
- Each window has its own goroutine and GPU context; stores are shared and
  must be safe for concurrent use.
- On X11, `Window.Run`/`Option` run on the caller's goroutine: anything a
  driver method uses must be set before `SetDriver`.
- Never call another window's `Option`, `Perform` or `Run` from a window's
  goroutine and wait: on macOS one main thread serves every window, and
  while it hands an event to one it runs only that window's calls, so both
  hang. Use `appwindow`'s `SetTitle` and `PerformLater`.

The full list, with the story behind each item, is README "Pitfalls met while
working on this code".

## Testing the app live

- `-demo` runs on made-up data, without an account; nothing it does
  reaches Telegram.
- `-no-integrations` makes the client find no FFmpeg, player or browser on
  the system, only the paths set in the settings: the way to see it as on a
  machine without them.
- Run the built binary from a scratch directory in the background. The
  demo's sticker and video, and the kitchen's pages, read `assets/` from
  the working directory: start those from the repository's root, the
  binary still built outside the tree.
- Local data is in the user's configuration directory, `komarugram-go` in
  it (accounts, sessions, `history.db.*`), crash reports in the cache
  directory's `komarugram-go/crashes`: `~/.config` and `~/.cache` on Linux,
  `%AppData%` and `%LocalAppData%` on Windows.

On Linux under X11 (XFCE here):

- Stop the client with `pkill -x messenger`: `pkill -f <path>` also
  matches, and kills, the shell that runs it.
- Drive a window with `xdotool mousemove --window <id> x y click 1` (client
  coordinates) and `xdotool type`; find it by its title with `wmctrl -l`
  (Gio's windows carry no PID there, `wmctrl -lp` shows 0); screenshot the
  active window with `xfce4-screenshooter -w -s file.png`.
- Close windows with `wmctrl -i -c <id>`, not `xdotool windowclose`: Gio never
  sees a `DestroyEvent` then.
- The tray (SNI) can be called over D-Bus: `busctl --user call
  org.kde.StatusNotifierItem-<pid>-1 /StatusNotifierItem
  org.kde.StatusNotifierItem Activate ii 0 0`.
- The instance socket is `$XDG_RUNTIME_DIR/komarugram-go.sock`; a path over 107
  bytes fails, so point `XDG_RUNTIME_DIR` at a short symlink when needed.

## Telegram sources

Telegram Desktop is the reference for Telegram's behavior, texts and UI,
AyuGram and materialgram are the forks features are ported from
(`docs/PLAN.md`), and Telegram for Android is the second reference, for
what Telegram Desktop lacks or does differently. They are not in the
repository. When a task needs one, clone it into the root under the name
below, without asking, and without its history: a shallow clone, then
remove every `.git` in it. `.gitignore` keeps these directories out of
KomaruGram's commits. Of Telegram Desktop and its forks only the
submodules `Telegram/lib_ui`, `lib_base` and `lib_tl` are needed; the
others, and `ThirdParty`, are large and can stay empty.

| Directory | Repository |
|---|---|
| `tdesktop` | https://github.com/telegramdesktop/tdesktop |
| `ayugram` | https://github.com/AyuGram/AyuGramDesktop (adds features such as message snapshots, `ayu/features/message_shot`) |
| `materialgram` | https://github.com/kukuruzka165/materialgram (restyles the UI) |
| `telegram-android` | https://github.com/DrKLO/Telegram (Telegram for Android) |

```sh
git clone --depth 1 https://github.com/AyuGram/AyuGramDesktop ayugram
git -C ayugram submodule update --init --depth 1 Telegram/lib_ui Telegram/lib_base Telegram/lib_tl
# then delete ayugram/.git and the .git of each submodule
```

Of Telegram for Android only the code is kept: after the clone, delete
`TMessagesProj/src/main/assets`, everything in `TMessagesProj/src/main/res`
but `values*` and `xml`, `TMessagesProj_AppTests`, the native libraries
in `TMessagesProj/jni` (`prebuild`, `voip`, `third_party`, `sqlite`,
`openssl`; `tgnet` stays), and images, fonts, sounds and archives
(about 77 MB left of 350).

A clone that is there already may be old: pull a new one when the code it
describes has to be current.
