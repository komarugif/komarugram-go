# Messenger UI components

The messenger's UI (`internal/messenger/ui`) is built from a small set of its
own components on top of Gio and a few `gio-mw` widgets. New UI reuses them,
so that the same thing looks and moves the same everywhere: a tab row is a
`tabRow`, a clickable row is a `surface`, a dialog is a `modal`. Before
writing a new one, look here; when a component is missing, make it general,
put it next to these, and add it to this page.

## Rules

- **Update, then Layout.** A component handles its input in `Update` (or a
  `Clicked` method) before the frame is drawn, and only draws in `Layout`.
  `App.Update` runs before `App.Layout`; a component that is only laid out
  handles its events at the start of its own `Layout`, as `modal` does.
- **The caller keeps the state.** Components keep what they need to draw
  and animate (clicks, tweens, scroll positions), not what is shown: the
  active tab of a `tabRow`, the section of the chat list or the text of a
  message come from the caller or the `Store`.
- **Colors and type come from the theme.** `scheme(gtx)` returns the MD3
  color scheme; text uses `token.Typestyle…`. No literal colors.
- **Text comes from the catalog.** Every string is a key of
  `localization.Catalog` (`l.T`, `l.Format`, `l.Count`), with Russian and
  English values. When Telegram Desktop shows the same text, the key maps to
  its `lng_…` key in `TelegramKeys`, and the server's language pack wins.
- **Motion follows the settings.** Animations use `wdk` tweens with `token`
  durations and easings; pass `animate` (from `Window.Motion`) where a
  component can do without, as the spoiler does.
- **Images are uploaded once per frame.** Draw decoded pictures through
  `imageOps.Op`, whose `BeginFrame`/`EndFrame` drop textures not drawn.
- **Stores change in the background.** A store calls its `changed` callback,
  which invalidates the window; components read the store again on the next
  frame and never block on it.

## Building blocks (`common.go`, `surface.go`, `buttons.go`)

| Component | Use it for |
|---|---|
| `surface` + `surfaceStyle` | Anything clickable: background of the selected state, hover and press state layer, ripple, pointer cursor, accessibility label. Rows, chips, tabs and text buttons all embed it. |
| `textButton(gtx, *surface, text)` | A text-only button in the primary color, as in dialogs and status lines. |
| `tonalButton(gtx, *surface, text, count)` | A filled button in the secondary container color with an optional count, as the actions over selected messages. |
| `navigationButton` | Icon and title with a 48 dp target, for navigation controls. |
| `button.Filled()` / `button.Text()` (gio-mw) | The main and the secondary action of a dialog or a card, side by side with a `layout.Spacer{Width: 8}`. |
| `label`, `centeredLabel` | Text in a typestyle and color, limited to a number of lines (0 is unlimited). |
| `card(gtx, content, padding)` | A rounded surface container; `defaultCardPadding` for settings and profile cards. |
| `pill(gtx, text)` | Text on a rounded plate, like service messages and the "Choose a chat" hint. |
| `toast` (`toast.go`) | Every error and outcome of an action (sent, copied, saved, failed): a grey plate at the bottom of the list or dialog it is about, gone after four seconds, a new one replacing it. Never keep such a message as text on screen. The history's is `chatPage.toast`, over the composer, floating or classic; a dialog's is `modal.Toast`, under the dialog when there is room; the chat list, `scrollPage` (settings, profile), the picker and the photo viewer have their own. A failure that repeats on every refresh is told once: compare with the one told last. A load that failed keeps a round retry button (icon only, `history_jump.go`, with the ones that scroll to the start and the end of the history), without the error text. The buttons that scroll are not there at the end of the history, nor within 480 dp of it, as in Telegram Desktop (`jumpReserve`), and the one to the start not within the same of the start; the retry stays. Errors of fields with room beside them (sign-in, passwords, profile, program paths) stay under the field. |
| `vspace(dp)` | Vertical gap in a `layout.Flex`. |
| `fillRect`, `fillRounded` | Filling a size with a theme color. |
| `offset`, `inRect`, `exact` | Placing a widget at a point, in a rectangle or at an exact size. |
| `avatar` / `App.layoutAvatar` | A chat's photo, or its colored initials; pass `layoutAvatar` down as `avatarLayout`. The avatar in a chat's header and at the top of its info (and the account's own on the profile page) opens the photos of its profile in the photo viewer (`layoutAvatarTarget`, `photoViewer.OpenProfile`). |
| `withBadges` / `App.badges` | A name with the Premium star, emoji status, check mark or scam marks around it. |
| `drawBadge`, `drawBadgeRight` | Unread counters. |
| `flatEditor` | A borderless one-line editor with a hint, as in the composer and the picker's search. |
| `textField` | A one-line outlined field with a label, which can mask what is typed (passwords). |
| `openBrowser` | Opening a link in the system browser; call it off the frame goroutine. |

## Containers and navigation

| Component | Use it for |
|---|---|
| `tabRow` (`tabs.go`) | Tabs of equal width: centered titles, the active one in the primary color, an indicator that slides to the tab switched to, ripple on each tab. `Slide` brings in the content of the tab switched to from its side. Used by the composer's picker and the search. |
| `folderChip` (`folderbar.go`) | A capsule that is selected or not, with an optional counter: folders in the compact layout, sections of the search. Put several in a horizontal `scroll.List`. |
| `modal` (`modal.go`) | A dialog over a scrim: animates in and out, closes on Escape or a click beside it, takes the focus. The owner keeps what it shows until `Layout` reports it closed. Examples: `sessionEndedDialog`, `connectionFailedDialog`, `frozenView`, the delete dialog. |
| `contextMenu` (`contextmenu.go`) | A menu that grows from a corner of a rectangle, such as the attachment menu. |
| `scrollPage` (`pages.go`) | A centered scrollable column for a page, such as the profile. |
| `settingsItem`, `settingsChoiceCard` (`settings.go`) | A settings row with an icon, title and subtitle; a card with a title, choices and a hint. |
| `buttonRow` (`security.go`) | A secondary action at the start of a row and the main one at its end. |
| `navButton`, `sidebar` | The sidebar's sections and folders. |
| `splitter` | The draggable edge between the chat list and the page. |
| `spoiler` | Covering a value, such as a phone number in visual privacy mode, until it is clicked. |
| `loadingIndicator` | The circular progress indicator, labeled for accessibility: `page` fills a page, `sized` draws it d wide, `centered` in the middle of a line. Each place that loads keeps its own. |
| `scroll.List`, `search.Bar` (gio-mw) | Scrolling lists with a scrollbar; the search field of the chat list. |
| `toggle`, `checkbox`, `radio`, `slider` (gio-mw) | Settings controls. |

## Messages (`history_bubble.go`)

The history is drawn as materialgram draws it:

- `messageJoins` groups a sender's messages in a row (same sender, same
  day, less than 15 minutes apart): the group shows the sender's name at
  its top and one avatar, which sticks to the bottom of the view while the
  group is in it (`stickyAvatars`).
