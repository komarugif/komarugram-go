// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// roundSize is the diameter of a video message, Telegram Desktop's
// maxVideoMessageSize.
const roundSize = unit.Dp(240)

// roundGrab is how far in from its edge a press on a video message that
// plays takes its ring, to move it, as Telegram Desktop's
// historyVideoMessageSeekGrabBand.
const roundGrab = unit.Dp(40)

// roundSeek is where a press on a video message is: on its ring, at, from 0
// to 1 clockwise from the top, while seeking; down while pressed.
type roundSeek struct {
	down, seeking bool
	at            float32
	// center and radius are the circle's on the last frame.
	center f32.Point
	radius float32
}

// angleAt is how far round the ring, from 0 to 1 clockwise from the top, a
// point is.
func (s *roundSeek) angleAt(pos f32.Point) float32 {
	d := pos.Sub(s.center)
	a := math.Atan2(float64(d.X), -float64(d.Y))
	if a < 0 {
		a += 2 * math.Pi
	}
	return float32(a / (2 * math.Pi))
}

// update takes the presses on the round of m: a click plays it, pauses it
// or goes on with it, and a press on the ring of one that plays, dragged
// round it, moves it there on release, as in Telegram Desktop. It returns
// what the player tells of m, and the progress to draw: the drag's, while
// there is one.
func (s *roundSeek) update(gtx layout.Context, p *chatPage, m model.Message, l localization.Catalog, inside bool) (audioState, float32) {
	state := p.audio.state(m)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: s, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		from := e.Position.Sub(s.center)
		distance := float32(math.Hypot(float64(from.X), float64(from.Y)))
		switch e.Kind {
		case pointer.Press:
			if distance > s.radius || e.Buttons != pointer.ButtonPrimary {
				continue
			}
			s.down = true
			s.seeking = inside && state.active && !state.loading && distance >= s.radius-float32(gtx.Dp(roundGrab))
			s.at = s.angleAt(e.Position)
		case pointer.Drag:
			if s.seeking {
				s.at = s.angleAt(e.Position)
			}
		case pointer.Release:
			switch {
			case !s.down:
			case s.seeking:
				p.audio.toggle(p, m, s.at)
			case !inside:
				p.play(gtx, m, p.reportMedia, l)
			default:
				p.audio.toggle(p, m, -1)
			}
			s.down, s.seeking = false, false
		case pointer.Cancel:
			s.down, s.seeking = false, false
		}
	}
	state = p.audio.state(m)
	if state.playing || state.loading {
		// The ring and the time move while it plays.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(50 * time.Millisecond)})
	}
	progress := state.progress
	if s.seeking {
		progress = s.at
	}
	return state, progress
}

