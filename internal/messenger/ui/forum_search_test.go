// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// appForum draws the demo's forum as the app does, its topic when one is
// open, on a router of its own.
type appForum struct {
	t      *testing.T
	router input.Router
	app    *App
	store  *mockstore.Store
	forum  model.Chat
	now    time.Time
	size   image.Point
}

func newAppForum(t *testing.T) *appForum {
	t.Helper()
	w := &appwindow.Window{Motion: motion.New(func() {})}
	t.Cleanup(w.Motion.Close)
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.selected = mockstore.DemoForum
	forum, ok := a.selectedChat()
	if !ok || !forum.Forum {
		t.Fatalf("no forum: %+v", forum)
	}
	h := &appForum{t: t, app: a, store: store, forum: forum, now: time.Now(), size: image.Pt(600, 700)}
	h.frames(2)
	return h
}

func (h *appForum) frames(n int) {
	l := localization.For("ru")
	for range n {
		gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(h.size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		if h.app.thread != nil {
			h.app.layoutComments(gtx, l)
		} else {
			h.app.layoutForum(gtx, h.forum, l)
		}
		h.router.Frame(gtx.Ops)
		h.now = h.now.Add(16 * time.Millisecond)
	}
}

func (h *appForum) click(x, y int) {
	h.now = h.now.Add(time.Second)
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(float32(x), float32(y)), Time: time.Duration(h.now.UnixNano())})
		h.frames(1)
	}
	h.frames(2)
}

// clickSearch clicks the search button at the end of the header.
func (h *appForum) clickSearch() {
	h.click(h.size.X-8-headButton/2, int(chatHeaderSize)/2)
}

func (h *appForum) waitFor(what string, done func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			h.t.Fatalf("no %s", what)
		}
		time.Sleep(time.Millisecond)
		h.now = h.now.Add(100 * time.Millisecond)
		h.frames(1)
	}
}

// The forum's header searches all its topics: what is found shows under
// its topic, and opens the topic at it, tinted; back in the forum, the
// search is as it was, and Escape closes it.
func TestForumSearchOpensTheTopicAtAMessage(t *testing.T) {
	h := newAppForum(t)
	h.clickSearch()
	s := &h.app.forum.search
	if !s.open {
		t.Fatal("the search did not open")
	}
	h.router.Queue(key.EditEvent{Text: "Сообщение 3 "})
	h.frames(1)
	h.waitFor("messages found", func() bool { return len(s.found) > 0 && !s.loading })
	for _, f := range s.found {
		if !strings.Contains(f.Message.Text, "Сообщение 3 ") || f.Topic.Title == "" {
			t.Fatalf("found %q in %+v", f.Message.Text, f.Topic)
		}
	}
	first := s.found[0]
	// The row names the sender once, apart from the text.
	if row := foundTopicRow(first, localization.For("ru")); row.LastSender == "" || strings.Contains(row.LastMessage, row.LastSender) {
		t.Fatalf("the row says %q: %q", row.LastSender, row.LastMessage)
	}
	h.click(200, int(chatHeaderSize)+int(chatRowHeight)/2)
	thread := h.app.thread
	if thread == nil || !thread.topic || thread.name != first.Topic.Title {
		t.Fatalf("opened %+v, want the topic %q", thread, first.Topic.Title)
	}
	if v, ok := h.store.Viewport(thread.chat.ID); !ok || v.AnchorMessageID != first.Message.Key.MessageID {
		t.Fatalf("the topic opens at %+v, want %d", v, first.Message.Key.MessageID)
	}
	if h.app.comments.highlight != first.Message.Key.MessageID {
		t.Fatalf("tinted %d", h.app.comments.highlight)
	}
	h.app.closeComments()
	h.frames(2)
	if !s.open || len(s.found) == 0 {
		t.Fatal("going back lost the search")
	}
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(1)
	if s.open || s.query != "" {
		t.Fatal("Escape left the search open")
	}
}

// A topic's header has its count of messages and a search, which searches
// the topic.
func TestTopicHeaderSearchesTheTopic(t *testing.T) {
	h := newAppForum(t)
	topic := h.store.Topics(h.forum.ID).Topics[2]
	h.app.openTopic(h.forum, topic)
	h.frames(2)
	h.clickSearch()
	page := h.app.comments
	if !page.chatSearch.open {
		t.Fatal("the topic's search did not open")
	}
	h.router.Queue(key.EditEvent{Text: "Сообщение 12"})
	h.frames(1)
	h.waitFor("the message found", func() bool { return len(page.chatSearch.results) > 0 })
	if got := page.chatSearch.results[0]; !strings.Contains(got.Text, "Сообщение 12") || page.highlight != got.Key.MessageID {
		t.Fatalf("found %q, tinted %d", got.Text, page.highlight)
	}
}

func TestTopicCount(t *testing.T) {
	ru := localization.For("ru")
	for _, c := range []struct {
		h    model.History
		want string
	}{
		{model.History{}, "Загрузка…"},
		{model.History{Counted: true, Count: 1}, "Нет сообщений"},
		{model.History{Counted: true, Count: 2}, "1 сообщение"},
		{model.History{Counted: true, Count: 15}, "14 сообщений"},
	} {
		if got := topicCount(c.h, ru); got != c.want {
			t.Errorf("%+v: %q, want %q", c.h, got, c.want)
		}
	}
}
