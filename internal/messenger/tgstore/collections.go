// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
)

func (p peerRecord) output() tg.PeerClass {
	switch p.Kind {
	case "channel":
		return &tg.PeerChannel{ChannelID: p.ID}
	case "chat":
		return &tg.PeerChat{ChatID: p.ID}
	default:
		return &tg.PeerUser{UserID: p.ID}
	}
}

// collectionMessage registers downloads without putting story/gift IDs in the
// message history table, where Telegram's message IDs have different semantics.
func (s *Store) collectionMessage(ctx context.Context, peer peerRecord, raw *tg.Message) (model.Message, error) {
	c := s.history
	c.mu.Lock()
	account, cache := c.account, c.cache
	c.mu.Unlock()
	raw.PeerID = peer.output()
	m, ref := convertMessage(account, raw, nil)
	if ref == nil {
		return m, nil
	}
	refs := map[string]fileLocation{m.Media.ID: *ref}
	for _, v := range m.Media.Variants {
		r := *ref
		r.Thumb = v.ID[strings.LastIndexByte(v.ID, '/')+1:]
		refs[v.ID] = r
	}
	if thumb := m.Media.Thumbnail; thumb != nil {
		r := *ref
		r.Thumb = thumb.ID[strings.LastIndexByte(thumb.ID, '/')+1:]
		if r.Thumb != "inline" {
			refs[thumb.ID] = r
		} else if cache != nil {
			if err := cache.SaveMedia(ctx, thumb.ID, thumb.Preview); err != nil {
				return m, err
			}
		}
	}
	c.mu.Lock()
	for id, r := range refs {
		c.refs[id] = r
	}
	c.mu.Unlock()
	if cache != nil {
		for id, r := range refs {
			if err := cache.Put(ctx, "ref/"+id, r); err != nil {
				return m, err
			}
		}
	}
	return m, nil
}
func (s *Store) SharedCollection(ctx context.Context, chat int64, kind model.SharedKind, cursor string, limit int) (model.SharedCollectionPage, error) {
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return model.SharedCollectionPage{}, err
	}
	defer done()
	out := model.SharedCollectionPage{}
	limit = min(100, max(1, limit))
	switch kind {
	case model.SharedStories, model.SharedStoryArchive:
		offset, _ := strconv.Atoi(cursor)
		var res *tg.StoriesStories
		var e error
		if kind == model.SharedStoryArchive {
			res, e = api.StoriesGetStoriesArchive(ctx, &tg.StoriesGetStoriesArchiveRequest{Peer: peer.input(), OffsetID: offset, Limit: limit})
		} else {
			res, e = api.StoriesGetPinnedStories(ctx, &tg.StoriesGetPinnedStoriesRequest{Peer: peer.input(), OffsetID: offset, Limit: limit})
		}
		if e != nil {
			return out, e
		}
		out.Total = res.Count
		s.rememberPeers(res.Users, res.Chats)
		last := 0
		for _, item := range res.Stories {
			last = item.GetID()
			story, ok := item.(*tg.StoryItem)
			if !ok {
				continue
			}
			m, e := s.collectionMessage(ctx, peer, &tg.Message{ID: -story.ID, Date: story.Date, Message: story.Caption, Entities: story.Entities, Media: story.Media})
			if e != nil {
				return out, e
			}
			out.Messages = append(out.Messages, m)
		}
		if len(res.Stories) >= limit && last > 0 && last != offset {
			out.Next = strconv.Itoa(last)
		}
	case model.SharedGifts:
		res, e := api.PaymentsGetSavedStarGifts(ctx, &tg.PaymentsGetSavedStarGiftsRequest{Peer: peer.input(), Offset: cursor, Limit: limit, ExcludeUnsaved: true})
		if e != nil {
			return out, e
		}
		out.Total, out.Next = res.Count, res.NextOffset
		s.rememberPeers(res.Users, res.Chats)
		for i, gift := range res.Gifts {
			m, e := s.savedGiftMessage(ctx, peer, gift, cursor, i)
			if e != nil {
				return out, e
			}
			out.Messages = append(out.Messages, m)
		}
	case model.SharedGroups:
		if peer.Kind != "user" {
			return out, nil
		}
		offset, _ := strconv.ParseInt(cursor, 10, 64)
		res, e := api.MessagesGetCommonChats(ctx, &tg.MessagesGetCommonChatsRequest{UserID: &tg.InputUser{UserID: peer.ID, AccessHash: peer.Hash}, MaxID: offset, Limit: limit})
		if e != nil {
			return out, e
		}
		chats := res.GetChats()
		out.Total = len(chats)
		if slice, ok := res.(*tg.MessagesChatsSlice); ok {
			out.Total = slice.Count
		}
		s.rememberPeers(nil, chats)
		for _, c := range chats {
			title := ""
			var id int64
			var username string
			switch c := c.(type) {
			case *tg.Chat:
				title = c.Title
				id = peerID(&tg.PeerChat{ChatID: c.ID})
			case *tg.Channel:
				title = c.Title
				id = peerID(&tg.PeerChannel{ChannelID: c.ID})
				username = c.Username
			}
			if title == "" {
				continue
			}
			m := model.Message{Key: model.MessageKey{ChatID: chat, MessageID: model.MessageID(id)}, Text: title, Date: time.Unix(0, 0), ContentRevision: 1}
			if username != "" {
				m.Buttons = [][]model.MessageButton{{{Text: "@" + username, URL: "https://t.me/" + username, Kind: "url"}}}
			}
			out.Messages = append(out.Messages, m)
		}
		if len(chats) >= limit {
			out.Next = strconv.FormatInt(chats[len(chats)-1].GetID(), 10)
			if out.Next == cursor {
				out.Next = ""
			}
		}
	default:
		return out, fmt.Errorf("unknown collection: %s", kind)
	}
	return out, nil
}
