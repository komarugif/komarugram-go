// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// communityPage is a page of dialogs with a community shown as one chat,
// pinned, a channel of it and a chat of no community.
func communityPage() *tg.MessagesDialogs {
	return &tg.MessagesDialogs{
		Dialogs: []tg.DialogClass{
			&tg.DialogCommunity{Pinned: true, CommunityID: 70},
			&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 71}, TopMessage: 5},
			&tg.Dialog{Peer: &tg.PeerUser{UserID: 2}, TopMessage: 6},
		},
		Messages: []tg.MessageClass{
			&tg.Message{ID: 5, PeerID: &tg.PeerChannel{ChannelID: 71}, Date: 2000, Message: "news"},
			&tg.Message{ID: 6, PeerID: &tg.PeerUser{UserID: 2}, Date: 1000, Message: "hi"},
		},
		Chats: []tg.ChatClass{
			&tg.Community{ID: 70, AccessHash: 7, Title: "Go", CollapsedInDialogs: true, Photo: &tg.ChatPhotoEmpty{}},
			func() *tg.Channel {
				c := &tg.Channel{ID: 71, AccessHash: 8, Title: "Go news", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}
				c.SetLinkedCommunityID(70)
				return c
			}(),
		},
		Users: []tg.UserClass{&tg.User{ID: 2, FirstName: "Anna"}},
	}
}

// A community comes among the dialogs as a chat of its own, pinned as a
// dialog is, shown as one chat as Telegram says; its channel names it.
func TestDialogsOfCommunity(t *testing.T) {
	l := newList(1)
	if n := l.add(communityPage()); n != 3 {
		t.Fatalf("added %d chats, want 3", n)
	}
	community := peerID(&tg.PeerChannel{ChannelID: 70})
	c := l.chats[0]
	if c.ID != community || c.Kind != model.KindCommunity || !c.Collapsed || c.Title != "Go" || !c.Pinned || c.PinRank != 1 {
		t.Fatalf("the community came as %+v", c)
	}
	if ch := l.chats[1]; ch.Community != community || ch.Kind != model.KindChannel {
		t.Fatalf("its channel came as %+v", ch)
	}
	if u := l.chats[2]; u.Community != 0 {
		t.Fatalf("a chat of no community names %d", u.Community)
	}
	// A community has no message: the chats kept past the page are not
	// cut at its time, pinned or not.
	page := communityPage()
	page.Dialogs[0].(*tg.DialogCommunity).Pinned = false
	l = newList(1)
	l.add(page)
	if oldest, ok := l.horizon(); !ok || !oldest.Equal(time.Unix(1000, 0)) {
		t.Fatalf("horizon %v %v, want the oldest message's", oldest, ok)
	}
}

// communityStore is a store that knows the community of communityPage.
func communityStore(t *testing.T) (*Store, int64) {
	s := testStore(t)
	page := communityPage()
	s.rememberPeers(page.Users, page.Chats)
	l := newList(1)
	l.add(page)
	s.chats = l.chats
	return s, peerID(&tg.PeerChannel{ChannelID: 70})
}

// A pinned community is named as a community when the pinned chats are
// put in order.
func TestReorderPinnedCommunity(t *testing.T) {
	s, community := communityStore(t)
	s.chats = model.WithPinOrder(s.chats, []int64{community, 2})
	var asked *tg.MessagesReorderPinnedDialogsRequest
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesReorderPinnedDialogsRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		asked = r
		return &tg.BoolTrue{}, nil
	})
	if err := s.ReorderPinned(context.Background(), []int64{2, community}); err != nil {
		t.Fatal(err)
	}
	if asked == nil || len(asked.Order) != 2 {
		t.Fatalf("asked %+v", asked)
	}
	peer, ok := asked.Order[1].(*tg.InputDialogPeerCommunity)
	if !ok {
		t.Fatalf("the community was named as %T", asked.Order[1])
	}
	if ch, ok := peer.Community.(*tg.InputChannel); !ok || ch.ChannelID != 70 || ch.AccessHash != 7 {
		t.Fatalf("the community was named as %+v", peer.Community)
	}
}

