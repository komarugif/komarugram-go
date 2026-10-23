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
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// videoControls are the controls of a video that plays in the viewer, on
// a plate over the bottom of the stage, as in Telegram Desktop: play or
// pause, the time, the bar of where it is, which moves it, the speed, the
// volume, and the external player. While it plays they go once the pointer
// has not moved for videoControlsIdle.
type videoControls struct {
	play, speed, volume, external widget.Clickable
	slider                        volumeSlider
	// moved is when the pointer moved over the viewer last; moves is the
	// tag of its moves, and over is set while it is over the plate.
	moved time.Time
	moves struct{}
	over  bool
	plate struct{}
	// seeking is set while the bar is pressed, at where it is dragged to,
	// from 0 to 1; width is the bar's on the last frame.
	seeking bool
	at      float32
	width   int
	bar     struct{}
	shown   wdk.FloatTween
}

// videoControlsIdle is how long the controls of a video that plays stay
// once the pointer stopped.
const videoControlsIdle = 3 * time.Second

// videoSeekStep is how far the keys move a video.
const videoSeekStep = 5 * time.Second

// videoPlate is the height of the plate of the controls.
const videoPlate = unit.Dp(48)

// wake shows the controls, as a move of the pointer does.
func (c *videoControls) wake(now time.Time) { c.moved = now }

// playVideo plays m from at, below 0 from the start: in the viewer, when
// the settings choose it and a decoder here takes m, or else in the
// external player.
func (v *mediaViewer) playVideo(gtx layout.Context, m model.Message, at time.Duration, l localization.Catalog) {
	engine := videoExternal
	if v.videoEngine != nil {
		engine = v.videoEngine(m)
	}
	if engine == videoExternal {
		if v.play != nil {
			v.play(gtx, m, at, l)
		}
		return
	}
	if v.pauseAudio != nil {
		v.pauseAudio()
	}
	ffmpeg := ""
	if v.ffmpeg != nil {
		ffmpeg = v.ffmpeg()
	}
	v.video.external = nil
	if v.play != nil {
		// A video none of the decoders here takes goes to the external
		// player, on the window's goroutine.
		v.video.external = func(m model.Message, start time.Duration) {
			v.externalMu.Lock()
			v.externalNext = &externalVideo{m: m, at: start}
			v.externalMu.Unlock()
			v.invalidate()
		}
	}
	v.video.open(v.source, m, engine, ffmpeg, at)
	v.controls.wake(gtx.Now)
}

// externalVideo is a video to open in the external player from a time.
type externalVideo struct {
	m  model.Message
	at time.Duration
}

// videoKey does what a key of the video's asks: play or pause, a move back
// or ahead, or the sound off or on.
func (v *mediaViewer) videoKey(gtx layout.Context, m model.Message, id string) {
	if m.Kind != model.MessageVideo {
		return
	}
	s := v.video.state(m)
	if !s.active {
		if id == "viewer.play_pause" {
			v.picture.Click()
		}
		return
	}
	switch id {
	case "viewer.play_pause":
		v.video.toggle()
	case "viewer.seek_back":
		v.video.seek(max(0, s.position-videoSeekStep))
	case "viewer.seek_forward":
		v.video.seek(min(s.duration, s.position+videoSeekStep))
	case "viewer.mute":
		v.video.toggleMute()
	}
	v.controls.wake(gtx.Now)
	gtx.Execute(op.InvalidateCmd{})
}

