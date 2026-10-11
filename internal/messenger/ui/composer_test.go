// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

type composerHarness struct {
	p      *chatPage
	router input.Router
	now    time.Time
	size   image.Point
	chat   int64
	kind   model.ChatKind
	// animate draws with automatic animations on.
	animate bool
	// still turns the animations of widgets off, as the setting does.
	still bool
	// drop, when set, is a drag of files over the page, drawn over it.
	drop *fileDrop
}

func newComposerHarness(t *testing.T) *composerHarness {
	s := mockstore.New(time.Now(), 0)
	p := newChatPage(s, func() {})
	p.images = &imageOps{}
	t.Cleanup(p.Close)
	h := &composerHarness{p: p, now: time.Now(), size: image.Pt(680, 720), chat: 1}
	h.frame()
	return h
}
func (h *composerHarness) frame() *op.Ops {
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(h.size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
	wdk.SetAnimationsEnabled(gtx, !h.still)
	h.p.images.BeginFrame()
	h.p.media.BeginFrame()
	h.p.Layout(gtx, model.Chat{ID: h.chat, Kind: h.kind}, localization.For("ru"), h.animate)
	h.p.layoutDialogs(gtx, localization.For("ru"))
	if h.drop != nil {
		h.drop.area, h.drop.metric = image.Rectangle{Max: h.size}, gtx.Metric
		h.drop.layout(gtx, localization.For("ru"))
	}
	h.p.media.EndFrame()
	h.p.images.EndFrame()
	h.router.Frame(ops)
	h.now = h.now.Add(time.Second / 60)
	return ops
}
func (h *composerHarness) click(x, y float32) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)})
		h.frame()
	}
	h.frame()
}
func TestComposerInputAndDrafts(t *testing.T) {
	h := newComposerHarness(t)
	h.click(160, 680)
	h.router.Queue(key.EditEvent{Text: "Привет 👋"})
	h.frame()
	if got := h.p.composer.draft(1).editor.Text(); got != "Привет 👋" {
		t.Fatalf("editor did not receive input: %q", got)
	}
	h.chat = 2
	h.frame()
	if h.p.composer.draft(2).editor.Text() != "" {
		t.Fatal("draft leaked")
	}
	h.chat = 1
	h.frame()
	if h.p.composer.draft(1).editor.Text() != "Привет 👋" {
		t.Fatal("lost draft")
	}
	h.click(640, 680)
	if !h.p.composer.pickerOpen {
		t.Fatal("smile did not open picker")
	}
	h.click(100, 100)
	if h.p.composer.pickerOpen {
		t.Fatal("outside click did not close picker")
	}
	h.click(38, 680)
	if !h.p.composer.attachOpen {
		t.Fatal("paperclip did not open menu")
	}
}

func TestPickerRowsShowFeaturedPacks(t *testing.T) {
	c := &messageComposer{tab: model.PickerStickers, page: model.PickerPage{
		Packs: []model.PickerPack{{ID: 1, Title: "Mine", Items: []model.PickerItem{{ID: "mine"}}}},
		Featured: []model.PickerPack{{ID: 2, Title: "Cats", Items: []model.PickerItem{{ID: "cover"}},
			Ref: model.StickerSetRef{Type: "id", ID: 2, AccessHash: 22}}},
	}}
	rows := c.pickerRows(300, 68, localization.For("en"))
	found := false
	for _, row := range rows {
		if row.featured != nil && row.featured.ID == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("featured set missing from picker rows: %+v", rows)
	}
}

