// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/op"
)

// The info of a user shows the profile: a chip for each collectible
// username, which tells how it was bought and copies its link; the
// user's channel, which opens; and the gifts of the cover, which open.
func TestChatInfoProfile(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	var opened []model.Chat
	p.openChannel = func(c model.Chat) { opened = append(opened, c) }
	var router input.Router
	l := localization.For("en")
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Source = router.Source()
		p.Layout(gtx, l, false)
		router.Frame(ops)
	}
	open := func() {
		t.Helper()
		p.Open(model.Chat{ID: 2, Kind: model.KindUser, Title: "Анна Смирнова"})
		awaitShared(t, p)
		until := time.Now().Add(3 * time.Second)
		for p.profile.Channel == nil && time.Now().Before(until) {
			p.drainProfile()
			time.Sleep(time.Millisecond)
		}
		frame()
	}
	open()
	if p.chips["anna"] == nil || p.chips["smirnova"] == nil || p.chips["anna_smirnova"] != nil {
		t.Fatalf("chips of %v", p.chips)
	}
	p.chips["anna"].click.Click()
	frame()
	d := p.collectible
	if d == nil || d.name != "anna" {
		t.Fatalf("collectible %+v", d)
	}
	until := time.Now().Add(3 * time.Second)
	for d.info == nil && time.Now().Before(until) {
		frame()
		time.Sleep(time.Millisecond)
	}
	if d.info == nil || d.info.URL != "https://fragment.com/username/anna" {
		t.Fatalf("info %+v, %v", d.info, d.err)
	}
	d.copy.click.Click()
	frame()
	if _, text, ok := router.WriteClipboard(); !ok || string(text) != "https://t.me/anna" {
		t.Fatalf("copied %q", text)
	}
	p.closeCollectible()

	p.channel.click.Click()
	frame()
	if len(opened) != 1 || opened[0].ID != 13 {
		t.Fatalf("opened %+v", opened)
	}

	open()
	p.cover.gifts[0].Click()
	frame()
	if p.gift == nil || p.gift.message.Gift == nil || p.gift.message.Gift.Number != 1001 {
		t.Fatalf("gift %+v", p.gift)
	}
}

// A collectible username's price is in TON, then in dollars, in the
// language's numbers.
func TestCollectiblePrice(t *testing.T) {
	info := model.CollectibleUsername{Currency: "USD", Amount: 1368, CryptoCurrency: "TON", CryptoAmount: 10_000_000_000}
	if got := collectiblePrice(info, localization.For("ru")); got != "10,00 TON ($13,68)" {
		t.Fatalf("ru %q", got)
	}
	info.CryptoAmount, info.Amount = 1_234_500_000, 150
	if got := collectiblePrice(info, localization.For("en")); got != "1.2345 TON ($1.50)" {
		t.Fatalf("en %q", got)
	}
}

// A pattern close to its backdrop's color is darkened, as Telegram
// Desktop's, to show; one that stands out keeps its color.
func TestPatternTone(t *testing.T) {
	if got := patternTone(0x3a7a4a, 0x2f6e43, false); got == 0x3a7a4a || contrast(got, 0x2f6e43) < 2 {
		t.Fatalf("close pattern %06x", got)
	}
	if got := patternTone(0xffffff, 0x101010, true); got != 0xffffff {
		t.Fatalf("light pattern %06x", got)
	}
}

// A text of the catalog is bold between "**", as Telegram Desktop's.
func TestMarkedRuns(t *testing.T) {
	runs := markedRuns("bought on **Fragment** for **10 TON**")
	want := []model.TextRun{{Text: "bought on "}, {Text: "Fragment", Bold: true}, {Text: " for "}, {Text: "10 TON", Bold: true}}
	if len(runs) != len(want) {
		t.Fatalf("runs %+v", runs)
	}
	for i := range want {
		if runs[i].Text != want[i].Text || runs[i].Bold != want[i].Bold {
			t.Fatalf("run %d %+v", i, runs[i])
		}
	}
}

// The line of a user's channel: its subscribers, what its post carries,
// and the post's text whole, its custom emoji as emoji runs.
func TestChannelLine(t *testing.T) {
	ch := model.PersonalChannel{Chat: model.Chat{Title: "Fund", Kind: model.KindChannel, Members: 6},
		Post: model.Preview{Text: "x\n💸", Media: "preview.photo"}, Entities: []model.Entity{{Kind: "emoji", Offset: 2, Length: 2, DocumentID: 8}}}
	var text string
	emoji := int64(0)
	for _, r := range channelLine(ch, localization.For("en")) {
		text += r.Text
		if r.Emoji != 0 {
			emoji = r.Emoji
		}
	}
	if text != "6 subscribers • Photo, x\n💸" || emoji != 8 {
		t.Fatalf("line %q, emoji %d", text, emoji)
	}
}

