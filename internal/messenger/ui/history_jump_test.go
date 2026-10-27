// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
)

var errFailed = errors.New("failed")

// jumpSource is a history that can be opened at its ends, and reloaded,
// and tells which it was asked for.
type jumpSource struct {
	benchmarkHistory
	calls *[]string
	// loading, if set, is what a jump starts: the history loads while it is.
	loading *bool
}

func (s jumpSource) HistorySince(chat int64, revision uint64) (model.History, bool) {
	h, fresh := s.benchmarkHistory.HistorySince(chat, revision)
	if s.loading != nil && *s.loading {
		h.LoadingOlder = true
	}
	return h, fresh
}

func (s jumpSource) RevealFirst(int64) bool { return s.reveal("first") }
func (s jumpSource) RevealLast(int64) bool  { return s.reveal("last") }

func (s jumpSource) reveal(call string) bool {
	*s.calls = append(*s.calls, call)
	if s.loading != nil {
		*s.loading = true
	}
	return true
}
func (s jumpSource) Reload(int64) { *s.calls = append(*s.calls, "reload") }

// jumpButton is the centre of the n-th button from the bottom in the
// harness's window: 40 dp round, 16 dp from the corner of the history above
// its composer, 12 dp apart.
func (h *chatInputHarness) jumpButton(n int) f32.Point {
	bottom := 600
	if c := h.page.composer; c != nil {
		bottom = c.top
	}
	return f32.Pt(100+400-16-20, float32(bottom-16-20-n*(40+12)))
}

func (h *chatInputHarness) click(at f32.Point) {
	h.send(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonPrimary})
	h.send(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	h.frame()
}

// seek puts the history of the harness at row first, offset px into it, before
// its end, and lays it out.
func (h *chatInputHarness) seek(first, offset int) {
	h.page.list.Position.First, h.page.list.Position.Offset, h.page.list.Position.BeforeEnd = first, offset, true
	h.frame()
}

func TestJumpButtonsScroll(t *testing.T) {
	// At the start of 100 messages, none older: more than one request's.
	h := newChatInputHarnessOf(t, 100, func(m model.History) model.ConversationStore { return benchmarkHistory{h: m} })
	p := h.page
	if !p.atStart(model.History{}, 0) || p.atEnd(model.History{}, 600, 480) {
		t.Fatalf("at the start: atStart %v, atEnd %v", p.atStart(model.History{}, 0), p.atEnd(model.History{}, 600, 480))
	}
	h.click(h.jumpButton(0))
	if p.list.Position.BeforeEnd {
		t.Fatal("the button to the end left the history before it")
	}
	// At the bottom no button is there, the one to the start neither.
	first, offset := p.list.Position.First, p.list.Position.Offset
	h.click(h.jumpButton(0))
	if p.list.Position.First != first || p.list.Position.Offset != offset {
		t.Fatal("a button to the start is there at the bottom")
	}
	// A little above it, far enough, the one to the start is.
	h.seek(len(p.messages)-30, 0)
	h.click(h.jumpButton(0))
	if p.list.Position.BeforeEnd {
		t.Fatal("the button to the end did not take the history there")
	}
	h.seek(len(p.messages)-30, 0)
	h.click(h.jumpButton(1))
	if p.list.Position.First != 0 || p.list.Position.Offset != 0 {
		t.Fatalf("the button to the start stopped at %d+%d", p.list.Position.First, p.list.Position.Offset)
	}
}

// The history is at its end, for the buttons, up to a reserve above it, as in
// Telegram Desktop; and at its start, up to the same below it.
func TestJumpReserve(t *testing.T) {
	h := newChatInputHarnessOf(t, 100, func(m model.History) model.ConversationStore { return benchmarkHistory{h: m} })
	p := h.page
	const viewport, reserve = 600, 480
	nearEnd, nearStart, far := 0, 0, 0
	for first := range p.messages {
		h.seek(first, 0)
		// The rows laid out replace their estimated heights, so the total
		// is taken after each.
		below := p.heights.Total() - (p.heights.Prefix(first) + int64(viewport))
		if got := p.atEnd(model.History{}, viewport, reserve); got != (below <= reserve) {
			t.Fatalf("row %d, %d px above the end: atEnd %v", first, below, got)
		}
		above := p.heights.Prefix(first)
		if got := p.atStart(model.History{}, reserve); got != (above <= reserve) {
			t.Fatalf("row %d, %d px below the start: atStart %v", first, above, got)
		}
		switch {
		case below <= reserve:
			nearEnd++
		case above <= reserve:
			nearStart++
		default:
			far++
		}
	}
	if nearEnd < 3 || nearStart < 3 || far < 10 {
		t.Fatalf("the rows of the test are too few near the ends or far: %d, %d, %d", nearEnd, nearStart, far)
	}
	// A few pixels above the end is still the end.
	h.seek(len(p.messages)-1, 0)
	h.page.list.Position.Offset = 3
	if !p.atEnd(model.History{}, viewport, reserve) {
		t.Fatal("a few pixels above the end is not the end")
	}
	// Not at the end, when there are newer messages that are not loaded.
	if p.atEnd(model.History{HasNewer: true}, viewport, reserve) {
		t.Fatal("the end of the loaded is the end of the chat")
	}
}