// A sticker of a featured set, not added, is sent as in Telegram Desktop;
// the set's title row opens it.
func TestFeaturedStickerSendsAndTitleOpensSet(t *testing.T) {
	h := newComposerHarness(t)
	open := func() {
		h.p.composer.tab = model.PickerStickers
		h.p.composer.page.Featured = []model.PickerPack{{ID: 42, Title: "Cats",
			Ref:   model.StickerSetRef{Type: "id", ID: 42, AccessHash: 4},
			Items: []model.PickerItem{{ID: "preview/1", Emoji: "🐈"}}}}
		h.p.composer.pickerOpen = true
		for range 10 {
			h.frame()
		}
	}
	open()
	h.click(350, 300)
	if !h.p.stickers.modal.Shown() || h.p.composer.pickerOpen {
		t.Fatal("featured title did not open its sticker set")
	}
	h.p.stickers.stop()
	open()
	h.click(350, 350)
	if recent := h.p.composer.recent[model.PickerStickers]; h.p.stickers.modal.Shown() || h.p.composer.pickerOpen || len(recent) == 0 || recent[0].ID != "preview/1" {
		t.Fatal("featured sticker was not sent")
	}
}
func TestComposerSendEnterAndShiftEnter(t *testing.T) {
	h := newComposerHarness(t)
	h.click(150, 680)
	h.router.Queue(key.EditEvent{Text: "hello"}, key.SelectionEvent{Start: 5, End: 5})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, Modifiers: key.ModShift, State: key.Press})
	h.frame()
	if h.p.composer.draft(1).editor.Text() != "hello\n" {
		t.Fatalf("Shift+Enter: %q", h.p.composer.draft(1).editor.Text())
	}
	h.router.Queue(key.EditEvent{Range: key.Range{Start: 6, End: 6}, Text: "world"}, key.SelectionEvent{Start: 11, End: 11})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame()
	deadline := time.Now().Add(time.Second)
	for h.p.composer.draft(1).sending && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		h.frame()
	}
	d := h.p.composer.draft(1)
	if d.err != nil || d.editor.Text() != "" {
		t.Fatalf("send failed: %v / %q", d.err, d.editor.Text())
	}
	hist := h.p.source.History(1)
	if got := hist.Messages[len(hist.Messages)-1].Text; got != "hello\nworld" {
		t.Fatalf("sent %q", got)
	}
}
func TestCustomEmojiEditOffsets(t *testing.T) {
	e := []model.Entity{{Kind: "emoji", Offset: 2, Length: 2, DocumentID: 42}}
	shifted := shiftEmojiEntities("a 🙂 z", "🦆a 🙂 z", e)
	if len(shifted) != 1 || shifted[0].Offset != 4 {
		t.Fatalf("UTF16 offset: %+v", shifted)
	}
	if got := shiftEmojiEntities("a 🙂 z", "a x z", e); len(got) != 0 {
		t.Fatalf("deleted emoji kept entity: %+v", got)
	}
}
func TestGIFGridFits(t *testing.T) {
	for _, width := range []int{80, 240, 352, 700} {
		items := []model.PickerItem{{Media: model.Message{Media: &model.MessageMedia{Width: 400, Height: 100}}}, {Media: model.Message{Media: &model.MessageMedia{Width: 100, Height: 400}}}}
		ws, h := gifWidths(items, width, 4)
		if h <= 0 || ws[0]+ws[1]+4 != width {
			t.Fatalf("bad layout %v %d", ws, h)
		}
	}
}
func TestComposerStaleSearchResults(t *testing.T) {
	h := newComposerHarness(t)
	c := h.p.composer
	c.generation = 2
	c.loading = true
	c.results <- pickerResult{generation: 1, page: model.PickerPage{Items: []model.PickerItem{{ID: "stale"}}}}
	h.frame()
	if len(c.page.Items) > 0 || !c.loading {
		t.Fatal("stale search replaced current search")
	}
}

type failingComposer struct{ requests chan model.OutgoingMessage }