- `bubbleShape`/`shapeOf` round a bubble 16 dp, and 6 dp where it touches
  its group; `chatThemeController.Bubble` takes the same shape.
- `senderColor` colors a sender's name from its avatar color, readable on
  both themes; `datePill` is the tinted date over the history.
- `replyQuote` is the quote of a replied message, which jumps to it;
  `messageFooter` has the comments, views, author, edit mark and time;
  `reactions` wraps reaction chips.
- Channel posts have no avatar; groups, channels and bots have an icon of
  their kind before the title in the chat list (`chatKindIcon`).

### Code and quote entities (`history_text_blocks.go`)

`chatPage.richText` groups `model.TextRuns` into inline flows and `pre`/quote
blocks, retaining source rune offsets for selection across them. The block
plate uses theme colors. Code copying uses a compact `ContentContentCopy` icon button; a collapsed quote
reserves a 24 dp right gutter with a 16 dp expand/collapse icon on `surface`,
without a footer row. Quotes animate their height; while contracting they
keep the full text behind a moving clip. Selection fragments and clusters
are clipped too, so hidden lines have no hit regions. Code wraps by graphemes. Source newlines beside
blocks remain in copied text without adding empty lines to the layout.

`TEXT_BLOCKS_PNG_DIR=/tmp/text-blocks go test ./internal/messenger/ui -run
TestRenderTextBlocks` draws both themes, narrow/wide and collapsed/expanded.
`go run ./cmd/render-all -only text-blocks /tmp/text-blocks` runs the same
scenes. The live demo includes the same example at the end of each history.

A code block with a language is colored (`code_colors.go`): its text goes
to `internal/messenger/codehighlight`, which tokenizes it with Prism's
grammars on a goroutine of its own and redraws the window when the colors
come; until then it is plain. Its runs are cut into spans where the color
changes, each still mapped to its run, so selection, links and spoilers
work as before. The eight colors, in `codePalettes`, are the theme's for
light and dark. `CODE_COLORS_PNG_DIR=/tmp/code go test
./internal/messenger/ui -run TestRenderCodeColors` (`render-all -only
code-colors`) draws JavaScript, Python, HTML and a diff in both themes.

