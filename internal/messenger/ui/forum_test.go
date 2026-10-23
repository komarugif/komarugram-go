// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
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

// forumHarness draws the topics of the demo's forum on a router of its own.
type forumHarness struct {
	t      *testing.T
	router input.Router
	store  *mockstore.Store
	page   *forumPage
	now    time.Time
}

func newForumHarness(t *testing.T) *forumHarness {
	t.Helper()
	h := &forumHarness{t: t, store: mockstore.New(time.Unix(100000, 0), 0), page: newForumPage(), now: time.Unix(100000, 0)}
	h.frames(2)
	return h
}

func (h *forumHarness) frames(n int) {
	for range n {
		gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		h.page.layout(gtx, model.Chat{ID: mockstore.DemoForum, Kind: model.KindGroup, Forum: true}, h.store, localization.For("en"))
		h.router.Frame(gtx.Ops)
		h.now = h.now.Add(16 * time.Millisecond)
	}
}

func (h *forumHarness) click(pos f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: time.Duration(h.now.UnixNano())})
		h.frames(1)
	}
}

// TestForumTopicClickOpens clicks the third topic of the list and expects
// exactly that topic to open.
func TestForumTopicClickOpens(t *testing.T) {
	h := newForumHarness(t)
	var opened []model.Topic
	h.page.open = func(_ model.Chat, topic model.Topic) { opened = append(opened, topic) }
	topics := h.store.Topics(mockstore.DemoForum).Topics
	row := int(float32(chatRowHeight))
	h.click(f32.Pt(200, float32(2*row+row/2)))
	if len(opened) != 1 || opened[0].ID != topics[2].ID {
		t.Fatalf("opened %+v, want topic %d", opened, topics[2].ID)
	}
}

// TestForumOpensTopicAndGoesBack opens a topic of the demo's forum as the
// comments page does and goes back to the list.
func TestForumOpensTopicAndGoesBack(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	if a.forum == nil || a.comments == nil {
		t.Fatalf("no forum page (%v) or thread page (%v) for a store with forums", a.forum, a.comments)
	}
	a.selected = mockstore.DemoForum
	chat, ok := a.selectedChat()
	if !ok || !chat.Forum {
		t.Fatalf("selected chat %+v is not a forum", chat)
	}
	topic := store.Topics(chat.ID).Topics[2]
	a.openTopic(chat, topic)
	if a.thread == nil || !a.thread.topic || a.thread.name != topic.Title || a.thread.title != chat.Title {
		t.Fatalf("thread %+v", a.thread)
	}
	if got := store.History(a.thread.chat.ID); len(got.Messages) == 0 {
		t.Fatalf("the topic has no messages")
	}
	a.closeComments()
	if a.thread != nil {
		t.Fatalf("thread %+v left after going back", a.thread)
	}
}

// TestRenderForum saves the topics of the demo's forum, for looking at
// them: FORUM_PNG=/tmp/forum.png.
func TestRenderForum(t *testing.T) {
	path := os.Getenv("FORUM_PNG")
	if path == "" {
		t.Skip("set FORUM_PNG to a file")
	}
	store := mockstore.New(time.Now(), 0)
	page := newForumPage()
	l := localization.For("ru")
	chat := model.Chat{ID: mockstore.DemoForum, Title: "Клуб Go: обсуждения", Kind: model.KindGroup, Forum: true, Members: 240}
	renderFrames(t, image.Pt(520, 620), path, func(gtx layout.Context) {
		layoutChatPage(gtx, chat, l, func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, _ unit.Dp) layout.Dimensions {
			return layout.Dimensions{}
		}, nil, nil, func(gtx layout.Context) layout.Dimensions {
			return page.layout(gtx, chat, store, l)
		}, nil)
	})
}

// TestTopicReadsWhatItShows checks that a topic is read as a chat is, while
// the comments to a post, which are a thread too, are not.
func TestTopicReadsWhatItShows(t *testing.T) {
	for _, topic := range []bool{false, true} {
		var store *ghostStore
		h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
			store = &ghostStore{menuStore: s}
			return store
		})
		h.store.benchmarkHistory.h.ThreadRoot = 1
		h.store.benchmarkHistory.h.Revision++
		store.read = nil // What the page read before it was a thread.
		h.page.thread, h.page.topic = true, topic
		h.page.list.Position.BeforeEnd = false
		h.frames(3)
		if read := len(store.read) > 0; read != topic {
			t.Errorf("topic %t: read %v", topic, store.read)
		}
	}
}