// updateVideo takes the input of the controls of m, before they are drawn.
func (v *mediaViewer) updateVideo(gtx layout.Context, m model.Message, l localization.Catalog) {
	c := &v.controls
	v.externalMu.Lock()
	next := v.externalNext
	v.externalNext = nil
	v.externalMu.Unlock()
	if next != nil && v.play != nil {
		v.play(gtx, next.m, next.at, l)
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.moves, Kinds: pointer.Move | pointer.Drag | pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			c.wake(gtx.Now)
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.plate, Kinds: pointer.Enter | pointer.Leave | pointer.Cancel | pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			switch e.Kind {
			case pointer.Press:
				// A press where the hidden plate is shows it.
				c.wake(gtx.Now)
			default:
				c.over = e.Kind == pointer.Enter
			}
		}
	}
	s := v.video.state(m)
	if !s.active || m.Kind != model.MessageVideo {
		c.seeking = false
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.bar, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || c.width <= 0 {
			continue
		}
		at := max(0, min(1, e.Position.X/float32(c.width)))
		switch e.Kind {
		case pointer.Press:
			if e.Source == pointer.Mouse && e.Buttons != pointer.ButtonPrimary {
				continue
			}
			gtx.Execute(pointer.GrabCmd{Tag: &c.bar, ID: e.PointerID})
			c.seeking, c.at = true, at
		case pointer.Drag:
			if c.seeking {
				c.at = at
			}
		case pointer.Release:
			if c.seeking {
				v.video.seek(time.Duration(float64(at) * float64(s.duration)))
			}
			c.seeking = false
		case pointer.Cancel:
			c.seeking = false
		}
	}
	if c.play.Clicked(gtx) {
		v.video.toggle()
	}
	if c.speed.Clicked(gtx) {
		v.video.nextSpeed()
	}
	if c.volume.Clicked(gtx) {
		v.video.toggleMute()
	}
	if volume, changed, keep := c.slider.update(gtx, volumePanel(gtx), v.video.loudness()); changed {
		v.video.setVolume(volume, keep)
	}
	if c.external.Clicked(gtx) && v.play != nil {
		v.video.stop()
		v.play(gtx, m, s.position, l)
	}
}

