# Local Premium and outgoing delivery

Local Premium keeps its additional data in each account's cache. It never
changes `Store.Premium().Active`, the subscription reported by Telegram. It has
five separate parts (`model.LocalPremium`), of use with a subscription too, past
its higher limits. Each account has its own (`preferences.Account`). An account
that never changed them follows the one switch this was, all of them on but the
last:

- **Star**: the Premium star beside the accounts signed in here. Nothing else.
- **Media**: Premium stickers and custom emoji, the first two points below. Off,
  the chat and the message field show what Telegram has, what the other side sees:
  the overlays are taken off messages (also those the cache kept with them), local
  stickers are not shown, and the field draws a paid emoji as its ordinary one.
  Nothing is deleted, and it all comes back when it is on. Saved Messages are the
  exception: Telegram keeps paid emoji there for anyone, so they go there as they
  are, and are always shown. The panel keeps showing paid stickers and emoji unless
  the Visibility section's calm card takes out those that cannot be sent
  (`CannotSendPaid`): with a subscription or this part on, nothing is taken out, nor
  paid emoji in Saved Messages.
- **GIFs** and **Pins**: the lists past Telegram's limits.
- **Sync**: the storage channel, inactive and off without GIFs or Pins, having
  nothing to share. Ticking it, or GIFs while it is on, starts the offer; closing the
  offer with no channel bound unticks it. A part off reads nothing of the file, and writes
  back what the file said of it.

- Restricted stickers stay on this device, in a separate collection outside
  Telegram's ID-ordered history and contiguous cache spans. Their cached bytes
  are retained outside the disposable media budget. Regular stickers still send.
  Documents cached before entitlement metadata existed are checked against their
  sticker set before deciding whether they may leave the device.
- Custom emoji are drawn in the composer using the same media decoder and
  frame cache as message text. Without a real subscription, Local Premium sends
  their ordinary emoji fallback, preserving other text formatting. A local
  overlay associates the selected document IDs with the accepted message ID.
  Incoming history pages and offline pages restore the overlay only while the
  server text matches. The same mechanism covers captions and UTF-16 offsets.
  Free custom emoji remain native server entities. Changing or removing a local
  emoji also works when the ordinary server text has not changed.
- Pins and saved GIFs extend the server's lists rather than replacing them, and
  disabling Local Premium shows the server's lists again. This does not unlock
  uploads, reactions or server features.

## Pinned chats

There is one list. Its first chats, as many as the account's real
`dialogs_pinned_limit`, are pinned on Telegram and sync with other devices; the
rest are shown on this device only. `s.chats` keeps what Telegram says in either
mode, and `Chats()` overlays the list (`tgstore/local_pins.go`).

- A new pin comes first. At the limit, the last synced chat is unpinned on
  Telegram and stays in the list; the new one takes its place.
- Unpinning a synced chat lets the next local one up. Moving a local chat among
  the first ones changes which are synced; an order among the local ones sends
  nothing.
- The list changes at once, offline too. A background worker tells Telegram, and
  runs after each change, update or load of the dialogs.
- `Synced`, what Telegram had when last compared, tells "pushed off by this
  device" from "unpinned elsewhere". Changes from other devices are merged against
  it: an unpin elsewhere leaves the list, a pin elsewhere comes first, and a new
  order elsewhere is taken.
- A Premium that ended can leave more pins on Telegram than the limit. They stay
  until a new pin needs the room.
- A local chat gone from the dialogs (deleted, left, archived) leaves the list.
- Devices share their local chats only through the storage channel.

## Saved GIFs

Saving and removing go to Telegram as usual. Telegram's list is ordered by use and
moves a GIF up when it is sent, and at its limit pushes the last one out, whichever
device did it, with no `saveGif`. A list read from Telegram is therefore compared
with the one before it (`evictedGIFs`): of those missing, the last
`old + new - limit` were pushed out and the others removed. If both happened at once
it can take a removed GIF for one pushed out. What was pushed out stays here, under
Telegram's list, and leaves when it is on that list again, as after it is sent.
Removing a GIF kept here needs no connection.

Telegram's list no longer renews the reference of such a GIF. A message that has it
does: the channel's message, or the last message it was met in (`renewKeptGIF`).

## Storage channel

`tgstore/storage_channel.go` keeps the lists in a private channel of the user's,
shared by the account's devices. The channel is archived and muted.

