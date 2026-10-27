// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/model"
)

// telegramBlue is the color Telegram paints its check mark and Premium star
// with, whatever the theme around them.
var telegramBlue = color.NRGBA{R: 0x40, G: 0xa7, B: 0xe3, A: 0xff}

// badgesLayout returns what goes before and after the name of a peer, called
// name, with badges b, nil
// where there is nothing; see App.badges.
type badgesLayout func(b model.Badges, name string, size unit.Dp, both bool) (before, after layout.Widget)

// localPremium reports whether user is one of the accounts signed in here
// while Local Premium is on: they show the Premium star to themselves, as
// in AyuGram, and only there; Telegram does not know of it.
func (a *App) localPremium(user int64) bool {
	if user == 0 || !a.preferences.Global().LocalPremium {
		return false
	}
	if user == a.store.Me().ID {
		return true
	}
	own := a.ownUsers.Load()
	return own != nil && (*own)[user]
}

// refreshOwnUsers reads the users of the accounts signed in here.
func (a *App) refreshOwnUsers(accounts model.Accounts) {
	own := map[int64]bool{}
	for _, info := range accounts.All() {
		if info.UserID != 0 {
			own[info.UserID] = true
		}
	}
	a.ownUsers.Store(&own)
}

// badges follows Telegram Desktop. A third party's verification is its
// custom emoji before the name. After the name, SCAM or FAKE replaces every
// other mark; otherwise the check mark of a verified name, and, where both
// is set (the profile) or there is no check mark, the emoji status — or the
// Premium star while there is no status or it has not loaded. The marks of
// the forks of Telegram clients (peerMarks) come first, and stay beside
// SCAM or FAKE. name is what the popup of such a mark calls the owner; size
// is the height of the name's line. The popup is a toast of the chat list.
func (a *App) badges(b model.Badges, name string, size unit.Dp, both bool) (before, after layout.Widget) {
	return a.toldBadges(b, name, size, both, nil)
}

// badgesTelling is badges whose popups tell shows: a dialog's toast, which
// the chat's would lie under.
func (a *App) badgesTelling(tell func(text string)) badgesLayout {
	return func(b model.Badges, name string, size unit.Dp, both bool) (before, after layout.Widget) {
		return a.toldBadges(b, name, size, both, tell)
	}
}

// toldBadges is badges with the popups of the marks shown by tell, nil for
// the chat list's toast.
func (a *App) toldBadges(b model.Badges, name string, size unit.Dp, both bool, tell func(text string)) (before, after layout.Widget) {
	b.Premium = b.Premium || a.localPremium(b.User)
	if b.BotVerification != 0 {
		before = func(gtx layout.Context) layout.Dimensions {
			return a.layoutCustomEmoji(gtx, b.BotVerification, size)
		}
	}
	marks := a.peerMarks(b, name, size, tell)
	switch {
	case b.Scam:
		return before, joinMarks(append(marks, func(gtx layout.Context) layout.Dimensions { return layoutTextBadge(gtx, "SCAM") }))
	case b.Fake:
		return before, joinMarks(append(marks, func(gtx layout.Context) layout.Dimensions { return layoutTextBadge(gtx, "FAKE") }))
	}
	if b.Verified {
		marks = append(marks, func(gtx layout.Context) layout.Dimensions { return layoutVerified(gtx, size) })
	}
	if b.EmojiStatus != 0 && (both || !b.Verified) {
		premium := b.Premium && !b.Verified
		status := func(gtx layout.Context) layout.Dimensions {
			if dims := a.layoutCustomEmoji(gtx, b.EmojiStatus, size); dims.Size.X > 0 || !premium {
				return dims
			}
			return a.layoutPremiumMark(gtx, size)
		}
		if both && a.statusClicked != nil {
			// In the profile a click on the status shows its emoji set.
			emoji := b.EmojiStatus
			status = a.badgeClick(badgeKey{emoji, badgeStatus}, status, func() { a.statusClicked(emoji) })
		}
		marks = append(marks, status)
	} else if b.Premium && !b.Verified {
		marks = append(marks, func(gtx layout.Context) layout.Dimensions { return a.layoutPremiumMark(gtx, size) })
	}
	return before, joinMarks(marks)
}

