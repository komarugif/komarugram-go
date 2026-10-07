// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// The comments to a channel post are a thread of its discussion group: the
// post's copy in the group, and the messages that reply to it there
// (messages.getDiscussionMessage, messages.getReplies). The UI shows a
// thread as a chat of its own, under an id this store makes up for the
// session; everything asked of that chat is asked of the group.
//
// A thread's messages are not saved to the group's history in the cache: they
// are single messages from anywhere in it, and the cache keeps each chat's
// history without gaps.

// threadBase is below every peer id: thread ids count down from it.
const threadBase = -(int64(1) << 62)

// threadPage is how many comments a page asks for.
const threadPage = 50

// thread is the discussion of a channel post.
type thread struct {
	// post is the channel post.
	post model.MessageKey
	// group is the discussion group and top the post's copy there, the
	// thread's root; both 0 until the discussion is found.
	group int64
	top   int
	// root is the root message, album parts included.
	root []model.Message
	// topic is set when the thread is a topic of a forum: its group and
	// root are known from the start, and its messages are those of the
	// topic.
	topic bool
}

func isThread(chat int64) bool { return chat <= threadBase }

// realChat returns the chat a thread chat lives in, 0 while it is not
// known, or chat itself for any other chat.
func (s *Store) realChat(chat int64) int64 {
	if !isThread(chat) {
		return chat
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	group, _ := c.threadChat(chat)
	return group
}

// threadChat returns the chat the thread chat lives in, and its root; chat
// itself and 0 for any other chat. The caller holds c.mu.
func (c *conversation) threadChat(chat int64) (int64, int) {
	if !isThread(chat) {
		return chat, 0
	}
	if t := c.threads[chat]; t != nil && t.group != 0 {
		return t.group, t.top
	}
	return 0, 0
}

// OpenComments implements model.CommentsStore.
func (s *Store) OpenComments(post model.Message) model.Chat {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads == nil {
		c.threads, c.threadIDs = map[int64]*thread{}, map[model.MessageKey]int64{}
	}
	chat := model.Chat{Kind: model.KindGroup, Title: c.peers[post.Key.ChatID].Name}
	if id, ok := c.threadIDs[post.Key]; ok {
		chat.ID = id
		// Opened again, it opens at its end, not where a search went.
		delete(c.views, id)
		if h := c.histories[id]; h != nil && h.Err == nil {
			return chat
		}
	} else {
		chat.ID = threadBase - int64(len(c.threadIDs))
		c.threadIDs[post.Key] = chat.ID
		c.threads[chat.ID] = &thread{post: post.Key}
	}
	if c.closing {
		return chat
	}
	c.histories[chat.ID] = &model.History{LoadingOlder: true, HasOlder: true}
	c.wg.Go(func() {
		defer crash.Recover("comments", func(p *crash.Panic) { s.threadFailed(chat.ID, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if err := s.openThread(ctx, chat.ID); err != nil {
			s.threadFailed(chat.ID, err)
		}
	})
	return chat
}

func (s *Store) threadFailed(id int64, err error) {
	c := s.history
	c.mu.Lock()
	if h := c.histories[id]; h != nil {
		h.LoadingOlder, h.LoadingNewer, h.Err = false, false, err
		h.Revision++
	}
	c.mu.Unlock()
	s.changed()
}

// openThread finds the discussion of the thread's post and loads its newest
// comments.
func (s *Store) openThread(ctx context.Context, id int64) error {
	c := s.history
	c.mu.Lock()
	api, t := c.api, c.threads[id]
	channel := c.peers[t.post.ChatID]
	c.mu.Unlock()
	if api == nil {
		return errNotConnected
	}
	found, err := api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{Peer: channel.input(), MsgID: int(t.post.MessageID)})
	if err != nil {
		return err
	}
	s.rememberPeers(found.Users, found.Chats)
	if len(found.Messages) == 0 {
		return errors.New("the post has no discussion")
	}
	peer, ok := messagePeer(found.Messages[0])
	if !ok {
		return errors.New("the discussion has no group")
	}
	group, top := peerID(peer), found.Messages[0].GetID()
	for _, m := range found.Messages {
		top = min(top, m.GetID())
	}
	root, err := s.threadMessages(ctx, found.Messages)
	if err != nil {
		return err
	}
	c.mu.Lock()
	t.group, t.top, t.root = group, top, root
	c.mu.Unlock()
	return s.threadPage(ctx, id, 0)
}

// threadMessages converts messages of a thread without saving them to the
// cache's history.
func (s *Store) threadMessages(ctx context.Context, raw []tg.MessageClass) ([]model.Message, error) {
	c := s.history
	c.apply.Lock()
	defer c.apply.Unlock()
	msgs, err := s.convert(ctx, raw, false, 0)
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Key.MessageID < msgs[j].Key.MessageID })
	return msgs, err
}

// threadPage loads the comments before offset, the newest ones for 0, and
// puts them in the thread's history, with the root once the first comment
// is in.
func (s *Store) threadPage(ctx context.Context, id int64, offset int) error {
	c := s.history
	c.mu.Lock()
	api, t := c.api, c.threads[id]
	group := c.peers[t.group]
	top := t.top
	c.mu.Unlock()
	if api == nil {
		return errNotConnected
	}
	res, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: group.input(), MsgID: top, OffsetID: offset, Limit: threadPage})
	if err != nil {
		return err
	}
	page, ok := res.AsModified()
	if !ok {
		return errors.New("unexpected replies")
	}
	s.rememberPeers(page.GetUsers(), page.GetChats())
	msgs, err := s.threadMessages(ctx, page.GetMessages())
	if err != nil {
		return err
	}
	c.mu.Lock()
	h := c.histories[id]
	if h == nil {
		c.mu.Unlock()
		return nil
	}
	older := len(page.GetMessages()) >= threadPage
	merged := append(msgs, h.Messages...)
	if !older {
		merged = append(append([]model.Message(nil), t.root...), merged...)
	}
	h.Messages = dedupe(merged)
	h.HasOlder, h.LoadingOlder, h.Err = older, false, nil
	h.ThreadRoot = model.MessageID(top)
	h.Count, h.Counted = repliesCount(res), true
	h.Revision++
	c.mu.Unlock()
	s.changed()
	return nil
}

