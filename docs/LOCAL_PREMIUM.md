# Local Premium and outgoing delivery

Local Premium keeps its additional data in each account's cache. It never
changes `Store.Premium().Active`, the subscription reported by Telegram.

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
- Saved GIFs have a local list without the server's item limit. Server refreshes
  cannot replace it, and saving or removing sends no mutation RPCs. Pins extend
  the server's list. Disabling Local Premium shows the server's lists again. This
  does not unlock uploads, reactions or server features.

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
- Devices do not share their local chats, only the synced ones.

## Outbox

The composer finishes submitting when `QueueMessage` has committed the request,
its peer/thread routing, stable random IDs, presentation data and private copies
of all attachments. It can then accept the next message while delivery continues
in the account store. Closing a window does not cancel delivery.
Delivery clears a cloud draft only if it still matches the snapshot captured
when the outgoing request was queued, preserving a newer draft written meanwhile.

`outbox` and `outbox_files` belong to the account database and follow its local
data protection. Files are copied in bounded chunks in the same transaction as
the request; a failed or interrupted copy publishes nothing. These copies are
independent of the original paths and of cache eviction. Upload attempts unpack
them into a private temporary directory, removed when the attempt ends.

The worker restores pending messages after `Configure` and resumes after the
account connects. Text, photos, albums, documents, music, voice, stickers, GIFs,
inline results, checklists and forwarded messages have pending presentation.
Forwarding and Repeat use the same queue and retain their original identities.
Transport failures and
server errors receive up to five attempts with increasing delays; flood waits
respect Telegram's requested delay. Offline time does not spend attempts.
Permanent errors remain in history with an error mark and a menu to retry or
delete the outgoing request. Failed requests do not block later messages.

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
go test ./internal/messenger/tgstore -run 'TestOutgoing|TestLocalPremium|Pin'
go test ./internal/messenger/ui -run TestComposerDrawsCustomEmojiPixels
```

`COMPOSER_VIEW=custom-emoji` with `COMPOSER_PNG` renders the custom emoji field;
`cmd/render-all` includes it. The pixel test uses a deterministic bitmap, rather
than checking only that an entity remains in the draft.
