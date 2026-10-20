// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/resample"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// The cover of a chat's info, as Telegram Desktop's profile: the avatar
// with the name centered under it and the status, over the background of
// the gift the emoji status is, or the profile's color, with the gift's
// pattern and the gifts pinned to the profile around the avatar. Sizes are
// Telegram Desktop's, scaled from its avatar of 80 to pageAvatarSize.

const (
	coverAvatarTop = unit.Dp(24)
	coverGiftSize  = unit.Dp(24)
	coverGlowSize  = unit.Dp(48)
	coverScale     = float32(pageAvatarSize) / 80
)

// shuffledPlaces are the places of the cover's gifts in an order of
// chance, as Telegram Desktop gives them each time a profile opens.
func shuffledPlaces() [model.MaxCoverGifts]int {
	var places [model.MaxCoverGifts]int
	for i, j := range rand.Perm(model.MaxCoverGifts) {
		places[i] = j
	}
	return places
}

// coverMotion follows what moves in the cover and its header, for the
// streaks their motion leaves: see motionTrack.
type coverMotion struct {
	gifts  [model.MaxCoverGifts]motionTrack
	avatar motionTrack
	// giftStreaks and avatarStreak tell whether a gift's streak or the
	// avatar's was drawn in the frame drawn last.
	giftStreaks, avatarStreak bool
}

// drawStreak draws sprite, whose corner is at at, moved by streak while
// the shutter was open. The next frame is drawn too, in which the sprite
// that stopped is sharp again: otherwise the last streak would stay.
func drawStreak(gtx layout.Context, images *imageOps, sprite image.Image, at image.Point, streak f32.Point) {
	gtx.Execute(op.InvalidateCmd{})
	out, origin := blurSprite(sprite, streak)
	offset(gtx, at.Add(origin), func(gtx layout.Context) layout.Dimensions {
		return drawImage(gtx, images, out, out.Rect.Size())
	})
}

// coverFades fade in what the cover shows that was not there when it
// opened: the background of a profile not known before, and the pattern
// and the gifts, as their pictures load.
type coverFades struct {
	background, pattern wdk.FloatTween
	// gifts fly out from under the avatar, as Telegram Desktop's: from the
	// first frame, once their pictures have loaded.
	gifts  [model.MaxCoverGifts]wdk.FloatTween
	primed bool
	// launched is set once every gift has loaded and they have set off
	// together; a picture dropped later does not call them back.
	launched bool
	// places are where the gifts fly to, other each time the info opens.
	places [model.MaxCoverGifts]int
}

// easeOutQuint is Telegram Desktop's anim::easeOutQuint, which its gifts
// fly out with.
var easeOutQuint = token.Easing{X1: 0.22, Y1: 1, X2: 0.36, Y2: 1}

// coverGiftDelays are how late each place's gift sets off, in fifths of
// the flight, as Telegram Desktop's.
var coverGiftDelays = [model.MaxCoverGifts]float32{1.6, 0, 0.9, 1.6, 0.9, 0}

// giftFlight is where a gift of place is at progress of its flight from
// center to at: it sets off late by its delay, and goes out sideways
// faster than up or down, as Telegram Desktop's.
func giftFlight(place int, at, center image.Point, progress float32) image.Point {
	const delayFraction = .2
	const maxDelay = 1.6 * delayFraction
	delay := coverGiftDelays[place] * delayFraction
	out := float32(1)
	if progress < 1-delay {
		out = max(0, min(1, (progress-maxDelay+delay)/(1-maxDelay)))
	}
	outX := 1 - (1-out)*(1-out)
	return image.Pt(center.X+int(float32(at.X-center.X)*outX), center.Y+int(float32(at.Y-center.Y)*out))
}

// fade is how far t has faded in towards shown.
func fade(gtx layout.Context, t *wdk.FloatTween, shown bool) float32 {
	t.Duration = token.DurationMedium2
	if shown {
		return t.Animate(gtx, 1)
	}
	return t.Animate(gtx, 0)
}