// Near the bottom no button scrolls, so that none is over the message that
// has just come; a failure still has its retry.
func TestNoScrollButtonsNearTheBottom(t *testing.T) {
	h := newChatInputHarnessOf(t, 100, func(m model.History) model.ConversationStore { return benchmarkHistory{h: m} })
	p := h.page
	// A row from which some px are left to the end, fewer than the reserve.
	first := -1
	for i := range p.messages {
		if below := p.heights.Total() - (p.heights.Prefix(i) + 600); below > 100 && below < 400 {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatal("no row is a little above the end")
	}
	h.seek(first, 0)
	if !p.list.Position.BeforeEnd || p.list.Position.First != first {
		t.Fatalf("the history is not above its end: row %d, before the end %v", p.list.Position.First, p.list.Position.BeforeEnd)
	}
	h.click(h.jumpButton(0))
	h.click(h.jumpButton(1))
	if p.list.Position.First != first || !p.list.Position.BeforeEnd {
		t.Fatal("a button is there near the bottom")
	}

	var calls []string
	failed := newChatInputHarnessOf(t, 100, func(m model.History) model.ConversationStore {
		m.Err = errFailed
		return jumpSource{benchmarkHistory{h: m}, &calls, nil}
	})
	failed.page.list.Position.BeforeEnd = false
	failed.frame()
	failed.click(failed.jumpButton(0))
	if len(calls) != 1 || calls[0] != "reload" {
		t.Fatalf("at the bottom, with a failure, the store was asked for %v", calls)
	}
}

func TestJumpButtonsHideWhenThereIsNowhereToGo(t *testing.T) {
	h := newChatInputHarnessOf(t, 2, func(m model.History) model.ConversationStore { return benchmarkHistory{h: m} })
	h.page.list.Position.BeforeEnd = false
	h.frame()
	if !h.page.atStart(model.History{}, 0) || !h.page.atEnd(model.History{}, 600, 0) {
		t.Fatalf("two messages that fit: atStart %v, atEnd %v", h.page.atStart(model.History{}, 0), h.page.atEnd(model.History{}, 600, 0))
	}
}

func TestJumpButtonsReachUnloadedEnds(t *testing.T) {
	for n, want := range []string{"last", "first"} {
		var calls []string
		source := func(m model.History) model.ConversationStore {
			m.HasOlder, m.HasNewer = true, true
			return jumpSource{benchmarkHistory{h: m}, &calls, nil}
		}
		h := newChatInputHarnessOf(t, 40, source)
		// Both ends are unloaded, so both buttons are there: the end below.
		h.click(h.jumpButton(n))
		if len(calls) != 1 || calls[0] != want {
			t.Fatalf("button %d: the store was asked for %v, want %s", n, calls, want)
		}
	}
}

func TestRetryButton(t *testing.T) {
	var calls []string
	source := func(m model.History) model.ConversationStore {
		m.Err = errFailed
		return jumpSource{benchmarkHistory{h: m}, &calls, nil}
	}
	h := newChatInputHarnessOf(t, 40, source)
	// At the start with a failure: the end, then the retry above it.
	h.click(h.jumpButton(1))
	if len(calls) != 1 || calls[0] != "reload" {
		t.Fatalf("the store was asked for %v, want a reload", calls)
	}
}

func (s jumpSource) LoadOlder(int64) {}
func (s jumpSource) LoadNewer(int64) {}

func TestNoButtonToTheStartOfAChatOfOneRequest(t *testing.T) {
	h := newChatInputHarness(t) // 40 messages, all there is
	p := h.page
	// Far from both ends, past the reserve at the start, as tall as the
	// messages are: only the one to the end is there.
	first := 0
	for p.heights.Prefix(first) <= jumpReserve {
		first++
	}
	h.seek(first, 0)
	if p.heights.Prefix(len(p.messages))-p.heights.Prefix(first) <= jumpReserve+600 {
		t.Fatal("no place in 40 messages far from both ends")
	}
	if !p.oneRequest(model.History{}) {
		t.Fatal("40 messages are not one request's")
	}
	h.click(h.jumpButton(1)) // where the one to the start would be
	if p.list.Position.First != first {
		t.Fatal("a button to the start is drawn in a chat of one request")
	}
	h.click(h.jumpButton(0))
	if p.list.Position.BeforeEnd {
		t.Fatal("the button to the end did nothing")
	}
}

// TestRenderJumpButtons saves the buttons over the history, for looking at
// them: JUMP_PNG_DIR=/tmp/jump. In the middle of a chat with older and newer
// messages, with a load on its way and with one failed.
func TestRenderJumpButtons(t *testing.T) {
	dir := os.Getenv("JUMP_PNG_DIR")
	if dir == "" {
		t.Skip("set JUMP_PNG_DIR to a directory")
	}
	for name, set := range map[string]func(*model.History){
		"middle":  func(h *model.History) {},
		"loading": func(h *model.History) { h.LoadingOlder = true },
		"failed":  func(h *model.History) { h.Err = errFailed },
	} {
		var calls []string
		var messages []model.Message
		for i := 1; i <= 100; i++ {
			messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: time.Unix(1700000000, 0), Text: fmt.Sprintf("Message %d", i), ContentRevision: 1})
		}
		hist := model.History{Messages: messages, Revision: 1, HasOlder: true, HasNewer: true}
		set(&hist)
		p := newChatPage(jumpSource{benchmarkHistory{h: hist}, &calls, nil}, func() {})
		p.list.Position.First, p.list.Position.BeforeEnd = 50, true
		renderFrames(t, image.Pt(500, 800), filepath.Join(dir, "jump-"+name+".png"), func(gtx layout.Context) {
			p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, localization.For("ru"), false)
		})
		p.Close()
	}
}