A rich message is an article (`article.go`, `article_layout.go`,
`article_blocks.go`): `prepareArticle` makes its blocks' texts one
sequence of runs, each block's a leaf that `textFlow` sets in its role
(`flowStyle`), so the text selects across blocks; `articleLayout` stacks
the blocks, registers one text area of the article's size and lays the
controls over it (code's copy, details' headers, media, buttons, cards).
`articleState` on the row keeps details opened, slideshows' items and
the surfaces, and where its anchors were laid out (`tops`): a link to
`#name` opens the details over the anchor and scrolls to it in the next
frame (`article_anchors.go`; the history with `restore`, under
`bubblePadTop` and `messageRow.articleAbove`). An article Telegram sent
cut short has "Show more" under it (`showMore`), which opens
`articleWindow` (`article_window.go`): a window of its own whose
`chatPage` draws the article as the history does, with its own photo
viewer, dialogs and toasts; `newArticleView` is the same without the
window, for tests. Its bar steps back and ahead once it went to an anchor
(`articleWindow.step`), and holds the search, sharing and zoom
(`article_window_tools.go`): the match gone to is the text's selection,
the others are tinted over the text (under it, the plates of code and
tables would hide them); the zoom scales the window's `Metric`, the same
in every window and kept in the settings. What the bar holds is laid out
with `Constraints.Min` zeroed, and an icon button's content is given its
exact size, since `surface.Layout` passes its caller's constraints on.
Until a rich message's row is laid out, the
history guesses its height from its article (`article_height.go`, sharing
the media's sizes with the layout). A row learns where its article is in
the view (`messageRow.viewTop`, from `chatPage.rowTop`), and media far
from it are not laid out, nor loaded. A table wider than its article
scrolls sideways under its view (`tableScroll`), its view passing presses
to the text under it. `ARTICLE_PNG_DIR=/tmp/article go test
./internal/messenger/ui -run TestRenderArticle` (`render-all -only
article`) draws every kind of block, and a part with its button, narrow
and wide, in both themes.

A message's link preview is a card under its text (`web_preview.go`),
in the style of a reply's quote; a page with an Instant View has a
button under it, as wide, in the style of "Show more". An Instant View
and a Markdown file (`markdown_viewer.go`) are made into a message with
a rich page off the frame (`chatPage.openSource`), which
`sourceEvents` gives to `openSourceWindow`: the article window, whose
bar has a button that opens the source as the system would
(`articleSource`).

Formulas (`formulas.go`) are laid out by `internal/messenger/formula`:
RaTeX (`pkg/ratex`) in a sandbox on a goroutine of its own, as code is
colored. `chatPage.formula` asks for one and redraws when it comes; until
then, and when RaTeX cannot read it or it is too large to draw, its
source shows in the code's font. An inline formula is one `styledtext`
box (`SpanStyle.Box`), its baseline on the line's, scaled down to fit
the line, to half its size at most; a block's is centred, 1.21 times the
text, and scrolls sideways as a wide table does. Either is one cluster
of all its source (`formulaFragment`), so it selects and copies as its
source. `ratex.List.Draw` fills KaTeX's glyphs from their outlines, and
draws what those fonts lack, Cyrillic in `\text`, with the client's text
font (`formulaGlyph`); a formula without a colour of its own takes the
text's. The article's render test draws them once they are laid out
(`waitFormulas`).

The drafts bots stream are messages with `Streaming` set at the end of
the history (`streamed_drafts.go`): a ring (`chatPage.writing`) turns in
their footer, their buttons do nothing, they have no menu and no place
in a selection, and while one may be stopped the composer's Stop
(`messageComposer.stopDraft`) takes the place of Send. Their text types
itself in (`typing.go`): `typingStep` moves a caret along the lines of the
text's fragments and gives the height to show, down to the caret's line;
`typed` replays the recorded text area clipped to what the caret passed,
the caret's line under an edge drawn in strips of falling opacity. The
message a draft becomes takes its caret over in `rebuild`
(`handOverTyping`). Details in an article unfold through a
`heightTransition` of their body (`articleState.opening`), its blocks
faded and the arrow turned with it.

Hashtags, commands, email, phone and card numbers and formatted dates
act on a click (`history_entities.go`, `chatPage.activateRun`). A phone
number, a card or a date opens `entityMenu`, a context menu at the press
(`contextMenu.Place`) with lines that may tell only, or say more in a
second line; what it asks Telegram comes into it while it is open. It
takes the presses itself, before the messages take their clicks, since
menus that read them after open at the previous press when a click comes
in one frame. Formatted dates are written in the reader's language when
the row is made (`messageRuns`), and again when a relative one changes.

### Height transitions (`height.go`)

A view owns a `heightTransition`. `Value` moves from the displayed height
when a target changes or reverses; `Card` measures once, paints the shared
card background at the animated size and clips both drawing and input.
First layout, width changes and disabled animations snap to the target.
Transitions use `token.DurationMedium2` and `token.EasingStandard`.

The disclosure audit covers code quotes, open context menus (including
expanded reactions), modal cards, settings cards (logout confirmation,
privacy/security, fonts, emoji, appearance and integrations), profile editing,
login/lock forms, chat-info cards, reply strips, bot reply keyboards and the
player's height reservation. These views keep independent height state;
`modal.Card` resets with its dialog. Fixed-size pickers and attachment forms
retain their existing entrance/exit transition and now also animate changes
to an open panel's height. Menu backdrop sampling follows the displayed
origin so blur does not slide independently of the panel. Cursor menus use
`contextMenu.Place`: choose a side only on opening, then clamp growth to
the window edge instead of flipping the whole menu across the pointer.
A viewport resize snaps both menu position and height and finishes any
entrance in progress; it does not retarget the content animation each frame.

Use `heightTransition.Card` for a stateful card whose contents change height;
plain `card` remains suitable for static or virtualized list items. Keep
source data until a closing strip has reached zero height and disable its
controls as it closes. Avoid animating normal window resizing. The Gio blur capture is aligned to
the deepest downsampling grid so moving the whole capture, as when a menu
grows upward, does not change the blur sampling phase. The older clip- and
size-stability fixes remain in place.

## Messenger views worth copying

- **Chat list rows** (`chatlist.go`): `layoutRowWith` draws any row that looks
  like a chat (avatar, title, time, line, counter) with a `surface` of the
  caller's; search results reuse it for found messages.
- **Devices** (`sessions.go`): the settings section of the account's
  sessions, grouped as Telegram Desktop's Active Sessions, with a dialog of a
  session's details; it asks again every minute while it is open.
- **Search** (`search.go`): a `tabRow` over `folderChip` capsules over a list
  of results with a status line and its action button: a pattern for any
  panel with modes and filters. `recentSearch` (`search_recent.go`) is its
  history while nothing is typed, with a menu on a right click that takes
  the keyboard for its Escape (`key.FocusFilter`, then `key.FocusCmd`).
- **Box for sending files** (`composer_send_files.go`): a `modal` of what was
  chosen in the attachment menu, as Telegram Desktop's: media as a grid of
  squares (one picture alone keeps its shape), other files and, with "Send as
  documents", everything as rows with a thumbnail, a caption editor that takes
  the composer's text and Enter to send, and the checkboxes that
  `sendfiles` says apply (`HasGroupOption`, `HasDocumentsOption`,
  `HasHighQualityOption`). Files are looked at in the background
  (`sendfiles.Inspect`, `Thumbnail`; music as "Performer – Title" with its
  cover, from `pkg/audiotag`; the first frame of a video from
  FFmpeg, when there is one); a file that cannot be sent is told in the
  dialog's toast. What it sends is `model.OutgoingFiles` in an
  `OutgoingMessage`, so retries reuse the composer's identity handling.
- **Dropping files** (`drop.go`): `fileDrop`, the App's, follows the
  `app.DropEvent`s of a drag from another program (`appwindow` hands them
  over, less its own title bar) and draws Telegram Desktop's areas over the
  page of the open chat when it takes files (`chatPage.takesFiles`): one
  for documents, and one for photos or media when `sendfiles.DropStateOf`
  says the files are pictures, or pictures and videos. A drop goes to the
  box above, as documents or not by the area; over an open box the files
  join it. Where the page is comes from the last frame.
- **Bots** (`bot.go`): `botPage` on the chat page. The buttons under a message
  are `textButton`s in the bubble (`history_bubble.go`); a callback asks the store
  (`model.BotStore`) in a goroutine and comes back through `botUpdate` to the
  toast. The reply keyboard is a strip under the composer's bar, over which the
  reply strip stacks: `keyboardHeight` is added to `replyHeight` wherever the
  history reserves room for the composer, and `composer.Layout` draws it.
  An empty chat with a bot draws `layoutStart` in the bar's place. Typing
  `/` lists commands over the composer (`layoutCommands`).
- **Overlays and blur** (`overlay.go`): the floating composer, the context menus
  (including the composer picker) and the toasts may blur what is behind them
  and let it show through
  (`preferences.Overlays`: one transparency for all, a switch each for menus and
  toasts, and `ComposerBlur` for the composer; all off while animations are).
  How it is done, as the composer did it first: the owner of the content records
  it while drawing (`chatPage.layoutHistory`, `chatList.Layout`) into a
  `blurBackdrop` and draws the recording as usual; an overlay calls
  `overlayFill`/`overlayPlate` with it and its own origin in the coordinates
  the recording was made in (`shifted` for overlays drawn in others, as the
  page header's menu), which draws the recording again under a blur and the
  fill over it at the chosen opacity. An overlay whose owner gives none is
  opaque. A toast is never less than 80% opaque (`toastMinOpacity`): its
  light text is only readable on its dark plate. A new overlay: take `p.menuBackdrop()` / `p.toastBackdrop()` (or the
  list's), fill with `overlayFill`. Not covered: forms, dialogs' toasts, the photo viewer,
  and the toast of the search results.
- **Main window surfaces** (`window_surface.go`): on Wayland and Windows the
  sidebar, chat list (including the compact folder bar) and chat header let
  the desktop show through at `preferences.Global.WindowTransparency`. The
  compositor supplies blur through the same effects as the photo viewer's
  window: `appwindow.Options.Transparent` / `BlurBehind`, and
  `Window.SetEffects` to change them; do not pass `app.Transparent` or
  `app.BlurBehind` to a window yourself. `fillWindowSurface` changes only the fill alpha; text, avatars and
  history stay opaque. Other backends retain opaque surfaces. Never paint an
  opaque root behind translucent panels, or stack replacement headers over
  each other. Search and selection replace the ordinary header. The separate
  slider is in Appearance → Transparency and blur; zero disables compositor
  blur and keeps the window opaque. `WindowBlur`, a separate checkbox in the
  same card, can turn compositor blur off without changing transparency; it
  does not depend on animation settings. `TestWindowSurfacePixels` checks actual
  frame alpha, with `WINDOW_SURFACES_PNG_DIR` saving the frames.
- **The window's own frame** (`internal/appwindow/frame.go`): on Windows blur
  is acrylic, which is drawn right only behind a window without the system's
  frame, so a window that blurs has none and `appwindow` draws one: a caption
  of 31 dp, the height of the system's at 100%, with the title and the minimize, maximize and close buttons, above
  the content, which gets the rest of the window. The caption moves the
  window, and the system maximizes it on a double click and snaps it; the
  buttons are beside the move area, not over it, or the system would take
  their pointer. Without blur the window has the system's frame again. The
  caption is of the theme's `SurfaceContainer` unless the content implements
  `appwindow.FrameFiller`, as the main window (its sidebar's translucent
  fill) and the photo window (its backdrop) do. A button lights up under the
  pointer and fades once it has left, as the system's buttons on Windows 10
  do (80 ms in, 220 ms out, by eye), and switches at once with animations
  off; the close button is red with a white glyph. The window has a
  border of a pixel all round, of the theme's `OutlineVariant`, drawn over
  the content (`layoutBorder`): the system draws a white line of a pixel
  along the top of such a window behind the content, which showed through a
  translucent caption, and the border covers it. A maximized window has no
  line and no border. `TestOwnFrameIsDrawn`
  draws the frame, and saves both themes with `FRAME_PNG_DIR`.
- **Mini Apps** (`webapp.go`, `internal/messenger/miniapps`): the window's
  `webApps` owns a `miniapps.Runner`, which asks the store for the link, opens
  it with `pkg/miniapp` and answers the app's events on its own goroutines;
  what it needs the window for (a link to confirm, a failure) is queued and
  taken by `updateMiniApps` on the next frame, shown in the page's `toast` and
  `askLink`. Buttons call `chatPage.openWebApp`; the bot's menu button is
  `layoutBotMenu`, a pill beside the paperclip.
- **Menu of a chat in the list** (`chatlist_menu.go`): a right click on a row
  opens it at the pointer, as a `contextMenu` in the header menu's style. The
  rows and the list take the presses as `search_recent.go`'s do (`PassOp` areas
  over what is clicked). Items are `chatRowPin`/`chatRowUnpin` (only in the list
  of all chats) and `chatRowRead`; a pin over the limit is answered by the
  list's `toast` without asking the store, and a store's failure comes back to
  the toast on the next frame. A pinned chat with nothing unread draws
  `drawPin` where its counter would be.
- **Forum page** (`forum.go`): the topics of a forum chat in place of its
  history, under the chat's header (which opens its info). A row is drawn as the
  chat list's: `surface`, icon plate (`fillRounded`, the topic's colour, a house
  for General, `drawPin`/lock for the marks), badges through `drawBadgeRight`.
  A topic opens as `commentsView{topic: true}` shown by `layoutComments` on
  the thread's `chatPage`, whose `topic` flag makes it read what it shows;
  its header tells how many messages it has (`topicCount`, from the
  thread's `History.Count`, the topic's first message not counted, as
  Telegram Desktop's). The search button at the end of the forum's header
  (`forum_search.go`, `model.ForumSearcher`) searches all the topics: a
  field over the header, as a chat's search has it (`layoutSearchField`),
  and what is found in place of the topics, each message drawn as its
  topic's row draws its last one; a click opens the topic at the message
  (`OpenTopicAt`), tinted. Going back finds the search as it was.
  `FORUM_PNG` saves the list.
- **Message sent** (`history_send.go`): a message the composer sent, when it
  shows at the end of a history that is at its end, flies up from the
  composer to its place while it fades in (300 ms); the rest of the history
  moves at once. The composer notes each send (`noteSent`) and the page takes
  a note for each message of the user that comes (an outgoing one, or any in
  Saved Messages, where Telegram does not mark them outgoing), so one from
  another device does not fly. With the animations off, by the setting or the window, or
  when the history is not at its end, the message is in its place at once.
- **Composer picker** (`composer_picker.go`): installed sticker/emoji packs and
  a Trending section for server-featured packs. A featured sticker is sent
  like an installed one; the pack's title row opens the sticker set dialog.
  The emoji tab has Telegram Desktop's seven static sections (people, nature,
  food, activity, travel, objects, symbols and flags), each with a title, and a
  button in the footer for each that takes the list to it; the button of the
  section in view is lit. Their emoji are in `emoji_sections.go`, and their
  names and keywords, in English and Russian, in `emoji_keywords.go`: both are
  generated by `go run ./cmd/emoji-sections -unicode emoji-test.txt -cldr cldr/common`
  from Unicode's `emoji-test.txt` (https://unicode.org/Public/emoji/16.0/) and
  CLDR's annotations (`common/annotations` and `annotationsDerived` of
  release-46, https://github.com/unicode-org/cldr). They are shown once the
  tab's page has come, not while it loads: the recent emoji arrive with it,
  above them, and only the first page is waited for (`pageLoaded`).
  The search of the emoji tab (`emoji_search.go`) is done by the client, at
  once, offline: by the words of the names and keywords in the language and in
  English, an exact name or keyword first; Telegram's own results, custom
  emoji, come after them. What a search found stays until the next one has
  come, and it keeps the recent and the packs of the page, so that the list
  does not empty and fill as a query is typed or cleared.
- **Frozen account** (`frozen.go`): one view shared by a bar over the chat
  list, a bar in place of the composer and a `modal`.
- **Sticker set** (`sticker_set.go`): a `modal` with an animated media grid and
  add/remove action, shared by sticker and custom emoji packs. The header's
  more menu exports the original documents as a ZIP archive. A click sends the
  sticker or puts the emoji into the draft (`messageComposer.chooseIn`), added
  set or not; emoji cells are 40 dp, sticker cells 64 dp. Stickers animate on hover even
  when automatic animations are off (also in chat and the composer picker).
  In the set's grid and the picker a sticker starts once the pointer has
  rested on it for 100 ms and no list has scrolled for 250 ms (`hoverPlay`),
  so sweeps and scrolls start no decoder. What is cheap to play
  (`chatmedia.Manager.CheapToPlay`: Lottie, drawn by the in-process WASM
  runtime, and a video sticker whose loop is decoded already) plays at once,
  as does anything in chat.
  A set opened again shows at once as last fetched (`model.StickerSetCache`),
  with its stickers' first frames, and is fetched again behind it; offline,
  it stays as cached. Loading stays at the center of the dialog. The creator menu action opens
  an accessible user or copies the creator ID hint, decoded from known pack
  ID formats; this is undocumented Telegram metadata, not verified authorship.
- **Message menu** (`history_menu.go`): a right click on a message opens a
  `contextMenu` at the pointer with Telegram Desktop's actions in its order.
  The area that takes right clicks passes every input on, and a closing menu
  lets clicks through. Actions reuse the selection's forward and delete
  dialogs (`openForwardParts`, `openDeleteParts`, `rightsFor`). The emoji
  packs item lists several sets in `emojiPacksDialog` (`emoji_packs.go`).
- **External player** (`player.go`): media opens through `chatPage.play`,
  which asks once in a `modal` when both mpv and VLC are installed and none
  is chosen; `playerSettings` is the same choice in the settings.
  `programSetting` (`programs.go`) is a card for a program the app runs — the
  one found, or a file the user picks, kept only after it passed a check. Use
  it for any new external program. `fontSettings` (`settings_fonts.go`) is
  the same for font files, a row per role in one card of the appearance
  settings: a file is kept once `internal/messenger/fonts` loaded it, and
  the windows' themes are made anew (`defaults.FontsVersion`).
  `emojiSettings` (`settings_emoji.go`) is the card of the emoji packs, the
  emoji sets of Telegram Desktop of the catalog built in (those of
  Telegram's cloud are left out where there is no account to download them
  through, as in the demo): a row a pack with its state (to download, downloading, installed, in use,
  outdated) and the buttons of that state; downloads run off the frame and
  report through a channel, as `programSetting`'s checks do. FFmpeg uses the same picker and validation;
  its path also applies to GIFs and animated avatars (with a companion
  `ffprobe` beside it, or on PATH). The internal WebM sticker player defaults
  to FFmpeg when found and falls back to WASM on decode failures; WASM can
  also be selected explicitly. Static WebM previews always decode one frame
  through WASM, then close the decoder instances. FFmpeg only starts for
  playback; dormant previews wait for an event without a frame timer. The
  choice does not affect Lottie, GIFs or avatars. Stickers do not require
  `ffprobe`. The internal MP4 animation player (`decoderSettings.animations`,
  `sticker_player.go`) is the same choice for GIFs and animated avatars:
  FFmpeg when found, otherwise, or when chosen, FFmpeg's H.264 decoder in a
  WASM sandbox, one GIF at a time on hover. That decoder is not in the
  binary: `wasmmodule.AVCDec` downloads it the first time it is needed
  from [libavcodec-wasm](https://github.com/komarugif/libavcodec-wasm), at a
  pinned commit, checks its SHA-256 and keeps it in the cache directory;
  `KOMARUGRAM_AVCDEC` names a build of the user's instead (a path or a URL).
  Every FFmpeg the client runs, voice messages' and the `ffprobe` beside it
  for videos sent as media included, is `video.ResolveFFmpeg` of the path in
  the settings, else PATH. Without one, the microphone (`composer_voice.go`)
  opens the system's file chooser for Opus in OGG, MP3 or M4A
  (`voice.FileExtensions`) and sends the file as a voice message, its
  duration read from its headers (`voice.FileDuration`), without a waveform;
  the file is the user's and is not removed. `program.LookPath` and
  `FindFlatpak` are the only searches for external programs:
  `-no-integrations` turns them off. Voice messages and music play in the
  client (`audio_player.go`): `audioPlayer`, one per window, which its
  chat pages share, so that what plays goes on in another chat; the bar
  over the chat (`audio_bar.go`, `layoutAudioBar`) tells what plays, with
  play or pause, the speed of what changes its speed (`speedChanges`,
  `audio.Stretched`), the buttons of the track before and after
  (`neighborAudio`), the volume button with its vertical slider under it
  (`volumeSlider`, shown while the pointer is over either) and a button
  that ends it, and what follows plays
  after the end (`nextAudio`);
  `voiceLayout`, the row with its button, waveform (`drawWaveform`) and
  time, and `musicLayout`, with the title, the performer and a bar
  (`drawBar`); `audioRow.update` takes their clicks and drags. `showWaveform`
  works one out, as soon as it is shown, for a voice message sent without
  one; `audioFormat` picks libopus, dr_libs or fdk-aac, and a format none
  takes goes to the external player (`takeExternal`); tests give
  `audioPlayer.play` a silent playback, so that nothing is heard. A GIF shown still starts no process either: it shows Telegram's
  thumbnail, fetched before the file (a real GIF file's first frame is
  decoded in Go); large GIFs have none and stay blank until played, as in the
  official clients. `ffprobe` and `ffmpeg` start when it plays, and `ffmpeg`
  exits a second after it stops.
- **Without a bubble** (`history_unwrapped.go`): stickers and a lone emoji
  (half a sticker's size) lie on the chat's background, as in the official
  clients; `infoPlate` is their time, and a reply or forward goes in a card
  at the side.
- **Reactions** (`reactions.go`): chips under a message toggle a reaction
  (`model.Reactor`); `reactionStrip` is the row at the top of the message
  menu that expands into all the chat's reactions. A double click on a
  bubble, off its text, puts the default reaction (`model.QuickReactor`);
  `reactedDialog` (`reacted.go`) lists who reacted, a tab for each reaction.
- **Comments** (`comments.go`): `commentsBar` ends a channel post's bubble,
  clipped to its shape; the comments open in a second `chatPage` under a
  `chatHead` — `layoutChatPageHead` with a back button in place of the
  avatar — over their channel.
- **Service messages** (`history_service.go`): an action, as a user added or
  a message pinned, is its words on a plate across the middle of the
  history (`servicePill`), worded in the UI's language by
  `localization.Catalog.Service` from `model.ServiceAction`. A pin's plate
  quotes the message and shows it on a click; a call is a bubble.
- **Themes and wallpapers** (`chat_theme.go`, `chat_theme_page.go`,
  `settings_chats.go`, `wallpaper_thumbs.go`; how they are chosen:
  `docs/CHAT_APPEARANCE_AND_MEDIA.md`). `chatThemeController`, one a window,
  is what the open chat is drawn in: `Background` draws its wallpaper,
  rendered off the frame at the history's size, `Backdrop` the same for a
  view of another size, `historyContext` gives the plates of dates and
  service messages the wallpaper's hue, and `messageContext` a bubble's
  colors; both make their values once a frame, not once a message.
  `themeScene` draws a chat as a theme will make it look (a date and a
  message each way), `themePreview` a theme small, as the cards of themes,
  and `wallpaperThumbs` renders wallpapers small in the background for
  cards and galleries. `actionRow` is a row of a dialog that does
  something, an icon and its words in the primary color.
- **Header actions** (`chat_menu.go`, `chat_search.go`): the search and
  menu buttons at the end of a chat's header. The search is a field over
  the header (`model.ChatSearcher`) with a counter and buttons to the older
  and newer found messages; the one shown is tinted for a moment
  (`highlight`). The menu is a `contextMenu` under its button. A thread's
  header, a topic's or a post's comments', has the search only; it
  searches the thread (`top_msg_id`), and a found message the thread has
  not loaded loads it around the message (`Reveal`). A hashtag clicked
  searches the chat it is in the same way.
- **Message shot** (`snapshot.go`): the dialog the selection's snapshot
  button opens, as AyuGram's message shot box: a preview rendered off the
  frame (`buildSnapshot`, `renderSnapshot`), the theme and what it shows,
  and buttons that save the PNG or copy it (`clipboard.WriteCmd` with
  `image/png`).
- **Pinned bar** (`pinned_bar.go`): under the chat's header, the latest
  pinned message above the history's bottom (`model.PinnedSource`); a click
  goes to it and shows the one before, as Telegram Desktop's does. Its line
  has a segment for each pinned message, up to four.
- **Reply strip** (`composer_reply.go`): the message a draft replies to, over
  the composer; the history's end and what opens from the composer move up by
  its height (`replyHeight`).

## Don'ts

- Don't bring in another `gio-mw` widget for something listed here, such as
  `tab.Group` for tabs: it looks and moves differently from the rest.
- Don't show loading as text ("Loading…", "Searching…"): use a
  `loadingIndicator` where the content will appear. Text is for what went
  wrong or what the user can do.
- Don't use `widget.Clickable` alone for a visible element: without a
  `surface` it has no hover, press or ripple.
- Don't keep a copy of store data in a component beyond a frame, except
  what an animation needs to finish, as `modal` does.

## Checking UI changes

Look at the result, not only at the tests: alignment, centering and how the
new piece sits next to its neighbours.

- **Render tests.** `renderFrames` in `session_ended_render_test.go` draws a
  component headlessly for enough frames to finish its animations and saves a
  PNG; `TestRenderComposer`, `TestRenderSettingsAccounts` and
  `TestRenderSessionEnded` show how. `TestRenderStickerSet` saves both sticker
  and emoji pack states; `TestRenderMessageMenu`, the message menu over a
  reply, with a floating and a classic composer. They are skipped unless their
  variable (`COMPOSER_PNG`, `COMPOSER_MOTION_PNG`, `SETTINGS_PNG`,
  `ACCOUNTS_PNG_DIR`, `SESSION_PNG_DIR`, `STICKER_SET_PNG_DIR`, `MENU_PNG`, `SAVED_EMPTY_PNG`, `VIEWER_PNG`, `PLAYER_PNG`, `COMMENTS_PNG`, `UNWRAPPED_PNG`, `SERVICE_PNG`, `JUMP_PNG_DIR`, `PINNED_PNG`, `REACTED_PNG`, `CHAT_SEARCH_PNG`, `SHOT_PNG`, `SESSIONS_PNG`, `FORUM_PNG`, `SHARED_PNG`, `TOAST_PNG_DIR`, `AUDIO_PNG_DIR`) is set; `go run ./cmd/render-all` runs them all. `TOAST_PNG_DIR` gets a toast in every place that has one, light and dark. The last one shows Saved Messages before its
  first dialog exists. `COMPOSER_VIEW=emoji-search`, the picker's emoji found for a word;
  `COMPOSER_VIEW=featured-stickers` or `featured-emoji`
  with `COMPOSER_PNG` shows the picker's recommendations; `COMPOSER_VIEW=voice`,
  a voice message being recorded; `files`, `files-one`, `files-documents`,
  `files-many`, `files-caption` and `files-music`, the box for sending files;
  `drop-photos`, `drop-media` and `drop-files`, the areas files dragged over
  the chat are dropped on.
  `SETTINGS_SECTION=premium` with `SETTINGS_PNG` shows the Premium section of
  an account without Premium and with Local Premium on (`LOCAL_PREMIUM=off`,
  off).
  `SETTINGS_SECTION=chats` with `SETTINGS_PNG` shows Chat Settings: the
  themes, the wallpaper, the composer and the look of messages;
  `wallpapers`, the gallery of wallpapers over it. `SHARED_PNG` saves the
  page of a chat's theme (`-themes`) and the chat in the theme (`-chat`).
  `SETTINGS_SECTION=integrations` with `SETTINGS_PNG` shows the choice of the
  external player; `PLAYER_PNG`, the dialog that asks for it. `MENU_PNG` also
  saves the menu with reactions (`-reactions*.png`); `COMMENTS_PNG`, the
  header of the comments page; `UNWRAPPED_PNG`, stickers and lone emoji; `SERVICE_PNG`, service messages
  and calls.
- **The app itself.** `go run ./cmd/messenger -demo` runs without an account.
  On Linux under X11, `xdotool` drives a window (`mousemove --window … click`)
  and `xfce4-screenshooter -w` saves the active one.

## Composer permissions

`model.SendPermissionsSource` separates default group bans from personal
restrictions and their expiry. The Telegram store reads every sending flag of
`chatBannedRights`, exempts group admins, and requires `post_messages` in
broadcast channels. Full peer refreshes supply the linked discussion group and
boost exemptions. Unknown group rights from old caches do not grant permission.

The composer preserves drafts while permissions change, disables individual
picker tabs and the microphone, and replaces forbidden text input with its
restriction and expiry. Media captions remain available when only plain text is
restricted. Files are checked in the selected upload mode (music remains music
in document mode); errors appear in the file dialog's toast. `Store.Send` checks
again before uploading anything. The channel bar has a local notification
placeholder and opens the linked discussion without joining it.

Render variants: `COMPOSER_VIEW=channel-readonly`, `restricted-text`, and
`restricted-media`, with `COMPOSER_PNG`; included in `cmd/render-all`.

`model.MembershipStore` tracks membership separately from send permissions. A
nonmember channel shows Join instead of the local notification placeholder.
The header menu offers a destructive Leave action for members of groups and
channels, with a confirmation. Joining and leaving apply returned updates and
change the dialog list only after server acceptance; cached history is retained.

Before an owner leaves, `messages.getFutureChatCreatorAfterLeave` supplies the
successor for the warning: transfer after seven days in channels/supergroups,
immediately in basic groups. The store refreshes and rechecks this plan on
confirmation. RPC errors from the preflight use Telegram's ordinary leave flow;
transport errors do not bypass it. Choosing another successor requires the
ownership-transfer flow in Telegram Desktop for now. Leaving never calls the
methods that delete a group/channel or revoke history for everyone.

Peer display metadata (badges, forum kind and known member count) is cached
independently of membership. Joining, receiving a first message, or rebuilding
a search result all use the same metadata conversion; an omitted count is not
zero. Full peer refreshes can change or clear these facts without overwriting
unread counts, pins, notification settings or message previews. Leave notices
use the operation's captured channel/group kind and are delivered to the chat
list, which remains visible after the dialog is removed.