// lerpColor is a color t of the way from a to b.
func lerpColor(a, b token.MatColor, t float32) token.MatColor {
	mix := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + .5) }
	return token.MatColor{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: mix(a.A, b.A)}
}

// profileResult is a chat's profile as the store told it.
type profileResult struct {
	chat    int64
	profile model.PeerProfile
	err     error
}

// coverState is what the cover keeps between frames.
type coverState struct {
	gifts [model.MaxCoverGifts]widget.Clickable
	// glows are the gifts' backdrops, by their messages' media.
	glows map[string]image.Image
	// gradient is the background drawn last, for gradientKey.
	gradient    image.Image
	gradientKey string
	// patternTint is patternSource, the pattern's emoji, in its color for
	// a dark theme or not.
	patternSource, patternTint image.Image
	patternDark                bool
	// nameBottom is where the name ends, from the top of the cover.
	nameBottom int
	// text is the color of the name over the cover as it fades in, which
	// the header's buttons take.
	text token.MatColor
	// avatar is where the avatar is in the cover, which scrolls.
	avatar image.Rectangle
}

// loadProfile asks for the open chat's profile.
func (p *chatInfo) loadProfile() {
	if p.profileCancel != nil {
		p.profileCancel()
		p.profileCancel = nil
	}
	p.profileResult = nil
	if p.profiles == nil || p.chat.ID == 0 || p.chat.Kind == model.KindSaved {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p.profileCancel = cancel
	ch := make(chan profileResult, 1)
	p.profileResult = ch
	source, chat, invalidate := p.profiles, p.chat.ID, p.invalidate
	go func() {
		defer cancel()
		defer crash.Recover("profile", func(e *crash.Panic) { ch <- profileResult{chat: chat, err: e} })
		profile, err := source.PeerProfile(ctx, chat)
		ch <- profileResult{chat: chat, profile: profile, err: err}
		invalidate()
	}()
}

// drainProfile takes the profile that came. A profile is extra: one that
// did not come leaves the info as it is.
func (p *chatInfo) drainProfile() {
	select {
	case r := <-p.profileResult:
		p.profileResult = nil
		if r.chat == p.chat.ID && r.err == nil {
			p.profile = r.profile
			p.cover = coverState{}
		}
	default:
	}
}

// coverText is the color of the name over the cover, and whether there is
// a cover.
func (p *chatInfo) coverText(gtx layout.Context) (token.MatColor, bool) {
	c := p.profile.Cover
	switch {
	case c.Gift:
		return token.NewMatColorFromHexRGB(c.TextColor), true
	case len(p.coverColors(gtx)) > 0:
		return token.NewMatColorFromHexRGB(0xffffff), true
	}
	return scheme(gtx).Surface.OnColor, false
}

// coverColors are the profile color's background for the theme.
func (p *chatInfo) coverColors(gtx layout.Context) []uint32 {
	c := p.profile.Cover
	if isDark(gtx) && len(c.DarkColors) > 0 {
		return c.DarkColors
	}
	return c.Colors
}

// layoutCover draws the cover across the width it is given.
func (p *chatInfo) layoutCover(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	for i := range p.cover.gifts {
		if i < len(p.profile.Gifts) && p.cover.gifts[i].Clicked(gtx) {
			p.gift = newGiftDialog(p.profile.Gifts[i])
		}
	}
	width := gtx.Constraints.Max.X
	coverColor, covered := p.coverText(gtx)
	shown := fade(gtx, &p.fades.background, covered)
	textColor := lerpColor(scheme(gtx).Surface.OnColor, coverColor, shown)
	p.cover.text = textColor
	gtx.Constraints.Min = image.Point{}

	// The name and the status are measured first: they give the height.
	textGtx := gtx
	textGtx.Constraints.Max.X = max(0, width-gtx.Dp(32))
	macro := op.Record(gtx.Ops)
	var before, after layout.Widget
	if p.badges != nil {
		before, after = p.badges(p.chat.Badges, 22, true)
	}
	nameDims := withBadges(textGtx, before, func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.ChatTitle(p.chat), token.TypestyleTitleLarge, textColor, 1)
	}, after)
	name := macro.Stop()
	macro = op.Record(gtx.Ops)
	statusDims := centeredStatus(textGtx, p.renderer.chatStatusOnline(p.chat, gtx.Now, l), textColor.SetOpacity(.75))
	status := macro.Stop()

	avatar := gtx.Dp(pageAvatarSize)
	avatarAt := image.Pt((width-avatar)/2, gtx.Dp(coverAvatarTop))
	nameY := avatarAt.Y + avatar + gtx.Dp(12)
	statusY := nameY + nameDims.Size.Y + gtx.Dp(2)
	size := image.Pt(width, statusY+statusDims.Size.Y+gtx.Dp(16))
	p.cover.nameBottom = statusY

	if covered && shown > 0 {
		opacity := paint.PushOpacity(gtx.Ops, shown)
		p.layoutCoverBackground(gtx, size, avatarAt, avatar)
		opacity.Pop()
	}
	userpic := image.Rectangle{Min: avatarAt, Max: avatarAt.Add(image.Pt(avatar, avatar))}
	if p.profile.Cover.Pattern != 0 {
		p.layoutCoverPattern(gtx, userpic)
	}
	p.layoutCoverGifts(gtx, userpic, animate)
	p.fades.primed = true
	// The avatar is drawn over the info, with the header: see layoutAvatar.
	p.cover.avatar = userpic
	offset(gtx, image.Pt((width-nameDims.Size.X)/2, nameY), func(gtx layout.Context) layout.Dimensions {
		name.Add(gtx.Ops)
		return nameDims
	})
	offset(gtx, image.Pt((width-statusDims.Size.X)/2, statusY), func(gtx layout.Context) layout.Dimensions {
		status.Add(gtx.Ops)
		return statusDims
	})
	return layout.Dimensions{Size: size}
}