func (f failingComposer) Picker(context.Context, model.PickerRequest) (model.PickerPage, error) {
	return model.PickerPage{}, nil
}
func (f failingComposer) Send(_ context.Context, _ int64, m model.OutgoingMessage) error {
	f.requests <- m
	return errors.New("offline")
}
func TestComposerRetryKeepsIdentity(t *testing.T) {
	h := newComposerHarness(t)
	c := h.p.composer
	f := failingComposer{make(chan model.OutgoingMessage, 2)}
	c.source = f
	d := c.draft(1)
	d.editor.SetText("keep me")
	c.submitText()
	first := <-f.requests
	deadline := time.Now().Add(time.Second)
	for d.sending && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		h.frame()
	}
	if d.err == nil || d.editor.Text() != "keep me" {
		t.Fatal("failed send lost draft")
	}
	c.submitText()
	second := <-f.requests
	if first.RandomID != second.RandomID {
		t.Fatal("retry can duplicate a message")
	}
}
func TestRenderComposer(t *testing.T) {
	if os.Getenv("COMPOSER_PNG") == "" {
		t.Skip("set COMPOSER_PNG to render")
	}
	t.Chdir("../../..")
	h := newComposerHarness(t)
	h.p.composer.pickerOpen = true
	switch os.Getenv("COMPOSER_VIEW") {
	case "channel-readonly":
		restrictComposer(h, model.SendPermissions{Unavailable: true, Broadcast: true, DiscussionID: 9})
	case "restricted-text":
		restrictComposer(h, model.SendPermissions{Personal: model.SendText, Until: 1800000000})
	case "restricted-media":
		restrictComposer(h, model.SendPermissions{Default: model.SendVoice | model.SendSticker | model.SendGIF})

	case "stickers":
		h.p.composer.tab = model.PickerStickers
	case "featured-stickers", "featured-emoji":
		if os.Getenv("COMPOSER_VIEW") == "featured-stickers" {
			h.p.composer.tab = model.PickerStickers
		} else {
			h.p.composer.page.Recent = []model.PickerItem{{ID: "emoji/basic", Emoji: "🙂"}}
		}
		h.p.composer.page.Featured = []model.PickerPack{{ID: 42, Title: "Коты и собаки",
			Ref:   model.StickerSetRef{Type: "id", ID: 42, AccessHash: 4},
			Items: []model.PickerItem{{ID: "preview/1", Emoji: "🐈"}, {ID: "preview/2", Emoji: "🐕"}, {ID: "preview/3", Emoji: "🐱"}}}}
	case "gif":
		h.p.composer.tab = model.PickerGIF
	case "emoji-search":
		// The emoji of the picker found for a word, before Telegram answers.
		h.p.composer.search.SetText("кот")
	case "classic", "floating", "toast", "toast-classic":
		h.p.composer.pickerOpen = false
		classic := strings.HasSuffix(os.Getenv("COMPOSER_VIEW"), "classic")
		h.p.classic = func() bool { return classic }
		h.chat = 2
		if strings.HasPrefix(os.Getenv("COMPOSER_VIEW"), "toast") {
			h.p.toast.Show("Не удалось отправить сообщение: нет соединения с Telegram")
		}
	case "blur":
		// The history scrolled so that messages pass behind the blurred capsule.
		h.p.composer.pickerOpen = false
		h.chat = 2
		h.p.blur = func() bool { return os.Getenv("COMPOSER_BLUR") != "off" }
		h.frame()
		h.p.list.Position.BeforeEnd = true
		h.p.list.Position.First = len(h.p.messages) - 4
		h.p.list.Position.Offset = 120
	case "avatars":
		// A group chat scrolled so that a message ends behind the capsule.
		h.p.composer.pickerOpen = false
		h.chat, h.kind = 2, model.KindGroup
		h.p.avatar = func(gtx layout.Context, id int64, kind model.ChatKind, name string, size unit.Dp) layout.Dimensions {
			return avatar(gtx, id, kind, name, size)
		}
		h.frame()
		h.p.list.Position.BeforeEnd = true
		h.p.list.Position.First = len(h.p.messages) - 4
		h.p.list.Position.Offset = 120
	case "files", "files-one", "files-documents", "files-many", "files-caption", "files-music":
		// The box for sending files, with what it shows for each way.
		h.p.composer.pickerOpen = false
		h.chat = 2
		// The composer moves to the chat before files are put in its box.
		h.frame()
		kinds := map[string][]string{
			"files":           {"photo", "photo", "photo", "video", "file"},
			"files-one":       {"photo"},
			"files-documents": {"photo", "photo", "video", "file"},
			"files-many":      {"photo", "photo", "photo", "photo", "photo", "photo", "photo"},
			"files-caption":   {"photo", "photo"},
			"files-music":     {"song", "track", "file"},
		}[os.Getenv("COMPOSER_VIEW")]
		h.p.composer.files.addPaths(h.p.composer, boxPaths(t, kinds...), os.Getenv("COMPOSER_VIEW") == "files-documents")
		if os.Getenv("COMPOSER_VIEW") == "files-caption" {
			h.p.composer.files.caption.SetText("Закат над озером, вид с вершины")
		}
		for range 200 {
			h.frame()
			time.Sleep(5 * time.Millisecond)
			if h.p.composer.files.ready() {
				break
			}
		}
	case "drop-photos", "drop-media", "drop-files":
		// Files dragged over the chat, the pointer over the area of
		// photos or of documents.
		h.p.composer.pickerOpen = false
		h.chat = 2
		h.frame()
		kinds := map[string][]string{"drop-photos": {"photo", "photo"}, "drop-media": {"photo", "video"}, "drop-files": {"photo", "file"}}[os.Getenv("COMPOSER_VIEW")]
		h.drop = &fileDrop{page: h.p}
		h.drop.take(app.DropEvent{Kind: app.DropEnter, Position: f32.Pt(340, 600), Paths: boxPaths(t, kinds...)})
	case "voice":
		// A voice message being recorded.
		h.p.composer.pickerOpen = false
		h.chat = 2
		h.frame()
		levels := make([]float32, 200)
		for i := range levels {
			levels[i] = float32(math.Abs(math.Sin(float64(i)/5))) * 0.6
		}
		h.p.composer.recording = &voiceRecording{rec: &fakeRecorder{}, chat: 2, levels: levels, sampled: h.now.Add(time.Hour)}
	case "tasks":
		h.p.composer.pickerOpen = false
		h.p.composer.form = 3
		h.p.composer.title.SetText("Планы на сегодня")
		h.p.composer.tasks.SetText("Проверить интерфейс\nОтправить обновление")
	}
	if h.p.composer.tab != model.PickerEmoji && !strings.HasPrefix(os.Getenv("COMPOSER_VIEW"), "featured-") {
		h.p.composer.page, _ = h.p.composer.source.Picker(context.Background(), model.PickerRequest{Tab: h.p.composer.tab})
	}
	// Let asynchronous media decoders produce their first frame for visual QA.
	for i := 0; i < 20; i++ {
		h.frame()
		time.Sleep(15 * time.Millisecond)
	}

	win, err := headless.NewWindow(h.size.X, h.size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := h.frame()
	if err = win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	im := image.NewRGBA(image.Rectangle{Max: h.size})
	if err = win.Screenshot(im); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(os.Getenv("COMPOSER_PNG"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
}
func TestComposerSmallWindows(t *testing.T) {
	h := newComposerHarness(t)
	for _, size := range []image.Point{image.Pt(260, 400), image.Pt(180, 240), image.Pt(80, 80)} {
		h.size = size
		h.p.composer.pickerOpen = true
		h.frame()
		h.p.composer.pickerOpen = false
		h.p.composer.form = 3
		h.frame()
		h.p.composer.form = 0
	}
}

// TestRenderComposerMotion saves frames of the context menus opening and
// of a picker tab switch, part way through, for looking at them:
//
//	COMPOSER_MOTION_PNG=/tmp/motion go test ./internal/messenger/ui -run RenderComposerMotion
func TestRenderComposerMotion(t *testing.T) {
	prefix := os.Getenv("COMPOSER_MOTION_PNG")
	if prefix == "" {
		t.Skip("set COMPOSER_MOTION_PNG to a path prefix")
	}
	t.Chdir("../../..")
	win, err := headless.NewWindow(680, 720)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	save := func(h *composerHarness, name string) {
		ops := h.frame()
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rectangle{Max: h.size})
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(prefix + "-" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, im); err != nil {
			t.Fatal(err)
		}
	}
	// frames advances the harness's clock by n frames of 1/60 s.
	frames := func(h *composerHarness, n int) {
		for range n {
			h.frame()
		}
	}
	for _, menu := range []string{"picker", "attach", "picker-blur"} {
		h := newComposerHarness(t)
		h.chat = 2
		if strings.HasSuffix(menu, "-blur") {
			h.p.overlays = func() overlayPrefs { return overlayPrefs{menus: true, opacity: 0.5} }
		}
		frames(h, 2)
		if strings.HasPrefix(menu, "picker") {
			h.p.composer.pickerOpen = true
		} else {
			h.p.composer.attachOpen = true
		}
		frames(h, 4)
		save(h, menu+"-opening")
		frames(h, 30)
		save(h, menu+"-open")
	}
	// Press the emoji button, at the capsule's end, and catch its ripple.
	h := newComposerHarness(t)
	h.chat = 2
	frames(h, 2)
	smile := f32.Pt(float32(h.size.X-16-24), float32(h.size.Y-12-28))
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: smile})
	frames(h, 2)
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: smile})
	frames(h, 6)
	save(h, "ripple")

	h = newComposerHarness(t)
	h.chat = 2
	frames(h, 2)
	h.p.composer.pickerOpen = true
	frames(h, 30)
	// Switch from emoji to GIFs, as a click on the tab does.
	h.p.composer.tabs.Switch(int(h.p.composer.tab), int(model.PickerGIF))
	h.p.composer.tab = model.PickerGIF
	frames(h, 5)
	save(h, "tab-switching")

	// A message sent, in the middle of its flight from the composer.
	h = newComposerHarness(t)
	h.chat, h.animate = 2, true
	frames(h, 3)
	before := len(h.p.messages)
	h.p.composer.submit(h.chat, model.OutgoingMessage{Text: "Отправлено из композера"})
	for deadline := time.Now().Add(3 * time.Second); len(h.p.messages) == before && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
		h.frame()
	}
	frames(h, 2)
	save(h, "sending-start")
	frames(h, 6)
	save(h, "sending")
}

