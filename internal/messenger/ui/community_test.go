// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"slices"
	"strings"
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

// communityChats are a pinned chat, a community of chats 2 and 4, chat 2
// with something unread and newer than chat 3, and chat 4 older.
func communityChats(collapsed bool) []model.Chat {
	at := func(m int) time.Time { return time.Unix(100000, 0).Add(-time.Duration(m) * time.Minute) }
	return []model.Chat{
		{ID: 1, Kind: model.KindUser, Title: "Pinned", Pinned: true, PinRank: 1, LastTime: at(60)},
		{ID: 2, Kind: model.KindChannel, Title: "News", Community: 100, Unread: 3, LastTime: at(1)},
		{ID: 3, Kind: model.KindUser, Title: "Anna", LastTime: at(2)},
		{ID: 4, Kind: model.KindGroup, Title: "Talk", Community: 100, LastTime: at(3)},
		{ID: 100, Kind: model.KindCommunity, Title: "Go", Collapsed: collapsed},
	}
}

// TestCommunityShownAsOneChat checks that the list of all chats shows a
// community shown as one chat in place of its chats, where the newest of
// them would be, with the chats that have something unread counted and
// their names for its line; shown apart, the community has no row.
func TestCommunityShownAsOneChat(t *testing.T) {
	h := newListHarness(t, communityChats(true))
	if !slices.Equal(h.list.shown, []int64{1, 100, 3}) {
		t.Fatalf("the list shows %v, want the community in place of chats 2 and 4", h.list.shown)
	}
	row := communityRows(h.chats, h.l)[1]
	if row.Unread != 1 || row.Last.Text != "News, Talk" || !row.LastTime.Equal(h.chats[1].LastTime) {
		t.Fatalf("the community's row is %+v", row)
	}
	h.chats = communityChats(false)
	h.frames(2)
	if !slices.Equal(h.list.shown, []int64{1, 2, 3, 4}) {
		t.Fatalf("shown apart, the list shows %v", h.list.shown)
	}
	// A folder lists the chats as they are.
	h.chats = communityChats(true)
	h.sec = section{kind: sectionFolder, folder: 1}
	h.frames(2)
	if !slices.Equal(h.list.shown, []int64{1, 2, 3, 4, 100}) {
		t.Fatalf("a folder shows %v", h.list.shown)
	}
}

// TestCommunityRowMenu checks that the menu of a community shown as one
// chat shows it apart, and marks all of its chats read; a chat of one shown
// apart opens it.
func TestCommunityRowMenu(t *testing.T) {
	h := newListHarness(t, communityChats(true))
	var ungrouped, opened []int64
	h.list.menu.ungroup = func(id int64) { ungrouped = append(ungrouped, id) }
	h.list.menu.openCommunity = func(id int64) { opened = append(opened, id) }
	h.frames(2)
	first := h.rightClick(1)
	if got := h.list.menu.shown; !slices.Equal(got, []chatRowAction{chatRowPin, chatRowRead, chatRowUngroup}) {
		t.Fatalf("menu of the community: %v", got)
	}
	h.press(pointer.ButtonPrimary, first)
	h.frames(30)
	h.rightClick(1)
	read := rowAt(1)
	read.Y += float32(menuPadding) + 1.5*float32(menuItemHeight)
	h.press(pointer.ButtonPrimary, read)
	h.frames(30)
	if got := h.store.read; !slices.Equal(got, []int64{2}) {
		t.Fatalf("marked %v read, want the community's chat with something unread", got)
	}
	h.rightClick(1)
	ungroup := rowAt(1)
	ungroup.Y += float32(menuPadding) + 2.5*float32(menuItemHeight)
	h.press(pointer.ButtonPrimary, ungroup)
	h.frames(5)
	if !slices.Equal(ungrouped, []int64{100}) {
		t.Fatalf("ungrouped %v", ungrouped)
	}

	h.chats = communityChats(false)
	h.frames(30)
	first = h.rightClick(1)
	if got := h.list.menu.shown; !slices.Equal(got, []chatRowAction{chatRowPin, chatRowRead, chatRowCommunity}) {
		t.Fatalf("menu of a chat of a community: %v", got)
	}
	open := rowAt(1)
	open.Y += float32(menuPadding) + 2.5*float32(menuItemHeight)
	h.press(pointer.ButtonPrimary, open)
	h.frames(5)
	if !slices.Equal(opened, []int64{100}) {
		t.Fatalf("opened %v", opened)
	}
}

