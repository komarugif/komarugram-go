// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gotd/td/tg"
	"komarugram/internal/messenger/model"
)

type outgoingForward struct {
	Source peerRecord
	IDs    []model.MessageID
}

func (s *Store) queueForward(ctx context.Context, from int64, ids []model.MessageID, to int64, wait bool) error {
	if len(ids) == 0 {
		return nil
	}
	if !s.CanSend(to) {
		return errors.New("cannot forward to this chat")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	id := int64(binary.LittleEndian.Uint64(random[:]))
	if id == 0 {
		id = 1
	}
	shownFrom := from
	from = s.realChat(from)
	c := s.history
	c.mu.Lock()
	source, knownSource := c.peers[from]
	real, top := c.threadChat(to)
	target, knownTarget := c.peers[real]
	if c.isTopic(to) && top == model.GeneralTopic {
		top = 0
	}
	var threadCopy *thread
	if thread := c.threads[to]; thread != nil {
		copy := *thread
		threadCopy = &copy
	}
	shown := map[model.MessageID]model.Message{}
	if h := c.histories[shownFrom]; h != nil {
		for _, m := range h.Messages {
			shown[m.Key.MessageID] = m
		}
	}
	cache := c.cache
	c.mu.Unlock()
	if !knownSource || !knownTarget || cache == nil {
		return errors.New("unknown forwarding peer")
	}
	e := &outgoingEntry{Chat: to, Peer: target, Top: top, Request: model.OutgoingMessage{RandomID: id}, Created: time.Now(), Done: map[int64]int{}, Forward: &outgoingForward{Source: source, IDs: slices.Clone(ids)}, result: &outgoingResult{done: make(chan struct{})}}
	if threadCopy != nil {
		e.ThreadPost, e.Topic, e.ThreadTop = threadCopy.post, threadCopy.topic, threadCopy.top
	}
	e.LocalEmoji = s.localPremium.Load() && !s.Premium().Active
	for _, messageID := range ids {
		if messageID <= 0 {
			return errors.New("cannot forward a local message")
		}
		m, found := shown[messageID]
		if !found {
			m, found, _ = cache.Message(ctx, from, int(messageID))
		}
		if !found {
			_, _ = cache.Get(ctx, fmt.Sprintf("lookup/%d/%d", from, messageID), &m)
		}
		if m.NoForwards {
			return errors.New("message forwarding is protected")
		}
		pending, err := s.outgoingMessages(to, model.OutgoingMessage{Text: m.Text, Entities: m.Entities}, e.Created)
		if err != nil {
			return err
		}
		p := pending[0]
		p.msg.Kind, p.msg.Media, p.msg.Todo, p.msg.Poll = m.Kind, m.Media, m.Todo, m.Poll
		p.msg.ForwardFromID, p.msg.ForwardName, p.msg.ForwardPostID = m.ForwardFromID, m.ForwardName, m.ForwardPostID
		if p.msg.ForwardFromID == 0 && p.msg.ForwardName == "" {
			p.msg.ForwardFromID = m.SenderID
			p.msg.ForwardName = m.SenderName
		}
		if p.msg.ForwardFromID == 0 && p.msg.ForwardName == "" {
			p.msg.ForwardFromID = from
			p.msg.ForwardName = source.Name
		}
		if m.GroupedID != 0 {
			p.msg.GroupedID = id ^ m.GroupedID
			if p.msg.GroupedID == 0 {
				p.msg.GroupedID = 1
			}
		}
		p.msg.ContentRevision = model.Revision(p.msg)
		e.Messages = append(e.Messages, p.msg)
		e.pending = append(e.pending, p)
	}
	q := &s.outbox
	if err := func() error {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.closed {
			return errors.New("outbox: closed")
		}
		if err := cache.PutOutgoing(ctx, id, e, nil); err != nil {
			return err
		}
		c.mu.Lock()
		c.pending.list = append(c.pending.list, e.pending...)
		c.touchPending(to)
		c.mu.Unlock()
		q.entries = append(q.entries, e)
		return nil
	}(); err != nil {
		return err
	}
	s.changed()
	s.startOutgoing()
	if wait {
		return s.waitOutgoingResult(ctx, e)
	}
	return nil
}

func (s *Store) deliverForward(ctx context.Context, e *outgoingEntry) error {
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return errNotConnected
	}
	if !s.CanSend(e.Chat) {
		return errors.New("cannot forward to this chat")
	}
	for start := 0; start < len(e.Forward.IDs); start += 100 {
		end := min(start+100, len(e.Forward.IDs))
		req := &tg.MessagesForwardMessagesRequest{FromPeer: e.Forward.Source.input(), ToPeer: e.Peer.input(), TopMsgID: e.Top}
		for i := start; i < end; i++ {
			identity := outgoingRandomID(e.Request.RandomID, i)
			if _, done := e.Done[identity]; done {
				continue
			}
			req.ID = append(req.ID, int(e.Forward.IDs[i]))
			req.RandomID = append(req.RandomID, identity)
		}
		if len(req.ID) == 0 {
			continue
		}
		res, err := api.MessagesForwardMessages(ctx, req)
		if err != nil {
			return fmt.Errorf("forward: %w", err)
		}
		if err := s.acceptOutgoing(ctx, e, res, req.RandomID); err != nil {
			return err
		}
		_ = s.Handle(ctx, res)
	}
	return s.restoreOutgoingEmoji(ctx, e)
}