// centeredStatus draws the status line in its own width.
func centeredStatus(gtx layout.Context, text string, col token.MatColor) layout.Dimensions {
	return label(gtx, text, token.TypestyleBodyMedium, col, 1)
}

// layoutCoverBackground fills size with the cover's background: solid, or
// a radial gradient around the avatar, as Telegram Desktop's.
func (p *chatInfo) layoutCoverBackground(gtx layout.Context, size, avatarAt image.Point, avatar int) {
	c := p.profile.Cover
	center, edge := uint32(0), uint32(0)
	switch colors := p.coverColors(gtx); {
	case c.Gift:
		center, edge = c.CenterColor, c.EdgeColor
	case len(colors) == 1:
		fillRect(gtx, token.NewMatColorFromHexRGB(colors[0]), size)
		return
	case len(colors) > 1:
		center, edge = colors[1], colors[0]
	default:
		return
	}
	// A quarter of the pixels is enough for a gradient, stretched smooth.
	small := image.Pt(max(1, size.X/4), max(1, size.Y/4))
	focus := image.Pt((avatarAt.X+avatar/2)/4, (avatarAt.Y+avatar/2)/4)
	key := fmt.Sprintf("%x/%x/%v/%v", center, edge, small, focus)
	if p.cover.gradientKey != key {
		p.cover.gradientKey = key
		p.cover.gradient = coverGradient(small, focus, chattheme.RGB(center), chattheme.RGB(edge))
	}
	widget.Image{Src: p.renderer.images.Op(p.cover.gradient), Fit: widget.Fill}.Layout(withSize(gtx, size))
}

// coverGradient is a radial gradient of size from center at focus to
// edge, out to the farther side.
func coverGradient(size, focus image.Point, center, edge color.NRGBA) image.Image {
	im := image.NewNRGBA(image.Rectangle{Max: size})
	radius := math.Max(float64(size.X)/2, float64(size.Y)) * 1.1
	for y := 0; y < size.Y; y++ {
		for x := 0; x < size.X; x++ {
			t := min(1, math.Hypot(float64(x-focus.X), float64(y-focus.Y))/radius)
			im.SetNRGBA(x, y, mix(center, edge, t, 255))
		}
	}
	return im
}