// dedupe drops repeated messages from msgs, sorted by id, keeping the
// later copy.
func dedupe(msgs []model.Message) []model.Message {
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Key.MessageID < msgs[j].Key.MessageID })
	out := msgs[:0]
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Key == m.Key {
			out[n-1] = m
			continue
		}
		out = append(out, m)
	}
	return out
}

// loadOlderComments loads the page of comments before the ones shown.
func (s *Store) loadOlderComments(id int64) {
	c := s.history
	c.mu.Lock()
	h, t := c.histories[id], c.threads[id]
	if c.closing || h == nil || t == nil || t.group == 0 || !h.HasOlder || h.LoadingOlder || len(h.Messages) == 0 {
		c.mu.Unlock()
		return
	}
	h.LoadingOlder = true
	offset := int(h.Messages[0].Key.MessageID)
	c.mu.Unlock()
	c.wg.Go(func() {
		defer crash.Recover("comments", func(p *crash.Panic) { s.threadFailed(id, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if err := s.threadPage(ctx, id, offset); err != nil {
			s.threadFailed(id, err)
		}
	})
}

// mergeThreads puts a new or edited message of a discussion group into the
// threads it belongs to; a new one counts in them. The caller holds c.mu.
func (c *conversation) mergeThreads(m model.Message, isNew bool) {
	for id, t := range c.threads {
		if t.group != m.Key.ChatID || t.group == 0 {
			continue
		}
		h := c.histories[id]
		if h == nil {
			continue
		}
		in := int(m.ReplyToTopID) == t.top || int(m.ReplyToMessageID) == t.top
		if t.topic {
			// The General topic's messages have no header to say so.
			in = m.TopicID() == t.top
		}
		found := false
		for i := range h.Messages {
			if h.Messages[i].Key == m.Key {
				h.Messages[i] = m
				found = true
			}
		}
		// A window that stops short of the newest messages, after a jump to
		// the start, gets the new ones when it pages down to them.
		add := in && !h.HasNewer
		if !found && add {
			h.Messages = dedupe(append(h.Messages, m))
		}
		if in && isNew && !found && h.Counted {
			h.Count++
		}
		if found || add || in && isNew {
			h.Revision++
		}
	}
}

// threadsOf are the thread chats of a discussion group. The caller holds
// c.mu.
func (c *conversation) threadsOf(group int64) []int64 {
	var ids []int64
	for id, t := range c.threads {
		if t.group == group && group != 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// revealThread replaces a thread's history with its oldest page, or with its
// newest one, as the buttons to its start and its end do. It reports false
// when the thread cannot be loaded now. What was shown is dropped as the
// load starts, so that the page comes alone, and paged from there: down for
// the oldest, up for the newest.
func (s *Store) revealThread(id int64, first bool) bool {
	c := s.history
	c.mu.Lock()
	h, t := c.histories[id], c.threads[id]
	if c.closing || h == nil || t == nil || t.group == 0 || h.LoadingOlder || h.LoadingNewer {
		c.mu.Unlock()
		return false
	}
	h.LoadingOlder, h.HasNewer, h.Err = true, false, nil
	h.Messages = nil
	h.Revision++
	// The page opens at its end, or its start, not at a message a search
	// went to.
	delete(c.views, id)
	c.mu.Unlock()
	s.changed()
	c.wg.Go(func() {
		defer crash.Recover("thread jump", func(p *crash.Panic) { s.threadFailed(id, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if err := s.threadWindow(ctx, id, first); err != nil {
			s.threadFailed(id, err)
		}
	})
	return true
}

// revealThreadAt replaces a thread's history with the replies around
// message at, and makes its page open there, as a search's jump to a
// message does in a chat (Reveal). A thread's view is not kept otherwise:
// see SaveView.
func (s *Store) revealThreadAt(id int64, at model.MessageID) {
	c := s.history
	c.mu.Lock()
	h, t := c.histories[id], c.threads[id]
	if c.closing || h == nil || t == nil || t.group == 0 || h.LoadingOlder || h.LoadingNewer {
		c.mu.Unlock()
		return
	}
	h.LoadingOlder, h.HasNewer, h.Err = true, false, nil
	h.Messages = nil
	h.Revision++
	c.views[id] = model.Viewport{AccountID: c.account, ChatID: id, AnchorMessageID: at, UpdatedAt: time.Now()}
	c.mu.Unlock()
	s.changed()
	c.wg.Go(func() {
		defer crash.Recover("thread jump", func(p *crash.Panic) { s.threadFailed(id, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		if err := s.threadAround(ctx, id, at); err != nil {
			s.threadFailed(id, err)
		}
	})
}

// threadAround loads the replies around message at into the thread's
// emptied history: half of a page newer than it, the rest it and older,
// with the root before them when they reach the start.
func (s *Store) threadAround(ctx context.Context, id int64, at model.MessageID) error {
	newer := threadPage / 2
	page, count, err := s.threadReplies(ctx, id, int(at)+1, -newer)
	if err != nil {
		return err
	}
	before, after := 0, 0
	for _, m := range page {
		if m.Key.MessageID > at {
			after++
		} else {
			before++
		}
	}
	c := s.history
	c.mu.Lock()
	defer s.changed()
	defer c.mu.Unlock()
	h, t := c.histories[id], c.threads[id]
	if h == nil {
		return nil
	}
	older := before >= threadPage-newer
	if !older {
		page = append(append([]model.Message(nil), t.root...), page...)
	}
	h.Messages = dedupe(page)
	h.HasOlder, h.HasNewer = older, after >= newer
	h.LoadingOlder, h.Err = false, nil
	h.ThreadRoot = model.MessageID(t.top)
	h.Count, h.Counted = count, true
	h.Revision++
	return nil
}

// threadWindow loads the oldest page of the thread's replies, with the root
// before it, or the newest, into its emptied history.
func (s *Store) threadWindow(ctx context.Context, id int64, first bool) error {
	if !first {
		return s.threadPage(ctx, id, 0)
	}
	page, count, err := s.threadReplies(ctx, id, 1, -threadPage)
	if err != nil {
		return err
	}
	c := s.history
	c.mu.Lock()
	h, t := c.histories[id], c.threads[id]
	if h == nil {
		c.mu.Unlock()
		return nil
	}
	// Nothing is older than the oldest page, and the root comes first.
	h.Messages = dedupe(append(append([]model.Message(nil), t.root...), page...))
	h.HasOlder, h.HasNewer = false, len(page) >= threadPage
	h.LoadingOlder, h.Err = false, nil
	h.ThreadRoot = model.MessageID(t.top)
	h.Count, h.Counted = count, true
	h.Revision++
	c.mu.Unlock()
	s.changed()
	return nil
}

// threadReplies asks for threadPage replies of thread id from offset with
// add: the ones before offset for 0, and the ones from it on for a negative
// add. The messages come sorted, oldest first, with how many the thread has.
func (s *Store) threadReplies(ctx context.Context, id int64, offset, add int) ([]model.Message, int, error) {
	c := s.history
	c.mu.Lock()
	api, t := c.api, c.threads[id]
	group := c.peers[t.group]
	top := t.top
	c.mu.Unlock()
	if api == nil {
		return nil, 0, errNotConnected
	}
	res, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: group.input(), MsgID: top, OffsetID: offset, AddOffset: add, Limit: threadPage})
	if err != nil {
		return nil, 0, err
	}
	page, ok := res.AsModified()
	if !ok {
		return nil, 0, errors.New("unexpected replies")
	}
	s.rememberPeers(page.GetUsers(), page.GetChats())
	msgs, err := s.threadMessages(ctx, page.GetMessages())
	return msgs, repliesCount(res), err
}

// repliesCount is how many messages Telegram counts where it answered res:
// all it sent, when it sent them all.
func repliesCount(res tg.MessagesMessagesClass) int {
	switch r := res.(type) {
	case *tg.MessagesMessagesSlice:
		return r.Count
	case *tg.MessagesChannelMessages:
		return r.Count
	case *tg.MessagesMessages:
		return len(r.Messages)
	}
	return 0
}

// loadNewerComments loads the page of replies after the ones shown, when the
// history was moved to the start of the thread.
func (s *Store) loadNewerComments(id int64) {
	c := s.history
	c.mu.Lock()
	h, t := c.histories[id], c.threads[id]
	if c.closing || h == nil || t == nil || t.group == 0 || !h.HasNewer || h.LoadingNewer || h.LoadingOlder || len(h.Messages) == 0 {
		c.mu.Unlock()
		return
	}
	h.LoadingNewer = true
	after := int(h.Messages[len(h.Messages)-1].Key.MessageID)
	c.mu.Unlock()
	c.wg.Go(func() {
		defer crash.Recover("comments", func(p *crash.Panic) { s.threadFailed(id, p) })
		ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
		defer cancel()
		page, count, err := s.threadReplies(ctx, id, after+1, -threadPage)
		if err != nil {
			s.threadFailed(id, err)
			return
		}
		// Past the newest message Telegram answers with the last page again.
		page = slices.DeleteFunc(page, func(m model.Message) bool { return int(m.Key.MessageID) <= after })
		c.mu.Lock()
		if h := c.histories[id]; h != nil {
			h.Messages = dedupe(append(h.Messages, page...))
			h.HasNewer, h.LoadingNewer, h.Err = len(page) >= threadPage, false, nil
			h.Count, h.Counted = count, true
			h.Revision++
		}
		c.mu.Unlock()
		s.changed()
	})
}
