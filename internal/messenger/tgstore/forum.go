// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"sort"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// A forum is a group divided in topics. Opening one lists its topics
// (messages.getForumTopics); a topic is opened as a thread of the group,
// like the comments to a post, whose root is the topic's first message
// (messages.getReplies): see threads.go.

// topicPage is how many topics a page asks for.
const topicPage = 30

// forumRefresh is how old a forum's topics are before opening it reads them
// again.
const forumRefresh = 30 * time.Second

// forum is the topics of a forum read so far.
type forum struct {
	topics  []topicEntry
	loading bool
	more    bool
	err     error
	loaded  time.Time
	// again is set when the topics changed under the reader, and asks for
	// them to be read once more.
	again bool
}

// topicEntry is a topic and what the store needs of it besides.
type topicEntry struct {
	model.Topic
	// top is the topic's last message.
	top int
}

// forumOf returns the forum chat's topics, made if there are none. The
// caller holds c.mu.
func (c *conversation) forumOf(chat int64) *forum {
	if c.forums == nil {
		c.forums = map[int64]*forum{}
	}
	f := c.forums[chat]
	if f == nil {
		f = &forum{}
		c.forums[chat] = f
	}
	return f
}

// OpenForum implements model.ForumSource.
func (s *Store) OpenForum(chat int64) {
	c := s.history
	c.mu.Lock()
	f := c.forumOf(chat)
	stale := time.Since(f.loaded) > forumRefresh
	c.mu.Unlock()
	if stale {
		s.readTopics(chat, false)
	}
}

// Topics implements model.ForumSource.
func (s *Store) Topics(chat int64) model.TopicList {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.forums[chat]
	if f == nil {
		return model.TopicList{}
	}
	list := model.TopicList{Loading: f.loading, More: f.more, Err: f.err, Topics: make([]model.Topic, len(f.topics))}
	for i, t := range f.topics {
		list.Topics[i] = t.Topic
	}
	return list
}

// LoadMoreTopics implements model.ForumSource.
func (s *Store) LoadMoreTopics(chat int64) {
	c := s.history
	c.mu.Lock()
	f := c.forums[chat]
	ok := f != nil && f.more && !f.loading
	c.mu.Unlock()
	if ok {
		s.readTopics(chat, true)
	}
}

