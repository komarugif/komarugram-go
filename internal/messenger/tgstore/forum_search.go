// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// SearchForum implements model.ForumSearcher: messages.search over the
// whole forum, whose answer has the topics of the messages it found, as
// Telegram Desktop searches a forum from its list of topics. Found messages
// are not saved to the history, as a chat's search does not save them.
func (s *Store) SearchForum(ctx context.Context, forum int64, text, next string, limit int) (model.ForumSearchPage, error) {
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[forum]
	topics := map[int]model.Topic{}
	if f := c.forums[forum]; f != nil {
		for _, t := range f.topics {
			topics[t.ID] = t.Topic
		}
	}
	c.mu.Unlock()
	text = strings.TrimSpace(text)
	if text == "" {
		return model.ForumSearchPage{}, nil
	}
	if api == nil || peer.ID == 0 {
		// The cache keeps no topic's messages apart.
		return model.ForumSearchPage{}, model.ErrSearchOffline
	}
	offsetID, _ := strconv.Atoi(next)
	res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: peer.input(), Q: text, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offsetID, Limit: limit})
	if err != nil {
		return model.ForumSearchPage{}, err
	}
	mod, ok := res.AsModified()
	if !ok {
		return model.ForumSearchPage{}, nil
	}
	s.rememberPeers(mod.GetUsers(), mod.GetChats())
	if withTopics, ok := mod.(interface{ GetTopics() []tg.ForumTopicClass }); ok {
		for _, raw := range withTopics.GetTopics() {
			if t, ok := raw.(*tg.ForumTopic); ok {
				topics[t.ID] = convertTopic(t)
			}
		}
	}
	raw := mod.GetMessages()
	c.apply.Lock()
	msgs, err := s.convert(ctx, raw, false, math.MaxUint64)
	c.apply.Unlock()
	if err != nil {
		return model.ForumSearchPage{}, err
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Key.MessageID > msgs[j].Key.MessageID })
	page := model.ForumSearchPage{Count: repliesCount(res)}
	for _, m := range msgs {
		id := m.TopicID()
		t, ok := topics[id]
		if !ok {
			t = model.Topic{ID: id, General: id == model.GeneralTopic}
		}
		page.Found = append(page.Found, model.FoundInTopic{Message: m, Topic: t})
	}
	if len(raw) == limit {
		page.Next = strconv.Itoa(raw[len(raw)-1].GetID())
	}
	return page, nil
}

// OpenTopicAt implements model.ForumSearcher: the topic's thread loads
// around message at, and its page opens there. While the topic's history
// is on its way already, only the page's place is set: it goes to the
// message if the history brings it.
func (s *Store) OpenTopicAt(chat int64, topic model.Topic, at model.MessageID) model.Chat {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.topicChat(chat, topic)
	c.views[out.ID] = model.Viewport{AccountID: c.account, ChatID: out.ID, AnchorMessageID: at, UpdatedAt: time.Now()}
	if h := c.histories[out.ID]; c.closing || h != nil && (h.LoadingOlder || h.LoadingNewer) {
		return out
	}
	c.histories[out.ID] = &model.History{LoadingOlder: true, HasOlder: true}
	c.wg.Go(func() {
		defer crash.Recover("topic", func(p *crash.Panic) { s.threadFailed(out.ID, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if err := s.threadAround(ctx, out.ID, at); err != nil {
			s.threadFailed(out.ID, err)
		}
	})
	return out
}
