// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/pool"
	"github.com/gotd/td/rpc"
	"github.com/gotd/td/tgerr"
	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// outgoingEntry owns the immutable request, routing and presentation data.
// Progress is committed after each accepted request, including parts of albums.
type outgoingEntry struct {
	Chat           int64
	Peer           peerRecord
	Top            int
	ThreadPost     model.MessageKey
	Topic          bool
	ThreadTop      int
	Request        model.OutgoingMessage
	Draft          *model.Draft
	Messages       []model.Message
	Created        time.Time
	Attempts       int
	RetryAt        time.Time
	Error          string
	LocalEmoji     bool
	EmojiLocal     map[int64]bool
	LocalSticker   bool
	ResolveSticker bool
	Done           map[int64]int
	Forward        *outgoingForward
	pending        []*pendingMessage
	result         *outgoingResult
}

type outgoingResult struct {
	done     chan struct{}
	err      error
	finished bool
}

type outgoingQueue struct {
	mu             sync.Mutex
	entries        []*outgoingEntry
	loaded, closed bool
	wake           chan struct{}
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func (s *Store) restoreOutgoing(ctx context.Context) error {
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.loaded {
		return nil
	}
	cache := s.Cache()
	if cache == nil {
		return errors.New("outbox: cache unavailable")
	}
	rows, err := cache.Outgoing(ctx)
	if err != nil {
		return err
	}
	var local []model.Message
	if _, err := cache.Get(ctx, "local-premium/stickers", &local); err != nil {
		return err
	}
	s.history.mu.Lock()
	s.history.localStickers = local
	for _, m := range local {
		s.history.pending.seq = max(s.history.pending.seq, -int(m.Key.MessageID)-pendingIDBase)
	}
	s.history.mu.Unlock()
	for _, row := range rows {
		e := new(outgoingEntry)
		if err := json.Unmarshal(row, e); err != nil {
			return fmt.Errorf("outbox: %w", err)
		}
		e.result = &outgoingResult{done: make(chan struct{})}
		if e.Error != "" {
			e.result.err = errors.New(e.Error)
			e.result.finished = true
			close(e.result.done)
		}
		if e.Done == nil {
			e.Done = map[int64]int{}
		}
		s.history.mu.Lock()
		if _, ok := s.history.peers[e.Peer.key()]; !ok {
			s.history.peers[e.Peer.key()] = e.Peer
		}
		if isThread(e.Chat) {
			if s.history.threads == nil {
				s.history.threads = map[int64]*thread{}
				s.history.threadIDs = map[model.MessageKey]int64{}
			}
			s.history.threads[e.Chat] = &thread{post: e.ThreadPost, group: e.Peer.key(), top: e.ThreadTop, topic: e.Topic}
			s.history.threadIDs[e.ThreadPost] = e.Chat
		}
		for index, msg := range e.Messages {
			msg.Sending, msg.SendError = true, e.Error
			p := &pendingMessage{chat: e.Chat, msg: msg, id: model.MessageID(e.Done[outgoingRandomID(e.Request.RandomID, index)])}
			e.pending = append(e.pending, p)
			s.history.pending.list = append(s.history.pending.list, p)
			if seq := -int(msg.Key.MessageID) - pendingIDBase; seq > s.history.pending.seq {
				s.history.pending.seq = seq
			}
		}
		s.history.touchPending(e.Chat)
		s.history.mu.Unlock()
		q.entries = append(q.entries, e)
	}
	q.loaded = true
	return nil
}

func (s *Store) QueueMessage(ctx context.Context, chat int64, msg model.OutgoingMessage) error {
	_, err := s.enqueueOutgoing(ctx, chat, msg)
	if err == nil {
		s.startOutgoing()
	}
	return err
}

// Send waits for delivery; the composer uses QueueMessage so that the next
// message can be written as soon as this one has been saved locally.
func (s *Store) Send(ctx context.Context, chat int64, msg model.OutgoingMessage) error {
	e, err := s.enqueueOutgoing(ctx, chat, msg)
	if err != nil {
		return err
	}
	s.startOutgoing()
	return s.waitOutgoingResult(ctx, e)
}

func (s *Store) waitOutgoingResult(ctx context.Context, e *outgoingEntry) error {
	s.outbox.mu.Lock()
	result := e.result
	s.outbox.mu.Unlock()
	select {
	case <-result.done:
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) enqueueOutgoing(ctx context.Context, chat int64, msg model.OutgoingMessage) (*outgoingEntry, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	if err := s.checkSend(chat, msg); err != nil {
		return nil, err
	}
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, errors.New("outbox: closed")
	}
	for _, e := range q.entries {
		if e.Request.RandomID == msg.RandomID {
			if e.Chat != chat {
				return nil, errors.New("outbox: message identity already used")
			}
			return e, nil
		}
	}
	c := s.history
	c.mu.Lock()
	real, top := c.threadChat(chat)
	if c.isTopic(chat) && top == model.GeneralTopic {
		top = 0
	}
	peer, ok := c.peers[real]
	cache := c.cache
	c.mu.Unlock()
	if !ok {
		return nil, errors.New("unknown chat")
	}
	if cache == nil {
		return nil, errors.New("outbox: cache unavailable")
	}
	// Clone all slices and pointers: the caller is free to change its draft.
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	var copied model.OutgoingMessage
	if err = json.Unmarshal(b, &copied); err != nil {
		return nil, err
	}
	msg = copied
	e := &outgoingEntry{Chat: chat, Peer: peer, Top: top, Request: msg, Created: time.Now(), Done: map[int64]int{}, result: &outgoingResult{done: make(chan struct{})}}
	e.Draft = s.draft(chat)
	c.mu.Lock()
	if thread := c.threads[chat]; thread != nil {
		e.ThreadPost, e.Topic, e.ThreadTop = thread.post, thread.topic, thread.top
	}
	c.mu.Unlock()
	e.LocalEmoji = s.localPremium.Load() && !s.Premium().Active
	if e.LocalEmoji {
		e.EmojiLocal = s.localEmojiKinds(ctx, msg.Entities)
	}
	e.LocalSticker = e.LocalEmoji && msg.Item != nil && msg.Item.Media.Kind == model.MessageSticker && msg.Item.Media.Media != nil && msg.Item.Media.Media.Premium
	e.ResolveSticker = e.LocalEmoji && msg.Item != nil && msg.Item.Media.Kind == model.MessageSticker && msg.Item.Media.Media != nil && !msg.Item.Media.Media.PremiumKnown && !msg.Item.Media.Media.Premium
	paths := outgoingPaths(msg)
	pending, err := s.outgoingMessages(chat, msg, e.Created)
	if err != nil {
		return nil, err
	}
	e.pending = pending
	for _, p := range pending {
		e.Messages = append(e.Messages, p.msg)
	}
	// Stored paths are display names; file contents are staged in the same
	// transaction. Actual paths are materialized only for an upload attempt.
	if msg.Path != "" {
		e.Request.Path = filepath.Base(msg.Path)
	}
	if msg.Files != nil {
		for i, path := range msg.Files.Paths {
			e.Request.Files.Paths[i] = filepath.Base(path)
		}
	}
	if err := cache.PutOutgoing(ctx, msg.RandomID, e, paths); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.pending.list = append(c.pending.list, pending...)
	c.touchPending(chat)
	c.mu.Unlock()
	q.entries = append(q.entries, e)
	s.changed()
	return e, nil
}

func outgoingPaths(msg model.OutgoingMessage) []string {
	if msg.Files != nil {
		return append([]string(nil), msg.Files.Paths...)
	}
	if msg.Path != "" {
		return []string{msg.Path}
	}
	return nil
}

func (s *Store) startOutgoing() {
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if q.wake == nil {
		q.wake = make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(context.Background())
		q.cancel = cancel
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			defer crash.Recover("outbox", func(p *crash.Panic) { s.failOutgoingPanic(p) })
			s.runOutgoing(ctx)
		}()
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (s *Store) stopOutgoing() {
	q := &s.outbox
	q.mu.Lock()
	q.closed = true
	if q.cancel != nil {
		q.cancel()
	}
	q.mu.Unlock()
	q.wg.Wait()
}

func (s *Store) runOutgoing(ctx context.Context) {
	q := &s.outbox
	for ctx.Err() == nil {
		q.mu.Lock()
		var next *outgoingEntry
		blocked := map[int64]bool{}
		wait := time.Second
		for _, e := range q.entries {
			if e.Error != "" {
				continue
			}
			if blocked[e.Chat] {
				continue
			}
			blocked[e.Chat] = true
			if e.Error != "" {
				continue
			}
			if delay := time.Until(e.RetryAt); delay > 0 {
				wait = min(wait, delay)
				continue
			}
			next = e
			break
		}
		q.mu.Unlock()
		if next != nil {
			s.history.mu.Lock()
			online := s.history.api != nil
			s.history.mu.Unlock()
			if online || next.LocalSticker {
				err := s.deliverOutgoing(ctx, next)
				if ctx.Err() != nil {
					return
				}
				s.finishOutgoing(next, err)
				continue
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-q.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Store) deliverOutgoing(ctx context.Context, e *outgoingEntry) (err error) {
	defer crash.Recover("outgoing message", func(p *crash.Panic) { err = p })
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if e.ResolveSticker {
		if err := s.resolveOutgoingSticker(ctx, e); err != nil {
			return err
		}
	}
	if e.Forward != nil {
		return s.deliverForward(ctx, e)
	}
	msg := e.Request
	if msg.Files == nil {
		if _, ok := e.Done[msg.RandomID]; ok {
			return s.restoreOutgoingEmoji(ctx, e)
		}
	}
	paths := outgoingPaths(msg)
	if len(paths) > 0 {
		dir, err := os.MkdirTemp("", "komarugram-outbox-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		materialized := make([]string, len(paths))
		for i, name := range paths {
			folder := filepath.Join(dir, fmt.Sprint(i))
			if err := os.Mkdir(folder, 0700); err != nil {
				return err
			}
			path := filepath.Join(folder, filepath.Base(name))
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			err = s.Cache().WriteOutgoingFile(ctx, msg.RandomID, i, f)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			materialized[i] = path
		}
		if msg.Files != nil {
			files := *msg.Files
			files.Paths = materialized
			msg.Files = &files
		} else {
			msg.Path = materialized[0]
		}
	}
	if e.LocalSticker {
		return s.keepLocalSticker(ctx, e)
	}
	err = s.sendOnce(ctx, e.Chat, msg, e)
	if msg.Item != nil && tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_EMPTY", "FILE_REFERENCE_INVALID") {
		if err = s.refreshReference(ctx, msg.Item.Media); err == nil {
			err = s.sendOnce(ctx, e.Chat, msg, e)
		}
	}
	return err
}

func outgoingRetry(err error, attempts int) (time.Duration, bool) {
	if attempts >= 5 {
		return 0, false
	}
	if delay, ok := tgerr.AsFloodWait(err); ok {
		return max(time.Second, delay), true
	}
	var serverError *tgerr.Error
	if errors.As(err, &serverError) {
		if serverError.Code < 500 {
			return 0, false
		}
		return time.Second << min(attempts, 5), true
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, errNotConnected) || errors.Is(err, net.ErrClosed) || errors.Is(err, pool.ErrConnDead) || errors.Is(err, rpc.ErrEngineClosed) {
		return time.Second << min(attempts, 5), true
	}
	return 0, false
}

func (s *Store) finishOutgoing(e *outgoingEntry, err error) {
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err == nil {
		err = s.Cache().DropOutgoing(ctx, e.Request.RandomID)
	}
	if err == nil {
		for i, other := range q.entries {
			if other == e {
				q.entries = append(q.entries[:i], q.entries[i+1:]...)
				break
			}
		}
		for _, p := range e.pending {
			s.dropPending(p)
		}
	} else {
		e.Attempts++
		if delay, retry := outgoingRetry(err, e.Attempts); retry {
			e.RetryAt = time.Now().Add(delay)
		} else {
			e.Error = err.Error()
		}
		if saveErr := s.Cache().PutOutgoing(ctx, e.Request.RandomID, e, nil); saveErr != nil {
			e.Error = errors.Join(err, saveErr).Error()
		}
		s.history.mu.Lock()
		for _, p := range e.pending {
			p.msg.SendError = e.Error
			p.msg.ContentRevision++
			s.history.touchPending(p.chat)
		}
		s.history.mu.Unlock()
	}
	if err == nil || e.Error != "" {
		if !e.result.finished {
			e.result.finished = true
			e.result.err = err
			close(e.result.done)
		}
	}
	s.changed()
}

func (s *Store) failOutgoingPanic(err error) {
	q := &s.outbox
	q.mu.Lock()
	entries := append([]*outgoingEntry(nil), q.entries...)
	q.mu.Unlock()
	for _, e := range entries {
		s.finishOutgoing(e, err)
	}
}

func (s *Store) RetryOutgoing(key model.MessageKey) {
	q := &s.outbox
	func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		for _, e := range q.entries {
			for _, p := range e.pending {
				if p.msg.Key == key && e.Error != "" {
					e.Error = ""
					e.Attempts = 0
					e.RetryAt = time.Time{}
					e.result = &outgoingResult{done: make(chan struct{})}
					s.history.mu.Lock()
					for _, p := range e.pending {
						p.msg.SendError = ""
						p.msg.ContentRevision++
						s.history.touchPending(p.chat)
					}
					s.history.mu.Unlock()
					_ = s.Cache().PutOutgoing(context.Background(), e.Request.RandomID, e, nil)
				}
			}
		}
	}()
	s.startOutgoing()
	s.changed()
}

func (s *Store) CancelOutgoing(key model.MessageKey) {
	// Cancellation is offered only after delivery stopped with an error: an
	// in-flight request may already have reached Telegram.
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	if s.removeLocalSticker(key) {
		return
	}
	for i, e := range q.entries {
		for _, p := range e.pending {
			if p.msg.Key == key && e.Error != "" {
				if s.Cache().DropOutgoing(context.Background(), e.Request.RandomID) != nil {
					return
				}
				q.entries = append(q.entries[:i], q.entries[i+1:]...)
				for _, p := range e.pending {
					s.dropPending(p)
				}
				s.changed()
				return
			}
		}
	}
}
