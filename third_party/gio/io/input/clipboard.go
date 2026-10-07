// SPDX-License-Identifier: Unlicense OR MIT

package input

import (
	"io"
	"slices"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
)

// clipboardState contains the state for clipboard event routing.
type clipboardState struct {
	receivers []event.Tag
}

type clipboardQueue struct {
	// request avoid read clipboard every frame while waiting.
	requested bool
	types     []string
	mime      string
	text      []byte
	html      []byte
}

// WriteClipboard returns the most recent data to be copied
// to the clipboard, if any.
func (q *clipboardQueue) WriteClipboard() (mime string, content []byte, ok bool) {
	mime, content, _, ok = q.WriteClipboardHTML()
	return mime, content, ok
}

// WriteClipboardHTML is WriteClipboard with the HTML of the content, if
// it has one (clipboard.WriteCmd.HTML).
func (q *clipboardQueue) WriteClipboardHTML() (mime string, content, html []byte, ok bool) {
	if q.text == nil {
		return "", nil, nil, false
	}
	content, html = q.text, q.html
	q.text, q.html = nil, nil
	return q.mime, content, html, true
}

// ClipboardRequested reports if any new handler is waiting
// to read the clipboard, and the types of content wanted.
func (q *clipboardQueue) ClipboardRequested(state clipboardState) ([]string, bool) {
	req := len(state.receivers) > 0 && q.requested
	q.requested = false
	if !req {
		return nil, false
	}
	return q.types, true
}

func (q *clipboardQueue) Push(state clipboardState, e event.Event) (clipboardState, []taggedEvent) {
	var evts []taggedEvent
	for _, r := range state.receivers {
		evts = append(evts, taggedEvent{tag: r, event: e})
	}
	state.receivers = nil
	q.types = nil
	return state, evts
}

func (q *clipboardQueue) ProcessWriteClipboard(req clipboard.WriteCmd) {
	defer req.Data.Close()
	content, err := io.ReadAll(req.Data)
	if err != nil {
		return
	}
	q.mime = req.Type
	q.text = content
	q.html = req.HTML
}

func (q *clipboardQueue) ProcessReadClipboard(state clipboardState, req clipboard.ReadCmd) clipboardState {
	types := req.Types
	if len(types) == 0 {
		types = []string{clipboard.TypeText}
	}
	for _, t := range types {
		if !slices.Contains(q.types, t) {
			q.types = append(q.types, t)
			q.requested = true
		}
	}
	if slices.Contains(state.receivers, req.Tag) {
		return state
	}
	n := len(state.receivers)
	state.receivers = append(state.receivers[:n:n], req.Tag)
	q.requested = true
	return state
}