// roundLayout draws a video message as Telegram Desktop does: a circle of
// its thumbnail, or of its pictures while it plays, with its sound; a ring
// around its edge of how much was seen, and its time on a plate under it.
// A click plays it or pauses it; one the client cannot play, or with the
// external player chosen for audio, opens in the external player.
func (p *chatPage) roundLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	inside := internalAudio(m) && (p.audioExternal == nil || !p.audioExternal())
	s := &r.audio.round
	state, progress := s.update(gtx, p, m, l, inside)
	d := min(gtx.Dp(roundSize), gtx.Constraints.Max.X)
	size := image.Pt(d, d)
	s.center, s.radius = f32.Pt(float32(d)/2, float32(d)/2), float32(d)/2

	var im image.Image
	if state.active {
		im = p.audio.roundPicture(m, gtx.Now, d)
	}
	var thumbErr error
	if thumb := m.Media.Thumbnail; im == nil && thumb != nil {
		target := m
		target.Kind, target.Media = model.MessagePhoto, thumb
		status := p.media.StatusFit(target, animate, size, true)
		im, thumbErr = status.Frame, status.Err
		if im == nil {
			im = status.Preview
		}
	}
	if thumbErr != nil && thumbErr != r.mediaTold {
		p.toast.Show(mediaErrorText(thumbErr))
	}
	r.mediaTold = thumbErr

	sc := scheme(gtx)
	white := token.NewMatColorFromHexRGB(0xffffff)
	circle := clip.Ellipse{Max: size}
	func() {
		defer circle.Push(gtx.Ops).Pop()
		paint.FillShape(gtx.Ops, sc.SurfaceContainerHigh.AsNRGBA(), circle.Op(gtx.Ops))
		if im != nil {
			cgtx := gtx
			cgtx.Constraints = layout.Exact(size)
			widget.Image{Src: p.images.Op(im), Fit: widget.Cover, Position: layout.Center}.Layout(cgtx)
		}
		event.Op(gtx.Ops, s)
		pointer.CursorPointer.Add(gtx.Ops)
	}()
	if state.active && !state.loading {
		// The part seen, clockwise from the top, as Telegram Desktop's,
		// with a knob where it is while the ring is dragged.
		line := float32(gtx.Dp(4))
		inset := float32(gtx.Dp(6))
		ink := color.NRGBA{R: 255, G: 255, B: 255, A: 184}
		arc(gtx, d, inset, line, -math.Pi/2, float64(progress)*2*math.Pi, ink)
		if s.seeking {
			radius := float64(float32(d)/2 - inset - line/2)
			angle := -math.Pi/2 + float64(progress)*2*math.Pi
			c := image.Pt(d/2+int(radius*math.Cos(angle)), d/2+int(radius*math.Sin(angle)))
			knob := gtx.Dp(16)
			paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Ellipse{Min: c.Sub(image.Pt(knob/2, knob/2)), Max: c.Add(image.Pt(knob/2, knob/2))}.Op(gtx.Ops))
		}
	}
	if !state.playing {
		// Play, or a ring while the file downloads and opens.
		diameter := min(gtx.Dp(48), d)
		offset(gtx, image.Pt((d-diameter)/2, (d-diameter)/2), func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, color.NRGBA{A: 145}, clip.Ellipse{Max: image.Pt(diameter, diameter)}.Op(gtx.Ops))
			if state.loading {
				return r.ring.layout(gtx, diameter, true, -1, iconClear, white, animate)
			}
			inset := diameter / 6
			offset(gtx, image.Pt(inset, inset), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(diameter-2*inset, diameter-2*inset), func(gtx layout.Context) layout.Dimensions {
					return iconPlayFile(gtx, white)
				})
			})
			return layout.Dimensions{Size: image.Pt(diameter, diameter)}
		})
	}
	// The time, and a dot while nobody saw it, on a plate in the corner
	// under the circle's left; the account's own has its own time in the
	// right one.
	shown := m.Media.Duration
	if state.active && !state.loading {
		shown = state.elapsed
	}
	macro := op.Record(gtx.Ops)
	pgtx := gtx
	pgtx.Constraints.Min = image.Point{}
	plate := layout.Inset{Top: 2, Bottom: 2, Left: 7, Right: 7}.Layout(pgtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, playTime(shown), token.TypestyleLabelMedium, sc.InverseSurface.OnColor, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !m.MediaUnread {
					return layout.Dimensions{}
				}
				dot := gtx.Dp(6)
				size := image.Pt(gtx.Dp(5)+dot, dot)
				paint.FillShape(gtx.Ops, sc.InverseSurface.OnColor.AsNRGBA(), clip.Ellipse{Min: image.Pt(size.X-dot, 0), Max: size}.Op(gtx.Ops))
				return layout.Dimensions{Size: size}
			}),
		)
	})
	call := macro.Stop()
	at := image.Pt(gtx.Dp(4), d-plate.Size.Y-gtx.Dp(4))
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, sc.InverseSurface.Color.SetOpacity(0.55), plate.Size, plate.Size.Y/2)
		call.Add(gtx.Ops)
		return plate
	})
	return layout.Dimensions{Size: size}
}
