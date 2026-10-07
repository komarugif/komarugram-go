// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// RichPage is a rich message, Telegram's richMessage: the blocks of an
// article, as Instant View has them, in a message. Telegram sends a rich
// message with empty text; Message.Text holds RichPage.Summary instead, for
// the chat list, replies, notifications and search.
type RichPage struct {
	RTL bool `json:",omitempty"`
	// Part is set when Telegram sent the message cut short; RichStore loads
	// it whole.
	Part   bool `json:",omitempty"`
	Blocks []RichBlock
}

// RichText is a block's text: plain text with entities, whose offsets and
// lengths count UTF-16 code units, as a message's do. Anchors are the
// names inside it that a link to #name goes to, beyond the block's own;
// AnchorAt, where in the text each is, in UTF-16 code units, -1 or left
// out where that is not known, and the link goes to the block's top.
type RichText struct {
	Text     string   `json:",omitempty"`
	Entities []Entity `json:",omitempty"`
	Anchors  []string `json:",omitempty"`
	AnchorAt []int    `json:",omitempty"`
}

// AddAnchor adds the anchor name at the end of t.
func (t *RichText) AddAnchor(name string) {
	t.padAnchors()
	t.Anchors = append(t.Anchors, name)
	t.AnchorAt = append(t.AnchorAt, UTF16Len(t.Text))
}

// AnchorOffset is where anchor i of t is, -1 when that is not known.
func (t RichText) AnchorOffset(i int) int {
	if i < len(t.AnchorAt) {
		return t.AnchorAt[i]
	}
	return -1
}

// padAnchors gives every anchor of t an offset, -1 for those without.
func (t *RichText) padAnchors() {
	for len(t.AnchorAt) < len(t.Anchors) {
		t.AnchorAt = append(t.AnchorAt, -1)
	}
}

// Rich block kinds, RichBlock.Kind.
const (
	RichUnsupported = "unsupported"
	RichHeading     = "heading"
	RichParagraph   = "paragraph"
	RichFooter      = "footer"
	RichThinking    = "thinking"
	RichAuthorDate  = "author_date"
	RichCode        = "code"
	RichDivider     = "divider"
	RichAnchor      = "anchor"
	RichButtons     = "buttons"
	RichList        = "list"
	RichQuote       = "quote"
	RichMediaBlock  = "media"
	RichEmbed       = "embed"
	RichEmbedPost   = "embed_post"
	RichChannel     = "channel"
	RichMath        = "math"
	RichTable       = "table"
	RichDetails     = "details"
	RichRelated     = "related"
	RichMap         = "map"
)