func mix(a, b color.NRGBA, t float64, alpha uint8) color.NRGBA {
	lerp := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.NRGBA{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B), A: alpha}
}

// coverPatternPoint is where the pattern's emoji goes around the avatar,
// and its size.
type coverPatternPoint struct {
	x, y, size float32
}

// coverPattern are Telegram Desktop's points of the pattern around
// userpic: four rings.
func coverPattern(userpic image.Rectangle, dp float32) []coverPatternPoint {
	ax, ay := float32(userpic.Min.X), float32(userpic.Min.Y)
	aw, ah := float32(userpic.Dx()), float32(userpic.Dy())
	acx, acy := ax+aw/2, ay+ah/2
	s := func(v float32) float32 { return v * .8 * coverScale * dp }
	r48 := (s(48) + aw/2) * float32(math.Cos(120*math.Pi/180))
	r16 := (s(16) + ah/2) * float32(math.Cos(160*math.Pi/180))
	size := func(v float32) float32 { return v * coverScale * dp }
	return []coverPatternPoint{
		{acx, ay - s(24), size(20)},
		{acx, ay + ah + s(24), size(20)},
		{ax - s(16), acy - ah/4 - s(8), size(23)},
		{ax + aw + s(16), acy - ah/4 - s(8), size(18)},
		{ax - s(16), acy + ah/4 + s(8), size(24)},
		{ax + aw + s(16) - 4*dp, acy + ah/4 + s(8), size(24)},

		{ax - s(48), acy, size(19)},
		{ax + aw + s(48), acy, size(19)},
		{acx + r48, ay - s(48) + s(12), size(17)},
		{acx - r48, ay - s(48) + s(12), size(17)},
		{acx + r48, ay + ah + s(48) - s(12), size(20)},
		{acx - r48, ay + ah + s(48) - s(12), size(20)},

		{ax - s(48) - s(8), acy + r16, size(20)},
		{ax + aw + s(48) + s(8), acy + r16, size(19)},
		{ax - s(48) - s(8), acy - r16, size(21)},
		{ax + aw + s(48) + s(8), acy - r16, size(18)},

		{ax - s(96), acy, size(19)},
		{ax + aw + s(96), acy, size(19)},
	}
}

// layoutCoverPattern draws the pattern's emoji around userpic, fading with
// the distance from it and below it.
func (p *chatInfo) layoutCoverPattern(gtx layout.Context, userpic image.Rectangle) {
	c := p.profile.Cover
	px := gtx.Dp(24 * unit.Dp(coverScale))
	emoji := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("emoji/%d", c.Pattern), MIMEType: "application/x-custom-emoji"}}
	frame := p.renderer.media.StatusFit(emoji, false, image.Pt(px, px), false).Frame
	shown := fade(gtx, &p.fades.pattern, frame != nil)
	if frame == nil {
		return
	}
	if p.cover.patternSource != frame || p.cover.patternDark != isDark(gtx) {
		p.cover.patternSource, p.cover.patternDark = frame, isDark(gtx)
		// As Telegram Desktop's: a gift's pattern in its color; a profile
		// color's darkens its background; without either, it is gray.
		switch {
		case c.Gift:
			p.cover.patternTint = colorImage(frame, chattheme.RGB(patternTone(c.PatternColor, c.EdgeColor, isDark(gtx))))
		case len(p.coverColors(gtx)) > 0:
			p.cover.patternTint = colorImage(frame, color.NRGBA{A: 204})
		case isDark(gtx):
			p.cover.patternTint = colorImage(frame, color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 153})
		default:
			p.cover.patternTint = colorImage(frame, color.NRGBA{R: 0x4d, G: 0x4d, B: 0x4d, A: 153})
		}
	}
	acx, acy := float32(userpic.Min.X+userpic.Dx()/2), float32(userpic.Min.Y+userpic.Dy()/2)
	aw := float32(userpic.Dx())
	bottom := float32(userpic.Max.Y) + 8*gtx.Metric.PxPerDp
	for _, at := range coverPattern(userpic, gtx.Metric.PxPerDp) {
		distance := float32(math.Hypot(float64(at.x-acx), float64(at.y-acy)))
		alpha := .5 * (1 - min(1, distance/(aw*2))) * shown
		if at.y > bottom {
			alpha *= 1 - min(1, (at.y-bottom)/(56*gtx.Metric.PxPerDp))
		}
		if alpha <= 0 {
			continue
		}
		n := int(at.size)
		box := withSize(gtx, image.Pt(n, n))
		offset(box, image.Pt(int(at.x)-n/2, int(at.y)-n/2), func(gtx layout.Context) layout.Dimensions {
			defer paint.PushOpacity(gtx.Ops, alpha).Pop()
			return widget.Image{Src: p.renderer.images.Op(p.cover.patternTint), Fit: widget.Contain}.Layout(gtx)
		})
	}
}

