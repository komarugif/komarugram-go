# komarugram: a messenger client and an MD3 kitchen (Gio)

Two applications built on the same base:

- **messenger** — a desktop messenger client: sign-in by phone number, a
  sidebar of sections, a resizable chat list and the page of the open chat,
  profile or settings. It reads chat history, renders media and follows live updates;
  chats support text, attachments, stickers, GIFs and checklists.
- **kitchen** — the MD3 widget kitchen from `gio-mw`, extended with media
  pages (video, stickers, Mini Apps, external playback). It is where widgets
  are tried out before the messenger uses them.

## Layout

| Path                        | What it is                                                        |
|-----------------------------|-------------------------------------------------------------------|
| `cmd/messenger`             | Messenger entry point                                             |
| `cmd/kitchen`               | Kitchen entry point                                               |
| `internal/appwindow`        | Shared window loop: theme, animation switch, system color scheme, F11 |
| `internal/crash`            | Recovers panics in a frame or a background load and writes crash reports |
| `internal/motion`           | Shared animation settings: user preference + power saving, and its UI |
| `internal/miniappprefs`     | Shared Mini App privacy settings (what an app may keep) and their UI |
| `internal/messenger/model`  | Messenger data types and the `Store` interface the UI reads        |
| `internal/messenger/mockstore` | Demo `Store` used with `-demo`                                  |
| `internal/messenger/account` | Accounts: the registry, sign-in by phone, import from tdata, log out, client identity, one connection per auth key |
| `internal/messenger/login`  | State of the sign-in for the UI: step, problem, answers            |
| `internal/messenger/security` | TPM-bound master key, session AEAD envelopes and unlock lifecycle |
| `internal/messenger/securedb` | Account-scoped encrypted SQLite opener |
| `internal/messenger/historycache` | SQLite history, media, viewport and update-state cache |
| `internal/messenger/chatmedia` | Media loading, bounded rendering cache and playback |
| `internal/messenger/styledtext` | Rich text rendering and glyph geometry for selection |
| `internal/messenger/richhtml` | A rich message as an HTML page: "Save as HTML", and the HTML of blocks copied |
| `internal/messenger/tgstore` | `Store` backed by a Telegram account through gotd               |
| `internal/messenger/ui`     | Messenger interface; its components: [docs/UI_COMPONENTS.md](docs/UI_COMPONENTS.md) |
| `internal/kitchen`          | Kitchen app shell, pages and services                             |
| `pkg/resample`              | Streaming Catmull-Rom image scaling with a few rows of memory       |
| `pkg/video`                 | ffmpeg-backed player and frame cache, independent of any UI        |
| `pkg/lottie`                | Lottie/.tgs renderer: tlottie compiled to wasm, run by wazero       |
| `pkg/ratex`                 | LaTeX formulas: RaTeX compiled to wasm, run by wazero; drawn with KaTeX's fonts |
| `pkg/cmark`                 | Markdown: cmark-gfm compiled to wasm, run by wazero; `internal/messenger/markdown` makes an article of its tree |
| `pkg/webm`                  | Matroska/WebM demuxer written in Go                                 |
| `pkg/vp9`                   | VP9 decoder: libvpx compiled to wasm, run by wazero                 |
| `pkg/opus`                  | Opus decoder for voice messages: libopus compiled to wasm, OGG parsed in Go, seekable |
| `pkg/drdec`                 | MP3, FLAC and WAV decoders: dr_libs compiled to wasm, run by wazero |
| `pkg/aac`                   | AAC decoder for M4A: fdk-aac compiled to wasm, fetched from fdk-aac-wasm, run by wazero |
| `pkg/audio`                 | Sound output through oto: PulseAudio/PipeWire or ALSA, WASAPI; suspended while idle; resampling to 48 kHz |
| `pkg/sandbox`               | Limits for the wasm sandboxes: memory per sandbox, a shared budget, slow-operation cutoff |
| `pkg/deviceinfo`            | The device model and system version, named as Telegram Desktop names them |
| `pkg/player`                | mpv or VLC as an external window, driven over its IPC socket         |
| `pkg/voice`                 | Voice messages: the microphone through ffmpeg, Opus encoding, Telegram's waveform |
| `pkg/program`               | Running external programs: `--version` checks, process groups, flatpak launchers |
| `pkg/tdata`                 | Telegram Desktop tdata (and Telethon/Pyrogram sessions): read and write |
| `pkg/set`, `pkg/helpers`    | Small generic helpers used by `pkg/tdata`                          |
| `pkg/miniapp`               | Telegram Mini Apps in the user's browser, bridged over CDP           |
| `assets/stickers/`, `assets/video.mp4`, `videos/` | Media the kitchen pages and `-demo` read from the working directory, the repository's root |
| `web/kitchen`               | A web build of the kitchen                                          |
| `third_party/gio` | Local Gio v0.10.2 with XKB shortcut handling fixes; see `LOCAL_CHANGES.md` |
| `third_party/gio-mw`        | Our fork of the widget library, wired in with a `replace`           |

The kitchen uses the fork's `exp/router` and `exp/examples` packages, which
upstream keeps under `internal/`; the fork makes them public so that the
project can have a module path of its own.

The messenger's UI is built from its own components: tabs, capsules,
clickable surfaces, dialogs and more. Before adding UI, see
[docs/UI_COMPONENTS.md](docs/UI_COMPONENTS.md) for what exists and how to
reuse it.

## Running

From the repository root, so that the kitchen finds its media:

```sh
go run ./cmd/messenger                     # sign in by phone number; the session is kept
go run ./cmd/messenger -demo               # demo data, no account
go run ./cmd/messenger -demo-chats 50000   # try the UI with a huge chat list
go run ./cmd/messenger -tdata pkg/tdata/assets/tdata.zip          # import and open a real account
go run ./cmd/messenger -tdata assets                              # import every tdata zip and open one window per account
go run ./cmd/messenger -tdata one.zip -tdata two.zip              # the same, with explicit archives
go run ./cmd/messenger -tdata assets -check                       # load every account, print safe counts and exit
go run ./cmd/kitchen
```

The kitchen's Video page requires `ffmpeg` and `ffprobe` on `PATH`.

## Messenger: profiler

Run `go run ./cmd/messenger -demo -profile` (or `-profile` with a real account)
for a separate diagnostics window: frame/FPS timings, runtime memory and cache
inventory, Telegram RPC metadata and chat-history/viewport events. CPU, heap,
allocation, goroutine profiles, runtime trace and JSON snapshots can be saved
on demand; `-profile-dir` selects the output directory.

See [docs/PROFILING.md](docs/PROFILING.md) for metric definitions, limits and a
repeatable history-regression workflow. Collection is off without `-profile`.

## Messenger: crash reports