// joinMarks puts marks in a row, nil for none.
func joinMarks(marks []layout.Widget) layout.Widget {
	switch len(marks) {
	case 0:
		return nil
	case 1:
		return marks[0]
	}
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, 2*len(marks))
		for i, mark := range marks {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: 2}.Layout))
			}
			children = append(children, layout.Rigid(mark))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
}

// layoutCustomEmoji draws custom emoji id in a square of size, or nothing
// until it has loaded.
func (a *App) layoutCustomEmoji(gtx layout.Context, id int64, size unit.Dp) layout.Dimensions {
	return a.layoutTintedEmoji(gtx, id, size, telegramBlue)
}

// layoutTintedEmoji is layoutCustomEmoji for an emoji of the color of the
// text, which is drawn in tint, as Premium's star is in Telegram's blue.
func (a *App) layoutTintedEmoji(gtx layout.Context, id int64, size unit.Dp, tint color.NRGBA) layout.Dimensions {
	if a.avatars == nil {
		return layout.Dimensions{}
	}
	// Drawn in the pixels it takes on screen, which the one picture the
	// manager keeps of a media is decoded in: the same emoji is drawn in
	// several sizes, in the list, the header and the profile, so each size
	// is a media of its own, "emoji/id@pixels", resampled by no one.
	px := gtx.Dp(size)
	// One of the text's color is in the tint, as a status is.
	frame := customEmoji(a.avatars.media, &a.images, id, px, a.window.Motion.AnimationsEnabled(), tint)
	if frame == nil {
		return layout.Dimensions{}
	}
	return drawImage(gtx, &a.images, frame, image.Pt(px, px))
}

