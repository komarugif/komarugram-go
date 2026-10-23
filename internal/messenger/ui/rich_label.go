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

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
)

// richLabel is a label of text runs, as a message's text has them: bold
// and italic, and custom emoji drawn as their pictures, where label has
// one style. A text of the catalog marks its bold parts with "**", as
// Telegram Desktop's: see markedRuns.
type richLabel struct {
	runs  []model.TextRun
	style token.Typestyle
	color token.MatColor
	align text.Alignment
	// maxLines 1 keeps the text on one line, cut with an ellipsis where it
	// does not fit; 0 wraps all of it.
	maxLines int
	// media and images draw the custom emoji, which stay their fallback
	// emoji without them.
	media   *chatmedia.Manager
	images  *imageOps
	animate bool
}

// emojiFrame is the frame of msg, a custom emoji, from media: in col
// when Telegram marks it of the text's color, as Telegram Desktop draws
// it; nil until it has loaded.
func emojiFrame(media *chatmedia.Manager, images *imageOps, msg model.Message, animate bool, col color.NRGBA) image.Image {
	frame, _ := media.Frame(msg, animate)
	if frame != nil && media.TextColored(msg) {
		frame = images.Colored(frame, col)
	}
	return frame
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
		if run.Emoji != 0 && r.media != nil && r.images != nil {
			msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("emoji/%d", run.Emoji), MIMEType: "application/x-custom-emoji"}}
			// A square of its own, a little over the letters' size: the
			// fallback emoji's glyph may be any width, or missing from the
			// fonts. It is kept while the picture loads.
			em := gtx.Sp(ty.Size) * 6 / 5
			st.Box = &styledtext.Box{Size: image.Pt(em, em), Ascent: em * 4 / 5}
			frames[i] = emojiFrame(r.media, r.images, msg, r.animate, fg)
		}
		styles[i] = st
	}
	t := styledtext.Text(theme.TextShaper, styles...)
	t.Alignment = r.align
	t.Decorate = func(gtx layout.Context, f styledtext.Fragment, draw func()) {
		draw()
		if frame := frames[f.Index]; frame != nil {
			drawImage(gtx, r.images, frame, f.Bounds.Size())
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
