// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
)

func sharedContext(ops *op.Ops, size image.Point) layout.Context {
	gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	return gtx
}
func awaitShared(t *testing.T, p *chatInfo) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for p.loading && time.Now().Before(until) {
		p.drain()
		time.Sleep(time.Millisecond)
	}
	if p.loading || p.problem != nil {
		t.Fatalf("shared load: %v", p.problem)
	}
}
func TestSharedPageIgnoresLateResultsAndKeepsNextOffset(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	p.Open(model.Chat{ID: 2})
	awaitShared(t, p)
	old := make(chan sharedResult, 1)
	p.result = old
	p.loading = true
	p.selectSection(model.SharedPhotos)
	old <- sharedResult{page: model.SharedPage{Messages: []model.Message{{Text: "stale"}}}}
	awaitShared(t, p)
	if len(p.messages) == 0 || p.messages[0].Kind != model.MessagePhoto {
		t.Fatal("old section contaminated photos")
	}
	p.Close()
	if p.visible || p.result != nil || len(p.messages) != 0 {
		t.Fatal("close retained page")
	}
}
func TestChatHeaderClickableAndDialogBlocksOutside(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	page := newChatPage(store, func() {})
	defer page.Close()
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	var router input.Router
	clicked := false
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Source = router.Source()
		if page.header.Clicked(gtx) {
			clicked = true
		}
		layoutChatPage(gtx, model.Chat{ID: 2, Title: "Chat"}, localization.For("en"), func(layout.Context, int64, model.ChatKind, string, unit.Dp) layout.Dimensions {
			return layout.Dimensions{}
		}, nil, nil, func(layout.Context) layout.Dimensions { return layout.Dimensions{} }, page)
		p.Layout(gtx, localization.For("en"), false)
		router.Frame(ops)
	}
	frame()
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(200, 25)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(200, 25)})
	frame()
	frame()
	if !clicked {
		t.Fatal("header is not clickable")
	}
	clicked = false
	p.Open(model.Chat{ID: 2})
	awaitShared(t, p)
	frame()
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(30, 25)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(30, 25)})
	// The dialog animates out before it closes.
	for until := time.Now().Add(time.Second); p.visible && time.Now().Before(until); {
		frame()
		time.Sleep(10 * time.Millisecond)
	}
	if p.visible || clicked {
		t.Fatalf("scrim did not close exclusively: visible %v, closing %v, header clicked %v", p.visible, p.modal.closing, clicked)
	}
}
func TestSharedPhotoOpensBeforeThumbnailLoads(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatPage(store, func() {})
	defer p.Close()
	p.images = &images
	r := &messageRow{}
	r.media.Click()
	opened := false
	p.openPhoto = func(model.Message) { opened = true }
	m := store.History(2).Messages[34]
	ops := new(op.Ops)
	gtx := sharedContext(ops, image.Pt(150, 150))
	p.media.BeginFrame()
	p.mediaTile(gtx, r, m, image.Pt(150, 150), true, localization.For("en"), false)
	p.media.EndFrame()
	if !opened {
		t.Fatal("photo click only cancels thumbnail download")
	}
}
func TestRenderSharedMediaAndThemes(t *testing.T) {
	prefix := os.Getenv("SHARED_PNG")
	if prefix == "" {
		t.Skip("set SHARED_PNG to a path prefix")
	}
	t.Chdir("../../..")
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	info := newChatInfo(store, &images, func() {})
	defer info.Destroy()
	themes := newChatThemeController(store, &images, func() {})
	defer themes.Close()
	info.themes = themes
	info.drawAvatar = avatar
	history := newChatPage(store, func() {})
	defer history.Close()
	history.images = &images
	history.appearance = themes
	size := image.Pt(1000, 800)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	chat := model.Chat{ID: 2, Title: "Анна Смирнова", Kind: model.KindUser}
	themes.Update(2, false)
	choices, _ := store.ChatThemes(context.Background())
	themes.choose(choices[0])
	info.Open(chat)
	awaitShared(t, info)
	shoot := func(name string, ops *op.Ops) {
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rectangle{Max: size})
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		f, e := os.Create(prefix + "-" + name + ".png")
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		if e = png.Encode(f, im); e != nil {
			t.Fatal(e)
		}
	}
	render := func(name string) {
		ops := new(op.Ops)
		for i := 0; i < 25; i++ {
			ops.Reset()
			gtx := sharedContext(ops, size)
			images.BeginFrame()
			history.media.BeginFrame()
			history.Layout(gtx, chat, localization.For("ru"), true)
			history.media.EndFrame()
			info.Layout(gtx, localization.For("ru"), true)
			images.EndFrame()
			time.Sleep(20 * time.Millisecond)
		}
		shoot(name, ops)
	}
	counts := info.counts
	info.counts = nil
	info.loading = true
	render("loading")
	info.counts = counts
	info.loading = false
	render("info")
	info.openCollectible("anna")
	render("collectible")
	info.closeCollectible()
	info.list.Position.Offset = 60
	render("info-scrolling")
	info.list.Position.Offset = 400
	render("info-scrolled")
	info.list.Position = layout.Position{}

	// Motion blur, in frames a 60th of a second apart: the gifts a few
	// frames into their flight, and the avatar halfway to the header as the
	// info scrolls fast, a picture of its own.
	info.avatarSprite = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, px int) image.Image {
		photo := image.NewRGBA(image.Rect(0, 0, 96, 96))
		for y := range 96 {
			for x := range 96 {
				photo.SetRGBA(x, y, color.RGBA{uint8(40 + x*2), uint8(90 + y), 200, 255})
			}
		}
		return roundedSprite(photo, px, avatarRadius(gtx, px))
	}
	now := time.Now()
	motion := func(ops *op.Ops) {
		ops.Reset()
		gtx := sharedContext(ops, size)
		gtx.Now = now
		images.BeginFrame()
		info.Layout(gtx, localization.For("ru"), true)
		images.EndFrame()
		now = now.Add(time.Second / 60)
	}
	ops := new(op.Ops)
	info.Open(chat)
	for i := 0; i < 500; i++ {
		motion(ops)
		st := info.renderer.media.StatusFullRate(info.profile.Gifts[0], true, image.Pt(24, 24), false)
		if info.modal.Entered() && (st.Frame != nil || st.Preview != nil) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for range 3 {
		motion(ops)
	}
	shoot("info-gifts-flying", ops)
	for range 40 {
		motion(ops)
	}
	for i := range 4 {
		info.list.Position.Offset = i * 20
		motion(ops)
	}
	shoot("info-avatar-moving", ops)
	info.list.Position = layout.Position{}
	info.avatarSprite = nil
	info.selectSection(model.SharedPhotos)
	awaitShared(t, info)
	render("photos")
	// Two photos selected, the selection's buttons in the header; then the
	// menu of one of them.
	info.toggleSelected(info.messages[1])
	info.toggleSelected(info.messages[2])
	render("photos-selected")
	info.sharedMenu.open, info.sharedMenu.id, info.sharedMenu.at = true, info.messages[1].Key.MessageID, image.Pt(420, 260)
	info.sharedMenu.shown = info.sharedMenuActions(info.messages[1])
	render("photos-menu")
	info.sharedMenu.open = false
	info.renderer.clearSelection()
	info.sharedMenu.id = info.messages[4].Key.MessageID
	info.sharedMenu.shown = info.sharedMenuActions(info.messages[4])
	info.sharedMenu.open = true
	render("photos-menu-one")
	info.sharedMenu.open = false
	// The ways a drag selects, by the three dots in the header.
	info.setSelectionMode = func(string) error { return nil }
	info.setColumns = func(int) error { return nil }
	info.mode.open, info.mode.at = true, image.Pt(744, 80)
	render("photos-selection-mode")
	info.mode.open = false
	info.selectSection(model.SharedVideos)
	awaitShared(t, info)
	render("videos")
	info.selectSection(model.SharedGIFs)
	awaitShared(t, info)
	render("gifs")
	info.selectSection(model.SharedLinks)
	awaitShared(t, info)
	render("links")
	for _, kind := range []model.SharedKind{model.SharedFiles, model.SharedMusic, model.SharedVoice} {
		info.selectSection(kind)
		awaitShared(t, info)
		render(string(kind))
	}
	info.selectSection(model.SharedFiles)
	awaitShared(t, info)
	info.toggleSelected(info.messages[0])
	render("files-selected")
	info.renderer.clearSelection()
	info.selectSection(model.SharedGifts)
	awaitShared(t, info)
	info.messages[0].Gift.SenderHidden = true
	render("gifts")
	info.gift = newGiftDialog(info.messages[1])
	render("gift-details")
	info.gift = nil
	info.selectSection(model.SharedPolls)
	awaitShared(t, info)
	render("polls")
	info.goBack()
	info.themePage = true
	themes.choose(choices[0])
	themes.LoadChoices()
	render("themes")
	info.Close()
	themes.choose(choices[0])
	render("chat")
	info.Open(model.Chat{ID: 4, Title: "Новости Go", Kind: model.KindChannel, Members: 48210, Badges: model.Badges{Verified: true}})
	awaitShared(t, info)
	render("channel")
}