// A thread's history is not opened again by the store from a viewport, so the
// page puts it at the end asked for once the load of the jump is over.
func TestJumpInAThread(t *testing.T) {
	for n, want := range []struct {
		call string
		to   jump
	}{{"last", jumpEnd}, {"first", jumpStart}} {
		var calls []string
		loading := new(bool)
		source := func(m model.History) model.ConversationStore {
			m.HasOlder, m.HasNewer, m.ThreadRoot = true, true, 7
			return jumpSource{benchmarkHistory{h: m}, &calls, loading}
		}
		h := newChatInputHarnessOf(t, 100, source)
		p := h.page
		h.click(h.jumpButton(n))
		if len(calls) != 1 || calls[0] != want.call {
			t.Fatalf("button %d: the store was asked for %v", n, calls)
		}
		// Loading: the jump waits, and the thread is not forgotten, for the
		// store would open it at the end again.
		if p.jump != want.to || p.chat != 1 {
			t.Fatalf("button %d: while loading, jump %d, chat %d", n, p.jump, p.chat)
		}
		p.list.Position.First, p.list.Position.BeforeEnd = 30, true
		*loading = false
		h.frame()
		if p.jump != jumpNone {
			t.Fatalf("button %d: the jump waits still", n)
		}
		if want.to == jumpStart && (p.list.Position.First != 0 || p.list.Position.Offset != 0) || want.to == jumpEnd && p.list.Position.BeforeEnd {
			t.Fatalf("button %d: the history is at %d+%d, before the end %v", n, p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd)
		}
	}
}

// The picker opens over the corner the buttons are in, and covers them.
func TestJumpButtonsGiveWayToThePicker(t *testing.T) {
	h := newChatInputHarness(t)
	c := h.page.composer
	if c == nil {
		t.Skip("no composer")
	}
	c.pickerOpen = true
	h.frame()
	h.click(h.jumpButton(0))
	if !h.page.list.Position.BeforeEnd {
		t.Fatal("the button to the end was there under the picker")
	}
}

// sendingJumpSource is a jumpSource with a composer, which takes what is
// sent.
type sendingJumpSource struct{ jumpSource }

func (s sendingJumpSource) Send(context.Context, int64, model.OutgoingMessage) error { return nil }
func (s sendingJumpSource) Picker(context.Context, model.PickerRequest) (model.PickerPage, error) {
	return model.PickerPage{}, nil
}

// A message sent takes the history to its end, wherever it was scrolled,
// as the button to the end does: loaded again from the newest message
// when the end is not loaded.
func TestSendJumpsToTheEnd(t *testing.T) {
	for _, unloaded := range []bool{false, true} {
		var calls []string
		h := newChatInputHarnessOf(t, 100, func(m model.History) model.ConversationStore {
			m.HasNewer = unloaded
			return sendingJumpSource{jumpSource{benchmarkHistory{h: m}, &calls, nil}}
		})
		p := h.page
		h.seek(10, 0)
		p.composer.submit(1, model.OutgoingMessage{Text: "hi"})
		h.frame()
		h.frame()
		switch {
		case unloaded && (len(calls) != 1 || calls[0] != "last"):
			t.Fatalf("the store was asked for %v, want the last message", calls)
		case !unloaded && p.list.Position.BeforeEnd:
			t.Fatal("the history stayed where it was scrolled")
		}
		// Another chat's message leaves this one where it is.
		h.seek(10, 0)
		p.composer.noteSent(2)
		h.frame()
		if !p.list.Position.BeforeEnd || p.list.Position.First != 10 {
			t.Fatalf("a message to another chat moved the history to %+v", p.list.Position)
		}
	}
}