// patternTone is the color of a gift's pattern over its backdrop, as
// Telegram Desktop's: the pattern's color darkened, when that stands out
// more from the edge of the backdrop, as it does where the two are close.
func patternTone(pattern, edge uint32, dark bool) uint32 {
	ratio := 160. / 255
	if dark {
		ratio = 140. / 255
	}
	c := mix(chattheme.RGB(pattern), color.NRGBA{A: 255}, ratio, 255)
	darkened := uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	if contrast(darkened, edge) > contrast(pattern, edge) {
		return darkened
	}
	return pattern
}

// contrast is WCAG's contrast ratio of two 0xRRGGBB colors, from 1 to 21.
func contrast(a, b uint32) float64 {
	luminance := func(v uint32) float64 {
		c := chattheme.RGB(v)
		channel := func(x uint8) float64 {
			f := float64(x) / 255
			if f <= .03928 {
				return f / 12.92
			}
			return math.Pow((f+.055)/1.055, 2.4)
		}
		return .2126*channel(c.R) + .7152*channel(c.G) + .0722*channel(c.B)
	}
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + .05) / (min(la, lb) + .05)
}

// coverGiftAt is where gift place goes around userpic, as Telegram
// Desktop's, but the left ones mirror the right ones: Telegram Desktop's
// are a part of its narrow cover's width.
func coverGiftAt(place int, userpic image.Rectangle, dp float32) image.Point {
	acx := float32(userpic.Min.X + userpic.Dx()/2)
	acy := float32(userpic.Min.Y + userpic.Dy()/2)
	aw, ay, ah := float32(userpic.Dx()), float32(userpic.Min.Y), float32(userpic.Dy())
	s := func(v float32) float32 { return v * coverScale * dp }
	var x, y float32
	switch place {
	case 0: // Left.
		x, y = acx-aw/2-s(50), acy-s(13)
	case 1: // Top left.
		x, y = acx-aw/3-s(30), ay-s(4)
	case 2: // Bottom left.
		x, y = acx-aw/3-s(40), ay+ah-s(16)
	case 3: // Right.
		x, y = acx+aw/2+s(50), acy-s(13)
	case 4: // Top right.
		x, y = acx+aw/3+s(30), ay-s(4)
	default: // Bottom right.
		x, y = acx+aw/3+s(40), ay+ah-s(13)
	}
	return image.Pt(int(x), int(y))
}

// coverGiftsLoaded reports whether the pictures of all of the gifts are
// there, and there is a gift; one that failed to load does not hold the
// others back.
func coverGiftsLoaded(statuses []chatmedia.Status) bool {
	for _, st := range statuses {
		if st.Err == nil && st.Frame == nil && st.Preview == nil {
			return false
		}
	}
	return len(statuses) > 0
}