// layoutVideoControls draws the controls of m, when it plays, over the
// bottom of stage, and returns the top of their plate.
func (v *mediaViewer) layoutVideoControls(gtx layout.Context, m model.Message, stage image.Rectangle, l localization.Catalog) (int, bool) {
	c := &v.controls
	s := v.video.state(m)
	if m.Kind != model.MessageVideo || !s.active {
		return 0, false
	}
	idle := gtx.Now.Sub(c.moved)
	visible := !s.playing || s.loading || c.over || c.seeking || idle < videoControlsIdle || c.slider.over || c.slider.dragging
	if s.playing {
		// The time and the bar move while it plays.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(250 * time.Millisecond)})
		if visible && idle < videoControlsIdle {
			gtx.Execute(op.InvalidateCmd{At: c.moved.Add(videoControlsIdle)})
		}
	}
	c.shown.Duration = token.DurationShort4
	shown := c.shown.Animate(gtx, boolFloat(visible))
	margin, h := gtx.Dp(12), gtx.Dp(videoPlate)
	width := min(stage.Dx()-2*margin, gtx.Dp(720))
	plate := image.Rect(stage.Min.X+(stage.Dx()-width)/2, stage.Max.Y-margin-h, stage.Min.X+(stage.Dx()+width)/2, stage.Max.Y-margin)
	if width < gtx.Dp(200) {
		return plate.Min.Y, true
	}
	if shown <= 0 {
		// Hidden, the plate takes a press, which shows it, rather than the
		// backdrop under it, which would close the viewer.
		area := clip.Rect(plate).Push(gtx.Ops)
		event.Op(gtx.Ops, &c.plate)
		area.Pop()
		return plate.Min.Y, true
	}
	white := token.NewMatColorFromHexRGB(0xffffff)
	defer paint.PushOpacity(gtx.Ops, shown).Pop()
	offset(gtx, plate.Min, func(gtx layout.Context) layout.Dimensions {
		size := plate.Size()
		area := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Push(gtx.Ops)
		paint.FillShape(gtx.Ops, color.NRGBA{A: 160}, clip.Rect{Max: size}.Op())
		event.Op(gtx.Ops, &c.plate)
		area.Pop()
		d := gtx.Dp(40)
		x := gtx.Dp(4)
		button := func(click *widget.Clickable, icon wdk.IconWidget) {
			offset(gtx, image.Pt(x, (size.Y-d)/2), func(gtx layout.Context) layout.Dimensions {
				return plateButton(gtx, click, image.Pt(d, d), func(gtx layout.Context) layout.Dimensions {
					inner := gtx.Dp(24)
					return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints = layout.Exact(image.Pt(inner, inner))
						return icon(gtx, white)
					})
				})
			})
			x += d
		}
		playIcon := iconPauseFile
		if !s.playing {
			playIcon = iconPlayFile
		}
		button(&c.play, playIcon)
		// The time: where it is, or where the bar is dragged to.
		position := s.position
		progress := float32(0)
		if s.duration > 0 {
			progress = min(1, float32(position)/float32(s.duration))
		}
		if c.seeking {
			progress = c.at
			position = time.Duration(float64(c.at) * float64(s.duration))
		}
		macro := op.Record(gtx.Ops)
		tgtx := gtx
		tgtx.Constraints.Min = image.Point{}
		times := label(tgtx, playTime(position)+" / "+playTime(s.duration), token.TypestyleLabelLarge, white, 1)
		call := macro.Stop()
		x += gtx.Dp(4)
		offset(gtx, image.Pt(x, (size.Y-times.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return times
		})
		x += times.Size.Y/2 + times.Size.X
		// The buttons at the right end, from the right.
		right := size.X - gtx.Dp(4)
		rightButton := func(click *widget.Clickable, w layout.Widget, wide int) int {
			right -= wide
			offset(gtx, image.Pt(right, (size.Y-d)/2), func(gtx layout.Context) layout.Dimensions {
				return plateButton(gtx, click, image.Pt(wide, d), w)
			})
			return right
		}
		icon := func(icon wdk.IconWidget) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				inner := gtx.Dp(24)
				return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(inner, inner))
					return icon(gtx, white)
				})
			}
		}
		if v.play != nil {
			rightButton(&c.external, icon(iconExternalPlayer), d)
		}
		volumeX := rightButton(&c.volume, icon(volumeIcon(v.video.loudness())), d)
		speedWide := gtx.Dp(48)
		rightButton(&c.speed, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(speedWide, d))
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return label(gtx, speedText(v.video.currentSpeed()), token.TypestyleLabelLarge, white, 1)
			})
		}, speedWide)
		// The bar between them.
		barArea := image.Rect(x+gtx.Dp(4), 0, right-gtx.Dp(8), size.Y)
		c.width = barArea.Dx()
		if barArea.Dx() > 0 {
			offset(gtx, barArea.Min, func(gtx layout.Context) layout.Dimensions {
				bsize := barArea.Size()
				area := clip.Rect{Max: bsize}.Push(gtx.Ops)
				event.Op(gtx.Ops, &c.bar)
				pointer.CursorPointer.Add(gtx.Ops)
				area.Pop()
				line := gtx.Dp(4)
				y := (bsize.Y - line) / 2
				at := int(progress * float32(bsize.X))
				paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 80}, clip.UniformRRect(image.Rect(0, y, bsize.X, y+line), line/2).Op(gtx.Ops))
				paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 235}, clip.UniformRRect(image.Rect(0, y, at, y+line), line/2).Op(gtx.Ops))
				knob := gtx.Dp(12)
				center := image.Pt(min(max(at, knob/2), bsize.X-knob/2), bsize.Y/2)
				paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Ellipse{Min: center.Sub(image.Pt(knob/2, knob/2)), Max: center.Add(image.Pt(knob/2, knob/2))}.Op(gtx.Ops))
				return layout.Dimensions{Size: bsize}
			})
		}
		// The volume's slider, over its button, as the audio bar's is under
		// its own.
		if c.slider.visible(gtx, c.volume.Hovered()) {
			panel := volumePanel(gtx)
			offset(gtx, image.Pt(volumeX+(d-panel.X)/2, -panel.Y), func(gtx layout.Context) layout.Dimensions {
				c.slider.layout(gtx, panel, v.video.loudness())
				return layout.Dimensions{}
			})
		}
		return layout.Dimensions{Size: size}
	})
	return plate.Min.Y, true
}

// plateButton is a button of the controls' plate, of size, whose face w
// draws, lighter while the pointer is over it.
func plateButton(gtx layout.Context, click *widget.Clickable, size image.Point, w layout.Widget) layout.Dimensions {
	gtx.Constraints = layout.Exact(size)
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		if click.Hovered() {
			paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 28}, clip.UniformRRect(image.Rectangle{Max: size}, size.Y/2).Op(gtx.Ops))
		}
		w(gtx)
		return layout.Dimensions{Size: size}
	})
}