// One line of rich text is cut to its width, with an ellipsis; a short
// one keeps its own width.
func TestRichLabelOneLine(t *testing.T) {
	layoutWidth := func(s string) image.Point {
		gtx := sharedContext(new(op.Ops), image.Pt(120, 100))
		gtx.Constraints.Min = image.Point{}
		r := richLabel{runs: []model.TextRun{{Text: s}, {Text: " bold", Bold: true}}, style: token.TypestyleBodySmall, color: scheme(gtx).Surface.OnColor, maxLines: 1}
		return r.Layout(gtx).Size
	}
	long, short := layoutWidth("a rather long line of text that does not fit\nat all"), layoutWidth("hi")
	if long.X != 120 || short.X >= 120 || long.Y != short.Y {
		t.Fatalf("long %v, short %v", long, short)
	}
}

// slowProfiles is the demo whose profiles come from Telegram once release
// is closed, and are known before only when cached is set.
type slowProfiles struct {
	*mockstore.Store
	release chan struct{}
	cached  bool
}

func (s slowProfiles) CachedPeerProfile(chat int64) (model.PeerProfile, bool) {
	if !s.cached {
		return model.PeerProfile{}, false
	}
	return s.Store.CachedPeerProfile(chat)
}

func (s slowProfiles) PeerProfile(ctx context.Context, chat int64) (model.PeerProfile, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
		return model.PeerProfile{}, ctx.Err()
	}
	return s.Store.PeerProfile(ctx, chat)
}

// A cover known before shows at once; one that comes from Telegram fades
// in, not flashing over the info.
func TestCoverFadesInOnlyWhenNotCached(t *testing.T) {
	now := time.Now()
	frame := func(p *chatInfo) {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Now = now
		p.Layout(gtx, localization.For("en"), true)
	}
	background := func(p *chatInfo) float32 {
		gtx := sharedContext(new(op.Ops), image.Pt(900, 800))
		gtx.Now = now
		return p.fades.background.Animate(gtx, 1)
	}
	var images imageOps
	cached := newChatInfo(slowProfiles{mockstore.New(time.Now(), 0), make(chan struct{}), true}, &images, func() {})
	defer cached.Destroy()
	cached.Open(model.Chat{ID: 2, Kind: model.KindUser})
	frame(cached)
	if got := background(cached); got != 1 {
		t.Fatalf("a cached cover is at %v", got)
	}

	release := make(chan struct{})
	p := newChatInfo(slowProfiles{mockstore.New(time.Now(), 0), release, false}, &images, func() {})
	defer p.Destroy()
	p.Open(model.Chat{ID: 2, Kind: model.KindUser})
	frame(p)
	if got := background(p); got != 0 {
		t.Fatalf("no cover is at %v", got)
	}
	close(release)
	until := time.Now().Add(3 * time.Second)
	for !p.profile.Cover.Gift && time.Now().Before(until) {
		p.drainProfile()
		time.Sleep(time.Millisecond)
	}
	frame(p)
	now = now.Add(100 * time.Millisecond)
	frame(p)
	if got := background(p); got <= 0 || got >= 1 {
		t.Fatalf("the cover is at %v while it fades in", got)
	}
	now = now.Add(time.Second)
	frame(p)
	if got := background(p); got != 1 {
		t.Fatalf("the cover is at %v once faded in", got)
	}
}

// Gifts fly out from under the avatar, as Telegram Desktop's: each sets
// off as late as its place has it, and they are all out at the end.
func TestGiftFlight(t *testing.T) {
	center, at := image.Pt(100, 100), image.Pt(200, 40)
	if got := giftFlight(1, at, center, 0); got != center {
		t.Fatalf("at the start %v", got)
	}
	// The top left gift sets off last; the left one first.
	if got := giftFlight(1, at, center, .3); got != center {
		t.Fatalf("a late gift left at %v", got)
	}
	if got := giftFlight(0, at, center, .3); got == center {
		t.Fatal("an early gift has not left")
	}
	for place := range model.MaxCoverGifts {
		if got := giftFlight(place, at, center, 1); got != at {
			t.Fatalf("gift %d ends at %v", place, got)
		}
	}
}