func TestInfoThumbDragStableAcrossUnequalContent(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	p.list.Axis = layout.Vertical
	p.kinds = append(append([]model.SharedKind{}, model.SharedKinds...), model.SharedGifts, model.SharedStories, model.SharedGroups)
	p.counts = map[model.SharedKind]int{}
	for _, k := range p.kinds {
		p.counts[k] = 100
	}
	var router input.Router
	now := time.Now()
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(640, 360))
		gtx.Source = router.Source()
		gtx.Now = now
		p.layoutInfo(gtx, localization.For("en"), false)
		router.Frame(ops)
		now = now.Add(16 * time.Millisecond)
	}
	frame()
	length := p.list.Position.Length
	send := func(kind pointer.Kind, y float32) {
		buttons := pointer.ButtonPrimary
		if kind == pointer.Move {
			buttons = 0
		}
		if kind == pointer.Drag {
			kind = pointer.Move
		}
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: f32.Pt(637, y)})
		frame()
	}
	send(pointer.Move, 20)
	frame()
	send(pointer.Press, 20)
	free := float32(356) - float32(356*360)/float32(length)
	for _, dy := range []float32{20, 45, 75, 100, 60, 25} {
		send(pointer.Drag, 20+dy)
		frame()
		want := int(dy / free * float32(length-360))
		got := p.list.Position.Offset
		if p.list.Position.First != 0 || got < want-5 || got > want+5 {
			t.Fatalf("thumb jump at %v: position %+v, want offset %d", dy, p.list.Position, want)
		}
	}
	send(pointer.Release, 45)
	offset := p.list.Position.Offset
	for i := 0; i < 8; i++ {
		frame()
	}
	if p.list.Position.Offset != offset {
		t.Fatal("jump after release")
	}
}
func TestEmptySharedSectionsHidden(t *testing.T) {
	p := chatInfo{kinds: []model.SharedKind{model.SharedPhotos, model.SharedVideos, model.SharedGifts}, counts: map[model.SharedKind]int{model.SharedPhotos: 3, model.SharedVideos: 0}}
	kinds := p.visibleKinds()
	if len(kinds) != 2 || kinds[0] != model.SharedPhotos || kinds[1] != model.SharedGifts {
		t.Fatal(kinds)
	}
	p.counts[model.SharedGifts] = 0
	if len(p.visibleKinds()) != 1 {
		t.Fatal("empty collection remains")
	}
}
func TestSharedLinksPlainUTF16AndNoMarkup(t *testing.T) {
	m := model.Message{Text: "😀 First bold link secret", Entities: []model.Entity{{Kind: "url", Offset: 3, Length: 5, URL: "https://example.org/a"}, {Kind: "bold", Offset: 9, Length: 4}, {Kind: "spoiler", Offset: 19, Length: 6}}}
	links := sharedURLs(m)
	if len(links) != 1 || links[0] != "https://example.org/a" {
		t.Fatal(links)
	}
	if snippet := sharedSnippet(m); snippet != "😀 First bold link •••" {
		t.Fatal(snippet)
	}
	m = model.Message{Text: "😀 https://telegram.org", Entities: []model.Entity{{Kind: "url", Offset: 3, Length: 20}}}
	if links := sharedURLs(m); len(links) != 1 || links[0] != "https://telegram.org" {
		t.Fatal(links)
	}
}

