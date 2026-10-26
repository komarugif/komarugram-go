// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// A community comes among the dialogs as dialogCommunity, its chats with
// the community's id as linked_community_id; what it has is read with
// channels.getFullChannel, which answers with communityFull, as Telegram
// Desktop reads it (Data::CommunityInfo).

// communityState is what was read of the communities opened.
type communityState struct {
	mu   sync.Mutex
	read map[int64]model.Community
}

// OpenCommunity implements model.CommunitySource. A community shown apart
// has no dialog, and Telegram may not have told of it yet: it is then read
// through one of its chats.
func (s *Store) OpenCommunity(community int64) {
	c := s.history
	c.mu.Lock()
	api, ctx := c.api, c.ctx
	closing := c.closing
	c.mu.Unlock()
	st := &s.communities
	st.mu.Lock()
	was := st.read[community]
	if was.Loading {
		st.mu.Unlock()
		return
	}
	if api == nil || closing {
		st.mu.Unlock()
		if was.Peers == nil {
			s.setCommunity(model.Community{ID: community, Err: errNotConnected})
		}
		return
	}
	if st.read == nil {
		st.read = map[int64]model.Community{}
	}
	was.ID, was.Loading, was.Err = community, true, nil
	st.read[community] = was
	st.mu.Unlock()
	s.changed()
	go func() {
		got := model.Community{ID: community}
		defer crash.Recover("community", func(e *crash.Panic) {
			s.setCommunity(model.Community{ID: community, Err: e})
		})
		peer, err := s.communityPeer(ctx, api, community)
		if err != nil {
			got.Err = err
			s.setCommunity(got)
			return
		}
		got.Title = peer.Name
		res, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
		if err != nil {
			got.Err = err
			s.setCommunity(got)
			return
		}
		s.rememberPeers(res.Users, res.Chats)
		full, ok := res.FullChat.(*tg.CommunityFull)
		if !ok {
			got.Err = errors.New("tgstore: not a community")
			s.setCommunity(got)
			return
		}
		got.About = full.About
		got.Peers = s.communityPeers(full.LinkedPeers, res.Chats, res.Users)
		s.setCommunity(got)
	}()
}

// errNoCommunity is a community Telegram told nothing of through its chats.
var errNoCommunity = errors.New("tgstore: the community is unknown")

// communityPeer is the peer of community: as it came with the dialogs or an
// update, or else as the full info of one of its chats brings it, or as
// a message of one names it (inputChannelFromMessage), as Telegram Desktop
// gets a channel it has no access hash of.
func (s *Store) communityPeer(ctx context.Context, api *tg.Client, community int64) (peerRecord, error) {
	c := s.history
	known := func() (peerRecord, []peerRecord, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if p, ok := c.peers[community]; ok && p.Kind == "community" {
			return p, nil, true
		}
		var members []peerRecord
		for _, p := range c.peers {
			if p.Community == community && (p.Kind == "channel" || p.Kind == "user") {
				members = append(members, p)
			}
		}
		// The same order each time: the chats of the list, which have a
		// last message, before the others.
		slices.SortFunc(members, func(a, b peerRecord) int {
			return cmp.Or(cmp.Compare(c.top[b.key()], c.top[a.key()]), cmp.Compare(a.ID, b.ID))
		})
		return peerRecord{}, members, false
	}
	p, members, ok := known()
	if ok {
		return p, nil
	}
	const tries = 3
	for i, m := range members {
		if i == tries {
			break
		}
		switch m.Kind {
		case "channel":
			if res, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: m.ID, AccessHash: m.Hash}); err == nil {
				s.rememberPeers(res.Users, res.Chats)
			}
		case "user":
			if res, err := api.UsersGetFullUser(ctx, &tg.InputUser{UserID: m.ID, AccessHash: m.Hash}); err == nil {
				s.rememberPeers(res.Users, res.Chats)
			}
		}
		if p, _, ok := known(); ok {
			return p, nil
		}
		c.mu.Lock()
		top := c.top[m.key()]
		c.mu.Unlock()
		if top == 0 {
			continue
		}
		var id constant.TDLibPeerID = constant.TDLibPeerID(community)
		res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannelFromMessage{Peer: m.input(), MsgID: top, ChannelID: id.ToPlain()}})
		if err == nil {
			s.rememberPeers(nil, res.GetChats())
		}
		if p, _, ok := known(); ok {
			return p, nil
		}
	}
	return peerRecord{}, errNoCommunity
}

func (s *Store) setCommunity(c model.Community) {
	st := &s.communities
	st.mu.Lock()
	if st.read == nil {
		st.read = map[int64]model.Community{}
	}
	if c.Err != nil {
		// What was read before stays shown.
		was := st.read[c.ID]
		c.Title, c.About, c.Peers = was.Title, was.About, was.Peers
	}
	st.read[c.ID] = c
	st.mu.Unlock()
	s.changed()
}

