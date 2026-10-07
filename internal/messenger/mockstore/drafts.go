// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"errors"
	"slices"
	"time"

	"komarugram/internal/messenger/model"
)

// demoStreamStep is how often the demo's bot streams its draft further.
const demoStreamStep = 350 * time.Millisecond

// demoStream is a reply the demo's bot streams as it writes it, as a bot of
// Telegram's live message streaming does (model.DraftStopper): its article
// grows a block at a time, and is a message at the end.
type demoStream struct {
	draft model.Message
	stop  chan struct{}
}

// SetChanged sets what is told when the store changes by itself, as when a
// bot streams a draft.
func (s *Store) SetChanged(f func()) {
	s.mu.Lock()
	s.changed = f
	s.mu.Unlock()
}

// startStream starts the bot of chat streaming the demo's article, which
// becomes the chat's next message. The caller holds s.mu.
func (s *Store) startStream(chat int64) {
	if s.streams == nil {
		s.streams = map[int64]*demoStream{}
	}
	if old := s.streams[chat]; old != nil {
		close(old.stop)
	}
	st := &demoStream{stop: make(chan struct{})}
	s.streams[chat] = st
	go func() {
		whole := RichExample(false)
		for n := 1; n <= len(whole.Blocks); n++ {
			select {
			case <-st.stop:
				return
			case <-time.After(demoStreamStep):
			}
			page := model.RichPage{Blocks: slices.Clone(whole.Blocks[:n])}
			summary := page.Summary()
			s.mu.Lock()
			if s.streams[chat] != st {
				s.mu.Unlock()
				return
			}
			st.draft = model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: -1}, Date: time.Now(), SenderName: "Бот", Text: summary.Text, Entities: summary.Entities, Rich: &page, Streaming: true, Stoppable: true}
			st.draft.ContentRevision = model.Revision(st.draft)
			s.touch(chat)
			s.mu.Unlock()
			s.tellChanged()
		}
		s.mu.Lock()
		if s.streams[chat] == st {
			delete(s.streams, chat)
			summary := whole.Summary()
			h := s.histories[chat]
			id := model.MessageID(1)
			if len(h.Messages) > 0 {
				id = h.Messages[len(h.Messages)-1].Key.MessageID + 1
			}
			m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: id}, Date: time.Now(), SenderName: "Бот", Text: summary.Text, Entities: summary.Entities, Rich: &whole, ContentRevision: 1}
			h.Messages = append(slices.Clone(h.Messages), m)
			s.histories[chat] = h
			s.touch(chat)
		}
		s.mu.Unlock()
		s.tellChanged()
	}()
}

// StopStreamedDraft implements model.DraftStopper: the draft goes, as one
// that does not keep on stop does.
func (s *Store) StopStreamedDraft(chat int64) error {
	s.mu.Lock()
	st := s.streams[chat]
	if st == nil {
		s.mu.Unlock()
		return errors.New("no draft to stop")
	}
	close(st.stop)
	delete(s.streams, chat)
	s.touch(chat)
	s.mu.Unlock()
	s.tellChanged()
	return nil
}

// streamedDrafts are the drafts the history of chat shows at its end. The
// caller holds s.mu.
func (s *Store) streamedDrafts(chat int64) []model.Message {
	if st := s.streams[chat]; st != nil && st.draft.Streaming {
		return []model.Message{st.draft}
	}
	return nil
}

// touch tells the history of chat that it changed. The caller holds s.mu.
func (s *Store) touch(chat int64) {
	h := s.histories[chat]
	h.Revision++
	s.histories[chat] = h
}

func (s *Store) tellChanged() {
	s.mu.Lock()
	f := s.changed
	s.mu.Unlock()
	if f != nil {
		f()
	}
}