// RichBlock is one block of a rich message. Which fields mean something
// depends on Kind.
type RichBlock struct {
	Kind string
	// Anchor is the name a link to #name scrolls to.
	Anchor string `json:",omitempty"`
	// Text is a heading's, a paragraph's, a code block's or a quote's text;
	// the author of an author_date; the title of a table, details or
	// related articles.
	Text    RichText `json:",omitzero"`
	Caption RichText `json:",omitzero"`
	// Level is a heading's, from 1 to 6.
	Level int `json:",omitempty"`
	// Language is a code block's.
	Language string `json:",omitempty"`
	// Formula is a math block's LaTeX source.
	Formula string `json:",omitempty"`
	// Date is an author_date's or an embedded post's.
	Date time.Time `json:",omitzero"`
	// Collapsed is a quote that shows its first lines only; Pullquote, one
	// set off from the text.
	Collapsed bool `json:",omitempty"`
	Pullquote bool `json:",omitempty"`
	// Open is details shown open.
	Open bool `json:",omitempty"`
	// Ordered, Reversed, Start and Type are an ordered list's: its numbers
	// go down when Reversed, begin at Start when it is set, and are
	// written as Type says ("a", "A", "i", "I" or their CSS names).
	Ordered  bool           `json:",omitempty"`
	Reversed bool           `json:",omitempty"`
	Start    *int           `json:",omitempty"`
	Type     string         `json:",omitempty"`
	Items    []RichListItem `json:",omitempty"`
	// Blocks are what a quote, details or an embedded post hold.
	Blocks []RichBlock `json:",omitempty"`
	// Bordered, Striped and Compact are a table's style.
	Bordered bool           `json:",omitempty"`
	Striped  bool           `json:",omitempty"`
	Compact  bool           `json:",omitempty"`
	Rows     []RichTableRow `json:",omitempty"`
	// Media is a media block's: one photo, video, audio or file, or several
	// as a collage, or a slideshow when Slideshow is set.
	Media     []RichMedia `json:",omitempty"`
	Slideshow bool        `json:",omitempty"`
	// Buttons are a row of buttons, aligned as Align says: "left",
	// "center", "right", or stretched across when it is empty.
	Buttons []RichButton `json:",omitempty"`
	Align   string       `json:",omitempty"`
	// URL is what an embed or an embedded post shows; Author, the post's.
	URL    string `json:",omitempty"`
	Author string `json:",omitempty"`
	// Channel, Title and Username are a channel block's.
	Channel  int64  `json:",omitempty"`
	Title    string `json:",omitempty"`
	Username string `json:",omitempty"`
	// Latitude, Longitude and Zoom are a map's; Width and Height, a map's
	// or an embed's size.
	Latitude  float64       `json:",omitempty"`
	Longitude float64       `json:",omitempty"`
	Zoom      int           `json:",omitempty"`
	Width     int           `json:",omitempty"`
	Height    int           `json:",omitempty"`
	Related   []RichArticle `json:",omitempty"`
}

// RichListItem is an item of a list: text, or blocks of its own. Num is an
// ordered item's own number, as written; Value and Type, its number and how
// it is written, when they differ from the list's.
type RichListItem struct {
	Checkbox bool        `json:",omitempty"`
	Checked  bool        `json:",omitempty"`
	Num      string      `json:",omitempty"`
	Value    *int        `json:",omitempty"`
	Type     string      `json:",omitempty"`
	Anchor   string      `json:",omitempty"`
	Text     RichText    `json:",omitzero"`
	Blocks   []RichBlock `json:",omitempty"`
}

// RichTableRow is a row of a table.
type RichTableRow struct {
	Cells []RichTableCell
}

// RichTableCell is a cell of a table. Align is "center" or "right", VAlign
// "middle" or "bottom", when they are not the default.
type RichTableCell struct {
	Text    RichText `json:",omitzero"`
	Colspan int      `json:",omitempty"`
	Rowspan int      `json:",omitempty"`
	Header  bool     `json:",omitempty"`
	Align   string   `json:",omitempty"`
	VAlign  string   `json:",omitempty"`
}

// RichMedia is a photo, video, audio or file of a rich message. Media is nil
// when the message does not carry the photo or document it names.
type RichMedia struct {
	Kind     MessageKind
	Media    *MessageMedia `json:",omitempty"`
	Spoiler  bool          `json:",omitempty"`
	Autoplay bool          `json:",omitempty"`
	Loop     bool          `json:",omitempty"`
}

// RichButton is a button of a rich message, with its style: "primary",
// "danger", "success" or "link", or empty.
type RichButton struct {
	Text   RichText `json:",omitzero"`
	Button MessageButton
	Style  string `json:",omitempty"`
}

// RichArticle is one of a block of related articles.
type RichArticle struct {
	URL         string    `json:",omitempty"`
	Title       string    `json:",omitempty"`
	Description string    `json:",omitempty"`
	Author      string    `json:",omitempty"`
	Date        time.Time `json:",omitzero"`
}

// RichStore loads the whole of a rich message that Telegram sent cut short
// (RichPage.Part).
type RichStore interface {
	RichMessage(ctx context.Context, key MessageKey) (RichPage, error)
}

// AnchorName is an anchor's name, or the target of a link to #name, as
// links find it, normalized as Telegram Desktop does: unescaped, trimmed,
// lower case, without leading '#'.
func AnchorName(name string) string {
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	return strings.TrimLeft(strings.ToLower(strings.TrimSpace(name)), "#")
}
