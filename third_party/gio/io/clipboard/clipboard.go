// SPDX-License-Identifier: Unlicense OR MIT

package clipboard

import (
	"io"

	"gioui.org/io/event"
)

// WriteCmd copies Text to the clipboard.
type WriteCmd struct {
	Type string
	Data io.ReadCloser
	// HTML, when set with text, is the same content as HTML, offered
	// beside the text as TypeHTML, for editors and browsers that paste
	// its formatting. X11, Wayland, Windows and macOS offer it; the
	// others put the text alone.
	HTML []byte
}

// ReadCmd requests the content of the clipboard, delivered to
// the handler through an [io/transfer.DataEvent].
type ReadCmd struct {
	Tag event.Tag
	// Types wanted, the most wanted first; none means text. The event's
	// Type is the first the clipboard has, or empty for none of them.
	Types []string
}

const (
	TypeText    = "application/text"
	TypeURIList = "text/uri-list" // files copied in a file manager
	TypePNG     = "image/png"
	TypeHTML    = "text/html"
)

func (WriteCmd) ImplementsCommand() {}
func (ReadCmd) ImplementsCommand()  {}
