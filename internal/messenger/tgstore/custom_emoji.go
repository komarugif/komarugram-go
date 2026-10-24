// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// Custom emoji not kept yet are asked of Telegram together, as Telegram
// Desktop's CustomEmojiManager asks them: a chat list or a message shows
// several at once, and asked one by one each cost a request. An emoji
// asked for while it is on its way waits for the same answer, and one
// Telegram does not know is not asked for again.
const (
	// emojiBatchWait is how long an emoji waits for others to be asked
	// with it.
	emojiBatchWait = 20 * time.Millisecond
	// emojiBatchMost is the most asked in one request.
	emojiBatchMost = 100
)

var errEmojiUnavailable = errors.New("emoji unavailable")

type emojiAnswer struct {
	m   model.Message
	err error
}

// emojiBatch gathers the custom emoji asked for until they are asked of
// Telegram.
type emojiBatch struct {
	mu      sync.Mutex
	waiting map[int64][]chan emojiAnswer
	pending bool
	// missing are the emoji Telegram had no document of.
	missing map[int64]bool
}

// askEmoji asks Telegram for custom emoji id with the others asked for
// meanwhile.
func (s *Store) askEmoji(ctx context.Context, id int64) (model.Message, error) {
	b := &s.emoji
	answer := make(chan emojiAnswer, 1)
	b.mu.Lock()
	if b.missing[id] {
		b.mu.Unlock()
		return model.Message{}, errEmojiUnavailable
	}
	if b.waiting == nil {
		b.waiting = map[int64][]chan emojiAnswer{}
	}
	b.waiting[id] = append(b.waiting[id], answer)
	if !b.pending {
		b.pending = true
		time.AfterFunc(emojiBatchWait, s.flushEmoji)
	}
	b.mu.Unlock()
	select {
	case a := <-answer:
		return a.m, a.err
	case <-ctx.Done():
		return model.Message{}, ctx.Err()
	}
}

// flushEmoji asks Telegram for the emoji gathered, and answers whoever
// asked for them.
func (s *Store) flushEmoji() {
	b := &s.emoji
	b.mu.Lock()
	waiting := b.waiting
	b.waiting, b.pending = nil, false
	b.mu.Unlock()
	answer := func(id int64, a emojiAnswer) {
		for _, ch := range waiting[id] {
			// Each has room for one; one answered already is passed.
			select {
			case ch <- a:
			default:
			}
		}
	}
	answerAll := func(err error) {
		for id := range waiting {
			answer(id, emojiAnswer{err: err})
		}
	}
	defer crash.Recover("custom emoji", func(p *crash.Panic) { answerAll(p) })
	c := s.history
	c.mu.Lock()
	api, cache, ctx := c.api, c.cache, c.ctx
	c.mu.Unlock()
	if api == nil || cache == nil {
		answerAll(errors.New("emoji unavailable offline"))
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids := slices.Sorted(maps.Keys(waiting))
	for len(ids) > 0 {
		part := ids[:min(len(ids), emojiBatchMost)]
		ids = ids[len(part):]
		docs, err := api.MessagesGetCustomEmojiDocuments(ctx, part)
		if err != nil {
			for _, id := range part {
				answer(id, emojiAnswer{err: err})
			}
			continue
		}
		found := map[int64]model.Message{}
		keep := map[string]any{}
		for _, doc := range docs {
			d, ok := doc.(*tg.Document)
			if !ok {
				continue
			}
			kind, meta, ref := documentMedia(d)
			m := model.Message{Kind: kind, Media: meta}
			found[d.ID] = m
			c.mu.Lock()
			c.refs[meta.ID] = *ref
			c.mu.Unlock()
			keep["ref/"+meta.ID] = ref
			keep[emojiKey(d.ID)] = m
		}
		err = cache.PutAll(ctx, keep)
		for _, id := range part {
			if m, ok := found[id]; ok {
				answer(id, emojiAnswer{m, err})
				continue
			}
			b.mu.Lock()
			if b.missing == nil {
				b.missing = map[int64]bool{}
			}
			b.missing[id] = true
			b.mu.Unlock()
			answer(id, emojiAnswer{err: errEmojiUnavailable})
		}
	}
}

// emojiKey is where the cache keeps custom emoji id: the second version of
// the key, as the first kept no TextColor.
func emojiKey(id int64) string {
	return fmt.Sprintf("emoji/v2/%d", id)
}
