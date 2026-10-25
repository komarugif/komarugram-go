// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"komarugram/internal/messenger/model"
)

// gifControls are those of a GIF that plays in the chat, as a video
// message's are: a click pauses it or plays it on, the middle button opens
// it in the viewer, and the bar along its bottom moves it where it is
// pressed or dragged to, a paused one showing the picture there. A GIF that
// plays shows none of them: its time
// fades in while the pointer is over it, as a video's plate fades out, the
// bar while the pointer is over the bar, and play while it is paused.
type gifControls struct {
	// middle is set while the middle button is down on the GIF.
	middle bool
	// seeking is set while the bar is pressed, at where it is dragged to,
	// from 0 to 1; width is the bar's on the last frame, and over is set
	// while the pointer is over it.
	seeking bool
	at      float32
	width   int
	over    bool
	// bar is the tag of the bar's presses; time and track how much the
	// time and the bar show.
	bar         struct{}
	time, track wdk.FloatTween
}

// update takes the presses of the GIF of m, and reports whether the middle
// button asked to open it.
func (c *gifControls) update(gtx layout.Context, p *chatPage, m model.Message) (open bool) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: c, Kinds: pointer.Press | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			c.middle = e.Buttons.Contain(pointer.ButtonTertiary)
		case pointer.Release:
			open = open || c.middle
			c.middle = false
		case pointer.Cancel:
			c.middle = false
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.bar, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Enter | pointer.Leave | pointer.Move})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || c.width <= 0 {
			continue
		}
		at := max(0, min(1, e.Position.X/float32(c.width)))
		switch e.Kind {
		case pointer.Enter, pointer.Move:
			c.over = true
		case pointer.Leave:
			c.over = false
		case pointer.Press:
			if e.Buttons != pointer.ButtonPrimary && e.Source == pointer.Mouse {
				continue
			}
			// The bar's, not the GIF's: a press on it pauses nothing.
			gtx.Execute(pointer.GrabCmd{Tag: &c.bar, ID: e.PointerID})
			c.seeking, c.at = true, at
		case pointer.Drag:
			if c.seeking {
				c.at = at
			}
		case pointer.Release:
			if c.seeking {
				// A paused one stays paused, on the picture there.
				p.media.SeekGIF(m, time.Duration(float64(at)*float64(m.Media.Duration)), false)
				gtx.Execute(op.InvalidateCmd{})
			}
			c.seeking = false
		case pointer.Cancel:
			c.seeking, c.over = false, false
		}
	}
	return open
}

// gifBarHeight is how high from the GIF's bottom its bar takes presses.
const gifBarHeight = 18

// layout draws the controls over a GIF of size, playing at position of
// long or paused, the pointer over it while hovered, and takes the
// presses on it.
func (c *gifControls) layout(gtx layout.Context, size image.Point, paused, hovered bool, position, long time.Duration) {
	event.Op(gtx.Ops, c)
	white := token.NewMatColorFromHexRGB(0xffffff)
	if paused {
		// Play, in the middle, as on a video message that does not play.
		diameter := min(gtx.Dp(48), min(size.X, size.Y))
		offset(gtx, image.Pt((size.X-diameter)/2, (size.Y-diameter)/2), func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, color.NRGBA{A: 145}, clip.Ellipse{Max: image.Pt(diameter, diameter)}.Op(gtx.Ops))
			inset := diameter / 6
			offset(gtx, image.Pt(inset, inset), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(diameter-2*inset, diameter-2*inset), func(gtx layout.Context) layout.Dimensions {
					return iconPlayFile(gtx, white)
				})
			})
			return layout.Dimensions{Size: image.Pt(diameter, diameter)}
		})
	}
	if long <= 0 {
		c.over, c.seeking = false, false
		return
	}
	inset := gtx.Dp(8)
	strip := gtx.Dp(gifBarHeight)
	bar := image.Rect(inset, size.Y-strip, size.X-inset, size.Y)
	c.width = bar.Dx()
	// The bar takes the pointer while it does not show.
	offset(gtx, bar.Min, func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: bar.Size()}.Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, &c.bar)
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: bar.Size()}
	})
	progress := min(1, float32(position)/float32(long))
	shown := position
	if c.seeking {
		progress = c.at
		shown = time.Duration(float64(c.at) * float64(long))
	}
	c.time.Duration, c.track.Duration = token.DurationShort2, token.DurationShort2
	timeShown := c.time.Animate(gtx, boolFloat(hovered || c.seeking))
	trackShown := c.track.Animate(gtx, boolFloat(c.over || c.seeking))
	if timeShown > 0 {
		// The time on a plate in the corner, as a video message's: where
		// it plays, or would go.
		macro := op.Record(gtx.Ops)
		plate := mediaPlate(gtx, playTime(shown))
		call := macro.Stop()
		// Over the bar while it shows.
		lift := int(trackShown * float32(gtx.Dp(6)))
		func() {
			defer paint.PushOpacity(gtx.Ops, timeShown).Pop()
			offset(gtx, image.Pt(inset, max(0, size.Y-inset-lift-plate.Size.Y)), func(gtx layout.Context) layout.Dimensions {
				call.Add(gtx.Ops)
				return plate
			})
		}()
	}
	if trackShown <= 0 {
		return
	}
	defer paint.PushOpacity(gtx.Ops, trackShown).Pop()
	offset(gtx, bar.Min, func(gtx layout.Context) layout.Dimensions {
		h, below := gtx.Dp(3), gtx.Dp(3)
		y := bar.Dy() - below - h
		x := int(progress * float32(bar.Dx()))
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 90}, clip.UniformRRect(image.Rect(0, y, bar.Dx(), y+h), h/2).Op(gtx.Ops))
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 230}, clip.UniformRRect(image.Rect(0, y, x, y+h), h/2).Op(gtx.Ops))
		d := gtx.Dp(10)
		if c.seeking {
			d = gtx.Dp(12)
		}
		center := image.Pt(min(max(x, d/2), bar.Dx()-d/2), y+h/2)
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Ellipse{Min: center.Sub(image.Pt(d/2, d/2)), Max: center.Add(image.Pt(d/2, d/2))}.Op(gtx.Ops))
		return layout.Dimensions{Size: bar.Size()}
	})
}

// boolFloat is 1 for true and 0 for false.
func boolFloat(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
