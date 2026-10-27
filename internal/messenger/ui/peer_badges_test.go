// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/peerbadges"
	"komarugram/internal/messenger/preferences"
)

// The marks of the forks of Telegram clients are drawn for who the lists name,
// users by their ids and channels by theirs, and not where the settings hide
// them or no lists were given.
func TestPeerMarksFollowTheLists(t *testing.T) {
	registry := peerbadges.New("")
	registry.Apply("ayu_rc.json", []byte(`{"developers":[1],"officialChannels":[20]}`))
	prefs := preferences.Memory()
	a := &App{preferences: prefs, peerBadges: registry}
	has := func(b model.Badges) bool {
		_, after := a.badges(b, "Name", 16, false)
		return after != nil
	}
	if !has(model.Badges{User: 1}) || !has(model.Badges{Channel: 20}) {
		t.Fatal("a listed peer has no mark")
	}
	if has(model.Badges{User: 3}) || has(model.Badges{}) {
		t.Fatal("a peer the lists do not name has a mark")
	}
	if err := prefs.SetHidePeerBadges(true); err != nil {
		t.Fatal(err)
	}
	if has(model.Badges{User: 1}) {
		t.Fatal("a mark is drawn though the settings hide them")
	}
	// The arrows and the faces are hidden apart.
	registry.Apply("komaru_badges.txt", []byte("1,#ff0000\n"))
	count := func() int { return len(a.peerMarks(model.Badges{User: 1}, "Name", 16, nil)) }
	if err := prefs.SetKeepPeerBadges(true, false); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("with the arrows kept: %d marks", count())
	}
	if err := prefs.SetKeepPeerBadges(true, true); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatalf("with both kept: %d marks", count())
	}
	if err := prefs.SetKeepPeerBadges(false, true); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("with the faces kept: %d marks", count())
	}
	if err := prefs.SetKeepPeerBadges(false, false); err != nil {
		t.Fatal(err)
	}
	if _, after := (&App{preferences: preferences.Memory()}).badges(model.Badges{User: 1}, "", 16, false); after != nil {
		t.Fatal("a window without lists draws a mark")
	}
}

func TestBadgeTextsAreWorded(t *testing.T) {
	for _, code := range []string{"en", "ru"} {
		l := localization.For(code)
		for _, key := range []string{
			"badge.extera_developer", "badge.extera_official", "badge.extera_supporter",
			"badge.komaru_developer", "badge.komaru_official", "badge.komaru_supporter",
		} {
			if got := l.Format(key, map[string]string{"name": "Ada"}); got == key || !strings.Contains(got, "Ada") {
				t.Errorf("%s %s: %q", code, key, got)
			}
		}
	}
}

// setStore is a store that has the emoji set of the Premium star.
type setStore struct {
	*mockstore.Store
	set   model.StickerSet
	calls int
}

func (s *setStore) StickerSet(_ context.Context, ref model.StickerSetRef) (model.StickerSet, error) {
	s.calls++
	if ref.ShortName == komaruIconSet {
		return model.StickerSet{Items: []model.PickerItem{{DocumentID: 20, Emoji: "🐱"}, {DocumentID: 21, Emoji: "🐱"}, {DocumentID: 22, Emoji: "🐱"}}}, nil
	}
	if ref.ShortName == exteraIconSet {
		return model.StickerSet{Items: []model.PickerItem{{DocumentID: 30, Emoji: "↗"}, {DocumentID: 31, Emoji: "↗"}}}, nil
	}
	if ref.ShortName != premiumIconSet {
		panic("another set asked for: " + ref.ShortName)
	}
	return s.set, nil
}

func (s *setStore) SetStickerSetInstalled(context.Context, model.StickerSetRef, bool) error {
	return nil
}