// A community opened is read with channels.getFullChannel: its chats in
// the account's list keep their last messages, and those it is not in are
// told apart by what the community lets it do.
func TestOpenCommunity(t *testing.T) {
	s, community := communityStore(t)
	member := peerID(&tg.PeerChannel{ChannelID: 71})
	s.history.ctx = context.Background()
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.ChannelsGetFullChannelRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		if ch, ok := r.Channel.(*tg.InputChannel); !ok || ch.ChannelID != 70 || ch.AccessHash != 7 {
			return nil, errors.New("not the community")
		}
		hidden := tg.CommunityPeer{Peer: &tg.PeerChannel{ChannelID: 74}}
		hidden.SetVisible(false)
		public := tg.CommunityPeer{Peer: &tg.PeerChannel{ChannelID: 75}}
		public.SetVisible(false)
		return &tg.MessagesChatFull{
			FullChat: &tg.CommunityFull{ID: 70, About: "All of Go", ChatPhoto: &tg.PhotoEmpty{}, LinkedPeers: []tg.CommunityPeer{
				{Peer: &tg.PeerChannel{ChannelID: 71}},
				{Peer: &tg.PeerChannel{ChannelID: 72}, CanViewHistory: true},
				{Peer: &tg.PeerChannel{ChannelID: 73}},
				hidden,
				public,
			}},
			Chats: []tg.ChatClass{
				&tg.Channel{ID: 71, AccessHash: 8, Title: "Go news", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}},
				&tg.Channel{ID: 72, AccessHash: 9, Title: "Go jobs", Broadcast: true, Left: true, Photo: &tg.ChatPhotoEmpty{}},
				&tg.Channel{ID: 73, AccessHash: 10, Title: "Meetups", Megagroup: true, Left: true, Photo: &tg.ChatPhotoEmpty{}},
				&tg.Channel{ID: 74, AccessHash: 11, Title: "Core", Megagroup: true, Left: true, Photo: &tg.ChatPhotoEmpty{}},
				&tg.Channel{ID: 75, AccessHash: 12, Title: "Announcements", Username: "goann", Broadcast: true, Left: true, Photo: &tg.ChatPhotoEmpty{}},
			},
		}, nil
	})
	s.OpenCommunity(community)
	deadline := time.Now().Add(2 * time.Second)
	for s.Community(community).Loading && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := s.Community(community)
	if got.Err != nil || got.About != "All of Go" {
		t.Fatalf("read %+v", got)
	}
	sections := got.Sections()
	joined, viewable := sections[model.CommunityJoined], sections[model.CommunityViewable]
	requestable, hiddenOnes := sections[model.CommunityRequestable], sections[model.CommunityHidden]
	if len(joined) != 1 || joined[0].Chat.ID != member || joined[0].Chat.Last.Text != "news" {
		t.Fatalf("joined %+v, want the channel as the list has it", joined)
	}
	// A public channel can be read, hidden or not, as Telegram for Android
	// has it.
	if len(viewable) != 2 || viewable[0].Chat.Title != "Go jobs" || viewable[1].Chat.Title != "Announcements" || !viewable[1].Public {
		t.Fatalf("viewable %+v", viewable)
	}
	if len(requestable) != 1 || requestable[0].Chat.Kind != model.KindGroup || len(hiddenOnes) != 1 || hiddenOnes[0].Chat.Title != "Core" {
		t.Fatalf("to ask to join %+v, hidden %+v", requestable, hiddenOnes)
	}
}

// Shown apart, a community is the list's so at once, without its row, as
// Telegram lists it; shown as one chat, it has the row back, the store says
// so though it had none, and a refusal leaves it as it was.
func TestSetCommunityCollapsed(t *testing.T) {
	s, community := communityStore(t)
	var fail error
	var asked *tg.CommunitiesToggleCommunityCollapsedInDialogsRequest
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.CommunitiesToggleCommunityCollapsedInDialogsRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		asked = r
		if fail != nil {
			return nil, fail
		}
		return &tg.Updates{}, nil
	})
	row := func() (collapsed, ok bool) {
		for _, c := range s.Chats() {
			if c.ID == community && c.Kind == model.KindCommunity {
				return c.Collapsed, true
			}
		}
		return false, false
	}
	if err := s.SetCommunityCollapsed(context.Background(), community, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := row(); asked == nil || asked.Collapsed || ok || s.Community(community).Collapsed {
		t.Fatalf("shown apart: asked %+v, row %v, collapsed %v", asked, ok, s.Community(community).Collapsed)
	}
	if err := s.SetCommunityCollapsed(context.Background(), community, true); err != nil {
		t.Fatal(err)
	}
	if v, ok := row(); !asked.Collapsed || !ok || !v || !s.Community(community).Collapsed {
		t.Fatalf("shown as one chat: row %v %v, collapsed %v", ok, v, s.Community(community).Collapsed)
	}
	fail = errors.New("boom")
	if err := s.SetCommunityCollapsed(context.Background(), community, false); err == nil {
		t.Fatal("a refusal was not told")
	}
	if v, ok := row(); !ok || !v || !s.Community(community).Collapsed {
		t.Fatal("the list kept what Telegram refused")
	}
}

