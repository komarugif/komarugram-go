// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// chatSearchStore finds the history's messages that contain the text.
type chatSearchStore struct {
	*menuStore
	mu    sync.Mutex
	asked []string
}

func (s *chatSearchStore) SearchChat(_ context.Context, _ int64, text, next string, limit int) (model.ChatSearchPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, text)
	var page model.ChatSearchPage
	msgs := s.benchmarkHistory.h.Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.Contains(msgs[i].Text, text) {
			page.Messages = append(page.Messages, msgs[i])
		}
	}
	page.Count = len(page.Messages)
	return page, nil
}

func (s *chatSearchStore) Reveal(int64, model.MessageID) {}

// headFrame draws the page with its header, as the app does.
func headFrame(h *menuHarness) {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	l := localization.For("en")
	p := h.page
	p.images.BeginFrame()
	p.media.BeginFrame()
	c := model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}
	layoutChatPage(gtx, c, l, avatar, nil, nil, func(gtx layout.Context) layout.Dimensions { return p.Layout(gtx, c, l, false) }, p)
	p.media.EndFrame()
	p.images.EndFrame()
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}

func headPress(h *menuHarness, at f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at, Time: time.Duration(h.now.UnixNano())})
		headFrame(h)
	}
}

// The header's search finds messages of the chat, newest first, goes to
// them and on to older ones; its menu searches, asks for the chat's info
// and goes to the chat's beginning.
func TestChatSearchAndMenu(t *testing.T) {
	var store *chatSearchStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &chatSearchStore{menuStore: s}
		return store
	})
	p := h.page
	for range 3 {
		headFrame(h)
	}
	// The search button is left of the menu's, at the header's end.
	headPress(h, f32.Pt(500-8-80+20, 28))
	if !p.chatSearch.open {
		t.Fatal("the search button did not open the search")
	}
	p.chatSearch.field.SetText("Message 1")
	for range 40 {
		headFrame(h)
	}
	store.mu.Lock()
	asked := store.asked
	store.mu.Unlock()
	if len(asked) != 1 || asked[0] != "Message 1" {
		t.Fatalf("searched %q", asked)
	}
	if p.chatSearch.current != 0 || p.highlight != 19 {
		t.Fatalf("the search shows found %d, message %d, not the newest", p.chatSearch.current, p.highlight)
	}
	if got := p.chatSearch.counter(localization.For("en")); got != "1 of 11" {
		t.Fatalf("the counter reads %q", got)
	}
	// Older, then close.
	headPress(h, f32.Pt(500-8-120+20, 28))
	if p.highlight != 18 {
		t.Fatalf("the older button went to %d", p.highlight)
	}
	headPress(h, f32.Pt(500-8-40+20, 28))
	if p.chatSearch.open {
		t.Fatal("the close button left the search open")
	}

	// The menu: its items are the search, the info and the beginning.
	item := func(a chatMenuAction) f32.Point {
		m := &p.chatMenu
		for i, shown := range p.chatMenuActions() {
			if shown == a {
				return f32.Pt(float32(m.rect.Min.X+40), float32(m.rect.Min.Y+menuPadding+i*menuItemHeight+menuItemHeight/2))
			}
		}
		t.Fatalf("the menu has no %d", a)
		return f32.Point{}
	}
	openMenu := func() {
		headPress(h, f32.Pt(500-8-40+20, 28))
		for range 20 {
			headFrame(h)
		}
		if !p.chatMenu.open {
			t.Fatal("the menu did not open")
		}
	}
	openMenu()
	headPress(h, item(chatMenuInfo))
	if p.chatMenu.open || !p.takeInfoAsked() {
		t.Fatal("the info item did not ask for the info")
	}
	for range 20 {
		headFrame(h)
	}
	openMenu()
	headPress(h, item(chatMenuBeginning))
	for range 3 {
		headFrame(h)
	}
	if p.list.Position.First != 0 {
		t.Fatalf("to the beginning shows message %d", p.messages[p.list.Position.First].Key.MessageID)
	}
}

// TestRenderChatSearch saves the header searching, for looking at it:
// CHAT_SEARCH_PNG=/tmp/search.png.
func TestRenderChatSearch(t *testing.T) {
	path := os.Getenv("CHAT_SEARCH_PNG")
	if path == "" {
		t.Skip("set CHAT_SEARCH_PNG to a file")
	}
	var store *chatSearchStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &chatSearchStore{menuStore: s}
		return store
	})
	p := h.page
	l := localization.For("ru")
	c := model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}
	frame := 0
	renderFrames(t, image.Pt(500, 500), path, func(gtx layout.Context) {
		frame++
		if frame == 2 {
			p.openChatSearch(gtx)
			p.chatSearch.field.SetText("Message 1")
		}
		p.images.BeginFrame()
		p.media.BeginFrame()
		layoutChatPage(gtx, c, l, avatar, nil, nil, func(gtx layout.Context) layout.Dimensions { return p.Layout(gtx, c, l, false) }, p)
		p.media.EndFrame()
		p.images.EndFrame()
		time.Sleep(20 * time.Millisecond)
	})
	_ = store
}

// farSearchStore finds a message the history has not loaded, as an old
// one of a long channel, which the page goes to by opening the chat
// again there.
type farSearchStore struct {
	*menuStore
	revealed []model.MessageID
}

func (s *farSearchStore) SearchChat(context.Context, int64, string, string, int) (model.ChatSearchPage, error) {
	return model.ChatSearchPage{Messages: []model.Message{{Key: model.MessageKey{ChatID: 1, MessageID: 500}, Text: "far"}}, Count: 1}, nil
}

func (s *farSearchStore) Reveal(_ int64, id model.MessageID) { s.revealed = append(s.revealed, id) }

// The search stays open when it goes to a message not loaded: the chat is
// opened again there, which closed it as if another chat were opened.
func TestChatSearchStaysOnAFarMessage(t *testing.T) {
	var store *farSearchStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &farSearchStore{menuStore: s}
		return store
	})
	p := h.page
	for range 3 {
		headFrame(h)
	}
	headPress(h, f32.Pt(500-8-80+20, 28))
	if !p.chatSearch.open {
		t.Fatal("the search button did not open the search")
	}
	p.chatSearch.field.SetText("far")
	for range 40 {
		headFrame(h)
	}
	if len(store.revealed) != 1 || store.revealed[0] != 500 {
		t.Fatalf("revealed %v", store.revealed)
	}
	if !p.chatSearch.open || p.chatSearch.query != "far" || p.chatSearch.current != 0 {
		t.Fatalf("the search closed or forgot what it found: open %v, query %q, current %d", p.chatSearch.open, p.chatSearch.query, p.chatSearch.current)
	}
}
