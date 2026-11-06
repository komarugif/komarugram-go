# Spoiler particles and the deletion effect, from Telegram for Android

Reconnaissance for porting two effects of Telegram for Android
(`telegram-android`, `TMessagesProj/src/main/java/org/telegram`): the animated
particles of spoilers, over text and over media, and the "Thanos" dissolve of a
deleted message. Not Telegram Desktop's: its spoilers fade, ours open with the
circular wave we already have, and Android opens them with a wave too.

**Behavior, not code** (`docs/PLAN.md`): Telegram for Android is GPLv2, ours is
`Unlicense OR MIT`. What follows describes what the effects look like and how
they behave — sizes, timings, colors, curves — to be implemented anew; its Java
and its GLSL shaders (`res/raw/spoiler_*.glsl`, `thanos_*.glsl`, not in the
local clone, which keeps no `res/raw`) are not to be translated.

## What we have

- **Text spoilers** (`internal/messenger/ui/history_text.go`, `history.go`
  around `paintLineSpoiler`, `rich_label.go`, `chatlist_preview.go`): a solid
  rounded cover in `OutlineVariant` per line fragment, joined into one band
  across runs (`spoilerJoin`). No particles.
- **The reveal, which stays**: `spoilerReveal` — a circle from the click,
  300 dp/s, 120 ms … 1.5 s, cut out of the cover (`paintSpoiler`: the cover with
  a circular hole, the text clipped to the circle). This is "our ripple"; the
  port keeps it and its timing, not Android's (`radius × 0.3 ms`, 250…550 ms).
- **Media spoilers**: `model.Message.MediaSpoiler` is filled
  (`tgstore/convert.go`, also for self-destructing media), but the history
  draws such media plainly; the chat list's thumbnails are blurred
  (`chatlist_preview.go`). Showing media spoilers in the history is the
  separate task of the session doing the send dialog's spoilers; the particles
  here go over whatever cover that task draws.
