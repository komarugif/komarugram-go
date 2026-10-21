# Media store and downloads (design, 2026-10-21)

Agreed with the maintainer on 2026-10-21, and built: the sealed format
(`pkg/sealedfile`), the files and their index in `historycache`
(`media_files.go`), streaming onto them, the downloads directory
(`pkg/userdirs`), downloads with their tags (`tgstore/downloads.go`,
`historycache/downloads.go`, names in `savefile`) and the Downloads
section (`ui/downloads_page.go`): media files kept and encrypted outside the history
database, one store for the cache, streaming and the user's downloads, and a
Downloads section. Numbers are measured on the Linux VM (i5-12500H, AES-NI,
SHA-NI) unless said otherwise.

## Why

Media are BLOBs in the per-account SQLite cache today (`historycache`,
table `media`), behind one connection and one mutex:

- every write of a medium went through SQLite's rollback journal (written
  about twice) and, encrypted, through the Adiantum VFS;
- the budget check summed the whole table on every write: 43 ms a 512 KiB
  write plain, 159 ms encrypted, with the cache locked for every other
  reader meanwhile (fixed separately: the size is kept, eviction runs once
  an eighth of the budget is over);
- a streamed video is kept as 128 KiB rows `<id>/range/<offset>`; a 2 GB
  file pushes everything else out of the 256 MiB budget;
- a file the user opens is decrypted whole into a temporary directory of
  the window (`ui/attachments.go`) and is gone when the window closes.

## Layout

- **Small media stay in SQLite**: thumbnails, inline previews, avatars,
  stickers under 64 KiB. SQLite reads small BLOBs faster than the file
  system does (https://www.sqlite.org/fasterthanfs.html,
  https://www.sqlite.org/intern-v-extern-blob.html), and thousands of tiny
  files are slow on NTFS with an antivirus.
- **Larger media are files** in the user's cache directory
  (`os.UserCacheDir()/komarugram-go/media/<account>/`, not the
  configuration directory, roaming on Windows; backups skip caches),
  removed with the account, two hex characters of fan-out:
  `ab/abcdef…-<segment>`. The name is HMAC-SHA256(key, media id), 16 bytes
  in hex: a name tells nothing of the medium.
- **A file is a segment** of 64 chunks (8 MiB): a medium read in part
  takes only the segments read. File systems without sparse files allocate
  a file written far from its start whole: BFS does (Haiku R1/beta6, one
  byte at 512 MiB took 524 292 KiB), NTFS does unless the file is marked
  sparse (by its documentation; not measured). A file is made as long as
  it will be: on BFS, chunks written at once past its end were lost
  (`docs/PLATFORMS.md`, Haiku).
- **An index** in the history database (encrypted with it), table
  `media_files`: name, media id, size, chunks present (a bitmap), last
  use. The budget is 512 MiB (it was 256 MiB for the database alone): 64
  MiB for the small media in the database, 448 MiB for the files, summed
  from the index and kept in memory; past it, files no one reads go, the
  oldest first, until an eighth of it is free.

## Cache file format

A header, then chunks of 128 KiB (the size of a Telegram download part,
so a part is a chunk), each at a fixed place, so that parts that come out
of order (seeks, parallel downloads) are written where they belong and the
file is sparse until complete:

```
header (64 bytes): magic "KGM1", algorithm, chunk size, size, salt (32 bytes)
chunk i at header + i × (128 KiB + 16): ciphertext, tag
```

- Per-file key: HKDF-SHA256(account media key, salt, the algorithm and a
  context: the file's name and segment). The account key is
  `security.Manager.DeriveKey("media/"+account)`, from the TPM-sealed or
  password key the database already uses; names are keyed with
  `DeriveKey("media-names/"+account)`. A segment moved to another place,
  or another medium's file copied in place of one, does not open.
- AEAD per chunk, nonce = chunk index (unique under the per-file key), the
  index and a last-chunk flag in the associated data: a chunk cannot be
  moved, swapped with another file's, or the file truncated unnoticed (the
  STREAM construction age and Tink use).
- AES-256-GCM where the CPU has AES instructions, ChaCha20-Poly1305
  elsewhere (constant-time in software). A chunk written and read back,
  system calls included: AES-GCM 1.64 GB/s on Linux, 1.70 GB/s on Haiku;
  ChaCha20-Poly1305 0.74 and 1.24 GB/s; a buffer pool keeps it at 4 small
  allocations a chunk (without it, 0.57 GB/s, a third of the time in GC).
  The header names the algorithm. Both are Go's standard library and
  `golang.org/x/crypto`, already a direct dependency: nothing new.
- Unlike Adiantum, which a database needs to keep page sizes, this
  authenticates: a changed cache file fails to decrypt instead of giving
  other bytes.
- Without protection (the plain database) files are kept as they are, in
  the same layout without encryption, as the plain database keeps its rows.

Writes: a chunk is written with `WriteAt` and its bit set in the index;
no file-wide lock, so streaming, full downloads and readers run at once.
The index is written in batches (every 32 chunks, when a file completes,
when the last reader lets go), not a transaction a chunk; a chunk whose
bit was saved but whose write did not finish fails to open and is fetched
again. Protection enabled or disabled removes the files: those kept as
they are must not outlive it, those sealed lose their key.

Measured with `TestStorageProbe` (600 media of 512 KiB): 25.7 s plain and
1 min 36 s encrypted in the table before; 6.0 s and 2.9 s as files, the
slowest write 26 and 9 ms (92 and 315 ms before), and the database's
mutex not held while a file is written.

## Streaming

`tgstore`'s range reader reads chunks from the medium's file; a missing
one is downloaded (8 parts ahead, 4 at once) and written into the file. A
video played once is complete in the cache; the `range` rows of before go.
Whole downloads (`MediaProgress`) still download into memory and are saved
as files after; writing them into the file as they come is to do.

## Downloads

A downloaded file is plain, in `<Downloads>/komarugram/<name>`, named as
the message names it, with ` (1)`, ` (2)` for clashes.

- A medium complete in the cache is decrypted from it: no network.
- Otherwise it downloads into the cache and is decrypted from there, so a
  partly downloaded file resumes, and streaming meanwhile shares it.
- The record of a download, in the database (`downloads`): message (chat,
  id), media id, name, path, size, modification time, when, and a tag a
  chunk: GMAC, or Poly1305 without AES instructions
  (`sealedfile.Tagger`), 16 bytes a 128 KiB chunk, 256 KiB for a 2 GB
  file, under a key of the download's own (HKDF of a key of the account's
  and a salt of the download's, so that no two contents are tagged under
  one key and nonce). Tagging is 14.9 GB/s with GMAC and 4.1 with Poly1305
  here, where SHA-256 is 0.39. The account's key is random and kept in the
  database (`kv` `download-key`), encrypted with it when the cache is
  protected; it outlives the protection being enabled or disabled, which a
  key derived from the protection would not. Without protection it tells a
  file changed by mistake, not by one who can read the database.