- **Which channel.** A channel's description comes only with its full info, so it
  is found by the fixed name `model.StorageChannelTitle`, among the private channels
  the user created: in the list of chats, then page by page in the archive
  (`contacts.search` did not find it there on a real account). Ticking the sync binds it, or
  asks: choose one of the user's private channels, or create one. A channel of
  another name works, and must be chosen on each device.
- **A GIF's message.** Sending a GIF moves it up Telegram's list and may push another
  out; forwarding it as it is does neither, while hiding the author or the caption
  counts as sending. So a GIF goes forwarded from where it was met (not from content
  that is protected), or sent when it is at the top of the list already. Forwarded,
  it keeps its author and caption in the channel.
- **The file.** The channel's pinned message is `komarugram-storage.json`, replaced by
  editing the message: the messages of the GIFs pushed out, in order, the list of
  pins, and what Telegram had pinned when written. Text would hold too few ids. It
  is written a few seconds after a change on this device, and read at the start,
  when its message is edited, and when the list of GIFs is read. Changes merge with
  `mergePins`; two devices writing at once leave the last one's. A file of a newer
  version is neither applied nor replaced.
- **Sending all GIFs** (`runStoragePass`), offered once for a channel with no GIFs:
  from the last to the first, so that the list ends in its order, and the end of the
  list, what another device could push out meanwhile, is safe first. Half a second
  between messages is a guess. Lost connections and flood waits are waited out, a GIF
  Telegram refuses or that left the list is passed over, and the pass goes on after
  a restart. It cannot be cancelled, and its dialog cannot be closed. A GIF already
  pushed out with no known message cannot be sent without pushing out another, and
  stays on its device.
- A channel that is gone unbinds; the lists stay on the device. With the sync on and
  no channel, found or left (`StorageMissing`), the box is unticked and a dialog says
  that what is pushed out stays on this device alone: thought safe, it would go with
  the cache.
- The cache is the client's to lose. The channel is kept with the settings too, for
  each account, so that with a new cache it is known whatever its name, and the file
  brings back the GIFs pushed out and the pins. Only what has a message in the
  channel and is in the file comes back: a GIF that never reached the channel goes
  with its device's cache.

## Outbox

The composer finishes submitting when `QueueMessage` has committed the request,
its peer/thread routing, stable random IDs, presentation data and private copies
of all attachments. It can then accept the next message while delivery continues
in the account store. Closing a window does not cancel delivery.
Delivery clears a cloud draft only if it still matches the snapshot captured
when the outgoing request was queued, preserving a newer draft written meanwhile.

`outbox` and `outbox_files` belong to the account database and follow its local
data protection. Files are copied a few chunks to a transaction, so that a long
file does not stop the account's cache meanwhile; the request is written last, so
a failed or interrupted copy publishes nothing, and its chunks are cleared when
the account next opens. These copies are independent of the original paths and of cache eviction. Upload attempts unpack
them into a private temporary directory, removed when the attempt ends.

The worker restores pending messages after `Configure` and resumes after the
account connects. Text, photos, albums, documents, music, voice, stickers, GIFs,
inline results, checklists and forwarded messages have pending presentation.
Forwarding and Repeat use the same queue and retain their original identities.
Server errors and attempts that run out of time receive up to five attempts
with increasing delays. Transport failures and flood waits spend none: delivery
waits for the connection, or for Telegram's requested delay, however long.
Permanent errors remain in history with an error mark and a menu to retry or
delete the outgoing request, at the place their time gives them: later messages
come under them, and a toast says why, as the composer is long done with them.
Retried, they go back to the end. Failed requests do not block later messages.

Every attempt keeps the same `random_id`. Each accepted file/group is committed
before proceeding, so a partial batch skips accepted groups after retry or
restart. A successful send does not depend on reading the message back.
The original attachment copies are removed only on completion or explicit
removal of a failed request. Cancelling the caller waiting in `Send` does not
remove its durable outgoing request.

## Verification

Tests use a fake Telegram API, never a real account's mutation endpoints:

```text
go test ./internal/messenger/historycache -run TestOutgoing
go test ./internal/messenger/tgstore -run 'TestOutgoing|TestLocalPremium|Pin|Storage|EvictedGIFs|KeptGIF'
go test ./internal/messenger/ui -run StorageOffer
go test ./internal/messenger/ui -run TestComposerDrawsCustomEmojiPixels
```

`COMPOSER_VIEW=custom-emoji` with `COMPOSER_PNG` renders the custom emoji field;
`cmd/render-all` includes it. The pixel test uses a deterministic bitmap, rather
than checking only that an entity remains in the draft.
