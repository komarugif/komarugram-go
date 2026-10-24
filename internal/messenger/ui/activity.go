// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// What others do in a chat now, typing, recording, sending a file, is
// drawn in place of its last message in the list and of its status in the
// header, as Telegram Desktop draws it: a mark that moves, and the words,
// in the accent's color, until it is over.

// activityStep is how long a picture of the mark lasts: it moves in a few
// steps a second, the window drawn for each only while someone acts.
const activityStep = 150 * time.Millisecond

// activityLine is what others do in a chat as a line, worded again only
// when it changes.
type activityLine struct {
	// acts, live, top, private and catalog are what text was worded of.
	acts    []model.Activity
	live    int
	top     int
	private bool
	catalog localization.Catalog
	text    string
	action  model.ChatAction
}

// update words what acts tell of thread top at gtx.Now, and has the window
// drawn again when the first of them ends; it reports whether anyone does
// anything.
func (a *activityLine) update(gtx layout.Context, acts []model.Activity, top int, private bool, catalog localization.Catalog) bool {
	live := 0
	var ends time.Time
	for _, x := range acts {
		if x.Until.After(gtx.Now) && (top < 0 || x.Top == top) {
			live++
			if ends.IsZero() || x.Until.Before(ends) {
				ends = x.Until
			}
		}
	}
	if live == 0 {
		return false
	}
	gtx.Execute(op.InvalidateCmd{At: ends})
	same := len(a.acts) == len(acts) && &a.acts[0] == &acts[0]
	if !same || a.live != live || a.top != top || a.private != private || a.catalog != catalog {
		a.text, a.action = catalog.Activity(acts, top, private, gtx.Now)
		a.acts, a.live, a.top, a.private, a.catalog = acts, live, top, private, catalog
	}
	return a.text != ""
}

// layout draws the mark and the words of the activity update found, in
// col, on a line of style.
func (a *activityLine) layout(gtx layout.Context, style token.Typestyle, col token.MatColor, animate bool) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	text := label(gtx, a.text, style, col, 1)
	words := macro.Stop()
	mark := image.Pt(gtx.Dp(18), text.Size.Y)
	if gtx.Constraints.Max.X < mark.X+gtx.Dp(24) {
		words.Add(gtx.Ops)
		return text
	}
	drawActivityMark(gtx, a.action, mark, col.AsNRGBA(), animate)
	gap := mark.X + gtx.Dp(4)
	textGtx := gtx
	textGtx.Constraints.Max.X -= gap
	macro = op.Record(gtx.Ops)
	text = label(textGtx, a.text, style, col, 1)
	words = macro.Stop()
	offset(gtx, image.Pt(gap, 0), func(gtx layout.Context) layout.Dimensions {
		words.Add(gtx.Ops)
		return text
	})
	return layout.Dimensions{Size: image.Pt(gap+text.Size.X, text.Size.Y)}
}

// drawActivityMark draws the mark of action in a box of size: a dot that
// swells for a recording, as Telegram Desktop's, three dots that light in
// turn for anything else. Stepping, it has the window drawn again for its
// next picture; still, it is its first.
func drawActivityMark(gtx layout.Context, action model.ChatAction, size image.Point, col color.NRGBA, animate bool) {
	step := int64(0)
	if animate {
		step = gtx.Now.UnixNano() / int64(activityStep)
		gtx.Execute(op.InvalidateCmd{At: time.Unix(0, (step+1)*int64(activityStep))})
	}
	cy := float32(size.Y) / 2
	dot := func(x, r float32, alpha uint8) {
		c := col
		c.A = uint8(uint32(c.A) * uint32(alpha) / 255)
		box := image.Rect(int(x-r), int(cy-r), int(x+r+.5), int(cy+r+.5))
		paint.FillShape(gtx.Ops, c, clip.Ellipse(box).Op(gtx.Ops))
	}
	unit := float32(gtx.Dp(1))
	switch action {
	case model.ActionRecordVoice, model.ActionRecordVideo, model.ActionRecordRound:
		// It swells and shrinks back in six steps.
		swell := []float32{2.5, 3, 3.5, 4, 3.5, 3}[step%6]
		dot(float32(size.X)/2, swell*unit, 255)
	default:
		gap := float32(size.X) / 3
		for i := range 3 {
			alpha := uint8(90)
			if int64(i) == step%4 {
				alpha = 255
			}
			dot(gap/2+gap*float32(i), 2*unit, alpha)
		}
	}
}

// layoutActivity draws what others do in chat c now in place of its status
// in the header, as Telegram Desktop's does; false when nobody does
// anything.
func (p *chatPage) layoutActivity(gtx layout.Context, c model.Chat, l localization.Catalog) (layout.Dimensions, bool) {
	source, ok := p.source.(model.ActivitySource)
	if !ok || c.Forum || !p.act.update(gtx, source.Activities(c.ID), 0, privateChat(c), l) {
		return layout.Dimensions{}, false
	}
	return p.act.layout(gtx, token.TypestyleBodySmall, scheme(gtx).Primary.Color, p.animated), true
}