A panic while a window lays out a frame does not end the process: the frame is
dropped, the window shows an error screen and tries again. A panic in a media
or history-page load becomes that item's error, with the usual retry. Each
report, with its stack, goes to the log and to `~/.cache/komarugram-go/crashes`
(`%LocalAppData%\komarugram-go\crashes` on Windows); the newest 32 are kept, and
a panic repeating every frame is written at most once every 10 seconds.
Recovered panics also open a separate dialog with actions to copy the report,
open its saved text file, or ignore that incident. `-demo-panic` starts on
demo data and raises one recoverable panic in the first UI frame to preview
the dialog without touching saved accounts.

## Messenger: accounts

The process starts with one window. If local data is protected, that window
is the one unlock screen of the process: nothing about the accounts, not even
which there are, can be read before the master password is entered. Then the
window reads the accounts, adds those given with `-tdata`, and becomes the
window of the last active account — or, with `-tdata`, of the first account
it added, the others each getting a window of their own. With no accounts it
signs in to one: phone number, the code Telegram sends, and the 2FA password
if the account has one. A wrong code or password is asked for again; "Назад"
returns to the phone number. Registering a new Telegram account is not
supported.

Before an account is added — by sign-in or by a `-tdata` archive with an
account not seen before — and while local data is not protected, the window
offers to protect it (see below). The offer can be declined; it comes again
before the next account is added.

Settings lists every account with its name, username or phone and profile
photo, whether or not its window has been opened in this run; each gets an
independent window and Telegram connection. **Add account** opens a sign-in
window, which becomes that account's window. Signing in to an account already
here is refused, and the new authorization is ended on the server so it does
not linger in the account's device list. **Log out** closes the account's
window, ends its session in Telegram (if Telegram answers within 30 seconds;
otherwise the session stays in the account's device list) and deletes its
directory and its row. Closing one window leaves the others running; closing
the last one ends the process.

Local data lives under the user's config directory (`%AppData%\komarugram-go` on
Windows, `~/.config/komarugram-go` on Linux):

| Path | What it is |
|------|------------|
| `security.json` | TPM-sealed root key and Argon2id salt, when protection is on |
| `accounts.db.plain` / `accounts.db.secure` | The registry: one row per account — user id, home DC, auth key fingerprint, order, name, username, phone and the 160 px profile photo as a BLOB |
| `accounts/<id>/session.json` | The account's gotd session, mode 0600 |
| `accounts/<id>/history.db.*` | The account's history, media and update-state cache |
| `settings.json` | Preferences, including the last active account |

The registry is SQLite: plaintext until protection is enabled, encrypted
after (see below). Sessions stay in files of their own: gotd reads and writes
a session through its `session.Storage` interface as one blob, each is used by
one connection at a time under its own lock, and under protection it is an
authenticated XChaCha20-Poly1305 envelope, which Adiantum-encrypted SQLite
does not give. Session writes replace the whole file, so a crash does not
leave half a key.

Earlier layouts (`account.json`, `card.json` and a single
`komarugram-go/session.json`) are not read; import such accounts again with `-tdata`.

### Local-data protection

Local data can be bound to TPM 2.0 and a master password, when an account is
about to be added or later in “Privacy and security”. Enabling it generates a
random root key and seals it as a TPM object whose authorization is derived
with Argon2id. The registry is copied into an encrypted database, renamed into
place, and only then the plaintext one deleted; every session is rewritten as
an envelope, and plaintext history caches are copied into encrypted SQLite.
A migration that was interrupted is completed on the next unlock. The TPM object uses
dictionary-attack protection and cannot be loaded under another TPM's storage
root, so a copied config directory cannot be opened on another machine even
when the password is weak or known.

The root key exists unsealed only in process memory. Clearing or replacing
the TPM, or forgetting the master password, makes protected data
unrecoverable; the accounts then have to be added again.

Settings → “Privacy and security” can permanently decrypt local data for a
backup or transfer to another computer. The action verifies the master
password again with the original TPM, even after the app has been unlocked.
It moves the account registry, sessions and history cache to plaintext and
removes the TPM configuration only after the migration completes. An
interrupted transition resumes after the next unlock. Protection can later
be enabled again from the same settings card. Deleted encrypted files may
still remain in filesystem snapshots or backups.

The same card offers visual window locking after a selected period of
inactivity, on minimize, and on closing an account window to the tray. The
three triggers are independent. Unlocking a window checks the master
password again; the account stays connected and the key remains in memory.

The application opens the TPM itself (`/dev/tpmrm0` on Linux, through
go-tpm), so it needs no TPM tools, only access to the device. Linux
distributions give that device to the `tss` group, which a user is not in by
default. When the TPM cannot be used, the offer and “Privacy and security”
say why and what to do:

- no TPM device: enable the TPM in the UEFI settings (fTPM on AMD, Intel PTT);
- the device belongs to no group: install the TPM udev rules and join `tss`
  (`sudo apt install tpm-udev`, `sudo usermod -aG tss $USER`);
- the user is not in the group: `sudo usermod -aG tss $USER`;
- the user joined the group in this login session: log out and in again.

`tpm2_getcap properties-fixed` (from `tpm2-tools`) checks access by hand.

`internal/messenger/securedb.Open` derives a different key for each account
(`OpenShared`, one for a database of no single account, such as the registry)
and opens SQLite through `go-sqlite3`'s pure-Go Adiantum VFS, with temporary tables
kept in memory. `historycache` uses this entry point when local-data protection is
enabled; otherwise its disposable cache uses a private plaintext SQLite file.
Adiantum provides confidentiality at rest but not a page-level authenticity
guarantee; integrity and recovery policy remain unfinished.

`pkg/tdata` brings accounts in from zipped Telegram Desktop `tdata` directories
(`-tdata`). The flag may be repeated and may name a directory, in which case
every `.zip` directly inside it is imported. Importing the same authorization
again is a no-op; another authorization key for an already registered Telegram
user is refused instead of overwriting the saved account. While a panel shows the tray icon
(a StatusNotifierItem on Linux, the notification area on Windows), closing an
account window leaves the account running in the background, so updates keep
arriving, and gives the window's memory back to the system, the GPU driver's
included; the icon, or starting the
messenger again, brings the windows back, and its menu quits. Without a tray,
closing the last window quits as before. `-demo` (or
`-demo-chats`) runs on demo data with no account; its settings are kept in
memory only, and it never reads or changes the saved settings, accounts or
local-data protection.

`internal/messenger/account` keeps the registry, does the sign-in on top of
gotd (`Add`, asking through the `Prompter` interface), imports tdata
(`ReadTData`, `ImportTData`), logs out (`LogOut`, `Remove`) and uses `Run` for
a connection. `-check` cannot ask for the master password, so it refuses
protected data. `internal/messenger/login` holds sign-in state for the UI, and
`ui/login.go` draws it.