- **Deletion**: the history is a snapshot (`model.History.Messages`), rebuilt
  by `chatPage.rebuild`; a message gone from it is gone, with no animation. A
  message can also leave the snapshot when the loaded window moves, so "gone
  from the snapshot" is not "deleted": the store has the deletions
  (`tgstore/history.go`, `UpdateDeleteMessages`/`UpdateDeleteChannelMessages`;
  `model.MessageDeleter` for the account's own).
- **Saved deleted messages** (AyuGram's, `docs/PLAN.md`): other people's deleted
  messages stay, marked "🧹" (`Message.Deleted`); the account's own deletions
  and bots' chats are erased. Only an erased message would dissolve.
- **Pieces to reuse**: `renderSnapshot` (`history_actions.go`, headless GPU,
  for message screenshots) and `App.avatarSprite` (an image drawn by the GPU off
  the frame) for a picture of the bubble; `motion_blur.go`; box blurs in
  `chattheme/render.go` and `photoedit/render.go`; `wdk.AnimationsEnabled`.

## Text spoiler particles (Android: `SpoilerEffect`, `SpoilerEffectBitmapFactory`)

- Under a spoiler the text is not drawn at all (laid out transparent); custom
  emoji in it are not drawn either. Over it: particles in the **text's color**,
  no solid cover (in a bubble the cover is the particles alone).
- The particles are **one shared tile**, animated, repeated (tiled) under every
  spoiler on screen: a square of `min(screen's shorter side / 2, 150 dp)` on fast
  devices or 100 dp on others, never under 80 dp, an alpha-only image. It is
  updated about every 32 ms (~30 fps) off the UI thread, double-buffered, and
  only the part of it the spoilers on screen use is redrawn.
- The tile is a 10×10 grid of emitters; each keeps up to `60 × side/200 dp`
  particles (and at most 750 on fast devices, 500 on others). A particle: born
  at a random point of its emitter, moving in a random direction at 4…10 px (not
  dp) per 500 ms in a straight line, living 1…3 s; it dies leaving the emitter's area inset 2.5 dp
  vertically. Drawn as round points, three groups of alpha 0.3, 0.6 and 1.0
  (stroke 1.4 dp for the faintest, 1.2 dp for the others); points near an edge
  are drawn on the opposite edge too, so the tile repeats seamlessly.
- **With animations off** (Android's power saving, `LiteMode.FLAG_CHAT_SPOILER`)
  the tile stands still: drawn once, not updated.
- **The reveal** (Android's, for reference; ours replaces its timing): inside
  the circle the text is drawn, outside it the particles; meanwhile the
  particles' color goes from the spoiler color to the text color and their alpha
  falls with the wave's progress. With our wave: clip the particles out of the
  circle as the cover is now; the color blend and the alpha may follow the
  wave's progress (`spoilerReveal.radius / reach`).

## Media spoiler particles (Android: `SpoilerEffect2`, `ChatMessageCell`)

- Media under a spoiler is drawn **blurred**: the picture scaled down to 20 dp
  wide (its aspect kept), stack-blurred with radius `max(10, side / 150)`, then
  brightness × 0.9 and saturation + 0.6 (a color matrix), stretched back over
  the media's rounded rectangle. Media Telegram sends as a blurred preview
  only (self-destructing) is drawn as it comes.
- Over it: **white particles**, one shared animated texture for all media on
  screen, of side `min(1280, (screen w + h) / 2)` on fast devices (900 / 0.8 and
  720 / 0.7 on average and slow ones). Each media draws it at its own size —
  scaled up when larger than the texture, never down — and, to look different
  from its neighbour, turned 180° or mirrored by its index (`index % 4`).
- The particles: `clamp(w × h / 500² × 1000, 500, 10 000)` of them (the texture's
  size), round points 1.2 dp across, drifting along a **curl-noise flow field**
  (smooth swirls that tile with the texture), speed capped at 6% of the
  shorter side per time unit, time running at 0.65× real time. Each goes
  through a cycle of 0.56…2.8 time units, fading in and out over it (alpha
  `sin(π t)`, times a random 0.6…1), then starts again where it is, at rest. The field moves slowly with time.
- **The reveal** (Android): a circle from the touch, as long as the text's
  (`maxRadius × 0.3 ms`, 250…550 ms, ease in-out), cutting the blur and the
  particles out; the play icon of a video grows with it. Ours: the same
  `spoilerReveal` circle.
- CPU cost here: a curl-noise field per particle per frame is the expensive part;
  Gio has no point sprites or transform feedback. Expect to simulate on the CPU
  at ~30 fps, a few thousand particles at most, into one shared `image.RGBA`
  tile uploaded once per update and drawn with `paint.ImageOp` under each media,
  the texture reused across frames (memory rule).

## Deletion: the "Thanos" dissolve (Android: `ThanosEffect`, `ChatListItemAnimator`)

- **When**: a message deleted while on screen, already sent (not one still
  sending, not a scheduled one moving), at least 2 s old in groups and bots'
  chats; and only if the effect is on (`LiteMode.FLAG_CHAT_THANOS`, Android's
  power saving setting). Otherwise the message fades out: alpha to 0 in 250 ms
  on the curve `cubic-bezier(0.199, 0.011, 0.279, 0.910)`, while the messages
  around it move into its place over the same time. Albums dissolve as one
  group.
- **What**: a picture of the bubble (its own bounds, not the full row width; the
  checkbox hidden first) is cut into a grid of tiny squares — about one per
  `max(0.4 dp, 1 px)`², up to 30 000…120 000 by device class — each a particle
  carrying its piece of the picture. The bubble itself is hidden the moment the
  particles take over.
- **Motion**: each particle starts where its piece was, with a random direction
  and a speed of 26…52 dp/s; it starts moving later the further right it is (a
  sweep from left to right over ~0.6 of a time unit), so the bubble crumbles
  from its left edge. Then it accelerates upward at 65 dp/s² and sideways, the
  way it already moves, at 19 dp/s², the sideways push fading out over the first
  0.35 time units of the effect. Each piece lives 0.5…1.1 time units once it
  moves, and fades out over the last ~0.45 of them. Time runs at 1.15× real time, so the whole effect lasts about
  1.5 s. The pieces are drawn a pixel larger than their cell, so the picture
  shows no gaps before it breaks up.
- **Where**: one overlay over the history, above the messages; it moves with the
  list when it scrolls during the effect.
- **For us**: hook it to the store's deletions, not to snapshot diffs; take the
  picture of the bubble before `rebuild` drops it (`renderSnapshot`, or the
  bubble's ops recorded at its place in the last frame); simulate on the CPU
  into an `image.RGBA` the size of the bubble plus the room the pieces fly into,
  reused across frames; with animations off, or for a message kept as deleted
  ("🧹"), nothing dissolves.

## Decided

1. **Text spoilers: particles over our cover.** The cover stays; the
   particles go over it, to be judged by looking at them. With animations off
   no particles are drawn at all, and spoilers are what they are now.
2. **No setting of their own.** `AnimationsEnabled` alone: off, a media
   spoiler's particles are one still frame, and nothing dissolves or fades on a
   deletion — a deleted message goes at once, as now.
3. **Kept deleted messages stay as they are.** Whether deleted messages are kept
   is the Visibility section's switch (`preferences.Keep.Deleted`); a message
   kept (marked "🧹") shows no effect. Only a message erased from the history
   dissolves or fades.
4. **A switch in the chats' settings**: "Thanos" or "Fade and move" (Android's
   fallback: alpha to 0 in 250 ms on its curve, the neighbours moving into the
   place). Either plays only with animations on.
