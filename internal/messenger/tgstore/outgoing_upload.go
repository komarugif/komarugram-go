// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
)

var _ model.UploadSource = (*Store)(nil)

// errOutgoingCancelled is how a message all of whose files were cancelled
// ends, for who waits for it.
var errOutgoingCancelled = errors.New("outbox: cancelled")

// outgoingPatience is how long delivering a message may go on with no file
// moving: an upload may take as long as its size asks.
var outgoingPatience = 10 * time.Minute

// uploadTable is how far each file on its way went. It has a lock of its
// own: the history reads it in every frame, and the queue's is held across
// writes to the database.
type uploadTable struct {
	mu    sync.Mutex
	files map[model.MessageKey]model.UploadStatus
}

func (t *uploadTable) add(key model.MessageKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.files == nil {
		t.files = map[model.MessageKey]model.UploadStatus{}
	}
	t.files[key] = model.UploadStatus{}
}

func (t *uploadTable) remove(key model.MessageKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.files, key)
}

func (t *uploadTable) get(key model.MessageKey) (model.UploadStatus, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status, ok := t.files[key]
	return status, ok
}

// reset starts key's file over, as a new attempt does.
func (t *uploadTable) reset(key model.MessageKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.files[key]; ok {
		t.files[key] = model.UploadStatus{}
	}
}

// advance tells of a chunk of key's file. The uploader's threads may tell out
// of order, and a file gone from the table stays gone.
func (t *uploadTable) advance(key model.MessageKey, done, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if status, ok := t.files[key]; ok && done > status.Done {
		t.files[key] = model.UploadStatus{Done: done, Total: total}
	}
}

// UploadStatus implements model.UploadSource.
func (s *Store) UploadStatus(key model.MessageKey) (model.UploadStatus, bool) {
	return s.outbox.uploads.get(key)
}

// addUploads tells the table of e's files still to go. The caller holds the
// queue's lock.
func (s *Store) addUploads(e *outgoingEntry) {
	if len(outgoingPaths(e.Request)) == 0 {
		return
	}
	for index, p := range e.pending {
		if !e.Cancelled[index] && !e.fileDone(index) {
			s.outbox.uploads.add(p.msg.Key)
		}
	}
}

// fileDone tells whether Telegram took file index of e. The caller holds the
// queue's lock, or is the worker delivering e, the only one to change it.
func (e *outgoingEntry) fileDone(index int) bool {
	_, ok := e.Done[outgoingRandomID(e.Request.RandomID, index)]
	return ok
}

// startFile is the context file index of e uploads in, ended by CancelUpload
// and by the function it returns; false for a file cancelled already. Its
// table entry starts over.
func (s *Store) startFile(ctx context.Context, e *outgoingEntry, index int) (context.Context, func(), bool) {
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	if e.Cancelled[index] {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(ctx)
	if e.cancels == nil {
		e.cancels = map[int]context.CancelFunc{}
	}
	e.cancels[index] = cancel
	if index < len(e.pending) {
		q.uploads.reset(e.pending[index].msg.Key)
	}
	return ctx, func() {
		q.mu.Lock()
		delete(e.cancels, index)
		q.mu.Unlock()
		cancel()
	}, true
}

// fileCancelled tells whether file index of e was cancelled.
func (s *Store) fileCancelled(e *outgoingEntry, index int) bool {
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	return e.Cancelled[index]
}

// claimFiles keeps of indexes the files not cancelled, which are then past
// cancelling: the request about to carry them may reach Telegram whatever
// happens to it here. Delivery ending lets them go.
func (s *Store) claimFiles(e *outgoingEntry, indexes []int) []int {
	q := &s.outbox
	q.mu.Lock()
	defer q.mu.Unlock()
	if e.sending == nil {
		e.sending = map[int]bool{}
	}
	var kept []int
	for _, index := range indexes {
		if !e.Cancelled[index] {
			e.sending[index] = true
			kept = append(kept, index)
		}
	}
	return kept
}

// CancelUpload implements model.UploadSource. The file leaves its message at
// once, and the upload of it, if any, ends; the others go on, and go without
// it. With nothing left to go, the message is over.
func (s *Store) CancelUpload(key model.MessageKey) {
	q := &s.outbox
	if _, ok := q.uploads.get(key); !ok {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e, index := q.fileOf(key)
	if e == nil || e.Cancelled[index] || e.sending[index] || e.fileDone(index) {
		return
	}
	if e.Cancelled == nil {
		e.Cancelled = map[int]bool{}
	}
	e.Cancelled[index] = true
	if cancel := e.cancels[index]; cancel != nil {
		cancel()
	}
	q.uploads.remove(key)
	s.moveCaption(e, index)
	s.dropPending(e.pending[index])
	defer s.changed()
	for i := range e.pending {
		if !e.Cancelled[i] && !e.fileDone(i) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Cache().PutOutgoing(ctx, e.Request.RandomID, e, nil); err != nil {
				log.Printf("outbox: %v", err)
			}
			return
		}
	}
	if q.current == e {
		// The worker ends it, once it lets go of what it holds.
		e.abandoned = true
		if e.stop != nil {
			e.stop()
		}
		return
	}
	s.dropOutgoing(e)
}

// fileOf is the entry key's message belongs to and its place there. The caller
// holds the queue's lock.
func (q *outgoingQueue) fileOf(key model.MessageKey) (*outgoingEntry, int) {
	for _, e := range q.entries {
		for index, p := range e.pending {
			if p.msg.Key == key {
				return e, index
			}
		}
	}
	return nil, 0
}

// dropOutgoing ends e, which has nothing left to send: what is staged of it
// goes, and its messages leave the history. The caller holds the queue's lock.
func (s *Store) dropOutgoing(e *outgoingEntry) {
	q := &s.outbox
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Cache().DropOutgoing(ctx, e.Request.RandomID); err != nil {
		log.Printf("outbox: %v", err)
	}
	for i, other := range q.entries {
		if other == e {
			q.entries = append(q.entries[:i], q.entries[i+1:]...)
			break
		}
	}
	for _, p := range e.pending {
		q.uploads.remove(p.msg.Key)
		s.dropPending(p)
	}
	if !e.result.finished {
		e.result.finished = true
		e.result.err = errOutgoingCancelled
		close(e.result.done)
	}
}

// moveCaption gives the caption of the cancelled file index, if it has it, to
// the file that takes it now, by the rule of sendfiles.CaptionTarget: the first
// of an album of photos and videos at the end, or else the last. What Telegram
// took already keeps as it was sent: with nothing else left the caption goes.
// The caller holds the queue's lock.
func (s *Store) moveCaption(e *outgoingEntry, index int) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	from := &e.pending[index].msg
	if from.Text == "" && len(from.Entities) == 0 {
		return
	}
	target := -1
	for i := len(e.pending) - 1; i >= 0; i-- {
		if e.Cancelled[i] || e.fileDone(i) {
			continue
		}
		m := &e.pending[i].msg
		if target < 0 {
			target = i
			if m.GroupedID == 0 || m.Kind != model.MessagePhoto && m.Kind != model.MessageVideo {
				break
			}
		} else if m.GroupedID == e.pending[target].msg.GroupedID {
			target = i
		} else {
			break
		}
	}
	if target < 0 {
		return
	}
	to := &e.pending[target].msg
	to.Text, to.Entities = from.Text, from.Entities
	to.ContentRevision++
	if target < len(e.Messages) {
		e.Messages[target].Text, e.Messages[target].Entities = from.Text, from.Entities
	}
	c.touchPending(e.pending[target].chat)
}