func TestSharedReleaseDropsGiftPixelsWithoutLosingNavigation(t *testing.T) {
	source := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(source, &images, func() {})
	defer p.Destroy()
	p.section = model.SharedGifts
	p.visible = true
	p.list.Position.Offset = 24
	im := image.NewRGBA(image.Rect(0, 0, 64, 64))
	row := &giftRow{background: im, patternSource: im, patternTint: im}
	p.giftRows[model.MessageKey{MessageID: 1}] = row
	p.Release()
	if row.background != nil || row.patternSource != nil || row.patternTint != nil {
		t.Fatal("derived gift pixels retained in tray")
	}
	if !p.visible || p.section != model.SharedGifts || p.list.Position.Offset != 24 {
		t.Fatal("release changed navigation")
	}
}

// usernameStore is the demo store whose chat 2 is public.
type usernameStore struct{ *mockstore.Store }

func (usernameStore) Username(chat int64) string {
	if chat == 2 {
		return "public_chat"
	}
	return ""
}

// The info of a public chat copies its @username and its link, as
// AyuGram's; a chat without one shows neither.
func TestChatInfoCopiesUsername(t *testing.T) {
	store := usernameStore{mockstore.New(time.Now(), 0)}
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	var router input.Router
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Source = router.Source()
		p.Layout(gtx, localization.For("en"), false)
		router.Frame(ops)
	}
	p.Open(model.Chat{ID: 3})
	awaitShared(t, p)
	if p.chatUsername() != "" {
		t.Fatal("a private chat has a username")
	}
	p.Open(model.Chat{ID: 2})
	awaitShared(t, p)
	frame()
	for _, c := range []struct {
		item *settingsItem
		want string
	}{{&p.username, "@public_chat"}, {&p.usernameLink, "https://t.me/public_chat"}} {
		c.item.click.Click()
		frame()
		if _, text, ok := router.WriteClipboard(); !ok || string(text) != c.want {
			t.Fatalf("copied %q, want %q", text, c.want)
		}
		if p.modal.toast.Text() == "" {
			t.Fatal("no notice of the copy")
		}
	}
}

