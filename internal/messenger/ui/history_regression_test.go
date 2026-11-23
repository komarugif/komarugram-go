// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
)

type chatInputHarness struct {
	router  input.Router
	page    *chatPage
	now     time.Time
	sibling widget.Clickable
	clicked bool
}

func newChatInputHarness(t *testing.T) *chatInputHarness {
	t.Helper()
	skipFrames(t)
	return newChatInputHarnessOf(t, 40, func(h model.History) model.ConversationStore { return benchmarkHistory{h: h} })
}

// newChatInputHarnessOf is a harness over n messages, in the history that
// source makes of them.
func newChatInputHarnessOf(t *testing.T, n int, source func(model.History) model.ConversationStore) *chatInputHarness {
	t.Helper()
	skipFrames(t)
	h := &chatInputHarness{now: time.Unix(1000, 0)}
	var messages []model.Message
	for i := 1; i <= n; i++ {
		messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: h.now, Text: fmt.Sprintf("Message %d: select and scroll", i), ContentRevision: 1})
	}
	h.page = newChatPage(source(model.History{Messages: messages, Revision: 1}), func() {})
	t.Cleanup(h.page.Close)
	h.frame()
	h.page.list.Position.First, h.page.list.Position.Offset, h.page.list.Position.BeforeEnd = 0, 0, true
	h.frame()
	return h
}
func (h *chatInputHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 600)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	if h.sibling.Clicked(gtx) {
		h.clicked = true
	}
	h.sibling.Layout(gtx, func(layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(100, 600)} })
	body := gtx
	body.Constraints = layout.Exact(image.Pt(400, 600))
	stack := op.Offset(image.Pt(100, 0)).Push(gtx.Ops)
	h.page.Layout(body, model.Chat{ID: 1}, localization.For("en"), true)
	stack.Pop()
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}
func (h *chatInputHarness) send(e pointer.Event) {
	e.Time = time.Duration(h.now.UnixNano())
	h.router.Queue(e)
	h.frame()
}
func TestChatKeyboardDoesNotBlockSiblingClicks(t *testing.T) {
	h := newChatInputHarness(t)
	h.send(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: f32.Pt(50, 150), Buttons: pointer.ButtonPrimary})
	h.send(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(50, 150)})
	if !h.clicked {
		t.Fatal("chat keyboard registration swallowed a sibling button click")
	}
}
func TestChatGutterDoesNotBlockWheel(t *testing.T) {
	h := newChatInputHarness(t)
	before := h.page.list.Position
	h.send(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(102, 150), Scroll: f32.Pt(0, 120)})
	for i := 0; i < 16; i++ {
		h.frame()
	}
	if h.page.list.Position.First == before.First && h.page.list.Position.Offset == before.Offset {
		t.Fatal("gutter swallowed wheel scrolling")
	}
}

func TestTextDragReleasesPointerForOtherControls(t *testing.T) {
	h := newChatInputHarness(t)
	y := float32(h.page.rows[1].bodyTop + 12 + 8)
	h.send(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: f32.Pt(129, y), Buttons: pointer.ButtonPrimary})
	h.send(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(250, y), Buttons: pointer.ButtonPrimary})
	h.send(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(250, y)})
	h.frame()
	if h.page.activeText == nil || h.page.activeText.selectedText() == "" {
		t.Fatal("text was not selected")
	}
	h.send(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: f32.Pt(50, 150), Buttons: pointer.ButtonPrimary})
	h.send(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(50, 150)})
	if !h.clicked {
		t.Fatal("text drag kept pointer captured")
	}
	before := h.page.list.Position
	h.send(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(150, y), Scroll: f32.Pt(0, 120)})
	for i := 0; i < 16; i++ {
		h.frame()
	}
	if h.page.list.Position.First == before.First && h.page.list.Position.Offset == before.Offset {
		t.Fatal("text selection swallowed subsequent wheel input")
	}
}