// emojiPage is an emoji tab with nothing of its own but the recent emoji.
// reply, when set, holds its response until the test has checked loading.
type emojiPage struct{ reply <-chan struct{} }

func (p emojiPage) Picker(ctx context.Context, r model.PickerRequest) (model.PickerPage, error) {
	if p.reply != nil {
		select {
		case <-p.reply:
		case <-ctx.Done():
			return model.PickerPage{}, ctx.Err()
		}
	}
	return model.PickerPage{Recent: []model.PickerItem{{ID: "emoji/🅰", Emoji: "🅰"}}}, nil
}
func (emojiPage) Send(context.Context, int64, model.OutgoingMessage) error { return nil }

func rowsItems(rows []pickerRow) map[string]bool {
	ids := map[string]bool{}
	for _, row := range rows {
		for _, item := range row.items {
			ids[item.ID] = true
		}
	}
	return ids
}

// The emoji tab has Telegram Desktop's seven sections, each with its title,
// and no emoji in two of them.
func TestEmojiSections(t *testing.T) {
	c := &messageComposer{tab: model.PickerEmoji}
	l := localization.For("en")
	rows := c.pickerRows(320, 40, l)
	var titles []string
	for _, row := range rows {
		if row.title != "" {
			titles = append(titles, row.title)
			if row.section != len(titles) {
				t.Fatalf("%q is section %d, want %d", row.title, row.section, len(titles))
			}
		}
	}
	want := []string{"Emoji & People", "Nature", "Food & Drink", "Activity", "Travel & Places", "Objects", "Symbols & Flags"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("the titles are %q", titles)
	}
	seen := map[string]int{}
	for _, row := range rows {
		for _, item := range row.items {
			if row.section == 0 {
				t.Fatalf("%q is in no section", item.Emoji)
			}
			if seen[item.Emoji] != 0 {
				t.Fatalf("%q is in sections %d and %d", item.Emoji, seen[item.Emoji], row.section)
			}
			seen[item.Emoji] = row.section
		}
	}
	// One of each kind: a face, an animal, a fruit, a ball, a car, a bulb, a
	// heart and a flag are where a user looks for them.
	for emoji, section := range map[string]int{"😀": 1, "🐶": 2, "🍎": 3, "⚽": 4, "🚗": 5, "💡": 6, "🇺🇦": 7, "🏁": 7} {
		if seen[emoji] != section && seen[emoji+"\ufe0f"] != section {
			t.Errorf("%q is in section %d, want %d", emoji, seen[emoji], section)
		}
	}
	if len(seen) < 1800 {
		t.Errorf("only %d emoji", len(seen))
	}
}