// A user's info estimates the registration from the id, and tells where
// the photo is kept, as materialgram's; a click copies the row.
func TestChatInfoDetails(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	l := localization.For("en")
	p.Open(model.Chat{ID: 2, Kind: model.KindUser})
	awaitShared(t, p)
	registration, dataCenter := p.detailTexts(l)
	if registration != "before 09.2013" || dataCenter != "DC 3, Miami" {
		t.Fatalf("details %q, %q", registration, dataCenter)
	}
	p.Open(model.Chat{ID: 3, Kind: model.KindGroup})
	awaitShared(t, p)
	if registration, dataCenter := p.detailTexts(l); registration != "" || dataCenter != "DC 4, Amsterdam" {
		t.Fatalf("group's details %q, %q", registration, dataCenter)
	}
	var router input.Router
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Source = router.Source()
		p.Layout(gtx, l, false)
		router.Frame(ops)
	}
	frame()
	p.dataCenter.click.Click()
	frame()
	if _, text, ok := router.WriteClipboard(); !ok || string(text) != "DC 4, Amsterdam" {
		t.Fatalf("copied %q", text)
	}
}

// TestOwnUsernameFollowsCollectibles shows a channel that put its
// collectible username first: its own follows the chips, which the card
// above does not show then, and the username of the card is not repeated.
func TestOwnUsernameFollowsCollectibles(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	p.chat = model.Chat{ID: 4, Kind: model.KindChannel}
	p.profile.Usernames = []model.Username{{Name: "gonews", Collectible: true}, {Name: "golang_news"}}
	if p.chatUsername() != "gonews" {
		t.Fatal("the card does not show the collectible username put first")
	}
	gtx := sharedContext(new(op.Ops), image.Pt(600, 200))
	p.layoutCollectibles(gtx)
	if p.chips["gonews"] == nil || p.plainNames["golang_news"] == nil {
		t.Fatal("the own username is not shown after the collectible one")
	}
	p.plainNames = map[string]*widget.Clickable{}
	p.chat.ID = 2
	p.profile.Usernames = []model.Username{{Name: "anna_smirnova"}, {Name: "anna", Collectible: true}}
	p.layoutCollectibles(gtx)
	if p.plainNames["anna_smirnova"] != nil {
		t.Fatal("the username of the card is shown again")
	}
}

// A video's plate tells its time as a GIF's and a video message's do, and
// that it is a video, unless all around are, as the info's section of them.
func TestVideoPlateText(t *testing.T) {
	l := localization.For("en")
	m := model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{Duration: 96 * time.Second}}
	for _, c := range []struct {
		gif, only bool
		want      string
	}{{false, false, "1:36 · Video"}, {false, true, "1:36"}, {true, false, "GIF"}} {
		if got := videoPlateText(m, c.gif, c.only, l); got != c.want {
			t.Errorf("gif %v, time only %v: %q, want %q", c.gif, c.only, got, c.want)
		}
	}
}

// The info's section of videos tells each one's time alone; the others
// say what it is too.
func TestSharedVideosTellTimeOnly(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	info := newChatInfo(store, &images, func() {})
	defer info.Destroy()
	info.drawAvatar = avatar
	info.Open(model.Chat{ID: 2, Title: "Анна Смирнова", Kind: model.KindUser})
	awaitShared(t, info)
	for _, c := range []struct {
		kind model.SharedKind
		want bool
	}{{model.SharedVideos, true}, {model.SharedPhotos, false}} {
		info.selectSection(c.kind)
		awaitShared(t, info)
		ops := new(op.Ops)
		for range 3 {
			ops.Reset()
			images.BeginFrame()
			info.Layout(sharedContext(ops, image.Pt(1000, 800)), localization.For("en"), false)
			images.EndFrame()
		}
		if info.renderer.videoTimeOnly != c.want {
			t.Errorf("%v: time only %v", c.kind, info.renderer.videoTimeOnly)
		}
	}
}