// withBadges draws name with before in front of it and after right behind
// it. The name gives up room to the badges when the line is short; badges
// are centered on the name's line.
func withBadges(gtx layout.Context, before, name, after layout.Widget) layout.Dimensions {
	if before == nil && after == nil {
		return name(gtx)
	}
	measure := func(w layout.Widget) (layout.Dimensions, op.CallOp) {
		if w == nil {
			return layout.Dimensions{}, op.CallOp{}
		}
		macro := op.Record(gtx.Ops)
		badgeGtx := gtx
		badgeGtx.Constraints.Min = image.Point{}
		dims := w(badgeGtx)
		return dims, macro.Stop()
	}
	beforeDims, beforeCall := measure(before)
	afterDims, afterCall := measure(after)
	gap := gtx.Dp(4)
	x := 0
	if beforeDims.Size.X > 0 {
		x = beforeDims.Size.X + gap
	}
	rest := x
	if afterDims.Size.X > 0 {
		rest += gap + afterDims.Size.X
	}
	nameGtx := gtx
	// The name is as large as its text, so that the badges sit beside it
	// whatever the constraints ask of the whole line.
	nameGtx.Constraints.Min = image.Point{}
	nameGtx.Constraints.Max.X = max(gtx.Constraints.Max.X-rest, 0)
	macro := op.Record(gtx.Ops)
	nameDims := name(nameGtx)
	nameCall := macro.Stop()
	height := max(nameDims.Size.Y, beforeDims.Size.Y, afterDims.Size.Y)
	place := func(at int, dims layout.Dimensions, call op.CallOp) {
		offset(gtx, image.Pt(at, (height-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
	}
	if beforeDims.Size.X > 0 {
		place(0, beforeDims, beforeCall)
	}
	place(x, nameDims, nameCall)
	width := x + nameDims.Size.X
	if afterDims.Size.X > 0 {
		place(width+gap, afterDims, afterCall)
		width += gap + afterDims.Size.X
	}
	return layout.Dimensions{Size: image.Pt(width, height), Baseline: nameDims.Baseline + (height-nameDims.Size.Y)/2}
}

// The shapes below are drawn after Telegram Desktop's 18×16 dp badge icons:
// a rosette of eight lobes with a check mark for a verified name, about 12 dp
// across and centered in a box as high as the line, and a five-pointed star
// with rounded points, which the Premium icon of the settings is drawn with;
// the Premium star beside a name is not drawn, but taken from an emoji set
// (peer_badges.go).

// premiumStarIcon draws the Premium star in col: a five-pointed star with
// rounded points, rounder than a geometric one, whose lower left point is
// parted from the rest by a thin curved slit that runs from the notch
// beside it almost to the middle — Telegram Premium's mark.
func premiumStarIcon(gtx layout.Context, col token.MatColor, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	drawShape(gtx, shapeKey{kind: shapePremiumStar, size: image.Pt(px, px), color: col.AsNRGBA()}, func(gtx layout.Context) {
		drawPremiumStar(gtx, col, px)
	})
	return layout.Dimensions{Size: image.Pt(px, px)}
}

func drawPremiumStar(gtx layout.Context, col token.MatColor, px int) {
	scale := float32(px) / 18
	center := f32.Pt(float32(px)/2, float32(px)/2+0.4*scale)
	// Larger and fuller than Telegram Desktop's 6 dp star: its thin points
	// and the slit leave a star of that size looking smaller than the check
	// mark beside the same name. At 8.6 dp with notches at 0.56 of that, it
	// covers about as much as the check mark.
	outer := 8.6 * scale
	polar := func(r float32, degrees float64) f32.Point {
		angle := degrees * math.Pi / 180
		return center.Add(f32.Pt(r*float32(math.Cos(angle)), r*float32(math.Sin(angle))))
	}
	var points [10]f32.Point
	for i := range points {
		r := outer
		if i%2 == 1 {
			r *= 0.56
		}
		points[i] = polar(r, float64(i)*36-90)
	}
	// The slit, from outside the notch at 162° (screen angles, clockwise
	// from the right) bending up to just right of the middle.
	start := polar(outer*0.75, 162)
	control := polar(outer*0.28, 150)
	end := center.Add(f32.Pt(0.1*outer, -0.1*outer))
	half := 0.055 * outer
	const steps = 12
	var left, right []f32.Point
	for i := 0; i <= steps; i++ {
		t := float32(i) / steps
		// A quadratic Bézier and its tangent.
		p := start.Mul((1 - t) * (1 - t)).Add(control.Mul(2 * (1 - t) * t)).Add(end.Mul(t * t))
		d := control.Sub(start).Mul(2 * (1 - t)).Add(end.Sub(control).Mul(2 * t))
		length := float32(math.Hypot(float64(d.X), float64(d.Y)))
		n := f32.Pt(-d.Y/length*half, d.X/length*half)
		left = append(left, p.Add(n))
		right = append(right, p.Sub(n))
	}
	slit := append([]f32.Point(nil), left...)
	for i := len(right) - 1; i >= 0; i-- {
		slit = append(slit, right[i])
	}
	// A hole is a path wound against the outline around it.
	if (signedArea(slit) > 0) == (signedArea(points[:]) > 0) {
		slices.Reverse(slit)
	}
	star := func(path *clip.Path) {
		roundedPolygon(path, points[:], func(i int) float32 {
			if i%2 == 0 {
				return 0.3 // Points are well rounded.
			}
			return 0.12
		})
	}
	// The slit starts outside the star, so that it opens the notch cleanly;
	// clipped to the star, its outer end does not paint.
	var outline clip.Path
	outline.Begin(gtx.Ops)
	star(&outline)
	defer clip.Outline{Path: outline.End()}.Op().Push(gtx.Ops).Pop()
	var path clip.Path
	path.Begin(gtx.Ops)
	star(&path)
	path.MoveTo(slit[0])
	for _, p := range slit[1:] {
		path.LineTo(p)
	}
	path.Close()
	paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.Outline{Path: path.End()}.Op())
}

// signedArea is twice the signed area of polygon, positive when it winds
// one way and negative the other.
func signedArea(polygon []f32.Point) float32 {
	var area float32
	for i, p := range polygon {
		q := polygon[(i+1)%len(polygon)]
		area += p.X*q.Y - q.X*p.Y
	}
	return area
}

// roundedPolygon adds the closed polygon points to path with each corner
// cut at the fraction round(i) of its edges and bent through the corner.
func roundedPolygon(path *clip.Path, points []f32.Point, round func(i int) float32) {
	n := len(points)
	lerp := func(a, b f32.Point, t float32) f32.Point { return a.Add(b.Sub(a).Mul(t)) }
	for i := range points {
		prev, corner, next := points[(i+n-1)%n], points[i], points[(i+1)%n]
		t := round(i)
		in, out := lerp(corner, prev, t), lerp(corner, next, t)
		if i == 0 {
			path.MoveTo(in)
		} else {
			path.LineTo(in)
		}
		path.QuadTo(corner, out)
	}
	path.Close()
}

// layoutVerified draws the check mark of a verified name in a square of
// size.
func layoutVerified(gtx layout.Context, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	drawShape(gtx, shapeKey{kind: shapeVerified, size: image.Pt(px, px), color: telegramBlue}, func(gtx layout.Context) {
		drawVerified(gtx, px)
	})
	return layout.Dimensions{Size: image.Pt(px, px)}
}

func drawVerified(gtx layout.Context, px int) {
	scale := float32(px) / 18
	center := f32.Pt(float32(px)/2, float32(px)/2)
	radius := 6.6 * scale
	fillRosette(gtx, px, telegramBlue)

	at := func(x, y float32) f32.Point { return center.Add(f32.Pt(x*radius, y*radius)) }
	var check clip.Path
	check.Begin(gtx.Ops)
	check.MoveTo(at(-0.34, 0.04))
	check.LineTo(at(-0.1, 0.3))
	check.LineTo(at(0.4, -0.26))
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, clip.Stroke{Path: check.End(), Width: 0.2 * radius}.Op())
}

// layoutTextBadge draws SCAM or FAKE: red capitals in a thin red frame.
func layoutTextBadge(gtx layout.Context, text string) layout.Dimensions {
	col := scheme(gtx).Error.Color
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Left: 3, Right: 3, Top: 1, Bottom: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return label(gtx, text, token.TypestyleLabelSmallEmphasized, col, 1)
	})
	call := macro.Stop()
	radius := gtx.Dp(3)
	drawShape(gtx, shapeKey{kind: shapeTextBadgeFrame, size: dims.Size, color: col.AsNRGBA(), radius: radius}, func(gtx layout.Context) {
		border := clip.UniformRRect(image.Rectangle{Max: dims.Size}, radius)
		paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.Stroke{Path: border.Path(gtx.Ops), Width: float32(max(gtx.Dp(1), 1))}.Op())
	})
	call.Add(gtx.Ops)
	return dims
}