// The emoji tab, opened for the first time, does not show sections and then
// others above them: what shows while the page loads is still there when it
// has come, and the recent emoji, which come with it, are above the sections
// only once.
func TestEmojiPickerDoesNotReplaceWhatItShows(t *testing.T) {
	h := newComposerHarness(t)
	h.chat = 2
	c := h.p.composer
	reply := make(chan struct{})
	c.source = emojiPage{reply: reply}
	h.frame()
	l := localization.For("en")
	c.pickerOpen = true
	c.request(l, false)
	h.frame()
	if !c.loading {
		t.Fatal("the page did not start to load")
	}
	before := rowsItems(c.pickerRows(320, 40, l))
	if len(before) != 0 {
		t.Fatalf("%d emoji shown while the page loads", len(before))
	}
	close(reply)
	for deadline := time.Now().Add(3 * time.Second); c.loading && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		h.frame()
	}
	after := rowsItems(c.pickerRows(320, 40, l))
	if !after["emoji/🅰"] || !after["emoji/😀"] {
		t.Fatalf("the loaded tab shows %d emoji, without the recent one or the sections", len(after))
	}

	// A page that did not come leaves the sections, which need none.
	c.page, c.loading = model.PickerPage{}, false
	if !rowsItems(c.pickerRows(320, 40, l))["emoji/😀"] {
		t.Fatal("no emoji when Telegram sent none")
	}
}

// A button of the footer takes the list to its section, and the button of the
// section in view is the one that is lit.
func TestEmojiFooterGoesToSection(t *testing.T) {
	h := newComposerHarness(t)
	h.chat = 2
	c := h.p.composer
	c.source = emojiPage{}
	h.frame()
	c.pickerOpen = true
	c.request(localization.For("en"), false)
	for deadline := time.Now().Add(3 * time.Second); (c.loading || len(c.page.Recent) == 0) && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		h.frame()
	}
	for range 30 {
		h.frame()
	}
	rows := c.pickerRows(352, 40, localization.For("en"))
	// The footer's buttons are 44 dp wide from the picker's left edge; the
	// first is the recent, then the seven sections.
	button := func(n int) (float32, float32) { return 292 + 44*float32(n) + 22, 620 }
	x, y := button(4)
	h.click(x, y)
	for range 5 {
		h.frame()
	}
	if want := sectionRow(rows, 4); want <= 0 || c.list.Position.First != want {
		t.Fatalf("the list is at row %d, section 4 starts at %d", c.list.Position.First, want)
	}
}