// As the info scrolls, its avatar moves into the header, where a click
// still opens the photos; loading is told in the header, moving no card.
func TestInfoHeaderAvatar(t *testing.T) {
	var images imageOps
	p := newChatInfo(mockstore.New(time.Now(), 0), &images, func() {})
	defer p.Destroy()
	p.drawAvatar = avatar
	opened := 0
	p.openAvatar = func(int64) { opened++ }
	var router input.Router
	size := image.Pt(700, 700)
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, size)
		gtx.Source = router.Source()
		p.Layout(gtx, localization.For("en"), false)
		router.Frame(ops)
	}
	click := func(at image.Point) {
		pos := f32.Pt(float32(at.X), float32(at.Y))
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos})
		frame()
	}
	p.Open(model.Chat{ID: 2, Kind: model.KindUser, Title: "Анна Смирнова"})
	awaitShared(t, p)
	frame()
	// The dialog is centered in the window.
	dialog := image.Pt((size.X-640)/2, (size.Y-min(size.Y, 740))/2)
	click(dialog.Add(p.cover.avatar.Min.Add(p.cover.avatar.Size().Div(2))))
	if opened != 1 {
		t.Fatalf("the cover's avatar opened %d times", opened)
	}
	// In the header's corner once the info has scrolled.
	p.list.Position.Offset = 1000
	frame()
	header := image.Pt(16+18, 36)
	click(dialog.Add(header))
	if opened != 2 {
		t.Fatalf("the header's avatar opened %d times", opened)
	}

	p.list.Position.Offset = 0
	gtx := sharedContext(new(op.Ops), image.Pt(640, 4000))
	gtx.Constraints.Min = image.Point{}
	p.loading = false
	idle := p.layoutCards(gtx, localization.For("en"), false).Size
	p.loading = true
	loading := p.layoutCards(gtx, localization.For("en"), false).Size
	if loading != idle {
		t.Fatalf("cards %v while loading, %v after", loading, idle)
	}
}

// The gifts fly out once the dialog has grown, not while it does.
func TestGiftsWaitForTheDialog(t *testing.T) {
	// The demo's gifts are its sticker, in assets.
	t.Chdir("../../..")
	var images imageOps
	p := newChatInfo(mockstore.New(time.Now(), 0), &images, func() {})
	defer p.Destroy()
	now := time.Now()
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Now = now
		images.BeginFrame()
		p.renderer.media.BeginFrame()
		p.Layout(gtx, localization.For("en"), false)
		p.renderer.media.EndFrame()
		images.EndFrame()
	}
	p.Open(model.Chat{ID: 2, Kind: model.KindUser})
	frame()
	// The gifts' pictures load while the dialog opens.
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		st := p.renderer.media.StatusFullRate(p.profile.Gifts[0], false, image.Pt(24, 24), false)
		if st.Frame != nil || st.Preview != nil {
			break
		}
		if st.Err != nil {
			t.Fatal(st.Err)
		}
		frame()
		time.Sleep(5 * time.Millisecond)
	}
	// A gift that set off at 100 ms would be on its way at 150.
	now = now.Add(100 * time.Millisecond)
	frame()
	now = now.Add(50 * time.Millisecond)
	frame()
	if p.modal.Entered() {
		t.Fatal("the dialog has grown in 150 ms")
	}
	gtx := sharedContext(new(op.Ops), image.Pt(900, 800))
	gtx.Now = now
	if got := p.fades.gifts[0].Animate(gtx, 1); got != 0 {
		t.Fatalf("a gift flew %v while the dialog grew", got)
	}
	now = now.Add(time.Second)
	frame()
	now = now.Add(100 * time.Millisecond)
	frame()
	gtx.Now = now
	if !p.modal.Entered() {
		t.Fatal("the dialog has not grown")
	}
	if got := p.fades.gifts[0].Animate(gtx, 1); got <= 0 {
		t.Fatalf("a gift is at %v after the dialog grew", got)
	}
}

// The gifts go to places of chance each time the info opens, as Telegram
// Desktop's, every place once.
func TestShuffledPlaces(t *testing.T) {
	first := shuffledPlaces()
	other := false
	for range 50 {
		places := shuffledPlaces()
		seen := map[int]bool{}
		for _, place := range places {
			seen[place] = true
		}
		if len(seen) != model.MaxCoverGifts {
			t.Fatalf("places %v", places)
		}
		other = other || places != first
	}
	if !other {
		t.Fatal("the same places every time")
	}
	var images imageOps
	p := newChatInfo(mockstore.New(time.Now(), 0), &images, func() {})
	defer p.Destroy()
	opened := map[[model.MaxCoverGifts]int]bool{}
	for range 20 {
		p.Open(model.Chat{ID: 2, Kind: model.KindUser})
		opened[p.fades.places] = true
	}
	if len(opened) < 2 {
		t.Fatal("the info opens with the same places each time")
	}
}

// TestCoverGiftsSetOffTogether: the gifts of the cover wait for the pictures
// of all of them, as Telegram Desktop's do, but not for one that failed.
func TestCoverGiftsSetOffTogether(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	loaded := chatmedia.Status{Preview: picture}
	if coverGiftsLoaded(nil) {
		t.Error("no gifts are loaded")
	}
	if !coverGiftsLoaded([]chatmedia.Status{loaded, loaded}) {
		t.Error("gifts with pictures did not set off")
	}
	if coverGiftsLoaded([]chatmedia.Status{loaded, {Loading: true}}) {
		t.Error("a gift set off while another was loading")
	}
	if !coverGiftsLoaded([]chatmedia.Status{loaded, {Err: errors.New("gone")}}) {
		t.Error("a failed gift held the others back")
	}
}