// What another device changes comes in updates: the community shown apart,
// without its row, and as one chat again, with it; a chat taken out of it.
func TestCommunityUpdates(t *testing.T) {
	s, community := communityStore(t)
	member := peerID(&tg.PeerChannel{ChannelID: 71})
	err := s.Handle(context.Background(), &tg.Updates{Chats: []tg.ChatClass{
		&tg.Community{ID: 70, AccessHash: 7, Title: "Go", Photo: &tg.ChatPhotoEmpty{}},
		&tg.Channel{ID: 71, AccessHash: 8, Title: "Go news", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range s.Chats() {
		switch c.ID {
		case community:
			t.Fatal("the community shown apart kept its row")
		case member:
			if c.Community != 0 {
				t.Fatal("the channel taken out still names the community")
			}
		}
	}
	err = s.Handle(context.Background(), &tg.Updates{Chats: []tg.ChatClass{
		&tg.Community{ID: 70, AccessHash: 7, Title: "Go", CollapsedInDialogs: true, Photo: &tg.ChatPhotoEmpty{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := chatOf(s, community); !ok || c.Kind != model.KindCommunity || !c.Collapsed || c.Title != "Go" {
		t.Fatalf("shown as one chat again, the list has %+v %v", c, ok)
	}
}

func chatOf(s *Store, id int64) (model.Chat, bool) {
	for _, c := range s.Chats() {
		if c.ID == id {
			return c, true
		}
	}
	return model.Chat{}, false
}

// A channel found or opened by a link, not in the list, keeps the community
// it is linked to.
func TestLinkedCommunityOutOfTheList(t *testing.T) {
	s := testStore(t)
	ch := &tg.Channel{ID: 81, AccessHash: 9, Title: "Gate", Broadcast: true, Left: true, Photo: &tg.ChatPhotoEmpty{}}
	ch.SetLinkedCommunityID(80)
	s.rememberPeers(nil, []tg.ChatClass{ch})
	if got := s.LinkedCommunity(peerID(&tg.PeerChannel{ChannelID: 81})); got != peerID(&tg.PeerChannel{ChannelID: 80}) {
		t.Fatalf("linked to %d", got)
	}
}

// A community the store has not heard of, shown apart, is read through the
// full info of one of its chats, which brings it.
func TestOpenCommunityThroughItsChat(t *testing.T) {
	s := testStore(t)
	member := &tg.Channel{ID: 81, AccessHash: 9, Title: "Gate", Broadcast: true, Left: true, Photo: &tg.ChatPhotoEmpty{}}
	member.SetLinkedCommunityID(80)
	s.rememberPeers(nil, []tg.ChatClass{member})
	community := peerID(&tg.PeerChannel{ChannelID: 80})
	s.history.ctx = context.Background()
	var asked []string
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.ChannelsGetFullChannelRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		ch := r.Channel.(*tg.InputChannel)
		switch ch.ChannelID {
		case 81:
			asked = append(asked, "member")
			return &tg.MessagesChatFull{
				FullChat: &tg.ChannelFull{ID: 81, ChatPhoto: &tg.PhotoEmpty{}, NotifySettings: tg.PeerNotifySettings{}, ExportedInvite: nil},
				Chats:    []tg.ChatClass{member, &tg.Community{ID: 80, AccessHash: 8, Title: "Gate community", Photo: &tg.ChatPhotoEmpty{}}},
			}, nil
		case 80:
			asked = append(asked, "community")
			if ch.AccessHash != 8 {
				return nil, errors.New("wrong access hash")
			}
			return &tg.MessagesChatFull{FullChat: &tg.CommunityFull{ID: 80, ChatPhoto: &tg.PhotoEmpty{}, LinkedPeers: []tg.CommunityPeer{{Peer: &tg.PeerChannel{ChannelID: 81}, CanViewHistory: true}}},
				Chats: []tg.ChatClass{member}}, nil
		}
		return nil, errors.New("unknown channel")
	})
	s.OpenCommunity(community)
	deadline := time.Now().Add(2 * time.Second)
	for (s.Community(community).Loading || len(asked) == 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	got := s.Community(community)
	if got.Err != nil || got.Title != "Gate community" || len(got.Peers) != 1 || !got.Peers[0].CanView {
		t.Fatalf("read %+v after asking %v", got, asked)
	}
}

// The actions of communities and of paid messages come as their kinds, the
// community named when known.
func TestCommunityServiceActions(t *testing.T) {
	community := peerID(&tg.PeerChannel{ChannelID: 80})
	added := &tg.MessageActionChangeCommunity{}
	added.SetCommunityID(80)
	a := serviceAction(added, map[int64]string{community: "Gate"})
	if a.Kind != model.ServiceCommunity || a.Title != "Gate" || a.Count != community || a.Reason != "" {
		t.Fatalf("added: %+v", a)
	}
	if a := serviceAction(&tg.MessageActionChangeCommunity{}, nil); a.Kind != model.ServiceCommunity || a.Reason != "removed" {
		t.Fatalf("removed: %+v", a)
	}
	if a := serviceAction(&tg.MessageActionChatJoinedViaCommunity{CommunityID: 80}, map[int64]string{community: "Gate"}); a.Kind != model.ServiceJoinedViaCommunity || a.Title != "Gate" {
		t.Fatalf("joined: %+v", a)
	}
	if a := serviceAction(&tg.MessageActionPaidMessagesPrice{BroadcastMessagesAllowed: true}, nil); a.Kind != model.ServicePaidMessages || a.Reason != "allowed" {
		t.Fatalf("paid messages: %+v", a)
	}
}
