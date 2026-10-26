// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/styledtext"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
)

// richLabel is a label of text runs, as a message's text has them: bold,
// italic, code, underlined and struck out, links in their color, spoilers
// covered, and custom emoji drawn as their pictures, where label has one
// style. It only shows them: a click on a link opens nothing. A text of
// the catalog marks its bold parts with "**", as Telegram Desktop's: see
// markedRuns.
type richLabel struct {
	runs  []model.TextRun
	style token.Typestyle
	color token.MatColor
	// link is the color of links, the scheme's primary when nil.
	link  *token.MatColor
	align text.Alignment
	// revealed uncovers the spoilers.
	revealed bool
	// maxLines 1 keeps the text on one line, cut with an ellipsis where it
	// does not fit; 0 wraps all of it.
	maxLines int
	// media and images draw the custom emoji, which stay their fallback
	// emoji without them.
	media   *chatmedia.Manager
	images  *imageOps
	animate bool
}

// customEmojiMessage is the media of custom emoji id shown px pixels
// square, "emoji/id@px": it is decoded in just that size, as a picture
// drawn at another size is resampled and blurred.
func customEmojiMessage(id int64, px int) model.Message {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("emoji/%d@%d", id, px), MIMEType: "application/x-custom-emoji"}}
}

// customEmoji is the picture of custom emoji id, px pixels square, from
// media: in col when Telegram marks it of the text's color, as Telegram
// Desktop draws it; nil until it has loaded. Every custom emoji of the
// client is drawn from it, in a text, a reaction, a status beside a name.
func customEmoji(media *chatmedia.Manager, images *imageOps, id int64, px int, animate bool, col color.NRGBA) image.Image {
	if media == nil || images == nil || px <= 0 {
		return nil
	}
	msg := customEmojiMessage(id, px)
	frame := media.StatusFit(msg, animate, image.Pt(px, px), false).Frame
	if frame != nil && media.TextColored(msg) {
		frame = images.Colored(frame, col)
	}
	return frame
}

// emojiBox is the square a custom emoji takes in a line of text of size,
// a little over the letters, on their baseline: the fallback emoji's glyph
// may be any width, or missing from the fonts. It is kept while the
// picture loads.
func emojiBox(gtx layout.Context, size unit.Sp) *styledtext.Box {
	em := gtx.Sp(size) * 6 / 5
	return &styledtext.Box{Size: image.Pt(em, em), Ascent: em * 4 / 5}
}

// markedRuns are the runs of a text whose bold parts are between "**".
func markedRuns(s string) []model.TextRun {
	var runs []model.TextRun
	for i, part := range strings.Split(s, "**") {
		if part != "" {
			runs = append(runs, model.TextRun{Text: part, Bold: i%2 == 1})
		}
	}
	return runs
}

func (r richLabel) Layout(gtx layout.Context) layout.Dimensions {
	theme := wdk.GetMaterialTheme(gtx)
	ty := theme.Typescale[r.style]
	fg := r.color.AsNRGBA()
	link := scheme(gtx).Primary.Color.AsNRGBA()
	if r.link != nil {
		link = r.link.AsNRGBA()
	}
	styles := make([]styledtext.SpanStyle, len(r.runs))
	frames := make([]image.Image, len(r.runs))
	for i, run := range r.runs {
		content := run.Text
		if r.maxLines == 1 {
			content = strings.ReplaceAll(content, "\n", " ")
		}
		st := styledtext.SpanStyle{Font: font.Font{Typeface: ty.Font, Weight: ty.Weight}, Size: ty.Size, Content: content, Color: fg}
		if run.Bold {
			st.Font.Weight = font.Bold
		}
		if run.Italic {
			st.Font.Style = font.Italic
		}
		if run.Code {
			st.Font.Typeface = theme.Typescale[token.TypestylePreformatted].Font
		}
		if run.URL != "" || run.Action != "" {
			st.Color = link
		}
		if run.Emoji != 0 && r.media != nil && r.images != nil {
			st.Box = emojiBox(gtx, ty.Size)
			if !run.Spoiler || r.revealed {
				frames[i] = customEmoji(r.media, r.images, run.Emoji, st.Box.Size.X, r.animate, fg)
			}
		}
		styles[i] = st
	}
	t := styledtext.Text(theme.TextShaper, styles...)
	t.Alignment = r.align
	t.Decorate = func(gtx layout.Context, f styledtext.Fragment, draw func()) {
		run, size := r.runs[f.Index], f.Bounds.Size()
		if run.Spoiler && !r.revealed {
			// Covered as the history's spoilers are before a click.
			paintSpoiler(gtx, size, f32.Point{}, 0, r.color.SetOpacity(.35).AsNRGBA(), nil)
			return
		}
		draw()
		if frame := frames[f.Index]; frame != nil {
			drawImage(gtx, r.images, frame, size)
		}
		if run.Underline || run.Strike || run.URL != "" || run.Action != "" {
			y := size.Y - 1
			if run.Strike {
				y = size.Y / 2
			}
			paint.FillShape(gtx.Ops, styles[f.Index].Color, clip.Rect(image.Rect(0, y, size.X, y+max(gtx.Dp(1), 1))).Op())
		}
	}
	if r.maxLines != 1 {
		t.MaxLines = r.maxLines
		return t.Layout(gtx, nil)
	}
	// One line is laid out whole, then cut where the ellipsis begins.
	width := gtx.Constraints.Max.X
	t.MaxLines = 1
	wide := gtx
	wide.Constraints = layout.Constraints{Max: image.Pt(1<<20, gtx.Constraints.Max.Y)}
	macro := op.Record(gtx.Ops)
	dims := t.Layout(wide, nil)
	line := macro.Stop()
	if dims.Size.X <= width {
		line.Add(gtx.Ops)
		return dims
	}
	macro = op.Record(gtx.Ops)
	ellipsis := label(gtx, "…", r.style, r.color, 1)
	dots := macro.Stop()
	cut := max(0, width-ellipsis.Size.X)
	stack := clip.Rect{Max: image.Pt(cut, dims.Size.Y)}.Push(gtx.Ops)
	line.Add(gtx.Ops)
	stack.Pop()
	// Its line is the text's, of the same style: centered on it.
	offset(gtx, image.Pt(cut, (dims.Size.Y-ellipsis.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
		dots.Add(gtx.Ops)
		return ellipsis
	})
	return layout.Dimensions{Size: image.Pt(width, dims.Size.Y)}
}
