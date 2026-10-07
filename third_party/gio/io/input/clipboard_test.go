// SPDX-License-Identifier: Unlicense OR MIT

package input

import (
	"io"
	"slices"
	"strings"
	"testing"

	"gioui.org/io/clipboard"
	"gioui.org/io/transfer"
	"gioui.org/op"
)

func TestClipboardDuplicateEvent(t *testing.T) {
	ops, r, handlers := new(op.Ops), new(Router), make([]int, 2)

	// Both must receive the event once.
	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[0]})
	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[1]})

	event := transfer.DataEvent{
		Type: "application/text",
		Open: func() io.ReadCloser {
			return io.NopCloser(strings.NewReader("Test"))
		},
	}
	r.Queue(event)
	for i := range handlers {
		f := transfer.TargetFilter{Target: &handlers[i], Type: "application/text"}
		assertEventTypeSequence(t, events(r, -1, f), transfer.DataEvent{})
	}
	assertClipboardReadCmd(t, r, 0)

	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[0]})

	r.Frame(ops)
	// No ClipboardEvent sent
	assertClipboardReadCmd(t, r, 1)
	for i := range handlers {
		f := transfer.TargetFilter{Target: &handlers[i]}
		assertEventTypeSequence(t, events(r, -1, f))
	}
}

func TestQueueProcessReadClipboard(t *testing.T) {
	ops, r, handler := new(op.Ops), new(Router), make([]int, 2)

	// Request read
	r.Source().Execute(clipboard.ReadCmd{Tag: &handler[0]})

	assertClipboardReadCmd(t, r, 1)
	ops.Reset()

	for range 3 {
		// No ReadCmd
		// One receiver must still wait for response

		r.Frame(ops)
		assertClipboardReadDuplicated(t, r, 1)
	}

	// Send the clipboard event
	event := transfer.DataEvent{
		Type: "application/text",
		Open: func() io.ReadCloser {
			return io.NopCloser(strings.NewReader("Text 2"))
		},
	}
	r.Queue(event)
	assertEventTypeSequence(t, events(r, -1, transfer.TargetFilter{Target: &handler[0], Type: "application/text"}), transfer.DataEvent{})
	assertClipboardReadCmd(t, r, 0)
}

// Handlers that wait together are asked the types they want, the first
// handler's first; one that asks for more while waiting asks again. Each
// gets the event only of the types it filters for, and none waits after it.
func TestClipboardReadTypes(t *testing.T) {
	r, handlers := new(Router), make([]int, 2)
	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[0], Types: []string{clipboard.TypeURIList, clipboard.TypePNG}})
	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[1]})
	types, ok := r.ClipboardRequested()
	if want := []string{clipboard.TypeURIList, clipboard.TypePNG, clipboard.TypeText}; !ok || !slices.Equal(types, want) {
		t.Fatalf("asked for %v, %v; want %v", types, ok, want)
	}
	if _, ok := r.ClipboardRequested(); ok {
		t.Fatal("asked twice")
	}
	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[0], Types: []string{clipboard.TypePNG}})
	if _, ok := r.ClipboardRequested(); ok {
		t.Fatal("asked again for a type asked for")
	}

	r.Queue(transfer.DataEvent{Type: clipboard.TypePNG, Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader("png")) }})
	assertEventTypeSequence(t, events(r, -1, transfer.TargetFilter{Target: &handlers[0], Type: clipboard.TypePNG}), transfer.DataEvent{})
	assertEventTypeSequence(t, events(r, -1, transfer.TargetFilter{Target: &handlers[1], Type: clipboard.TypeText}))
	assertClipboardReadCmd(t, r, 0)

	// The next read asks for its own types only.
	r.Source().Execute(clipboard.ReadCmd{Tag: &handlers[1]})
	if types, _ := r.ClipboardRequested(); !slices.Equal(types, []string{clipboard.TypeText}) {
		t.Fatalf("asked for %v", types)
	}
}

func TestQueueProcessWriteClipboard(t *testing.T) {
	r := new(Router)

	const mime = "application/text"
	r.Source().Execute(clipboard.WriteCmd{Type: mime, Data: io.NopCloser(strings.NewReader("Write 1"))})

	assertClipboardWriteCmd(t, r, mime, "Write 1")
	assertClipboardWriteCmd(t, r, "", "")

	r.Source().Execute(clipboard.WriteCmd{Type: mime, Data: io.NopCloser(strings.NewReader("Write 2"))})

	assertClipboardReadCmd(t, r, 0)
	assertClipboardWriteCmd(t, r, mime, "Write 2")
}

// A write's HTML comes with its text, and a later write without one has
// none.
func TestQueueProcessWriteClipboardHTML(t *testing.T) {
	r := new(Router)
	r.Source().Execute(clipboard.WriteCmd{Type: clipboard.TypeText, Data: io.NopCloser(strings.NewReader("bold")), HTML: []byte("<b>bold</b>")})
	if _, text, html, ok := r.WriteClipboardHTML(); !ok || string(text) != "bold" || string(html) != "<b>bold</b>" {
		t.Fatalf("%q %q %v", text, html, ok)
	}
	r.Source().Execute(clipboard.WriteCmd{Type: clipboard.TypeText, Data: io.NopCloser(strings.NewReader("plain"))})
	if _, text, html, ok := r.WriteClipboardHTML(); !ok || string(text) != "plain" || html != nil {
		t.Fatalf("%q %q %v", text, html, ok)
	}
}

func assertClipboardReadCmd(t *testing.T, router *Router, expected int) {
	t.Helper()
	if got := len(router.state().receivers); got != expected {
		t.Errorf("unexpected %d receivers, got %d", expected, got)
	}
	if _, ok := router.ClipboardRequested(); ok != (expected > 0) {
		t.Error("missing requests")
	}
}

func assertClipboardReadDuplicated(t *testing.T, router *Router, expected int) {
	t.Helper()
	if len(router.state().receivers) != expected {
		t.Error("receivers removed")
	}
	if _, ok := router.ClipboardRequested(); ok {
		t.Error("duplicated requests")
	}
}

func assertClipboardWriteCmd(t *testing.T, router *Router, mimeExp, expected string) {
	t.Helper()
	if (router.cqueue.text != nil) != (expected != "") {
		t.Error("text not defined")
	}
	mime, text, ok := router.cqueue.WriteClipboard()
	if ok != (expected != "") {
		t.Error("duplicated requests")
	}
	if string(mime) != mimeExp {
		t.Errorf("got MIME type %s, expected %s", mime, mimeExp)
	}
	if string(text) != expected {
		t.Errorf("got text %s, expected %s", text, expected)
	}
}