// layoutCoverGifts draws the pinned gifts around userpic, each on a glow
// of its backdrop; a click opens one.
func (p *chatInfo) layoutCoverGifts(gtx layout.Context, userpic image.Rectangle, animate bool) {
	if p.cover.glows == nil {
		p.cover.glows = map[string]image.Image{}
	}
	art, glow := gtx.Dp(coverGiftSize), gtx.Dp(coverGlowSize)
	p.motion.giftStreaks = false
	// The gifts fly out together, once all of their pictures have loaded,
	// as Telegram Desktop's do: a gift that has not failed holds the rest.
	var statuses [model.MaxCoverGifts]chatmedia.Status
	shown := 0
	for i, m := range p.profile.Gifts {
		if i >= len(statuses) {
			break
		}
		statuses[i] = p.renderer.media.StatusFullRate(m, animate, image.Pt(art, art), false)
		shown = i + 1
	}
	if p.fades.primed && p.modal.Entered() && coverGiftsLoaded(statuses[:shown]) {
		p.fades.launched = true
	}
	for i, m := range p.profile.Gifts {
		if i >= len(p.fades.places) {
			break
		}
		place := p.fades.places[i]
		// A gift and its glow fly out, coming into sight as they go.
		st := statuses[i]
		flight := &p.fades.gifts[i]
		flight.Duration, flight.Easing = 400*time.Millisecond, &easeOutQuint
		// The first frame of the info sets every flight at its start, so
		// that a gift decoded already flies out too, once the dialog has
		// grown.
		target := float32(0)
		if p.fades.launched && (st.Frame != nil || st.Preview != nil) {
			target = 1
		}
		progress := flight.Animate(gtx, target)
		if progress <= 0 {
			continue
		}
		center := userpic.Min.Add(userpic.Size().Div(2))
		at := giftFlight(place, coverGiftAt(place, userpic, gtx.Metric.PxPerDp), center, progress)
		// Its flight streaks it; the cover's scrolling, which moves all of
		// the info, does not.
		streak := p.motion.gifts[i].Streak(gtx, layout.FPt(at))
		blurred := animate && moving(streak)
		p.motion.giftStreaks = p.motion.giftStreaks || blurred
		opacity := paint.PushOpacity(gtx.Ops, progress)
		if g := m.Gift; g != nil && g.HasBackdrop && m.Media != nil {
			im := p.cover.glows[m.Media.ID]
			if im == nil {
				im = giftGlow(chattheme.RGB(g.CenterColor), chattheme.RGB(g.EdgeColor))
				p.cover.glows[m.Media.ID] = im
			}
			if blurred {
				drawStreak(gtx, p.renderer.images, resample.Resize(im, glow, glow), at.Sub(image.Pt(glow/2, glow/2)), streak)
			} else {
				offset(withSize(gtx, image.Pt(glow, glow)), at.Sub(image.Pt(glow/2, glow/2)), func(gtx layout.Context) layout.Dimensions {
					return widget.Image{Src: p.renderer.images.Op(im), Fit: widget.Fill}.Layout(gtx)
				})
			}
		}
		frame := st.Frame
		if frame == nil {
			frame = st.Preview
		}
		if blurred && frame != nil {
			b := frame.Bounds()
			fit := resample.Fit(b.Dx(), b.Dy(), image.Pt(art, art), false)
			drawStreak(gtx, p.renderer.images, resample.Resize(frame, fit.X, fit.Y), at.Sub(fit.Div(2)), streak)
		}
		offset(withSize(gtx, image.Pt(art, art)), at.Sub(image.Pt(art/2, art/2)), func(gtx layout.Context) layout.Dimensions {
			return p.cover.gifts[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				if blurred && frame != nil {
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}
				return p.giftArt(gtx, m, animate)
			})
		})
		opacity.Pop()
	}
}

// giftGlow is a gift's backdrop as the cover shows it: its center color
// fading out to the edge.
func giftGlow(center, edge color.NRGBA) image.Image {
	const n = 32
	im := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			t := min(1, math.Hypot(float64(x)-n/2+.5, float64(y)-n/2+.5)/(n/2))
			im.SetNRGBA(x, y, mix(center, edge, t, uint8(255*(1-t))))
		}
	}
	return im
}

// withSize is gtx with exact constraints of size.
func withSize(gtx layout.Context, size image.Point) layout.Context {
	gtx.Constraints = layout.Exact(size)
	return gtx
}

// The header of the info, over it.
const (
	infoHeaderHeight = unit.Dp(72)
	headerAvatarSize = unit.Dp(36)
)