Two things make the server end a session, and both are easy to do by mistake:

- **Connecting under another application.** An authorization belongs to the
  api_id it was created with. A session from Telegram Desktop has to connect
  as Telegram Desktop — api_id 2040 with its api_hash, the current version
  (`7.2.9 x64` on 64-bit Windows, `7.2.9` on x86-64 Linux), language pack
  `tdesktop`, a device name and a system version. `account.TDesktop()` builds
  all of it; keep the version current when Telegram Desktop updates. The
  device and the system are named as Telegram Desktop names them
  (`pkg/deviceinfo`, a port of lib_base's `DeviceModelPretty` and
  `SystemVersionPretty`): the model from the firmware, or `Desktop` when it
  gives only placeholders, and `Windows 11 x64` or
  `Linux XFCE X11 glibc 2.39`.
- **Two connections on one auth key.** A duplicated key looks like a stolen
  one. The account manager refuses a second connection to the same key, in
  the same process and — through a lock file in the user cache directory — in
  another one. It cannot see other machines: a session copied from a
  computer must not be used there and here at the same time. Tests that talk
  to the server must not run in parallel for the same reason; none of the
  tests in this repository connect anywhere.

The store reads profile, folders, dialogs and paginated message history. It marks
nothing as read and sets no online status. A gotd updates manager follows new,
edited and deleted messages, with synchronization state persisted per account.
See [PLAN.md](PLAN.md) for remaining pagination, cache and update-handling gaps.

## Messenger: devices

Settings → Devices lists the account's sessions (`account.getAuthorizations`),
as Telegram Desktop's Active Sessions do: this device, the logins that stopped
at the password, and the other devices, the last active first, each with its
device, app, country or IP and when it was last active. The app's name and
version, and the icon of the kind of device, follow Telegram Desktop's rules
(`TypeFromEntry`, `ParseEntry`). A click shows the session's details; the list
is asked for again every minute while the section is open. Sessions are not
terminated from here yet: that call logs devices out.

## Messenger: profile and visual privacy

The profile shows the account's phone number, username, bio, user ID and home
data center (from the connection's configuration). The pencil at its top
makes the name, username and bio editable in place — the page keeps its look,
the values become fields — and saves them with `account.updateProfile` and
`account.updateUsername`. Telegram's rules (a first name of at most 64
characters, a username of 5–32 Latin letters, digits and `_`, a bio of at most
70 characters without Premium) are checked before anything is sent; a taken
username is reported from the server's answer. Enter moves from field to field
and saves from the last one. The phone number cannot be changed here: that
needs a confirmation code. The demo profile can be edited too; `durov` plays a
username that is taken.

**Visual privacy mode** (“Privacy and security”) is for showing the screen to
others. It hides phone numbers everywhere they appear — the drawer, the list of
accounts, the sign-in code step — and covers the ID, number and username of the
profile with spoilers that open with a click, as message spoilers do; leaving
the profile covers them again. What is being typed in a field is not hidden.

## Messenger: Telegram Premium

The server tells what Premium changes, and the client reads it rather than
hard-coding it:

- `user.premium` and `user.emoji_status` mark a user with Premium. Beside
  such a name — in the chat list, the chat header, the profile, the drawer
  and the list of accounts — the client draws the user's emoji status (a
  custom emoji, drawn like those in messages; an expired one is ignored) or,
  without one or until it loads, the Premium star.
- `help.getAppConfig` holds the limits as `<name>_default` and
  `<name>_premium` pairs: folders, chats per folder, pinned chats, channels,
  saved GIFs, favorite stickers, bio, caption and message length, upload size
  and more. Keys the server does not send keep Telegram Desktop's values
  (`model.PremiumLimits`). The profile editor allows the account's own bio
  limit — 70 characters, 140 with Premium.
- Settings → **Telegram Premium** shows whether the account has it and every
  limit with and without it. Subscribing happens with the bot the
  configuration names (`premium_bot_username`), opened in the browser, and
  only when `premium_purchase_blocked` is false — Telegram Desktop's rule.

Around a name the client draws Telegram's other marks too, in Telegram
Desktop's order (`ui/badges.go`): a third party's verification — the custom
emoji a verifying bot (`bot_verification_icon`) put, as Mini Apps such as
Major do — before the name; after it, SCAM or FAKE in a red frame, which
replaces every other mark, or else Telegram's check mark, which hides the
Premium star and, except in the profile, the emoji status. Users and channels
carry these marks. The star and the check mark are vector shapes drawn after
Telegram Desktop's icons — a round-pointed star with the slit of the Premium
mark, a rosette of eight lobes — in Telegram's blue; the icons themselves are
not copied, as Telegram Desktop is GPL-licensed. The demo shows each mark.

The account list keeps whether each account has Premium in its registry row
(schema 2; a schema-1 registry is upgraded in place). The demo account has
Premium, as do two demo contacts.

## Messenger: reading chats

History opens around the saved message anchor, loads older/newer pages as needed,
and caches messages, viewport and layout measurements in SQLite. Visible photos,
GIFs, stickers, custom emoji and avatars load automatically; video opens on
click in an external player, mpv or VLC. Voice messages and music play in the
client, with their waveform: see "Voice messages". With both installed, the first
video asks which one to use; the choice is kept and can be changed in Settings →
External integrations. Links ask for confirmation before opening in an external
browser.

- Drag inside message text to select it. **Ctrl/Cmd+C** copies the selection;
  **Ctrl/Cmd+A** selects the current text block. These shortcuts also accept
  **С/Ф** in Russian and Ukrainian layouts. On Linux, the local Gio XKB fix
  resolves Ctrl/Cmd alphabet shortcuts through a Latin layout of the same key,
  with a physical-key fallback when only a non-Latin layout is configured;
  ordinary text input keeps its active layout. Double-click selects a word; triple-click selects the whole
  source-text line between explicit line breaks, including all its visual wraps.
  Dragging after a double/triple click extends by words/source lines. Shift-click
  and Shift+arrow keys extend selection. Hidden spoilers copy as `[•••]` until revealed.
- Click a spoiler to reveal it with a circular wave starting at the click. The
  wave moves at one speed (300 dp/s), so a short spoiler opens as briskly as a
  long one; it takes at least 0.12 and at most 1.5 seconds.
  With animations disabled it opens immediately.
- Click or drag through the empty space beside message bubbles to select a range
  of messages. Reversing the drag adjusts the range; dragging from a selected
  message removes that range. Dragging near the viewport edge scrolls the list.
- The selection header shows the message count and **Forward**, **Delete**,
  **Snapshot**, **Cancel**. Delete opens a dialog for deletion for yourself or
  everyone (channels/supergroups only support everyone), including every member
  of selected albums. Forward and Snapshot remain placeholders. Albums are selected as a unit. **Escape** or
  **Cancel** clears message selection; switching chats clears both selections.