// readTopics reads a page of the forum's topics in the background: the
// first, replacing what is known, or the one after it.
func (s *Store) readTopics(chat int64, more bool) {
	c := s.history
	c.mu.Lock()
	f := c.forumOf(chat)
	if f.loading {
		f.again = f.again || !more
		c.mu.Unlock()
		return
	}
	api, peer := c.api, c.peers[chat]
	if c.closing || api == nil || peer.ID == 0 {
		c.mu.Unlock()
		return
	}
	f.loading, f.err = true, nil
	req := &tg.MessagesGetForumTopicsRequest{Peer: peer.input(), Limit: topicPage}
	if more && len(f.topics) > 0 {
		last := f.topics[len(f.topics)-1]
		req.OffsetID, req.OffsetTopic = last.top, last.ID
		req.OffsetDate = int(last.LastTime.Unix())
	}
	c.mu.Unlock()
	s.changed()
	c.wg.Go(func() {
		defer crash.Recover("forum", func(p *crash.Panic) { s.topicsFailed(chat, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		res, err := api.MessagesGetForumTopics(ctx, req)
		if err != nil {
			s.topicsFailed(chat, err)
			return
		}
		s.rememberPeers(res.Users, res.Chats)
		s.topicsRead(chat, res, more)
	})
}

func (s *Store) topicsFailed(chat int64, err error) {
	c := s.history
	c.mu.Lock()
	f := c.forumOf(chat)
	f.loading, f.err = false, err
	c.mu.Unlock()
	s.changed()
}

// topicsRead puts a page of topics among the forum's.
func (s *Store) topicsRead(chat int64, res *tg.MessagesForumTopics, more bool) {
	c := s.history
	names := map[int64]string{}
	users := map[int64]*tg.User{}
	for _, u := range res.Users {
		if user, ok := u.(*tg.User); ok {
			names[user.ID], users[user.ID] = userName(user), user
		}
	}
	messages := map[int]tg.MessageClass{}
	for _, m := range res.Messages {
		messages[m.GetID()] = m
	}
	s.mu.RLock()
	var group model.Chat
	for _, ch := range s.chats {
		if ch.ID == chat {
			group = ch
		}
	}
	s.mu.RUnlock()
	me := s.Me().ID
	var page []topicEntry
	for _, raw := range res.Topics {
		t, ok := raw.(*tg.ForumTopic)
		if !ok || t.Hidden {
			continue
		}
		e := topicEntry{top: t.TopMessage, Topic: convertTopic(t)}
		if m, ok := messages[t.TopMessage]; ok {
			e.LastMessage, e.LastTime = preview(m)
			switch m := m.(type) {
			case *tg.MessageService:
				converted, _ := convertMessage("", m, names)
				e.LastMessage = serviceSummary(converted, group)
			case *tg.Message:
				switch {
				case m.Out:
					e.LastSender = "Вы"
				default:
					if from, ok := m.GetFromID(); ok {
						if user, ok := from.(*tg.PeerUser); ok && user.UserID != me {
							if u := users[user.UserID]; u != nil {
								e.LastSender = u.FirstName
							}
						}
					}
				}
			}
		}
		page = append(page, e)
	}
	c.mu.Lock()
	f := c.forumOf(chat)
	if more {
		known := map[int]bool{}
		for _, t := range f.topics {
			known[t.ID] = true
		}
		for _, t := range page {
			if !known[t.ID] {
				f.topics = append(f.topics, t)
			}
		}
	} else {
		f.topics = page
	}
	f.more = len(res.Topics) >= topicPage && res.Count > len(f.topics)
	f.loading, f.err, f.loaded = false, nil, time.Now()
	again := f.again
	f.again = false
	c.mu.Unlock()
	s.changed()
	if again {
		s.readTopics(chat, false)
	}
}

// convertTopic is a topic as Telegram describes it.
func convertTopic(t *tg.ForumTopic) model.Topic {
	out := model.Topic{
		ID: t.ID, Title: t.Title, IconColor: t.IconColor, General: t.ID == model.GeneralTopic,
		Closed: t.Closed, Pinned: t.Pinned, Unread: t.UnreadCount, Mentions: t.UnreadMentionsCount,
	}
	out.IconEmoji, _ = t.GetIconEmojiID()
	out.Muted = mutedNow(t.NotifySettings)
	return out
}

// noteTopic keeps a forum's list of topics up to date with a message that
// came: its topic shows it, and counts it unread when it is new and not the
// account's own. A topic made or changed is read again. The caller holds
// c.mu.
func (c *conversation) noteTopic(m model.Message, isNew bool) (refresh bool) {
	f := c.forums[m.Key.ChatID]
	if f == nil || f.loaded.IsZero() {
		return false
	}
	if m.Service != nil {
		switch m.Service.Kind {
		case model.ServiceTopicCreate, model.ServiceTopicEdit:
			return true
		}
	}
	id := m.TopicID()
	for i := range f.topics {
		t := &f.topics[i]
		if t.ID != id {
			continue
		}
		if int(m.Key.MessageID) < t.top {
			return false
		}
		t.top = int(m.Key.MessageID)
		text, sender := topicPreview(m)
		t.LastMessage, t.LastSender, t.LastTime = text, sender, m.Date
		if isNew && !m.Outgoing {
			t.Unread++
		}
		// The topic takes its place: after the pinned ones, before older
		// topics. Telegram's dates are whole seconds, and of two messages
		// of one second the later has the greater id.
		sort.SliceStable(f.topics, func(a, b int) bool {
			x, y := f.topics[a], f.topics[b]
			if x.Pinned != y.Pinned {
				return x.Pinned
			}
			if !x.LastTime.Equal(y.LastTime) {
				return x.LastTime.After(y.LastTime)
			}
			return x.top > y.top
		})
		return false
	}
	// A message of a topic not listed: the list is out of date.
	return true
}

// topicPreview is what a topic shows of its last message m, and who wrote
// it, as the chat list shows a group's.
func topicPreview(m model.Message) (text, sender string) {
	var chat model.Chat
	chat.Kind = model.KindGroup
	setPreview(&chat, m)
	return chat.LastMessage, chat.LastSender
}

// readTopic marks topic read up to message id: the unread count of the
// topic goes, if id is its last message.
func (c *conversation) readTopic(group int64, topic int, id int) {
	f := c.forums[group]
	if f == nil {
		return
	}
	for i := range f.topics {
		if f.topics[i].ID == topic && id >= f.topics[i].top {
			f.topics[i].Unread, f.topics[i].Mentions = 0, 0
		}
	}
}

// topicChat is the thread chat that shows topic, made the first time it
// is asked for. The caller holds c.mu.
func (c *conversation) topicChat(chat int64, topic model.Topic) model.Chat {
	if c.threads == nil {
		c.threads, c.threadIDs = map[int64]*thread{}, map[model.MessageKey]int64{}
	}
	key := model.MessageKey{ChatID: chat, MessageID: model.MessageID(topic.ID)}
	out := model.Chat{Kind: model.KindGroup, Title: topic.Title}
	if id, ok := c.threadIDs[key]; ok {
		out.ID = id
		return out
	}
	out.ID = threadBase - int64(len(c.threadIDs))
	c.threadIDs[key] = out.ID
	c.threads[out.ID] = &thread{post: key, group: chat, top: topic.ID, topic: true}
	return out
}

// OpenTopic implements model.ForumSource.
func (s *Store) OpenTopic(chat int64, topic model.Topic) model.Chat {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.topicChat(chat, topic)
	// Opened again, it opens at its end, not where a search went.
	delete(c.views, out.ID)
	if h := c.histories[out.ID]; h != nil && h.Err == nil {
		return out
	}
	if c.closing {
		return out
	}
	c.histories[out.ID] = &model.History{LoadingOlder: true, HasOlder: true}
	c.wg.Go(func() {
		defer crash.Recover("topic", func(p *crash.Panic) { s.threadFailed(out.ID, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if err := s.threadPage(ctx, out.ID, 0); err != nil {
			s.threadFailed(out.ID, err)
		}
	})
	return out
}

// isTopic reports whether the thread chat is a forum topic. The caller
// holds c.mu.
func (c *conversation) isTopic(chat int64) bool {
	t := c.threads[chat]
	return t != nil && t.topic
}

// markTopicRead marks the topic that thread chat shows read up to id, with
// messages.readDiscussion, as MarkRead does for a chat; the comments to a post
// are not marked.
func (s *Store) markTopicRead(chat int64, id model.MessageID, asked bool) {
	gs := &s.ghost
	gs.mu.Lock()
	if !asked && !gs.ghost.SendRead || gs.read[chat] >= id {
		gs.mu.Unlock()
		return
	}
	c := s.history
	c.mu.Lock()
	t := c.threads[chat]
	if t == nil || !t.topic {
		c.mu.Unlock()
		gs.mu.Unlock()
		return
	}
	group, top := c.peers[t.group], t.top
	c.readTopic(t.group, top, int(id))
	c.mu.Unlock()
	if gs.read == nil {
		gs.read = map[int64]model.MessageID{}
	}
	gs.read[chat] = id
	gs.mu.Unlock()
	s.changed()
	if group.ID == 0 {
		return
	}
	s.goSend("read", func(ctx context.Context, api *tg.Client) error {
		_, err := api.MessagesReadDiscussion(ctx, &tg.MessagesReadDiscussionRequest{Peer: group.input(), MsgID: top, ReadMaxID: int(id)})
		return err
	})
}
