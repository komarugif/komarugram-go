// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// searchHarness is the chat list searching, on the demo store.
type searchHarness struct {
	t      *testing.T
	router input.Router
	store  *mockstore.Store
	list   *chatList
	now    time.Time
	picks  []chatPick
}

func newSearchHarness(t *testing.T) *searchHarness {
	skipFrames(t)
	store := mockstore.New(time.Now(), 0)
	l := newChatList()
	l.panel = newSearchPanel(store)
	return &searchHarness{t: t, store: store, list: l, now: time.Unix(1000, 0)}
}

func (h *searchHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(360, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	l := localization.For("ru")
	sec := section{kind: sectionSearch}
	if pick, ok := h.list.Update(gtx, sec, l); ok {
		h.picks = append(h.picks, pick)
	}
	h.list.Layout(gtx, sec, nil, h.store.Chats(), 0, false, l)
	h.list.panel.recent.layoutConfirm(gtx, l)
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(50 * time.Millisecond)
}

func (h *searchHarness) frames(n int) {
	for range n {
		h.frame()
	}
}

func (h *searchHarness) press(button pointer.Buttons, pos f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: button, Position: pos, Time: time.Duration(h.now.UnixNano())})
		h.frame()
	}
}

// resultsTop is where the results start, under the search bar, the tabs
// and the capsules.
func (h *searchHarness) resultsTop() float32 {
	return float32(int(searchHeader) + int(searchTabsHeight) + int(searchChipsHeight))
}

// The avatar's corner in search opens the chat, including when it belongs
// to a community; search has no shortcut to that community.
func TestSearchCommunityAvatar(t *testing.T) {
	h := newSearchHarness(t)
	var chat model.Chat
	for _, c := range h.store.Chats() {
		if c.Community != 0 && c.Kind != model.KindCommunity {
			chat = c
			break
		}
	}
	if chat.ID == 0 {
		t.Fatal("demo has no chat belonging to a community")
	}
	h.list.search.SetText(chat.Title)
	h.frames(10)
	h.press(pointer.ButtonPrimary, f32.Pt(59, h.resultsTop()+51))
	if len(h.picks) != 1 || h.picks[0].ID != chat.ID || h.picks[0].Community {
		t.Fatalf("a click on the avatar's corner picked %+v, want chat %d", h.picks, chat.ID)
	}
}

func TestSearchHistory(t *testing.T) {
	h := newSearchHarness(t)
	h.frames(2)
	if len(h.list.panel.recent.shown) != 0 {
		t.Fatal("a history before any search")
	}
	// A chat picked from what was found is remembered.
	for _, query := range []string{"Анна", "Мама"} {
		h.list.search.SetText(query)
		h.frames(10)
		h.press(pointer.ButtonPrimary, f32.Pt(150, h.resultsTop()+float32(chatRowHeight)/2))
		if n := len(h.picks); n == 0 || h.picks[n-1].Chat == nil || h.picks[n-1].Chat.Title == "" {
			t.Fatalf("searching %q picked %+v", query, h.picks)
		}
	}
	recent := h.store.RecentChats()
	if len(recent) != 2 || recent[0].Title != "Мама" || recent[1].Title != "Анна Смирнова" {
		t.Fatalf("history %+v", recent)
	}
	// Nothing typed: the history shows, under its heading.
	h.list.search.SetText("")
	h.frames(10)
	items := h.list.items
	if len(items) != 3 || !items[0].recentHeading || !items[1].recent || items[1].chat.Title != "Мама" {
		t.Fatalf("items %+v", items)
	}
	// A right click on a chat of it offers to remove it.
	r := &h.list.panel.recent
	y := h.resultsTop() + 20
	for ; y < h.resultsTop()+200 && !r.open; y += 8 {
		h.press(pointer.ButtonSecondary, f32.Pt(150, y))
	}
	if !r.open || r.id != recent[0].ID {
		t.Fatalf("menu open %t on %d", r.open, r.id)
	}
	// It opens where the click was.
	if at := r.at.Y + int(h.resultsTop()); at < int(y)-16 || at > int(y) {
		t.Errorf("the menu opened at %d, the click was at %d", at, int(y)-8)
	}
	h.frames(10)
	// Escape closes it, though the search field had the keyboard.
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(2)
	if r.open {
		t.Fatal("Escape left the menu open")
	}
	for y := h.resultsTop() + 20; y < h.resultsTop()+200 && !r.open; y += 8 {
		h.press(pointer.ButtonSecondary, f32.Pt(150, y))
	}
	h.frames(10)
	item := func(i int) f32.Point {
		return f32.Pt(float32(r.rect.Min.X+40), h.resultsTop()+float32(r.rect.Min.Y+menuPadding+i*menuItemHeight+menuItemHeight/2))
	}
	h.press(pointer.ButtonPrimary, item(0))
	if got := h.store.RecentChats(); len(got) != 1 || got[0].Title != "Анна Смирнова" {
		t.Fatalf("after removing: %+v", got)
	}
	// Clearing asks first.
	h.frames(10)
	for y := h.resultsTop(); y < h.resultsTop()+40 && !r.confirm.Shown(); y += 4 {
		h.press(pointer.ButtonPrimary, f32.Pt(330, y))
	}
	if !r.confirm.Shown() || len(h.store.RecentChats()) != 1 {
		t.Fatal("the history was cleared without asking")
	}
	h.frames(20)
	yes := h.findButton(func() bool { return len(h.store.RecentChats()) == 0 })
	if !yes {
		t.Fatal("the confirmation did not clear the history")
	}
	h.frames(10)
	if len(h.list.items) != 0 && h.list.items[0].recentHeading {
		t.Error("the heading stays over an empty history")
	}
}

// findButton clicks over the middle of the window, where the confirmation
// is, until done.
func (h *searchHarness) findButton(done func() bool) bool {
	confirm := &h.list.panel.recent.confirm
	for y := float32(300); y < 420 && !done(); y += 6 {
		for x := float32(200); x < 360 && !done(); x += 30 {
			h.press(pointer.ButtonPrimary, f32.Pt(x, y))
			if !done() && !confirm.Shown() {
				// The click fell on the scrim, which closes the dialog: where
				// the dialog ends depends on the fonts of the machine.
				confirm.Open()
				h.frames(20)
			}
		}
	}
	return done()
}