// The Premium star is the first star of the set Telegram's Android icons are
// in, and the faces of KomaruGram in another, loaded in the background; until then there is none to take.
func TestPremiumStarComesFromTheSet(t *testing.T) {
	store := &setStore{Store: mockstore.New(time.Now(), 0), set: model.StickerSet{Items: []model.PickerItem{
		{DocumentID: 10, Emoji: "✈"},
		{DocumentID: 11, Emoji: "⭐️"},
		{DocumentID: 12, Emoji: "⭐"},
	}}}
	a := &App{store: store, preferences: preferences.Memory()}
	if id := a.premiumEmoji(); id != 0 {
		t.Fatalf("the star is %d before the set is loaded", id)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.premiumSet.items.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if id := a.premiumEmoji(); id != 11 {
		t.Fatalf("the star is %d, want the first one of the set, 11", id)
	}
	if store.calls != 1 {
		t.Fatalf("the set was asked for %d times", store.calls)
	}
	// The Komaru faces come from their own set: the third is the developer's,
	// its rosette turning, the second the supporter's.
	if id := a.komaruEmoji(true); id != 0 {
		t.Fatalf("a face %d before the set is loaded", id)
	}
	for a.komaruSet.items.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if dev, sup := a.komaruEmoji(true), a.komaruEmoji(false); dev != 22 || sup != 21 {
		t.Fatalf("faces %d and %d, want 22 and 21", dev, sup)
	}
	// A set without the turning rosette has the developer's face still.
	two := []model.PickerItem{{DocumentID: 40}, {DocumentID: 41}}
	a.komaruSet.items.Store(&two)
	if dev := a.komaruEmoji(true); dev != 40 {
		t.Fatalf("the developer's face is %d in a set of two, want 40", dev)
	}
	// The arrow too: the first is the supporter's, the second the official one's.
	// Until it is loaded nothing is drawn in its place, nor in the star's.
	if dims := a.layoutExteraMark(layout.Context{}, 16, true, 0); dims.Size != (image.Point{}) {
		t.Fatalf("an arrow of %v before the set is loaded", dims.Size)
	}
	for a.arrowSet.items.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sup, off := a.arrowEmoji(false), a.arrowEmoji(true); sup != 30 || off != 31 {
		t.Fatalf("arrows %d and %d, want 30 and 31", sup, off)
	}
	// A store that cannot have sets draws nothing.
	bare := &App{store: mockstore.New(time.Now(), 0)}
	if id := bare.premiumEmoji(); id != 0 {
		t.Fatalf("a store without sets gave %d", id)
	}
	if dims := bare.layoutPremiumMark(layout.Context{}, 16); dims.Size != (image.Point{}) {
		t.Fatalf("a star of %v from a store without sets", dims.Size)
	}
}

// A click on a mark tells what it means where the badges were asked to: in
// a dialog's toast, which the chat list's would lie under, or the chat
// list's. The Markdown of a text from the lists is left out.
func TestBadgeClickTellsWhereAsked(t *testing.T) {
	registry := peerbadges.New("")
	registry.Apply("komaru_badges.txt", []byte("1,#ff0000\n"))
	a := &App{preferences: preferences.Memory(), peerBadges: registry, chats: newChatList()}
	faces := []model.PickerItem{{DocumentID: 20}, {DocumentID: 21}}
	a.komaruSet.items.Store(&faces)
	click := func(badges badgesLayout, b model.Badges) {
		t.Helper()
		var r input.Router
		frame := func() {
			ops := new(op.Ops)
			gtx := layout.Context{Ops: ops, Source: r.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(100, 16))}
			_, after := badges(b, "Ada", 16, true)
			after(gtx)
			r.Frame(ops)
		}
		frame()
		pos := f32.Pt(8, 8)
		r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos})
		frame()
	}
	var told []string
	dialog := a.badgesTelling(func(text string) { told = append(told, text) })
	click(dialog, model.Badges{User: 1})
	// A status's text from the list, on a status that has loaded and takes
	// room.
	status := a.badgeButton(badgeKey{2, badgeKomaruStatus}, func(text string) { told = append(told, text) }, "Ada", "badge.komaru_supporter", "**Komaru** cat", func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(16, 16)}
	})
	click(func(model.Badges, string, unit.Dp, bool) (layout.Widget, layout.Widget) { return nil, status }, model.Badges{})
	if len(told) != 2 || !strings.Contains(told[0], "Ada") || told[1] != "Komaru cat" {
		t.Fatalf("the dialog was told %q", told)
	}
	if text := a.chats.toast.Text(); text != "" {
		t.Fatalf("the chat list was told %q too", text)
	}
	click(a.badges, model.Badges{User: 1})
	if text := a.chats.toast.Text(); !strings.Contains(text, "Ada") || len(told) != 2 {
		t.Fatalf("the chat list was told %q, the dialog %q", text, told)
	}
}