// layoutInfoHeader draws the info's header over it. As the info scrolls,
// from its first pixel, the avatar moves from its place in the cover into
// the header's corner, beside the name, where it stays; the header's
// background and the name come in at the end of its way, once the cover's
// name goes under the header. The ring in the corner opposite the close
// button tells the info is loading, moving nothing when it goes; it gives
// the corner up to the avatar.
func (p *chatInfo) layoutInfoHeader(gtx layout.Context, l localization.Catalog, animate bool) {
	sc := scheme(gtx)
	width := gtx.Constraints.Max.X
	header := gtx.Dp(infoHeaderHeight)
	scrolled := p.list.Position.Offset
	if p.list.Position.First > 0 {
		scrolled = 1 << 30
	}
	t := max(0, min(1, float32(scrolled)/float32(max(1, p.cover.nameBottom-header))))
	// The header's own comes in over the last part of the way.
	own := max(0, min(1, (t-.7)/.3))
	if own > 0 {
		opacity := paint.PushOpacity(gtx.Ops, own)
		fillRect(gtx, sc.SurfaceContainerLow, image.Pt(width, header))
		opacity.Pop()
	}
	content := lerpColor(p.cover.text, sc.Surface.OnColor, own)
	pad, slot := gtx.Dp(12), gtx.Dp(48)
	if p.loading && own < 1 {
		ringSize := gtx.Dp(32)
		opacity := paint.PushOpacity(gtx.Ops, 1-own)
		offset(withSize(gtx, image.Pt(ringSize, ringSize)), image.Pt(pad+(slot-ringSize)/2, (header-ringSize)/2), func(gtx layout.Context) layout.Dimensions {
			return p.loader.Layout(gtx, l)
		})
		opacity.Pop()
	}

	// The avatar, from its place in the cover to the header's corner.
	small := gtx.Dp(headerAvatarSize)
	to := image.Rectangle{Min: image.Pt(gtx.Dp(16), (header-small)/2)}
	to.Max = to.Min.Add(image.Pt(small, small))
	from := p.cover.avatar
	if from.Empty() {
		from = to
	}
	lerp := func(a, b int) float32 { return float32(a) + float32(b-a)*t }
	full := gtx.Dp(pageAvatarSize)
	side := lerp(from.Dx(), to.Dx())
	at := f32.Pt(lerp(from.Min.X, to.Min.X), lerp(from.Min.Y, to.Min.Y))
	scale := side / float32(max(1, full))
	// Moving, it streaks: as a picture of its own, when there is one.
	streak := p.motion.avatar.Streak(gtx, at)
	var sprite image.Image
	if animate && moving(streak) && p.avatarSprite != nil {
		sprite = p.avatarSprite(gtx, p.chat.ID, p.chat.Kind, p.chat.Title, int(side+.5))
	}
	p.motion.avatarStreak = sprite != nil
	if sprite != nil {
		drawStreak(gtx, p.renderer.images, sprite, image.Pt(int(at.X+.5), int(at.Y+.5)), streak)
	}
	stack := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale)).Offset(at)).Push(gtx.Ops)
	if sprite != nil {
		// Where it is, it still takes the clicks.
		p.avatar.Layout(withSize(gtx, image.Pt(full, full)), func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	} else {
		p.layoutAvatar(withSize(gtx, image.Pt(full, full)))
	}
	stack.Pop()

	closeAt := image.Pt(width-pad-slot, (header-slot)/2)
	if name := own; name > 0 {
		x := to.Max.X + pad
		nameGtx := gtx
		nameGtx.Constraints = layout.Constraints{Max: image.Pt(max(0, closeAt.X-x), header)}
		macro := op.Record(gtx.Ops)
		dims := label(nameGtx, l.ChatTitle(p.chat), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
		call := macro.Stop()
		opacity := paint.PushOpacity(gtx.Ops, name)
		offset(gtx, image.Pt(x, (header-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		opacity.Pop()
	}
	offset(gtx, closeAt, func(gtx layout.Context) layout.Dimensions {
		return navigationButtonColor(gtx, &p.close, iconClear, l.T("viewer.close"), content)
	})
}