// TestCommunityItems checks the sections of a community's page, as
// Telegram for Android's: the chats the account is in, those it can read,
// bots and public channels hidden or not among them, those it can ask to
// join, the hidden ones.
func TestCommunityItems(t *testing.T) {
	c := model.Community{Peers: []model.CommunityPeer{
		{Chat: model.Chat{ID: 1, Kind: model.KindGroup}, CanView: true},
		{Chat: model.Chat{ID: 2, Kind: model.KindChannel}, Joined: true},
		{Chat: model.Chat{ID: 3, Kind: model.KindGroup}, Hidden: true},
		{Chat: model.Chat{ID: 4, Kind: model.KindBot}, Hidden: true},
		{Chat: model.Chat{ID: 5, Kind: model.KindGroup}},
		{Chat: model.Chat{ID: 6, Kind: model.KindChannel}, Hidden: true, Public: true},
	}}
	var got []string
	for _, it := range communityItems(c, localization.For("en")) {
		switch {
		case it.toggle:
			got = append(got, "toggle")
		case it.heading != "":
			got = append(got, it.heading)
		default:
			got = append(got, string(rune('0'+it.peer.Chat.ID)))
		}
	}
	want := []string{"toggle", "Chats you are in", "2", "Chats you can view", "1", "4", "6", "Chats you can request to join", "5", "Hidden chats", "3"}
	if !slices.Equal(got, want) {
		t.Fatalf("items %v, want %v", got, want)
	}
}

// TestRenderCommunity draws the demo's community shown as one chat, for
// looking at it: COMMUNITY_PNG=/tmp/community.png has its page, -list
// beside it the list of all chats with its row, and -apart the list with
// it shown apart, its chats with the badge that opens it.
func TestRenderCommunity(t *testing.T) {
	path := os.Getenv("COMMUNITY_PNG")
	if path == "" {
		t.Skip("set COMMUNITY_PNG to a file")
	}
	store := mockstore.New(time.Now(), 0)
	l := localization.For("ru")
	info := store.Community(mockstore.DemoCommunity)
	chat := model.Chat{ID: mockstore.DemoCommunity, Title: "Сообщество Go", Kind: model.KindCommunity, Collapsed: true, Members: len(info.Peers)}
	page := newCommunityPage()
	page.collapse.SetValues([]string{"one"})
	rows := newChatList()
	noAvatar := func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, _ unit.Dp) layout.Dimensions {
		return layout.Dimensions{}
	}
	renderFrames(t, image.Pt(520, 980), path, func(gtx layout.Context) {
		layoutChatPage(gtx, chat, l, noAvatar, nil, nil, func(gtx layout.Context) layout.Dimensions {
			return page.layout(gtx, info, rows, l, func(model.CommunityPeer) {})
		}, nil)
	})
	list := newChatList()
	renderFrames(t, image.Pt(360, 620), strings.TrimSuffix(path, ".png")+"-list.png", func(gtx layout.Context) {
		list.Layout(gtx, section{kind: sectionAll}, nil, store.Chats(), mockstore.DemoCommunity, false, l)
	})
	apart := newChatList()
	chats := model.WithCommunityShown(store.Chats(), model.Chat{ID: mockstore.DemoCommunity}, false)
	renderFrames(t, image.Pt(360, 620), strings.TrimSuffix(path, ".png")+"-apart.png", func(gtx layout.Context) {
		apart.Layout(gtx, section{kind: sectionAll}, nil, chats, mockstore.DemoForum, false, l)
	})
}

// TestCommunityBadge checks that a chat of a community shown apart has the
// badge on its avatar, and a click on it opens the community, not the chat;
// shown as one chat, its chats have no row to have it on.
func TestCommunityBadge(t *testing.T) {
	h := newListHarness(t, communityChats(false))
	// Chat 2, the second row: its avatar's corner.
	badge := f32.Pt(59, float32(6+64*1+51))
	h.press(pointer.ButtonPrimary, badge)
	h.frames(2)
	if !slices.Equal(h.picks, []int64{100}) {
		t.Fatalf("a click on the badge picked %v, want the community", h.picks)
	}
	// Chat 3, of no community, opens as it is from the same place.
	h.picks = nil
	h.press(pointer.ButtonPrimary, f32.Pt(59, float32(6+64*2+51)))
	h.frames(2)
	if !slices.Equal(h.picks, []int64{3}) {
		t.Fatalf("a click on the corner of a chat of no community picked %v", h.picks)
	}
}

// apartStore is the demo without the rows of its communities, as Telegram
// lists a community shown apart: none.
type apartStore struct{ *mockstore.Store }

func (s apartStore) Chats() []model.Chat {
	var out []model.Chat
	for _, c := range s.Store.Chats() {
		if c.Kind != model.KindCommunity {
			out = append(out, c)
		}
	}
	return out
}

