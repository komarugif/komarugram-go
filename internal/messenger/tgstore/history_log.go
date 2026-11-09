// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// historyState tells a history's flags and the messages it holds, for the
// journal (historylog).
func historyState(h *model.History) string {
	if h == nil {
		return "none"
	}
	span := "-"
	if n := len(h.Messages); n > 0 {
		span = fmt.Sprintf("%d..%d", h.Messages[0].Key.MessageID, h.Messages[n-1].Key.MessageID)
	}
	return fmt.Sprintf("n=%d %s older=%t newer=%t loadingOlder=%t loadingNewer=%t offline=%t err=%v rev=%d",
		len(h.Messages), span, h.HasOlder, h.HasNewer, h.LoadingOlder, h.LoadingNewer, h.Offline, h.Err, h.Revision)
}

var fetchSeq atomic.Uint64

// fetchStuck is how long a page may take before the journal tells where
// it waits: its requests end in 30 s, so one still running after it waits
// on something that does not end with them.
const fetchStuck = 40 * time.Second

// fetchTrace follows a page through fetchAt in the journal: the step it is
// at, told when it takes longer than any of its requests may.
type fetchTrace struct {
	id    uint64
	chat  int64
	dir   int
	start time.Time
	mu    sync.Mutex
	step  string
	timer *time.Timer
}

func traceFetch(epoch uint64, chat int64, dir, anchor int, online bool, kind string) *fetchTrace {
	if !historylog.On() {
		return nil
	}
	t := &fetchTrace{id: fetchSeq.Add(1), chat: chat, dir: dir, start: time.Now(), step: "start"}
	historylog.Printf("fetch #%d chat=%d dir=%d anchor=%d epoch=%d online=%t peer=%q", t.id, chat, dir, anchor, epoch, online, kind)
	t.timer = time.AfterFunc(fetchStuck, func() {
		t.mu.Lock()
		step := t.step
		t.mu.Unlock()
		historylog.Printf("fetch #%d chat=%d dir=%d STILL RUNNING after %s at %s", t.id, chat, dir, fetchStuck, step)
		historylog.DumpOnce(fmt.Sprintf("fetch #%d stuck at %s", t.id, step))
	})
	return t
}

// at notes the step the page is at.
func (t *fetchTrace) at(step string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.step = step
	t.mu.Unlock()
}

// note writes a line about the page.
func (t *fetchTrace) note(format string, args ...any) {
	if t == nil {
		return
	}
	historylog.Printf("fetch #%d chat=%d %s", t.id, t.chat, fmt.Sprintf(format, args...))
}

func (t *fetchTrace) done() {
	if t == nil {
		return
	}
	t.timer.Stop()
	t.mu.Lock()
	step := t.step
	t.mu.Unlock()
	historylog.Printf("fetch #%d chat=%d dir=%d done in %s at %s", t.id, t.chat, t.dir, time.Since(t.start).Round(time.Millisecond), step)
}

// pageRefusals keeps why page last asked for nothing, by chat and
// direction, to tell each reason once: the view asks on every frame.
type pageRefusals struct {
	mu   sync.Mutex
	last map[[2]int64]string
}

var refusals pageRefusals

// refuse tells why a page of chat was not asked for, when the reason
// changed; "" is a page asked for.
func (r *pageRefusals) refuse(chat int64, dir int, why string, h *model.History) {
	if !historylog.On() {
		return
	}
	key := [2]int64{chat, int64(dir)}
	r.mu.Lock()
	if r.last == nil {
		r.last = map[[2]int64]string{}
	}
	same := r.last[key] == why
	r.last[key] = why
	r.mu.Unlock()
	if same || why == "" {
		return
	}
	historylog.Printf("page chat=%d dir=%d refused: %s; %s", chat, dir, why, historyState(h))
}
