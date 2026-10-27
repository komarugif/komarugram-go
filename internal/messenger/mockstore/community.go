// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"time"

	"komarugram/internal/messenger/model"
)

// The demo has two communities: one shown as one chat, of the demo's Go
// channel and forum, and one shown apart, of its channel of photos, which,
// as Telegram lists them, has no dialog of its own. Each lists chats the
// account is not in too: ones it can read, one it can ask to join, and a
// hidden one; those it can read open from it, not in the list, linked.

const (
	// DemoCommunity is the community shown as one chat.
	DemoCommunity int64 = 900001
	// DemoPhotoCommunity is the community shown apart.
	DemoPhotoCommunity int64 = 900002
)

// demoCommunityPeers are the chats of the communities the account is not
// in, by community.
var demoCommunityPeers = map[int64][]model.CommunityPeer{
	DemoCommunity: {
		{Chat: model.Chat{ID: 900011, Kind: model.KindChannel, Title: "Go: вакансии", Members: 18240, Community: DemoCommunity}, CanView: true},
		{Chat: model.Chat{ID: 900012, Kind: model.KindGroup, Title: "Go-митапы в Москве", Members: 640, Community: DemoCommunity}},
		{Chat: model.Chat{ID: 900013, Kind: model.KindGroup, Title: "Мейнтейнеры", Members: 14, Community: DemoCommunity}, Hidden: true},
		// Hidden, they open all the same, as in Telegram for Android: a bot,
		// and a public channel.
		{Chat: model.Chat{ID: 900014, Kind: model.KindBot, Title: "Бот поддержки Go", Community: DemoCommunity}, Hidden: true},
		{Chat: model.Chat{ID: 900015, Kind: model.KindChannel, Title: "Go: анонсы", Members: 920, Community: DemoCommunity}, Hidden: true, Public: true},
	},
	DemoPhotoCommunity: {
		{Chat: model.Chat{ID: 900021, Kind: model.KindChannel, Title: "Фотокритика", Members: 2210, Community: DemoPhotoCommunity}, CanView: true},
	},
}

// demoCommunityMembers are the demo's chats in its communities.
var demoCommunityMembers = map[int64]int64{4: DemoCommunity, DemoForum: DemoCommunity, 13: DemoPhotoCommunity}

// demoCommunityTitles are the names of the demo's communities.
var demoCommunityTitles = map[int64]string{DemoCommunity: "Сообщество Go", DemoPhotoCommunity: "Фотоклуб"}

// withDemoCommunities is chats with the demo's community shown as one chat,
// and its chats in them linked to them.
func withDemoCommunities(chats []model.Chat) []model.Chat {
	for i := range chats {
		chats[i].Community = demoCommunityMembers[chats[i].ID]
	}
	return append(chats, demoCommunityRow(DemoCommunity))
}

// demoCommunityRow is the dialog of a community shown as one chat.
func demoCommunityRow(community int64) model.Chat {
	return model.Chat{ID: community, Kind: model.KindCommunity, Title: demoCommunityTitles[community], Collapsed: true}
}

// OpenCommunity implements model.CommunitySource: the demo has its
// communities at once.
func (s *Store) OpenCommunity(int64) {}

// Community implements model.CommunitySource.
func (s *Store) Community(community int64) model.Community {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := model.Community{ID: community}
	switch community {
	case DemoCommunity:
		out.About = "Каналы и чаты о Go: новости, обсуждения, работа и встречи."
	case DemoPhotoCommunity:
		out.About = "Фотографии природы и их разбор."
	default:
		return out
	}
	out.Title = demoCommunityTitles[community]
	_, out.Collapsed = chatByID(s.chats, community)
	for _, c := range model.CommunityMembers(s.chats, community) {
		out.Peers = append(out.Peers, model.CommunityPeer{Chat: c, Joined: true, CanView: true})
	}
	out.Peers = append(out.Peers, demoCommunityPeers[community]...)
	return out
}

// LinkedCommunity implements model.CommunitySource.
func (s *Store) LinkedCommunity(chat int64) int64 {
	if id, ok := demoCommunityMembers[chat]; ok {
		return id
	}
	for community, peers := range demoCommunityPeers {
		for _, p := range peers {
			if p.Chat.ID == chat {
				return community
			}
		}
	}
	return 0
}

// SetCommunityCollapsed implements model.CommunitySource: as Telegram's
// dialogs, the community has a row while shown as one chat.
func (s *Store) SetCommunityCollapsed(ctx context.Context, community int64, collapsed bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond): // As long as a round trip.
	}
	if _, ok := demoCommunityTitles[community]; !ok {
		return errors.New("mockstore: no such community")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chats = model.WithCommunityShown(s.chats, demoCommunityRow(community), collapsed)
	return nil
}

// chatByID finds the chat id in chats.
func chatByID(chats []model.Chat, id int64) (model.Chat, bool) {
	for _, c := range chats {
		if c.ID == id {
			return c, true
		}
	}
	return model.Chat{}, false
}