// communityPeers are the chats linked, as the community lists them: those
// in the account's list as they are there.
func (s *Store) communityPeers(linked []tg.CommunityPeer, chats []tg.ChatClass, users []tg.UserClass) []model.CommunityPeer {
	s.mu.RLock()
	self := s.me.ID
	listed := make(map[int64]model.Chat, len(s.chats))
	for _, c := range s.chats {
		listed[c.ID] = c
	}
	s.mu.RUnlock()
	l := newList(self)
	l.add(&tg.MessagesDialogs{Chats: chats, Users: users})
	out := make([]model.CommunityPeer, 0, len(linked))
	for _, p := range linked {
		chat, _ := l.chat(&tg.Dialog{Peer: p.Peer})
		visible, set := p.GetVisible()
		peer := model.CommunityPeer{Chat: chat, CanView: p.CanViewHistory, Hidden: set && !visible}
		switch q := p.Peer.(type) {
		case *tg.PeerChannel:
			if ch := l.channels[q.ChannelID]; ch != nil {
				peer.Joined, peer.Public = !ch.Left, channelUsername(ch) != ""
			}
		case *tg.PeerChat:
			if g := l.groups[q.ChatID]; g != nil {
				peer.Joined = !g.Left
			}
		case *tg.PeerUser:
			_, peer.Joined = listed[chat.ID]
		}
		if c, ok := listed[chat.ID]; ok {
			peer.Chat = c
		}
		if peer.Chat.Title == "" && chat.Kind != model.KindUser && chat.Kind != model.KindBot {
			continue // a chat Telegram told nothing of
		}
		out = append(out, peer)
	}
	return out
}

// Community implements model.CommunitySource.
func (s *Store) Community(community int64) model.Community {
	st := &s.communities
	st.mu.Lock()
	defer st.mu.Unlock()
	c := st.read[community]
	c.ID = community
	s.history.mu.Lock()
	if p, ok := s.history.peers[community]; ok && p.Kind == "community" {
		if c.Title == "" {
			c.Title = p.Name
		}
		c.Collapsed = p.Collapsed
	}
	s.history.mu.Unlock()
	return c
}

// LinkedCommunity implements model.CommunitySource: the community of a chat
// the store has heard of, in the list or not.
func (s *Store) LinkedCommunity(chat int64) int64 {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peers[chat].Community
}

// SetCommunityCollapsed implements model.CommunitySource. The list has it at
// once, and as it was when Telegram refuses it.
func (s *Store) SetCommunityCollapsed(ctx context.Context, community int64, collapsed bool) error {
	ctx, api, peer, _, done, err := s.peerOperation(ctx, community)
	if err != nil {
		return err
	}
	defer done()
	if peer.Kind != "community" {
		return errors.New("tgstore: not a community")
	}
	// The row the list had, pinned or not, to have again when refused.
	row := model.Chat{ID: community, Kind: model.KindCommunity, Title: peer.Name}
	s.mu.RLock()
	for _, c := range s.chats {
		if c.ID == community && c.Kind == model.KindCommunity {
			row = c
		}
	}
	s.mu.RUnlock()
	show := func(collapsed bool) {
		s.setCommunityCollapsedPeer(community, collapsed)
		s.publish(func() { s.chats = model.WithCommunityShown(s.chats, row, collapsed) })
	}
	show(collapsed)
	updates, err := api.CommunitiesToggleCommunityCollapsedInDialogs(ctx, &tg.CommunitiesToggleCommunityCollapsedInDialogsRequest{
		Collapsed: collapsed,
		Community: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash},
	})
	if err != nil {
		show(!collapsed)
		return err
	}
	_ = s.Handle(ctx, updates)
	_ = s.persistDialogs(ctx)
	return nil
}

// setCommunityCollapsedPeer keeps whether community is shown as one chat.
func (s *Store) setCommunityCollapsedPeer(community int64, collapsed bool) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.peers[community]; ok {
		p.Collapsed = collapsed
		c.peers[community] = p
	}
}

// applyCommunities puts in the list what updates tell of communities and
// of the chats linked to them: a community shown as one chat or apart from
// another device, with its row or without, a chat added to one or taken
// out. The caller holds no lock.
func (s *Store) applyCommunities(users []tg.UserClass, chats []tg.ChatClass) {
	linked := map[int64]int64{}
	collapsed := map[int64]bool{}
	titles := map[int64]string{}
	for _, u := range users {
		if u, ok := u.(*tg.User); ok && !u.Min {
			linked[peerID(&tg.PeerUser{UserID: u.ID})] = linkedCommunity(u.GetLinkedCommunityID())
		}
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Channel:
			if !c.Min {
				linked[peerID(&tg.PeerChannel{ChannelID: c.ID})] = linkedCommunity(c.GetLinkedCommunityID())
			}
		case *tg.Community:
			if !c.Min {
				id := peerID(&tg.PeerChannel{ChannelID: c.ID})
				collapsed[id], titles[id] = c.CollapsedInDialogs, c.Title
			}
		}
	}
	if len(linked) == 0 && len(collapsed) == 0 {
		return
	}
	s.mu.Lock()
	rows := map[int64]model.Chat{}
	changed := false
	for i := range s.chats {
		c := &s.chats[i]
		if c.Kind == model.KindCommunity {
			rows[c.ID] = *c
			continue
		}
		if v, ok := linked[c.ID]; ok && c.Community != v {
			if !changed {
				s.chats = append([]model.Chat(nil), s.chats...)
				c = &s.chats[i]
				changed = true
			}
			c.Community = v
		}
	}
	for id, v := range collapsed {
		row, ok := rows[id]
		if ok == v && row.Collapsed == v {
			continue
		}
		if !ok {
			row = model.Chat{ID: id, Title: titles[id]}
		}
		s.chats = model.WithCommunityShown(s.chats, row, v)
		changed = true
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}
