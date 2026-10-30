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
- Pins and saved GIFs have local lists without the server's item limits. Server
  refreshes cannot replace them; disabling Local Premium shows the server's
  collections again. Saving, removing and reordering the local collections send
  no mutation RPCs. This does not unlock uploads, reactions or server features.

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
go test ./internal/messenger/tgstore -run 'TestOutgoing|TestLocalPremium'
go test ./internal/messenger/ui -run TestComposerDrawsCustomEmojiPixels
```

`COMPOSER_VIEW=custom-emoji` with `COMPOSER_PNG` renders the custom emoji field;
`cmd/render-all` includes it. The pixel test uses a deterministic bitmap, rather
than checking only that an entity remains in the draft.