- Stickers and a message of a single emoji have no bubble: they lie on the
  chat's background, as in the official clients, a lone emoji at half a
  sticker's size (Telegram Desktop's `UnwrappedMedia` and `LargeEmoji`). The
  time sits on a plate beside them, or over the corner of the account's own;
  a reply or forward goes in a small card at the side. Two emoji or more, or
  an emoji with formatting other than being a custom one, stay in a bubble.
- Click a reaction under a message to choose it or take it back; the menu of a
  message (right click) starts with the reactions its chat allows, and a
  button opens all of them. As in Telegram Desktop, the account keeps one
  reaction without Premium and three with it: a new one replaces the last
  over the limit. The change shows at once and is undone if Telegram refuses
  it (`messages.sendReaction`); reactions and views of others arrive as
  updates (`UpdateMessageReactions`, `UpdateChannelMessageViews`).
- A channel post with a discussion ends with a comments bar, as in
  materialgram: the userpics of the last commenters, the count, or "Leave a
  comment". It opens the comments over the channel, with a way back: the
  post's copy in the discussion group at the top, then the comments
  (`messages.getDiscussionMessage`, `messages.getReplies`). They are a thread
  of the group, shown as a chat of its own under an id made up for the
  session; the cache does not keep them, since they are not the group's
  history without gaps. What is written there goes to the group as a reply
  in the thread.
- The search remembers the chats picked from its results, as Telegram
  Desktop's does (`Data::RecentPeers`): while nothing is typed it shows them
  under "Recent", the last one first, 48 at most. "Clear" empties the list
  after asking; a right click on a chat removes it, or all of them. The list
  stays on this computer, in the account's encrypted cache.