// TestOpenCommunityWithoutRow checks that a community with no row in the
// list opens all the same, from a chat's menu or its badge: its page, with
// the name the store knows.
func TestOpenCommunityWithoutRow(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	t.Cleanup(w.Motion.Close)
	a := New(w, apartStore{mockstore.New(time.Now(), 0)}, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.chats.menu.openCommunity(mockstore.DemoPhotoCommunity)
	c, ok := a.selectedChat()
	if !ok || c.ID != mockstore.DemoPhotoCommunity || c.Kind != model.KindCommunity || c.Title != "Фотоклуб" {
		t.Fatalf("opened %+v %v, want the community's page", c, ok)
	}
	if !a.listPage(c) {
		t.Fatal("the community opens as a history")
	}
}

// TestChatHeaderCommunityBadge checks that the header of a chat of a
// community, opened from a search and not in the list, has the badge over
// its avatar, which takes the click; a chat of none has no badge.
func TestChatHeaderCommunityBadge(t *testing.T) {
	var router input.Router
	page := &chatPage{communityOf: func(chat int64) int64 {
		if chat == 7 {
			return 100
		}
		return 0
	}}
	l := localization.For("en")
	noAvatar := func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, _ unit.Dp) layout.Dimensions {
		return layout.Dimensions{}
	}
	now := time.Now()
	frame := func(chat int64) bool {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: now, Constraints: layout.Exact(image.Pt(500, 300)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		clicked := page.communityBadge.Clicked(gtx)
		layoutChatPageHead(gtx, model.Chat{ID: chat, Kind: model.KindChannel, Title: "Channel"}, l, noAvatar, nil, nil, nil, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}, page)
		router.Frame(gtx.Ops)
		now = now.Add(16 * time.Millisecond)
		return clicked
	}
	// The badge's center, at the avatar's corner.
	px := int(chatHeaderImage)
	y := (int(chatHeaderSize) - px) / 2
	at := f32.Pt(float32(16+px-int(communityBadgeSize)/2+1), float32(y+px-int(communityBadgeSize)/2+1))
	click := func(chat int64) bool {
		clicked := frame(chat)
		for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
			router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at, Time: time.Duration(now.UnixNano())})
			clicked = frame(chat) || clicked
		}
		return frame(chat) || clicked
	}
	if !click(7) {
		t.Fatal("the badge of a chat of a community took no click")
	}
	if click(8) {
		t.Fatal("a chat of no community has a badge")
	}
}

// TestCommunityPageStaysOnUngroup checks that the page of a community
// opened from its row stays open when the community is shown apart, and
// its row, as Telegram lists it, goes.
func TestCommunityPageStaysOnUngroup(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	t.Cleanup(w.Motion.Close)
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.open(chatPick{ID: mockstore.DemoCommunity})
	if err := store.SetCommunityCollapsed(context.Background(), mockstore.DemoCommunity, false); err != nil {
		t.Fatal(err)
	}
	c, ok := a.selectedChat()
	if !ok || c.Kind != model.KindCommunity || c.Collapsed || c.Title != "Сообщество Go" {
		t.Fatalf("after ungrouping, the page shows %+v %v", c, ok)
	}
}

// TestCommunitySwitchWithoutRow checks that the switch of a community shown
// apart, which has no row, stays as it was clicked while the store is asked,
// and shows what the store has after: the community has its row then.
func TestCommunitySwitchWithoutRow(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	t.Cleanup(w.Motion.Close)
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.openCommunity(mockstore.DemoPhotoCommunity)
	l := localization.For("ru")
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		c, ok := a.selectedChat()
		if !ok {
			t.Fatal("the community's page lost its chat")
		}
		a.layoutCommunity(gtx, c, l)
	}
	frame()
	on := true
	a.community.asked = &on // As the switch's click does.
	on2 := func() bool { return len(a.community.collapse.GetValues()) == 1 }
	frame()
	frame()
	if !on2() {
		t.Fatal("the switch went back as soon as it was clicked")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !store.Community(mockstore.DemoPhotoCommunity).Collapsed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	frame()
	if !on2() || a.community.pending != nil {
		t.Fatalf("after the store had it: on %v, pending %v", on2(), a.community.pending)
	}
	if _, ok := chatByID(store.Chats(), mockstore.DemoPhotoCommunity); !ok {
		t.Fatal("the community shown as one chat has no row")
	}
}

// TestOpenCommunityPeer checks that a chat of a community opens as Telegram
// for Android opens it: a hidden bot all the same, a hidden group only
// telling, in the toast, that it cannot.
func TestOpenCommunityPeer(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	t.Cleanup(w.Motion.Close)
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.openCommunity(mockstore.DemoCommunity)
	community, _ := a.selectedChat()
	peer := func(id int64) model.CommunityPeer {
		for _, p := range store.Community(mockstore.DemoCommunity).Peers {
			if p.Chat.ID == id {
				return p
			}
		}
		t.Fatalf("no chat %d in the community", id)
		return model.CommunityPeer{}
	}
	l := localization.For("en")
	a.openCommunityPeer(community, peer(900014), l) // the hidden bot
	if c, ok := a.selectedChat(); !ok || c.ID != 900014 {
		t.Fatalf("the hidden bot did not open: %+v", c)
	}
	a.openCommunity(mockstore.DemoCommunity)
	a.openCommunityPeer(community, peer(900013), l) // the hidden group
	if c, _ := a.selectedChat(); c.ID != mockstore.DemoCommunity {
		t.Fatalf("the hidden group opened: %+v", c)
	}
	if want := "You can't join or view this group until an admin invites you."; a.toast.Text() != want {
		t.Fatalf("toast %q, want %q", a.toast.Text(), want)
	}
}