- Opening a file (`ui/attachments.go`) downloads it there and opens it from
  there, as Telegram Desktop does; a file downloaded before, as it was, is
  opened again without downloading.
- Names are the message's, made safe as Telegram Desktop's
  `FileNameFromUserString` makes them, on every system (no path, control
  or bidirectional marks, none of Windows' characters or device names,
  `.lnk`/`.scf`/`.url`/`.desktop` masked), or its kind and date
  (`photo_2026-10-21_19-14-15.jpg`); a name taken is `name (1).ext`, and a
  file of the user's is never replaced (a hard link where the system makes
  one, which fails rather than replace).
- Deleting a download deletes the file only when it is as it was written;
  a file changed since is no longer the download's, and only the record
  goes.
- **A downloaded file stands in for the cache** once the cache dropped the
  medium: playing or opening it reads the downloaded file, and each chunk
  read is checked against its tag, under the account's key, so a file
  changed by someone else (even keeping its size and time) is caught. The
  cost is the chunks read, never the whole file in advance: a video seeks
  and checks only what it plays. A file that fails is no longer used and
  the record says so; the medium downloads again.
- Size and modification time are compared first only to skip a file
  known to have changed, not as proof it did not.

## The downloads directory

As Chromium finds it (`chrome/common/chrome_paths_*.cc`,
`base/nix/xdg_util.cc`, `chrome/browser/download/download_prefs.cc`):

- **Linux and BSD**: `XDG_DOWNLOAD_DIR` in `$XDG_CONFIG_HOME/user-dirs.dirs`
  (`~/.config` without it): `"$HOME/…"` or an absolute path, `\` escapes
  the next character, the last line wins. The names there are localized
  (`~/Завантаження`). Without the line the result is the home directory.
- **Windows**: `SHGetKnownFolderPath(FOLDERID_Downloads)` (a folder the
  user moved is found), `x/sys/windows.KnownFolderPath`; failing it,
  `Documents\Downloads`.
- **macOS**: `NSDownloadsDirectory`, which is `~/Downloads` (localized only
  in Finder's display).
- Chromium takes a result that is the home directory or the desktop for
  dangerous and uses `~/Downloads` (`Documents\Downloads` on Windows)
  instead.
- **Haiku**: WebPositive saves to the desktop by default (its settings,
  `download path` = `/boot/home/Desktop`, R1/beta6); `find_directory` has
  no downloads directory. As Chromium would, the desktop is passed over for
  `~/Downloads`, created if missing.

`ui.downloadsDir` does part of this today: it reads `user-dirs.dirs`
without escapes, and uses `~/Downloads` on Windows, missing a moved
folder. It and `sticker_set_export.go`'s own path become one function.

## The Downloads section

In the side menu, with `icons.FileFileDownload`; texts as Telegram
Desktop's (`lng_downloads_section`, `lng_downloads_view_in_chat`,
`lng_downloads_delete_sure_*`, `lng_downloads_delete_in_cloud*`):

- files on their way, with progress and cancel; files downloaded, newest
  first;
- open a file, show it in its folder, go to its message in the chat,
  select several and delete them from the disk (they stay in the cloud);
- the size of each file, and sorting by size, to see what to delete;
- a file deleted or changed outside shows so.

What the user downloads is theirs: nothing deletes it but the user. The
budget counts only the cache.

## Migration

At the first use of the files, the `range` rows go (a video streams again),
and the media rows over 64 KiB move to files in the background, one at a
time; a medium read meanwhile is read from the table. Once all are moved,
a database with more than 32 MiB free is rewritten once with
`auto_vacuum=INCREMENTAL` (`VACUUM`), and later only `incremental_vacuum`
gives free pages back. On the maintainer's cache (2026-10-21, plain): 142
media, 198 MiB, moved to files, 890 small ones (12.6 MiB) left in the
table; the database, 327 MB with 280 MiB of it free after the move, became
33.6 MB, in 381 ms on a copy of it.

## Order of work

1. The file store (format, index, budget, migration), with tests.
2. Streaming and whole downloads onto it.
3. The downloads directory, the records, decrypting into Downloads.
4. The Downloads section.

## Open

- Whether a downloaded file should also be dropped from the cache at once
  (one copy on the disk) or only when the cache needs room; for now the
  cache keeps it until it needs the room.
