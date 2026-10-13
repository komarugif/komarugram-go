# Data and Storage: porting plan from Telegram Android

Status: proposed implementation plan, 2026-10-03. No application changes are
included. The primary reference is Telegram Android, including its storage
visualization and management flows. Use KomaruGram's Gio components and theme
to implement the behavior independently; do not translate Android code or copy
its assets. Follow the porting rules in [PLAN.md](PLAN.md) and the component
rules in [UI_COMPONENTS.md](UI_COMPONENTS.md).

## Outcome and scope

Add **Settings → Data and Storage → Storage Usage**. The first usable storage
release must answer all three questions:

1. How much space does KomaruGram's local data occupy on the volume where it
   is stored, compared with that volume's capacity and available space?
2. Which media categories account for the downloaded data?
3. Which chats account for it, and what can be cleared from a particular chat?

Continue with individual-file selection, retention rules and exceptions, then
network usage and automatic downloads. Storage statistics must work offline.
Opening the section must not fetch remote history, enumerate server media,
download thumbnails, or create another Telegram connection.

The parent Android section is broader than storage. Keep its scope visible in
the roadmap, but only expose controls backed by working KomaruGram features.
Do not ship dead rows for calls, roaming, proxy settings or gallery integration.

## Reference baseline

The local `telegram-android/` source was inspected. On 2026-10-03,
`CacheControlActivity`, `DataSettingsActivity`, `CacheByChatsController` and
`AutoDeleteMediaTask` were checked byte-for-byte against upstream revision
[`f2908b14133bbffbf7ab04f641ecb5bfaf533242`](https://github.com/DrKLO/Telegram/commit/f2908b14133bbffbf7ab04f641ecb5bfaf533242)
(2026-09-30). Paths below are relative to
`TMessagesProj/src/main/java/org/telegram/` in that checkout. Android was
reviewed from source, not run on a device; visual parity still needs an Android
screen recording or device check during implementation.

| Android source | Behavior to carry over | KomaruGram adaptation |
|---|---|---|
| [`ui/DataSettingsActivity.java`](https://github.com/DrKLO/Telegram/blob/f2908b14133bbffbf7ab04f641ecb5bfaf533242/TMessagesProj/src/main/java/org/telegram/ui/DataSettingsActivity.java) | Storage, network usage, automatic downloads; additional streaming, gallery, calls, proxy and cloud-draft settings | Storage first; network usage and downloads later; remaining flows have separate prerequisites |
| [`ui/CacheControlActivity.java`](https://github.com/DrKLO/Telegram/blob/f2908b14133bbffbf7ab04f641ecb5bfaf533242/TMessagesProj/src/main/java/org/telegram/ui/CacheControlActivity.java) | Category totals, selected-byte cleanup, chat sizes, retention settings, maximum cache size | Main storage page, with explicit account and volume scope |
| `ui/Components/CacheChart.java`, `ui/Components/StorageUsageView.java` | Segmented chart, category legend, application/device usage presentation | Gio drawing, semantic colors, textual values and keyboard-accessible legend; distinguish media payload from physical database size |
| `ui/CachedMediaLayout.java`, `ui/Storage/CacheModel.java` | Chats ordered by size; cached media, files and music tabs; item selection | Virtualized local lists; initially chats, then per-file browsing. Stories and voice tab definitions are present but their page initialization is commented out in this revision |
| `ui/DialogCacheBottomSheet.java` | Per-chat breakdown, category/file selection and cleanup | Desktop detail page or `modal`, retaining the selected chat and scroll position |
| `ui/KeepMediaPopupView.java`, `ui/CacheChatsExceptionsFragment.java` | Retention by chat type and per-chat exceptions | Local policy editor; exceptions must be account-scoped |
| [`messenger/CacheByChatsController.java`](https://github.com/DrKLO/Telegram/blob/f2908b14133bbffbf7ab04f641ecb5bfaf533242/TMessagesProj/src/main/java/org/telegram/messenger/CacheByChatsController.java) | Chat/file attribution and retention resolution | Explicit object/reference index instead of Android file-path lookup |
| [`messenger/AutoDeleteMediaTask.java`](https://github.com/DrKLO/Telegram/blob/f2908b14133bbffbf7ab04f641ecb5bfaf533242/TMessagesProj/src/main/java/org/telegram/messenger/AutoDeleteMediaTask.java) | Age-based cleanup and cache-size enforcement | One cancellable process service, using the same safe cleanup engine as manual actions |
| `messenger/FilePathDatabase.java` | File metadata used to associate downloaded data with messages/chats | SQLite metadata beside cached BLOBs; no requirement to move media to plaintext files |
| `ui/DataUsage2Activity.java`, `messenger/StatsController.java`, `ui/DataAutoDownloadActivity.java`, `messenger/DownloadController.java` | Sent/received counters, reset dates, network categories and download presets | Later phases; measure real local traffic and use desktop network capabilities |

Android's category list includes photos, videos, documents, music, voice/audio,
stories, stickers, profile photos, miscellaneous data and logs. It orders
categories by size and collapses smaller ones under an expandable group.
Keep this hierarchy, while assigning KomaruGram-specific data explicitly.

Android offers ordinary-chat retention of one day, one week, one month or
forever; stories also have a two-day choice. Its defaults are forever for
private chats, one month for groups, one week for channels and two days for
stories. Its size choices depend on device capacity. These are reference
behaviors, not instructions to silently change KomaruGram's defaults.

## Existing KomaruGram foundations and gaps

| Area | Observed implementation | Required change |
|---|---|---|
| Settings | `internal/messenger/ui/settings.go` has sections, `settingsItem` and shared cards, but no Data and Storage section | Add navigation and independent storage view/controller |
| Account cache | `internal/messenger/historycache/cache.go`: `history.db.plain` or `.secure`; messages, edits, deletion markers, key/value metadata, layouts, FTS and media | Versioned metadata migration and statistics without reading media BLOBs into Go |
| Media budget | `SaveMedia` enforces 256 MiB per account with a cumulative SQL query after each write | Configurable, batched enforcement; keep the current budget on upgrade until changed |
| Usage timestamp | `media(key, data, used)` records write time; `Media` does not update `used` on reads | Separate creation/write/access times; batched last-access updates for real retention/LRU behavior |
| Media identity | `tgstore/media.go` saves by media ID; `tgstore/stream.go` stores 128 KiB ranges under `<id>/range/<offset>` | Account-scoped canonical object ID, variants/ranges, actual cached bytes and completeness |
| Attribution | Media rows have no category or chat reference. Thumbnails also enter through history/collections; search results and stories are not history rows | Record origin and references on every cache path, including cache hits and results outside history |
| Other storage | Preferences, downloaded emoji packs and wallpapers, WASM modules, crash reports, attachment/recording temporaries and Mini App profiles have separate owners | Explicit inventory of owned roots, cleanup rules and volume IDs |
| Runtime caches | Decoded frames, images, audio and GPU resources have their own lifetimes | Invalidate affected entries after cleanup; do not present RAM as disk usage |
| Diagnostics | RPC statistics in `docs/PROFILING.md` count TL payload and omit transport overhead/internal traffic | Do not reuse them as a claim of complete network usage |

Read `account/account.go`, `cmd/messenger/account_host.go`,
`preferences/preferences.go`, `emojipacks/store.go`, `wasmmodule/module.go`,
`ui/attachments.go`, `ui/composer_voice.go` and `pkg/miniapp/bridge.go` when
building the inventory. Mini App roots must come from the active runner's
configuration: the generic package default is not necessarily the messenger's
root. User-selected font/module files and exported downloads remain user files.

## Screen structure and interactions

### Data and Storage landing page

Use existing settings rows for Storage Usage, then Network Usage and Automatic
Media Download as their phases become available. Storage's subtitle uses the
latest completed local snapshot, with no synchronous scan in `Layout`.
Existing playback/integration choices should be linked or reused if needed,
not duplicated into competing preferences.

### Storage Usage

1. **Scope:** current account by default, with an explicit All Accounts choice.
   The volume summary always labels the footprint of all KomaruGram data on
   that volume; the media chart and chat list label their selected account scope.
   List shared application data separately, counted only once.
2. **Volume card:** capacity, available bytes, KomaruGram local-data footprint
   and the rest of the used volume, shown in a compact segmented bar. Label the
   volume/path. If config and cache are on different volumes, show a card for
   each; never divide bytes from several volumes by one volume's capacity.
   This measures managed local data, not an unmeasured installation footprint.
3. **Media chart:** a large segmented ring with the cached-media total in its
   center; selecting categories also shows the selected amount. Legend rows
   carry color, name, size, percentage and checkbox. Small categories collapse
   into expandable Other; their identities remain stable when sizes reorder.
4. **Cleanup action:** “Clear selected cache” with an estimated byte count.
   Confirmation names the account/chat/categories and explains offline impact.
   Show completion, skipped objects and failures through the existing toast;
   refresh statistics from actual results rather than subtracting the estimate.
5. **Automatic removal:** retention by private chats/groups/channels/stories,
   exceptions and maximum media-cache size, introduced only after the engine
   exists. Keep these controls distinct from privacy's message-preservation rules.
6. **Local content:** Chats, Media, Files and Music tabs. Chats ship first,
   descending by cached bytes, with avatar, name, byte size and multi-selection.
   Add a local title filter and stable tie-break ordering. Opening a chat shows
   its category breakdown, local items and scoped cleanup. Other-account rows
   carry an account label; forum topics roll up under the parent chat initially.

Use `card`, `settingsItem`, `surface`, `tabRow`, `modal`, `loadingIndicator` and
`toast`. Wide windows can place details beside the overview; narrow windows
use ordinary navigation. Implement chart drawing in Gio; a charting dependency
is not required. Selection must be possible without distinguishing colors or
using a pointer. Respect reduced motion and current privacy mode, and provide
light/dark themes, focus order, Escape/back and readable large-number formatting.

States to design explicitly: first scan, refresh with previous values, empty
cache, no category selected, incomplete legacy attribution, locked account,
offline, unknown disk capacity, partial scan failure, clearing, cancellation
and concurrent downloads. Unknown/locked values are never displayed as zero.
Browsing cached files must not itself download missing previews.

## Accounting contract

Keep three quantities distinct in the model and UI:

- **Cached payload:** actual locally stored bytes, categorized once per stored
  object/variant/range. A partly streamed 2 GiB video might occupy only 8 MiB;
  show 8 MiB, optionally alongside its remote size and partial-download badge.
- **Physical footprint:** database and sidecar files plus owned files on each
  volume. Prefer allocated bytes where supported; label a file-length fallback
  as an estimate. Shared blocks, compression and concurrent changes can prevent
  exact reconciliation with the operating system's free-space counter.
- **Expected reclaim:** eligible objects in a cleanup plan. SQLite row deletion
  makes space reusable inside its file; this is not necessarily space already
  returned to the OS. Report payload removed and observed file shrink separately.

Do not add media BLOB sizes to the full database size: that double-counts them.
Do not pretend to attribute database pages, FTS overhead or free pages precisely
to individual chats. Show database/history and internal reusable space as
separate technical detail; the category ring describes payload only.

Count complete objects and cached ranges as physical copies if both really
exist; coalesce redundant ranges safely in a later optimization. Do not use
remote message sizes or a history-only JSON scan as the source of storage totals.

A media object can be referenced by several messages or chats. Store references
separately. A chat's attributed total counts each distinct referenced object
once within that chat; totals across chats may overlap, which the UI must state.
The global total and multi-chat cleanup estimate use the union of object IDs.
For single-chat cleanup, preserve objects still referenced by an unselected
chat, and show their shared/non-reclaimable amount. Global category/file cleanup
can remove a shared object's cached bytes for all referencing chats after
making that scope clear. Never split a shared BLOB by an arbitrary chat share.

Unknown legacy objects remain counted in an Unattributed bucket. Backfill
references in bounded batches from local messages, edits, lookup/collection
metadata and known references; do not fetch remote history to make totals look
complete. Attribution completeness must be visible, and counts must still add
up while backfill runs. Unknown chat names use a local fallback.

Recommended first category mapping: Photos, Videos (including video messages),
Files, Music, Voice Messages, GIFs, Stickers and Emoji, Profile Photos, Stories,
and Other Media. Keep preview variants associated with their parent category;
represent standalone avatars, wallpapers and pack previews by explicit origin.
Stories need explicit origin metadata: they can be represented as photo/video
messages today. Locally installed emoji packs belong to shared application
assets, not repeatedly to every chat using the emoji.

## Backend design

Names here are proposed, not existing APIs.

- `model/storage.go`: immutable `StorageSnapshot`, category/chat summaries,
  paginated local objects, `CleanupPlan` and progress/result types. Expose a
  narrow storage-management interface to the UI; keep SQL and OS calls out of it.
- `historycache`: versioned tables for object metadata (kind, origin, parent,
  byte size, creation/access time, availability/protection state) and references
  (object, chat, message or collection identity). Update data and metadata in
  the same transaction. Preserve the existing encrypted SQLite storage.
- `tgstore`: pass attribution at media writes and reads, including full files,
  streams, inline previews, avatars, stickers/custom emoji, wallpapers, stories
  and search results. Record references without inserting found messages into
  history. Cache hits from a second chat also add that chat's reference.
- A process-level storage service under `internal/messenger` coordinates all
  account caches, shared roots, OS volume queries and jobs. Wire it through
  `account_host.go`, reusing live stores and account lifecycle locks. Inactive
  accounts can be inspected without starting Telegram; locked encrypted
  accounts expose only known file sizes until unlocked.
- `preferences`: versioned local policies with account-scoped exceptions.
  Persist sensitive reference metadata inside the protected account database;
  do not create a plaintext cross-account index of chat names and IDs.
- OS adapters: Linux filesystem statistics and Windows volume/free-space APIs
  using existing `golang.org/x/sys`; unsupported platforms return unavailable
  capacity while retaining local totals. Preserve macOS buildability.

Use bounded, cancellable background scans and one active mutation job per
account. Publish generation-stamped snapshots to all affected windows; discard
stale UI responses after navigation/account changes. Never hold the cache mutex
across a directory traversal or the entire metadata backfill. Aggregate from
metadata/counters instead of loading BLOBs or all media rows into memory.
Watch account removal, protection changes and logout throughout a job.

## Cleanup and retention rules

Manual cleanup is local. It must never invoke message deletion, clear cloud
drafts, leave chats, end sessions or remove account credentials. Preserve
messages, edit history, deletion markers, FTS consistency and history coverage.
Do not put “clear local database” into the ordinary cache action: resetting an
offline archive is a separate product decision with different data-loss risks.

The engine first produces a reviewable plan, then revalidates it at execution:
object identity/version, account, eligibility, references and active leases.
Downloads, playback, viewer use, attachments pending upload and draft recordings
must not lose their working data. Prevent late download completion from
silently repopulating a just-cleared object, using an epoch/tombstone or equivalent
coordinated cancellation. Clearing must not trigger eager background re-download.
Invalidate decoded caches in every account window after committed removal.

Treat media attached to preserved deleted messages/edits, unavailable source
messages and explicitly kept offline items as potentially irreplaceable.
Exclude these from automatic removal. Manual inclusion needs a separate clear
warning and opt-in; do not promise that every cached file remains downloadable.
This may require a protected-media marker and a policy for objects with unknown
recoverability. Unknown legacy objects stay out of automatic removal until
their eligibility is established, or a separate conservative policy is chosen.

Clean registered owned files only. Do not recurse into arbitrary user paths,
follow symlinks outside the owned root, delete user exports, or infer ownership
from a filename prefix in the global temp directory. Shared installed assets,
Mini App profiles (which include browser state), crash reports and active
temporary files require their owner's separate lifecycle/cleanup policy.

SQLite compaction is an implementation gate, not a UI afterthought. Prototype
bounded reclamation on both plaintext and Adiantum-backed databases. Check
`page_size`, `page_count`, `freelist_count`, journal mode and file sizes before
and after deletion. Evaluate incremental vacuum and migration requirements,
with scheduled full compaction only if necessary and enough free space exists.
Never compact by exporting decrypted content to an unprotected temporary file.
Coordinate compaction with readers, writes, protection transitions and Windows
file locking. If physical reclamation is deferred or fails, report that
accurately; the reusable-space result is still useful.

Retention uses last actual access, not message date, file mtime or the existing
write-only `used` field. Batch timestamp updates to avoid a database write per
render/cache read, and do not extend retention merely by listing statistics.
Resolve policy as per-chat exception, then chat-type default. For shared objects,
honor the most protective applicable reference and active-use lease.

Keep automatic age removal off on migration, and retain 256 MiB per-account
media budgets until changed. Offer Android-like age choices and desktop-sized
budget presets plus a custom value/unlimited option. Explain that “forever”
disables age expiry, while a size budget can still evict ordinary cached media;
explicit offline protection is stronger. If protected/in-use data exceeds the
budget, report the excess and defer optional cache writes rather than destroy
protected data or spin on repeated cleanup. Enforcement should be incremental
and coalesced, not a full-table sort after each streamed chunk.

## Phase 0: inventory and measurements

Done on Linux (container, ext4-like overlay); Windows was not measured.

### Storage owners

`<config>` and `<cache>` are `os.UserConfigDir()` and `os.UserCacheDir()`.

| Root | Owner | What | Cache cleanup |
|---|---|---|---|
| `<config>/komarugram-go/accounts/<id>/history.db.plain` or `.secure` (+ `-journal`) | `historycache` | Messages, edits, deletion markers, spans, FTS, layouts, kv (viewports, update state), media BLOBs | Media rows only |
| `<config>/komarugram-go/accounts/<id>/session.json` | `account` | Auth key | Never |
| `<config>/komarugram-go/accounts.db`, `security.json`, `settings.json` | `account`, `security`, `preferences` | Registry, protection, settings | Never |
| `<config>/komarugram-go/wallpapers/` | `preferences` | Wallpaper pictures | Own policy |
| `<config>/komarugram-go/emoji/` | `emojipacks` | Installed emoji packs | Shared asset, own policy |
| `<config>/komarugram-go/miniapp/` | `ui/webapp.go`, `pkg/miniapp` | Mini App browser profiles | Own policy |
| `<cache>/komarugram-go/*.wasm` | `wasmmodule` | Fetched decoders | Re-fetchable, shared |
| `<cache>/komarugram-go/locks/`, `crashes/`, `komarugram-go.sock` | `account`, `crash`, instance | Locks, crash reports, socket | Never / own policy |
| temp `komarugram-go-attachments-*`, `komarugram-paste-*`, `komarugram-voice-*.ogg`, player sockets | `ui`, `pkg/player` | Working files of a send or playback | In use; not by prefix |
| temp `sqlite-*.db` | `pkg/tdata` | Decrypted tdesktop database during import, removed after | Never |
| Pictures, export/diagnostics directories | `ui`, `diagnostics` | What the user saved | Never |

All media Telegram gives the client goes to one table, `media(key, data,
used)`, through four writers in `tgstore`: whole files (`media.go`, key =
media ID), streamed video ranges (`stream.go`, `<id>/range/<offset>`,
128 KiB each), inline previews (`history.go`) and
collection thumbnails (`collections.go`). No row says which chat, message or
category it belongs to: the key is the only link, and a media ID can be
reached from several messages. This is why phase 1 needs the object and
reference tables; a scan of message JSON can attribute only part of it. The
256 MiB budget in `SaveMedia` evicts by `used`, which is the write time.

### Measurements

`STORAGE_PROBE=1 go test ./internal/messenger/historycache -run
TestStorageProbe -v` builds a synthetic cache in each mode: 100,000
messages in 1,000 chats with their FTS index, then 400 media of 512 KiB
(200 MiB, under the budget). Results:

| | Plain | Adiantum |
|---|---|---|
| Messages + FTS, file | 58.4 MiB | 58.4 MiB |
| Writing 100,000 messages (batches of 500) | 27.1 s | 27.8 s |
| Writing 400 media | 15.9 s, slowest 92 ms | 41.0 s, slowest 253 ms |
| With media, file | 257.7 MiB | 257.7 MiB |
| `count, sum(length(data))` of media | 0.4 ms | 0.5 ms |
| `sum(length(payload))` of messages | 50 ms | 82 ms |
| Messages per chat, `GROUP BY chat` | 16 ms | 17 ms |

`page_size` 4096, `journal_mode` delete, `auto_vacuum` NONE. Encryption adds
nothing to the file size. `length()` of a BLOB does not read it, so totals
of media from SQL are cheap; scanning message JSON is not. Go heap stays
under 5 MiB throughout: SQLite's memory is the wasm module's, outside it.

Compaction, the same in both modes (only times differ):

| Step | File | Free pages | Time |
|---|---|---|---|
| Half the media deleted | 257.7 MiB | 100 MiB | |
| `PRAGMA incremental_vacuum` | 257.7 MiB | 100 MiB | no-op with `auto_vacuum` NONE |
| `PRAGMA auto_vacuum=INCREMENTAL; VACUUM` | 157.6 MiB | 0 | 2.5 s / 2.6 s |
| Another quarter deleted | 157.6 MiB | 50 MiB | |
| `incremental_vacuum(4096)` | 141.6 MiB | 34 MiB | 184 / 272 ms |
| `incremental_vacuum` | 107.5 MiB | 0 | 338 / 567 ms |

### Conclusions for phase 1 and 3

- Deleting rows returns nothing to the OS; the existing databases need one
  `VACUUM` to switch to `auto_vacuum=INCREMENTAL`. After that, bounded
  `incremental_vacuum(N)` shrinks the file in short steps under the cache
  lock, on Adiantum as on plaintext, in place, with no decrypted copy.
- With `temp_store=memory` (set in `historycache` and `securedb`), `VACUUM`
  builds the new database in the wasm module's memory: RSS grew by the
  database's size (26 → 195 MiB) and stayed there until the connection was
  closed (21 MiB after). With `temp_store=FILE` for the `VACUUM` alone,
  RSS stayed at 29 MiB. Adiantum gives temporary files a random key
  (`vfs/adiantum/hbsh.go`), so this writes nothing in plaintext. The
  one-time `VACUUM` should use `FILE`, or reopen the connection after it.
- `VACUUM` needs free disk space of about the database's size for the
  temporary copy and journal (SQLite documentation; not measured here).
- Media totals by SQL are fast enough to answer on demand; per-chat totals
  need the reference table, not a JSON scan.
- Not measured: 100,000 media rows and 10,000 chats (the budget caps media
  at 256 MiB, so the row count depends on sizes), latency under concurrent
  writes, Windows, allocated size vs file length.

## Delivery phases

Each phase should be independently reviewable. Storage phases 0–5 are the main
request; phases 6–7 complete the wider Data and Storage section.

| Phase | Work and dependencies | Exit criterion |
|---|---|---|
| 0. Inventory and measurements | Trace every storage owner/write path, capture baseline size/latency/RSS on synthetic caches, settle metric definitions and prototype encrypted compaction | Owned roots, attribution rules and physical-reclaim behavior are documented and demonstrated; no live account cleanup |
| 1. Metadata and service | Versioned migration, object/reference index, access accounting, aggregate queries, background backfill, volume adapters and multi-account snapshots | Offline totals reconcile against synthetic fixtures; legacy/unattributed bytes and shared references remain visible; upgrade is restart-safe |
| 2. Read-only storage UI | Data and Storage navigation, volume cards, category chart, chat sizes, chat detail, mock source and render variants | All three user questions are answered, including per-chat media breakdown; no cleanup controls are exposed before phase 3 |
| 3. Safe manual cleanup | Shared engine, preflight, category/chat selection, leases, late-write handling, invalidation, partial-failure reporting and measured compaction | Selected cached media is cleared locally, preserved history stays readable, disk-reclaim reporting matches actual behavior |
| 4. Media explorer | Cached Media/Files/Music tabs, local preview, pagination, size ordering, multi-selection and individual-object cleanup | Large caches remain responsive; opening this browser does not cause downloads; combined selections do not double-count |
| 5. Retention and limits | Chat-type ages, exception editor, configurable account budgets and background enforcement | Fake-clock tests demonstrate expiry, access refresh, exception precedence and protected/shared-object behavior across restarts |
| 6. Network Usage | Durable sent/received counters, category/direction totals, reset date and confirmed statistics reset | Cache hits add no network bytes, transfers/retries are counted at the declared measurement layer, and resetting counters does not delete cache |
| 7. Automatic downloads | Explicit policy gates for preview/full download and prefetch; size and peer-type rules; manual-download override | Denied automatic requests never start; an explicit user request can still fetch; changing settings affects existing queues safely |

For phase 6, inspect transport instrumentation supported by the current gotd
version before choosing hooks. Include main/media/CDN connections and direct
HTTP downloads where applicable, separate unattributed/service traffic, and
avoid counting the same bytes both at transport and RPC layers. If only payload
bytes can be measured, name that metric honestly. Persist counters without
retaining message content. External mpv/VLC/browser traffic may be outside the
observable process and needs a stated boundary. On desktops, start with All
Networks; only offer metered/unmetered distinctions if the OS can detect them
reliably. Do not invent mobile/roaming totals from interface names.

For phase 7, distinguish downloading from autoplay and on-demand streaming.
Provide presets with editable category/size/peer rules, with metered-network
options only where available. Locate the gate before initiating network work
in `chatmedia`/`tgstore`, not merely in the UI. Android's calls data-saving,
Save to Gallery, proxy management and cloud-draft deletion remain follow-up
features with their own implementation and authorization requirements.

## Verification and acceptance

Use disposable account/cache fixtures for every destructive test. Do not test
cleanup on the maintainer's real data. For regression tests, demonstrate failure
without the implementation/fix and success with it, as required by `AGENTS.md`.

- **Accounting:** empty cache; every media category/origin; preview plus original;
  partial ranges; duplicate objects in several messages/chats; two accounts;
  shared assets; unknown legacy rows; unavailable volume metrics. Compare byte
  sums against known fixture data, not against the same production aggregate.
- **Migration:** old plaintext/encrypted databases; interrupted backfill; restart;
  idempotence; preservation of messages, edits, deletion markers, references and
  FTS. Existing lookup/search results must not become history entries.
- **Cleanup:** selected/unselected objects, shared objects, protected media,
  stale plans, open streams, late downloads, multiple windows, account removal,
  disk-full/permission errors, cancellation and restart after partial progress.
  Fake network sources must detect unexpected RPC/download calls.
- **Physical disk:** measure file size, allocated space where available,
  SQLite free pages and volume availability before/after deletion and compaction
  on Linux and Windows. Verify both protection modes; never equate deleted BLOB
  bytes with guaranteed filesystem space reclaimed.
- **Retention:** fake clock, batched access persistence, unknown access age,
  exceptions, shared reference conflicts, limit reduction and protected data
  above budget. Listing statistics must not change access times.
- **Performance:** fixtures with at least 100,000 media metadata rows and 10,000
  chats, plus streamed writes during scans. Record baseline and final first-page
  latency, scan duration, frame stalls, peak Go heap and RSS using
  [PROFILING.md](PROFILING.md). Require bounded batches and virtualized lists;
  choose numerical latency targets from phase 0 measurements rather than promise
  unmeasured performance. Verify memory is released after closing the section.
- **Visual/live:** add `SETTINGS_SECTION=data` and `storage` variants plus a
  dedicated `STORAGE_PNG_DIR` suite to `cmd/render-all`; cover both themes,
  Russian/English, narrow/wide windows, selection, per-chat details, loading,
  empty, unknown, large values and failure states. Inspect the PNGs. Live-test
  the demo from a scratch directory on the development OS; explicitly record
  Windows/Linux checks that remain unperformed. If shared components change,
  run all renders and ask the maintainer to inspect affected screens too.

Run repository build, vet, tests and formatting checks from `AGENTS.md` for
implementation changes. Preserve Linux `nowayland`, `nox11`, `novulkan` builds
and Windows amd64/arm64 cross-builds, with output outside the repository. No new
dependency is planned; if one becomes necessary, present the choice before
adding it. Map matching copy through `localization/telegram_keys.strings` using verified
`lng_…` keys and placeholder contracts; write English/Russian fallbacks for
Android-specific concepts with no equivalent. Android resource names are
references, not valid tdesktop language-pack keys.

The storage milestone is complete when the user can inspect accurate scoped
totals and chat/category detail offline, clear selected eligible cached data,
understand the real disk-space result, and configure retention without losing
their local message archive. Apart from phase 0's measurements, this
document was validated against source only.