// iconPremium is the Premium star as an icon, in the color it is given.
func iconPremium(gtx layout.Context, col token.MatColor) layout.Dimensions {
	return premiumStarIcon(gtx, col, unit.Dp(float32(gtx.Constraints.Max.X)/gtx.Metric.PxPerDp))
}

// drawBadge draws an unread counter pill whose left edge is at origin.
func drawBadge(gtx layout.Context, origin image.Point, count int, background, foreground token.MatColor) image.Point {
	txt := fmt.Sprint(count)
	if count > 999 {
		txt = "999+"
	}
	macro := op.Record(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints.Min = image.Point{}
	dims := label(labelGtx, txt, token.TypestyleLabelSmallEmphasized, foreground, 1)
	call := macro.Stop()
	height := gtx.Dp(18)
	width := max(height, dims.Size.X+gtx.Dp(10))
	size := image.Pt(width, height)
	offset(gtx, origin, func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, background, size, height/2)
		return offset(gtx, image.Pt((width-dims.Size.X)/2, (height-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
	})
	return size
}

// drawBadgeRight draws an unread counter whose right edge is at right.X and
// returns its width.
func drawBadgeRight(gtx layout.Context, right image.Point, count int, background, foreground token.MatColor) int {
	macro := op.Record(gtx.Ops)
	size := drawBadge(gtx, image.Point{}, count, background, foreground)
	call := macro.Stop()
	offset(gtx, image.Pt(right.X-size.X, right.Y), func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
	return size.X
}

// fillRosette fills the rosette of a verified name, of eight lobes, in col.
func fillRosette(gtx layout.Context, px int, col color.NRGBA) {
	scale := float32(px) / 18
	center := f32.Pt(float32(px)/2, float32(px)/2)
	var rosette clip.Path
	rosette.Begin(gtx.Ops)
	const steps = 160
	for i := 0; i <= steps; i++ {
		angle := float64(i) / steps * 2 * math.Pi
		// Eight round lobes with sharp notches between them, from 6.2 dp at a
		// notch to 7 dp at a lobe, turned so that a notch is at the top.
		r := 6.2 * scale * float32(1+0.13*math.Abs(math.Cos(4*(angle-math.Pi/8))))
		p := center.Add(f32.Pt(r*float32(math.Sin(angle)), -r*float32(math.Cos(angle))))
		if i == 0 {
			rosette.MoveTo(p)
		} else {
			rosette.LineTo(p)
		}
	}
	rosette.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: rosette.End()}.Op())
}