- Telegram pushes a channel's updates only to its members. While a channel
  the account has not joined is open — found by search, say, or the
  discussion group of comments — its difference is polled as Telegram
  Desktop polls an open channel (`updates.getChannelDifference`, a second
  after it opens and then at the server's timeout), so new posts, edits and
  deletions show without reopening it. Channels the account is in are left to
  the updates manager.

Forwarding, bot callbacks and automatic read acknowledgements remain
unavailable. The demo includes rich text, a multiline spoiler, media,
reactions and a channel with comments for trying these interactions without
an account.

## Messenger: message composer

A floating rounded capsule overlays the bottom of chat history. Enter sends;
Shift+Enter adds a line. Draft text and custom-emoji entities stay with each chat
while switching chats. Failed sends preserve the draft and retry with the same
Telegram random ID, including attachment/checklist retries.

The paperclip opens photo/video, file and checklist actions. Photos, videos and
files are chosen, several at once, in the system's file chooser (`kdialog` or
`zenity` on Linux), with a path field as a fallback, and appear in a box for
sending files, as in Telegram Desktop: pictures in a grid, other files as rows, a
caption (the composer's text to start with) and the choices "Group items", "Send
as documents" and, for large photos, "High Quality". Photos are made ready as
Telegram Desktop makes them (`internal/messenger/sendfiles`): turned as their
Exif asks, scaled to fit 1280 pixels (2560 in high quality), alpha laid on white,
saved as JPEG; what can go in an album goes in one (`messages.sendMultiMedia`,
ten at most), the caption on the last message. Audio files (MP3, M4A, AAC, Ogg,
Opus, FLAC) that say how long they play go as tracks, as in Telegram Desktop,
whatever the choices: title, performer and length from their tags
(`pkg/audiotag`: ID3, Vorbis comments, iTunes metadata, read in Go), the cover
as the thumbnail, and grouped tracks in an album of music alone; other audio
goes as a file. Video upload uses `ffprobe` for
dimensions and duration. Files dragged from another program over an open
chat show Telegram Desktop's areas: one to send them as documents, and for
pictures, photos and videos another to send them compressed, as media; dropped,
they open the same box, or join it when it is open. The windows take drags
through `app.DropEvent` of the Gio fork: OLE's `IDropTarget` on Windows, XDND on
X11 and `wl_data_device` on Wayland. Creating native Telegram checklists requires Premium;
received checklists display their items and completion state. The demo simulates
sending locally without network traffic.

Bots work as in Telegram Desktop: the buttons under their messages ask the bot,
open a link or copy a text; a keyboard a bot sets shows under the composer; an
empty chat with a bot has a Start button; typing "/" lists the bot's commands.
Buttons that open Mini Apps (under a message, in a keyboard, or the bot's menu
button beside the field) open them in the browser of the Kitchen section below,
with the storage the settings choose; the app's main and back buttons are drawn
inside its page. Inline mode is not done.

A right click on a chat of the list opens its menu: pin it to the top or unpin it
(the pinned chats keep the order they were pinned in, and their number is
limited by the server, more with Premium) and mark it read. Chats and the
messages shown are marked read, and the account shows online and typing, as
Telegram does, unless Ghost Mode in "Privacy and security" says otherwise.

A forum (a group divided in topics) opens as the list of its topics instead of a
history, as in Telegram Desktop: the icon of the topic's colour, its title, the last
message, the unread and mention counters, a pin for a pinned topic and a lock for
a closed one. A topic opens as a page of its own, like the comments to a post, with
a way back and how many messages it has; messages sent there reply to the topic's
first message. The forum's header searches all its topics, and a message found
opens its topic there; a topic's header, and the comments', search the topic.
Creating and editing topics is not done.

With nothing written, a microphone takes Send's place: it records a voice
message (`pkg/voice`) through `ffmpeg` — PulseAudio/PipeWire or ALSA on Linux,
DirectShow on Windows, AVFoundation on macOS — into PCM for its time, loudness
and waveform, and encodes it to Opus in OGG when it is sent. The cross or Escape
drops it; recordings under 0.2 s are dropped, as in Telegram Desktop.

### Voice messages and music

A voice message is drawn as in Telegram Desktop: a round play button, the
waveform Telegram sends with it (`MessageMedia.Waveform`, 5-bit bars unpacked
by `voice.Bars`), the heard part in the primary color, and the time. It plays
in the client, without FFmpeg or an external player: `pkg/opus` parses the
OGG file in Go and decodes its packets with libopus compiled to WebAssembly
(`opusdec.wasm.gz`, BSD-licensed and embedded; `pkg/opus/build`), as it plays,
and `pkg/audio` puts the sound out through oto — PulseAudio or PipeWire,
else ALSA through dlopen, on Linux, WASAPI on Windows, all without cgo. A
click on the waveform, or a drag across it, moves the message there: the
decoder starts 80 ms before the point and decodes a few packets, so a voice
message of any length costs its compressed packets and one decoder. One
message plays at a time; it stops when the chat changes. A voice message
sent without a waveform gets one worked out while it plays, with a decoder
of its own (`voice.Loudness`, the bars Telegram Desktop would send), kept
for the session. One shown without a waveform gets it as soon as it is on
screen: its file, 3 MiB at most, is downloaded and decoded in the
background, one at a time, as Telegram Desktop does; Telegram sends none for
the voice messages of bots and some services. MP3 voice messages, which
some bots and clients send, play the same way through dr_mp3
(`pkg/drdec`: dr_libs's MP3, FLAC and WAV decoders compiled to WebAssembly,
public domain and embedded), taken to 48 kHz by `audio.Resample`. M4A voice
messages, as those sent from a file, play through fdk-aac (`pkg/aac`),
which `pkg/mp4` hands the access units of the sound track: the module is
not in the binary, as its license grants no patent rights on AAC, and
`wasmmodule.AACDec` fetches it from
[fdk-aac-wasm](https://github.com/komarugif/fdk-aac-wasm), at a pinned
commit, checked against its SHA-256; `KOMARUGRAM_AACDEC` names another
build. Any other format opens in the external player.

Music plays the same way, drawn as in Telegram Desktop: the button, the
title and the performer, a bar of how much was heard, which a click or a
drag moves, and the time. A file plays as it downloads: the decoders read
it through `MediaStream`, a 128 KiB range at a time, any range — dr_libs
through a `host.read` import, fdk-aac an access unit at a time, `pkg/mp4`
only the box headers and the movie box, wherever it is — while the rest
downloads ahead in the background; the time spent waiting for the network
is not counted against a sandbox's time limit. An MP3 still downloading
takes its length from Telegram, not from reading it through. OGG files are
read whole, their length being on their last page. Settings → External
integrations can send voice messages and music to the external player
instead (`AudioPlayer`).

The smile opens Emoji / Stickers / GIF with a debounced search and recent items.
Installed sticker/custom-emoji packs are fetched through Telegram, standard emoji
keywords through its language API. Global search puts matching saved packs first.
The pack strip supports touch swipes, mouse drags and the wheel, sharing the photo
viewer's gesture handling. GIF rows adapt to their aspect ratios; saved GIFs keep
Telegram's newest-first order. Global GIF search uses the server-configured inline
provider and supports both Telegram documents and web results, with pagination.
Recent selections are saved in the account cache. Picker errors offer retry.

## Messenger: photos

Telegram keeps each photo at several sizes (`m` 320, `x` 800, `y` 1280, `w`
2560 px). The store records all of them as variants of one `MessageMedia`,
each with its own file location and cache entry, so nothing downloads the
largest size by default:

- a chat tile downloads the smallest variant that covers its pixels and
  decodes it for the tile's size (rounded up to 128 px, so resizing the
  window does not decode it again);
- clicking a loaded photo opens the viewer over the whole window: the photo at
  its own size or smaller, with the neighbouring photos of the chat on either
  side — a click anywhere in the zone beside the photo, ←/→, Home/End or the
  wheel switch to them — and a strip of thumbnails below, which scrolls with
  a swipe, the wheel or a mouse drag. Escape, ✕ or a click on the background
  close it; the button beside ✕ moves it into a window of its own, which
  closes with its account window.
  On Wayland that window is translucent, and where the compositor supports
  `ext-background-effect-v1` (KWin 6.7 and later) what shows through is blurred.
  Elsewhere it is opaque; see `third_party/gio/LOCAL_CHANGES.md`.
- Ctrl with the wheel (or a touchpad pinch), Ctrl+= and Ctrl+− zoom smoothly
  around the pointer, up to 8 screen pixels per photo pixel; Ctrl+0 returns
  to the fitted photo. Enlarged, the photo is decoded at its own size and
  moves with a drag or the wheel. With animations off, zoom steps at once. Thumbnails use the smallest variant; the
  original is read only for the photo on screen and its two neighbours, and
  closing the viewer drops every decoded pixel.

The viewer pages Telegram's photo search independently of loaded chat history,
with the local photo cache available offline. Clicking the chat header opens
shared-media sections and chat appearance settings. See
[chat appearance and shared media](docs/CHAT_APPEARANCE_AND_MEDIA.md) for grids,
link previews, gifts, themes, server translations and cache behavior.
Messages cached before variants were recorded have only their largest size
until they are loaded again.

Scaling is done by `pkg/resample`, a Catmull-Rom filter that streams through
the source a row at a time. It paints what `x/image/draw.CatmullRom` paints
without its dstW×srcH×32-byte buffer: 2.5 MB instead of 54 MB, and 24 ms
instead of 140 ms, for a 2560×1920 photo shown at 840 px. A decoded JPEG is
scaled plane by plane — luma, then chroma at its own quarter resolution — and
converted to RGB only at the destination's pixels; other images match
`x/image/draw.CatmullRom` to ±1 per channel.
`VIEWER_PNG=/tmp/v.png go test ./internal/messenger/ui -run RenderPhotoViewer`
saves a GPU screenshot of the viewer over the demo photos.

## Kitchen: video

There are two approaches to video on display, and they suit different content.

**Video** streams one file: `assets/video.mp4` from the working directory is decoded by
an ffmpeg subprocess that writes raw RGBA frames into a pipe, paced by ffmpeg's
`-re` flag and looped by its `-stream_loop -1` in the same process; a process
per pass started several a second for a short GIF. Playback suspends itself about a second after the page stops being
shown and resumes when it is shown again. This is the approach for long or large
video, where keeping the frames around is out of the question. Audio is not
played.

**Video grid** takes the opposite trade: every `videos/*.mp4` is decoded once,
downscaled to tile size and resampled to 15 fps, and the frames are kept in
memory (`pkg/video/cache.go`). Looping afterwards is a lookup by wall clock, with no
decoder running at all. Memory is the price and it grows with clip length, so
`maxFrames` caps each clip at six seconds the way a chat preview does. The page
redraws only when a tile is due for a new frame rather than at display rate,
because the redraw rate — not the tile size — is what costs CPU.

Frames are stored either as RGBA (4 bytes per pixel, ready to paint) or as
YCbCr 4:2:0 (1.5 bytes per pixel, converted when painted). The button on the
page reloads the cache in the other format. Gio has no hook for a custom
shader — `paint.ImageOp` carries an `*image.RGBA` and nothing else, and its
shaders are compiled ahead of time — so the expansion happens on the CPU, at
about 77 µs per 134x240 frame.

`go test ./pkg/video -v` prints what the cache costs for the clips in `videos/` at
several tile sizes, frame rates and formats, and checks that the compact format
paints the same picture as RGBA.

## Kitchen: Mini Apps

**Mini Apps** opens a bundled Mini App in whatever Chromium-based browser or
Firefox the user already has, and talks to it over the browser's automation
protocol: the Chrome DevTools Protocol (`pkg/miniapp/cdp.go`) or WebDriver
BiDi, which Firefox serves itself (`bidi.go`). There is no embedded webview here
and no plan for one.

The transport Telegram's SDK expects turns out to be small enough to provide
from outside the browser:

- the page reaches the client through `TelegramWebviewProxy.postEvent`, which
  is installed by a shim that runs before the page's own scripts: in Chromium
  injected with `Page.addScriptToEvaluateOnNewDocument` and backed by a CDP
  binding, so every call arrives as `Runtime.bindingCalled`; in Firefox a
  preload script (`script.addPreloadScript`) handed a channel, so every call
  arrives as `script.message`;
- the client reaches the page through `Telegram.WebView.receiveEvent`, called
  with `Runtime.evaluate` or `script.evaluate`;
- the launch parameters — init data, version, platform, theme — travel in the
  URL fragment, exactly as they would into a webview.

`pkg/miniapp/assets/telegram-web-app.js` is Telegram's own SDK, so the demo
exercises the real thing. The page logs what the app asks for, and its buttons
push events the other way: theme changes, viewport changes, main and back
button presses. `go test ./pkg/miniapp -v` checks both directions.

The browser runs as a separate process with a profile of its own, so a Mini App
never sees the user's cookies and cannot reach into this process. Which profile
is the client's choice, and `miniapp.Profile` offers three: a throwaway one,
one per Mini App, or one shared by every app of an account. The last is what
official clients do — Telegram Desktop hands every bot webview the same storage
id, keeps it as `wvbots` in the account directory and clears it only on logout,
and Telegram for Android runs its webview with DOM storage and cookies enabled
and flushes them to disk. A Mini App is an ordinary web application, so on a
kept profile it finds its sessions and caches where it left them; on a throwaway
one it starts from nothing every time.

Any Chromium-based browser will do, and Firefox 140 or later, or a browser
built from it: LibreWolf and Waterfox. Chromium, Chrome, Brave, Firefox,
LibreWolf and Waterfox are looked for on `PATH` and among installed flatpaks,
each one is asked for `--version`, and the one built on the newest Chromium is
driven; Firefox only where there is no Chromium-based browser, as on Haiku,
since its window cannot be told to drop the browser's controls as `--app` does
(below). The browser engine is what a
Mini App — a page from a stranger — runs inside, and whether the flatpak or the
distribution's package is ahead cannot be told from how it was installed, so
the version decides; Brave reveals only its Chromium major, so it ties with
every Chromium of that major, and ties go to native programs first.
`KITCHEN_MINIAPP_BROWSER` names one directly, by program or by application id,
which is how the same suite is run against each of them; in the messenger, the
user can pick one in the settings, which is kept only when it answers
`--version` as a Chromium-based browser. Two of them need
something of their own: Brave shows a notice about its analytics, which is a
browser-level setting rather than a profile one, and a flatpak sees nothing
outside `/tmp` unless the profile directory is granted to it by name — without
that it keeps its own copy inside the sandbox, where the client cannot find it.
Both are handled where the profile is prepared.

Firefox differs in a few places. It has no `--app` window: the client writes
a `userChrome.css` into the profile that hides the tab bar and the toolbars and
lets the window be narrower than Firefox's own minimum of about 500 pixels, and
the window's first size into `xulstore.json`, where Firefox keeps it. Its
preferences go into `user.js`, written at every start: no first-run pages or
notices, no offer to translate, links opened by the app in a window of their
own (with the tab bar hidden, a tab would cover the app), and
`remote.prefs.recommended` off — under remote control Firefox otherwise sets
preferences meant for tests, among them Safe Browsing, the popup blocker and
tracking protection off. The forks need a few more: the window manager's title
bar (`browser.tabs.inTitlebar`), since Waterfox draws its own, which would go
with the toolbars and leave a window that cannot be moved or closed; and
LibreWolf's resistance to fingerprinting off, along with its clearing of what
pages stored at every quit — it asks in a dialog that holds up the page whether
to request pages in English and sizes the window itself, and a Mini App gets the
user's Telegram id and name in its launch parameters anyway, while what an app
keeps is the client's setting. The style sheet also hides the robot Firefox
shows while remote-controlled, which sits in the URL bar and is drawn over the
page even with its toolbar hidden, the browser's own notices over the page
(LibreWolf's that the default search engine changed; a page's own notices
stay), and the lines and margins the forks put around the page. Waterfox numbers its versions on its own (6.7.5 runs on
Firefox 153), so the version of the engine of a browser built from Firefox is
read from `platform.ini` beside the program or in the flatpak's files
(`Milestone=153.4.0`). Beside a launcher script, as distributions install
browsers, it cannot be found; such a browser is started all the same, and turned
down if the version it gives for its BiDi session (`browserVersion`, the
engine's: 153.4.0 for Waterfox) is older than 140. The page is opened only once the preload script is in
place, so nothing is reloaded. The BiDi session leaves the app's own dialogs
(`alert`, `confirm`) for the user rather than dismissing them. Firefox writes the
endpoint's port into `WebDriverBiDiServer.json` in the profile, as Chromium
writes `DevToolsActivePort`. Moving a window that is open
(`Bridge.SetWindowBounds`, which the external player uses) needs Firefox 151,
which added `browser.setClientWindowState`; Firefox 153 fixed preload scripts
that stopped working after several navigations. A BiDi session also outlives
its connection, and a browser takes one session only, so the bridge keeps its
one connection for as long as the browser runs.

Two things make a kept profile work. The browser is asked to shut down over its
protocol (`Browser.close`, `browser.close`) rather than killed, or it never
writes out what the app stored; and a profile already in use is refused,
because a second Chromium on one profile hands its window to the first and both
pages then answer on a single debugging endpoint; Firefox, which locks its
profile, allows one process on it too.

What this approach does not solve is the native chrome around the app — the
main button, the header, popups. Those belong to the client in official
clients, and here the app lives in a window of its own; drawing them means
injecting them into the page, which is what `Bridge.Eval` is for.

## Kitchen: external playback

**External player** hands a file to mpv or, without it, to VLC. The player
opens its own window with its own seek bar and is driven over its IPC socket:
mpv's JSON IPC pushes the position, duration and paused state back, while VLC's
remote control interface (`oldrc`) answers one text line per command, so its
position is polled four times a second and is whole seconds. The page's buttons
seek and pause without restarting anything. Decoding happens in the player's
process, so its codecs stay outside this one.

VLC runs with `--no-one-instance`, or a running VLC takes the file and the new
process exits at once, and with `--ignore-config --no-qt-recentplay
--qt-continue=0`, so that the loopback URL and its token are not written to its
recent media (`player.Kind.PrivateArgs`). On Windows the interface listens on
loopback TCP instead of a Unix socket. mpv is not offered on Windows yet: its
IPC there is a named pipe. Players installed as snaps are neither looked for nor
accepted: a snap has a `/tmp` of its own and no directory shared with this
process for a socket.

Players are looked for on `PATH`, then as flatpaks (`org.videolan.VLC`,
`io.mpv.Mpv`, through the launchers in `exports/bin`). A flatpak has a `/tmp` of
its own, so its socket goes to `$XDG_RUNTIME_DIR/app/<id>`, which flatpak
shares with the sandbox under the same path. A flatpak player is bwrap running
it, and killing bwrap alone leaves the player playing with no parent: players
run in a process group of their own, and closing one kills the group
(`pkg/program`).

The user can point the messenger at a player, or at the browser for Mini Apps,
in Settings → External integrations. Any file can be picked there, so a program
is kept only after it answered `--version` as what it was picked as
(`player.Check`, `miniapp.CheckBrowser`): mpv 0.17 or later, VLC 3, a browser
printing a four-part Chromium version, or Firefox 140 or later. The question runs in the C locale, with
nothing on its input, a 10-second limit and the first 64 KiB of its answer
kept (`program.Banner`). `player.Open` asks again, once for each size and
modification time of the file, so a path written into `settings.json` by hand
is not run either. On Windows, where a player prints nothing for `--version`
and a browser opens a window, the version resource of the file is read instead,
without running it.

`go test ./pkg/player -v` runs every test against each installed player, native
and flatpak; `PLAYER_VIDEO` points it at a video longer than 20 seconds.

The second button on each row plays the same file through `player.Serve`, a
loopback endpoint that answers byte ranges. That is the shape a download in
progress has: hand it an `io.ReaderAt` that blocks until the range has arrived
— a cache of chunks fetched over MTProto, say — and an external player can
start before the file is complete and still seek in it. Seeking forward is
served by reading on; seeking backwards makes the player ask for an earlier
range, which `go test ./pkg/player -v` checks.

The endpoint listens on 127.0.0.1 under a random token, so other local
processes cannot read the file just because they can reach the port.

## Kitchen: animated stickers

**Lottie** plays every `assets/stickers/*.json` and `assets/stickers/*.tgs`. Drop Telegram
stickers into that folder; `.tgs` is gzipped Lottie JSON and is unpacked on the
way in.

The renderer is [tlottie](https://github.com/dkaraush/tlottie) (MIT), written in
Rust and compiled to WebAssembly. It is embedded in the binary as
`pkg/lottie/tlottie.wasm.gz` and executed by [wazero](https://github.com/tetratelabs/wazero),
a WebAssembly runtime written in Go — so animations are drawn without cgo and
without a C or C++ dependency, and the build stays a single static binary.

Sticker JSON arrives from strangers, so it is parsed inside the sandbox: the
module declares no imports at all and therefore has no way to reach the
filesystem, the network or the host's memory. `go test ./pkg/lottie -v` feeds the
renderer malformed, truncated and hostile input and measures what a grid of
animations costs.

Frames are rendered on demand and not cached: at roughly 0.2 ms per 160px
frame, a grid of 50 stickers at 30 fps costs less than a quarter of one core,
while caching their frames would cost hundreds of megabytes.

## Kitchen: video stickers

**Video stickers** plays every `assets/stickers/*.webm`, the other format Telegram
uses for stickers and custom emoji. The work is split along the line where
trust changes:

- `pkg/webm/` parses the container in Go. The file layout is attacker controlled,
  and a demuxer in a memory-safe language cannot be talked into reading outside
  its own slices.
- `pkg/vp9/` decodes the frames with libvpx compiled to WebAssembly and run by
  wazero, so a malicious frame is confined to its sandbox. There is no cgo.
  libvpx has no WebAssembly SIMD of its own: `pkg/vp9/build/build.sh`
  configures it for arm64 and turns its NEON intrinsics into SIMD128 through
  SIMDe. On the 101 stickers of `assets/rigby` a 512 px frame with its alpha
  takes 2.2 ms instead of 7.1 ms of plain C (6.7% of a core per playing
  sticker instead of 21%), with the same frames bit for bit.
  `TestModuleUsesSIMD` keeps a rebuild from losing it.
- A frame is never composed at the sticker's 512 px for a grid cell:
  `Sticker.FrameSized` reads the planes in place in sandbox memory, averages
  blocks of alpha, alpha×luma and alpha×chroma down to twice the cell
  (`resample.VideoScaler`), filters that with Catmull-Rom and converts only
  the cell's pixels. For a 64 px cell a frame costs 1.9 ms instead of 4.2 ms,
  within 52 dB of composing first; `FrameAt` still composes the full frame.

Sticker transparency does not live in the VP9 bitstream: the alpha channel is a
second VP9 stream carried in each block's `BlockAdditional` element, so a
sticker runs two decoder instances whose output is composed into one
premultiplied image for Gio. Forgetting this is easy to miss — ffmpeg's own
native `vp9` decoder ignores the alpha channel silently, and only `-c:v
libvpx-vp9` honours it.

`go test ./pkg/vp9 -v` checks the decoded frames against ffmpeg's `libvpx-vp9`
output (the alpha channel matches exactly), feeds the demuxer and decoder
hostile input, and measures decoding cost.

`pkg/vp9/vpxdec.wasm.gz` is built from an unmodified libvpx checkout by
`pkg/vp9/build/build.sh`; that directory also holds the small C shim that is the
module's entire export surface.

## Pitfalls met while working on this code

Notes for whoever changes this code next, person or agent. Each one cost a
bug, a failed test or a wrong first guess at least once.

**Gio**

- *Handle input before drawing what it changes.* A widget whose events are
  read while laying out a later part of the frame (a thumbnail strip below a
  photo) changes state after the photo was drawn, and nothing draws again
  until the next input — it looks like a freeze. Read clicks, wheel and keys
  at the top of `Layout`, or `gtx.Execute(op.InvalidateCmd{})`. Tests can
  catch it: compare what the frame drew with the new state.
- *Minimum constraints travel down.* `layout.W`/`E` keep `Min.Y`; a vertical
  `Flex` inside then stretches to it and puts its children at the top. Zero
  `gtx.Constraints.Min` before content that should take its own size.
- *Hit testing stops at the first area without `pointer.PassOp`.* A
  `Clickable` over a region hides it from handlers below, scroll included. An
  overlay that must see the wheel or drags over buttons goes on top with
  `PassOp`, and takes the pointer with `pointer.GrabCmd` once a press becomes
  a drag, so the button underneath gets `Cancel` instead of a click.
- *`layout.List` scrolls by dragging only for touch.* Mouse drags and a
  vertical wheel over a horizontal list need a handler of their own
  (`photoViewer.dragStrip`).
- *Wheel units:* one notch is 10 on X11 and Wayland; touchpads and Wayland's
  kinetic scrolling send streams of small deltas. Accumulate to a threshold
  and keep a quiet period after acting on it.
- *Each window has its own goroutine and GPU context.* Textures (`imageOps`)
  and decoders belong to one window; the store is shared and must be safe for
  concurrent use (`mockstore` has a mutex for this reason).
- *Typed nil in an interface.* A `*image.RGBA` that is nil stored in an
  `image.Image` is not nil; `im != nil` passes and a later field access
  panics. `chatmedia.nilImage` normalizes decoder results.
- *The local fork is imported as `gioui.org/...`.* `go vet ./third_party/gio/app`
  fails with "main module does not contain package"; use
  `go vet gioui.org/app`. gofmt realigns a whole struct when a comment splits
  its field block, so keep new fields in their own block or with trailing
  comments to keep the fork's diff small.

**Native memory**

- *A leak in Go code can hold memory only C sees.* Closing a window leaked
  about 35 MB on X11, yet Go heap profiles stayed flat: the bug was in Gio's
  Go code, the memory in X11, XKB and the GPU driver. A closing driver queues
  an invalid `ViewEvent`, then the `DestroyEvent`, and the `DestroyEvent`
  has already cleared the driver. `Window.Event` delivered the `ViewEvent`,
  and on the next call it saw no driver and created a new window. That ghost
  was never destroyed, and it kept its `Display`, xcb connection, keymap and
  GPU context. It is fixed in the fork (`third_party/gio/LOCAL_CHANGES.md`).
  When RSS grows while the Go heap does not, look at the native side.
- *Freed is not returned.* glibc keeps what C code frees in per-thread arenas,
  and a GPU driver frees tens of MB when a window closes. `appwindow` calls
  `malloc_trim` after `debug.FreeOSMemory`, 2 s and 10 s after a window
  closes, since the driver frees about 1-1.5 s late. Without it the same
  cycles grew RSS to about 140 MB with nothing leaking.
- *Tell the two apart before fixing anything.* In `mallinfo2`, "used" C memory
  that grows is a real leak; "free" that grows is the allocator holding on.
  For a real leak, glibc's own tracer names the library that allocated each
  unfreed block, with no extra tools:
  `LD_PRELOAD=libc_malloc_debug.so.0 MALLOC_TRACE=trace.log`, with `mtrace()`
  and `muntrace()` called through cgo around the cycles you measure. Measure
  RssAnon over several open/close cycles, not one: a single cycle can't show
  a trend. Name the `smaps` mappings (Go's are `[anon: Go: …]`) to see which
  side grows.
- *`#ifdef __GLIBC__` in a cgo preamble needs a libc header included first.*
  Without one the macro is undefined, and the fallback stub compiles without a
  word: `malloc_trim` was never called, and an experiment measured on the stub
  came out wrong. Check with `go tool nm <binary> | grep malloc_trim`.
- *Xlib requests made off the event loop wait in its buffer.* On X11, Gio's
  `Run` executes on the caller's goroutine, so a close or raise from a tray
  icon waited until some other event arrived. The fork flushes in `Run`.
- *Reflection over methods doubles the binary.* One reachable
  `reflect.Type.Method` call, such as godbus `conn.Export`, `prop` or
  `introspect`, keeps every exported method in the program, all of gotd's
  `tg` among them: 38 MB became 67 MB. Use `conn.ExportMethodTable` with
  method values. Compare stripped builds (`-ldflags='-s -w'`); binaries left
  by `go run` are stripped already.

**Images**

- `golang.org/x/image/draw` kernel scalers allocate dstW×srcH×32 bytes per
  call (60 MB for a phone photo). Use `pkg/resample`.
- Photos come in several sizes (`MessageMedia.Variants`); ask
  `Variant(w, h)` for the one that covers the pixels shown. Never download the
  largest by default.
- Decode for the box shown (`chatmedia.StatusFit`); sizes are rounded up to
  128 px so resizing does not decode again.
- A `chatmedia.Manager`'s limit bounds what stays cached off screen, never
  what is on screen. Refusing media drawn this frame left stickers past the
  24th of a panel or set as emoji placeholders, redrawing forever. Sticker
  stills of up to 128 KB are kept outside that limit (up to 256 of them,
  within the byte limit): counted in it, a set scrolled back loaded every
  sticker again. A sticker that played is evicted down to its still, dropping
  its decoded loop, so that with animations on a sticker scrolled back to
  shows its first frame rather than its emoji placeholder. A sticker view
  closed (the set dialog, the composer's picker) drops the loops of its
  stickers at once (`Manager.DropLoops`), keeping their first frames, and the
  memory goes back to the system five seconds later
  (`appwindow.Window.ReleaseMemoryLater`), unless a sticker view is shown
  again meanwhile (`KeepMemory`): it decodes its loops again. Leaving a chat forgets the first
  frames of the sets shown in its dialog, not those of the picker, which is
  the same in every chat. Decoders left off screen ask for the frames that
  stop them: once a view closed, nothing else may draw for a while.

**SQLite**

- Message payloads are JSON stored as BLOBs. Recent SQLite reads a BLOB given
  to `json_extract` as JSONB, so cast: `json_extract(CAST(payload AS TEXT), ...)`.
  A partial index is used only when the query repeats its expression exactly
  (`historycache.photoWhere`); `EXPLAIN QUERY PLAN` in a test keeps it honest.

**Tests**

- Fakes must be cheap. Under `-race`, compressing a 1600×1200 PNG per request
  took seconds and timed a test out; encode once, uncompressed.
- Check that a new test fails without the fix (break the code on purpose,
  run, restore). Two tests written here passed at first without testing
  anything.
- Pixels can be checked on the GPU with `gioui.org/gpu/headless` (see
  `TestTranslucentViewerPixels`); `VIEWER_PNG=… go test ./internal/messenger/ui
  -run RenderPhotoViewer` saves a screenshot to look at.
- Demo photos are generated on request (about 280 ms for 2560 px), so load
  timings in `-demo` are slower than a real cache.

**Building**

- Cross-builds that work without extra toolchains, and must keep working:
  ```sh
  go build ./cmd/messenger ./cmd/kitchen
  go build -tags nowayland ./cmd/messenger   # also nox11, novulkan
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/messenger ./cmd/kitchen
  GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/messenger
  GOOS=js GOARCH=wasm go build gioui.org/app gioui.org/gpu/...
  ```
  darwin, Android and FreeBSD need C cross toolchains. Changes to shared Gio
  code that only those platforms compile (`internal/egl` is used by Android)
  cannot be checked without them — leave such changes until they can.
- On X11 with Mesa, the first EGL configs with alpha use 24-bit visuals, so a
  transparent X11 window needs an EGL config chosen for a 32-bit visual.
- Crash reports are in `~/.cache/komarugram-go/crashes`
  (`%LocalAppData%\komarugram-go\crashes` on Windows); a recovered panic is
  also in the log with its stack.
