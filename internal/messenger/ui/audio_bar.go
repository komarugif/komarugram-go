// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strconv"
	"strings"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// audioBarHeight is the height of the bar of what plays, under the chat's
// header and over its pinned bar, where Telegram Desktop has its own.
const audioBarHeight = unit.Dp(48)

// speedMinLength is the shortest music whose speed changes, as in Telegram
// Desktop (kMinLengthForChangeablePlaybackSpeed): a voice message's always
// does, a song's does not, a podcast's does.
const speedMinLength = 20 * time.Minute

// audioSpeeds are the speeds the bar's button goes through.
var audioSpeeds = []float64{1, 1.5, 2}

// speedChanges reports whether m plays at the speed chosen.
func speedChanges(m model.Message) bool {
	return m.Kind == model.MessageVoice || m.Media != nil && m.Media.Duration >= speedMinLength
}

// current is the message that plays or loads, what the player tells of it,
// and the speed chosen; false when there is none.
func (v *audioPlayer) current() (model.Message, audioState, float64, bool) {
	if v == nil {
		return model.Message{}, audioState{}, 1, false
	}
	v.mu.Lock()
	m := v.msg
	speed := v.speed
	v.mu.Unlock()
	if speed == 0 {
		speed = 1
	}
	state := v.state(m)
	return m, state, speed, state.active
}

// shownIn is the chat, or the thread, the message that plays is shown in.
func (v *audioPlayer) shownIn() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.list
}

// setSpeed chooses the speed of what changes its speed, 0 standing for 1,
// without keeping the choice: it comes from what was kept.
func (v *audioPlayer) setSpeed(speed float64) {
	if speed == 0 {
		speed = 1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.speed = speed
	if v.stretch != nil && speedChanges(v.msg) {
		v.stretch.SetTempo(speed)
	}
}

// nextSpeed goes to the speed after the one chosen, and keeps the choice.
func (v *audioPlayer) nextSpeed() {
	v.mu.Lock()
	next := audioSpeeds[0]
	for i, s := range audioSpeeds {
		if s == max(v.speed, 1) {
			next = audioSpeeds[(i+1)%len(audioSpeeds)]
		}
	}
	save := v.saveSpeed
	v.mu.Unlock()
	v.setSpeed(next)
	if save != nil {
		save(next)
	}
}

// watch waits for m to play to its end, and plays what follows it then.
func (v *audioPlayer) watch(ctx context.Context, m model.Message, playback audioPlayback) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		// Asking whether it plays is what notices that it ended.
		if !playback.Playing() && playback.Ended() {
			v.advance(m)
			return
		}
	}
}

// advance plays the voice message, or the music, that follows m where it
// is shown, as Telegram Desktop goes on through a chat's voice messages
// and through its music; after the last one the player has nothing.
func (v *audioPlayer) advance(m model.Message) {
	v.mu.Lock()
	if v.key != m.Key {
		v.mu.Unlock()
		return
	}
	page, list := v.page, v.list
	v.mu.Unlock()
	if next, ok := nextAudio(page.source.History(list).Messages, m); ok {
		v.toggleIn(page, list, next, -1)
	} else {
		v.mu.Lock()
		if v.key == m.Key {
			v.stopLocked()
		}
		v.mu.Unlock()
	}
	page.invalidate()
}

// nextAudio is the message after m among messages that is of m's kind, a
// voice message or music, if it plays in the client.
func nextAudio(messages []model.Message, m model.Message) (model.Message, bool) {
	return neighborAudio(messages, m, 1)
}

// neighborAudio is the message of m's kind, a voice message or music, that
// follows m among messages, or precedes it when delta is below 0, if it
// plays in the client.
func neighborAudio(messages []model.Message, m model.Message, delta int) (model.Message, bool) {
	var before *model.Message
	after := false
	for i, other := range messages {
		switch {
		case other.Key == m.Key:
			if delta < 0 {
				if before == nil {
					return model.Message{}, false
				}
				return *before, internalAudio(*before)
			}
			after = true
		case other.Kind != m.Kind || other.Media == nil || other.Deleted:
		case after:
			return other, internalAudio(other)
		default:
			before = &messages[i]
		}
	}
	return model.Message{}, false
}

// skip plays the voice message, or the music, before what plays or after
// it, where it is shown, as the bar's buttons ask.
func (v *audioPlayer) skip(delta int) {
	v.mu.Lock()
	m, page, list := v.msg, v.page, v.list
	v.mu.Unlock()
	if page == nil {
		return
	}
	if to, ok := neighborAudio(page.source.History(list).Messages, m, delta); ok {
		v.toggleIn(page, list, to, -1)
	}
}

// around reports whether something precedes what plays and whether
// something follows it, for the bar's buttons. The history is looked
// through again only when it changed.
func (v *audioPlayer) around() (before, after bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.page == nil {
		return false, false
	}
	var messages []model.Message
	if source, ok := v.page.source.(interface {
		HistorySince(int64, uint64) (model.History, bool)
	}); ok && v.aroundKey == v.key {
		history, fresh := source.HistorySince(v.list, v.aroundRev)
		if !fresh {
			return v.before, v.after
		}
		messages, v.aroundRev = history.Messages, history.Revision
	} else {
		history := v.page.source.History(v.list)
		messages, v.aroundRev = history.Messages, history.Revision
	}
	v.aroundKey = v.key
	_, v.before = neighborAudio(messages, v.msg, -1)
	_, v.after = neighborAudio(messages, v.msg, 1)
	return v.before, v.after
}

// newAudioPlayer is a player at full volume.
func newAudioPlayer() *audioPlayer {
	return &audioPlayer{volume: 1, unmuted: 1}
}

// loudness is the volume chosen, from 0 to 1.
func (v *audioPlayer) loudness() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.volume
}

// setVolume makes what plays, and what plays after it, as loud as volume,
// from 0 to 1, and keeps the choice when keep is set: not while a slider
// moves, nor when it comes from what was kept.
func (v *audioPlayer) setVolume(volume float64, keep bool) {
	volume = min(max(volume, 0), 1)
	v.mu.Lock()
	v.volume = volume
	if volume > 0 {
		v.unmuted = volume
	}
	if v.playback != nil {
		v.playback.SetVolume(volume)
	}
	save := v.saveVolume
	v.mu.Unlock()
	if keep && save != nil {
		save(volume)
	}
}

// toggleMute silences the player, or brings back the volume it had, as a
// click on the volume button of Telegram Desktop's player does.
func (v *audioPlayer) toggleMute() {
	v.mu.Lock()
	volume := 0.0
	if v.volume == 0 {
		volume = v.unmuted
	}
	v.mu.Unlock()
	v.setVolume(volume, true)
}

// audioBar is the bar of what the window's player plays, as a chat page
// shows it: the track before and the one after, play or pause, whose it
// is, the speed of what changes its speed, the volume, and a button that
// ends it. A click on it goes to the message, in the chat it is in.
type audioBar struct {
	height heightTransition
	// shown is what the bar told last: while it closes, it is drawn as it
	// was, and takes no input.
	shown                *audioShown
	bar                  surface
	previous, play, next *button.Button
	speed, volume, close *button.Button
	// slider is the volume slider under the volume button, shown while the
	// pointer is over the button or over it, and a little longer, as
	// Telegram Desktop's is.
	slider volumeSlider
}

// audioShown is what the bar of what plays tells: the message, what the
// player tells of it, the speed, whether there are tracks around it, and
// the volume.
type audioShown struct {
	m             model.Message
	state         audioState
	speed, volume float64
	before, after bool
}

// volumeSlider is the vertical slider of the player's volume.
type volumeSlider struct {
	shown bool
	// hideAt is when it goes, once the pointer left it and its button.
	hideAt time.Time
	// over is set while the pointer is over the slider, and dragging while
	// it is pressed there.
	over, dragging bool
	// wheel takes the wheel over the volume button.
	wheel struct{}
}

const (
	// volumeHideAfter is how long the slider stays once the pointer left.
	volumeHideAfter = 300 * time.Millisecond
	// volumeWheelStep is how much a notch of the wheel changes the volume.
	volumeWheelStep = 0.05
)

// Telegram Desktop's slider is 27 by 100 px; ours is a little wider, for
// the knob.
const (
	volumePanelWidth  = unit.Dp(36)
	volumeTrackHeight = unit.Dp(100)
	volumePanelPad    = unit.Dp(12)
)

// volumePanel is the size of the volume slider's panel.
func volumePanel(gtx layout.Context) image.Point {
	return image.Pt(gtx.Dp(volumePanelWidth), gtx.Dp(volumeTrackHeight)+2*gtx.Dp(volumePanelPad))
}

// volumeIcon is the icon of the volume button, as in Telegram Desktop:
// silent, below two thirds, and above.
func volumeIcon(volume float64) wdk.IconWidget {
	switch {
	case volume == 0:
		return iconVolumeOff
	case volume < 0.66:
		return iconVolumeDown
	}
	return iconVolumeUp
}

// audioBarSize is how much of the page's top the bar takes: nothing while
// nothing plays.
func (p *chatPage) audioBarSize(gtx layout.Context) int {
	target := 0
	if p.audioBarLive() {
		target = gtx.Dp(audioBarHeight)
	}
	h := p.audioBar.height.Value(gtx, target, true)
	if h == 0 {
		p.audioBar.shown = nil
	}
	return h
}

// audioBarLive reports whether the bar tells of what plays now: something
// plays or loads, and not in a player of its own.
func (p *chatPage) audioBarLive() bool {
	_, _, _, ok := p.audio.current()
	return ok && (p.audioExternal == nil || !p.audioExternal())
}

// audioBarInput handles the clicks on the bar of what plays and on its
// volume slider.
func (p *chatPage) audioBarInput(gtx layout.Context, away bool) {
	b := &p.audioBar
	if b.close.Clicked(gtx) {
		p.audio.stop()
		return
	}
	m, _, _, _ := p.audio.current()
	before, after := p.audio.around()
	switch {
	case b.previous.Clicked(gtx) && before:
		p.audio.skip(-1)
	case b.next.Clicked(gtx) && after:
		p.audio.skip(1)
	case b.play.Clicked(gtx):
		p.audio.toggle(p, m, -1)
	}
	if b.speed.Clicked(gtx) {
		p.audio.nextSpeed()
	}
	if b.volume.Clicked(gtx) {
		p.audio.toggleMute()
	}
	if volume, changed, keep := b.slider.update(gtx, volumePanel(gtx), p.audio.loudness()); changed || keep {
		p.audio.setVolume(volume, keep)
	}
	if b.bar.Clicked(gtx) {
		switch {
		case away && p.openAudio != nil:
			p.openAudio(m)
		case !away && p.audio.shownIn() == p.chat:
			p.jumpTo(m.Key.MessageID)
		}
	}
}

// audioTitle is what the bar tells of m in two lines: a voice message's
// sender and when it was sent, as Telegram Desktop does; music's title and
// performer.
func audioTitle(m model.Message, now time.Time, l localization.Catalog) (string, string) {
	if m.Kind == model.MessageMusic {
		title := m.Media.Title
		if title == "" {
			title = m.Media.FileName
		}
		if title == "" {
			title = l.T("history.file")
		}
		return title, m.Media.Performer
	}
	name := m.SenderName
	if m.Outgoing {
		name = l.T("history.you")
	}
	if name == "" {
		name = l.T("history.voice")
	}
	clock := m.Date.Format("15:04")
	day := func(t time.Time) time.Time {
		y, mo, d := t.Date()
		return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
	}
	sent := day(m.Date.In(now.Location()))
	switch {
	case sent.Equal(day(now)):
		return name, strings.ReplaceAll(l.T("audio.today"), "{time}", clock)
	case sent.Equal(day(now).AddDate(0, 0, -1)):
		return name, strings.ReplaceAll(l.T("audio.yesterday"), "{time}", clock)
	}
	return name, strings.NewReplacer("{date}", m.Date.Format("02.01.2006"), "{time}", clock).Replace(l.T("audio.date"))
}

// speedText is a speed as its button tells it: 1×, 1.5×.
func speedText(speed float64) string {
	return strconv.FormatFloat(speed, 'g', 3, 64) + "×"
}

// update takes the pointer over the slider, whose area is size, and the
// wheel over its button, and returns the volume they ask for and whether
// to keep it: a drag is kept when it ends.
func (s *volumeSlider) update(gtx layout.Context, size image.Point, volume float64) (float64, bool, bool) {
	changed, keep := false, false
	pad := gtx.Dp(volumePanelPad)
	track := max(size.Y-2*pad, 1)
	at := func(y float32) float64 {
		return min(max(1-float64(y-float32(pad))/float64(track), 0), 1)
	}
	wheel := pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: s, Kinds: pointer.Enter | pointer.Leave | pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Scroll, ScrollY: wheel},
			pointer.Filter{Target: &s.wheel, Kinds: pointer.Scroll, ScrollY: wheel},
		)
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Enter:
			s.over = true
		case pointer.Leave:
			s.over = false
		case pointer.Press:
			s.dragging = true
			volume, changed = at(e.Position.Y), true
		case pointer.Drag:
			if s.dragging {
				volume, changed = at(e.Position.Y), true
			}
		case pointer.Release:
			if s.dragging {
				volume, changed, keep = at(e.Position.Y), true, true
			}
			s.dragging = false
		case pointer.Cancel:
			keep = keep || s.dragging
			s.dragging = false
		case pointer.Scroll:
			if e.Scroll.Y != 0 {
				// The wheel away from the user is louder.
				step := volumeWheelStep
				if e.Scroll.Y > 0 {
					step = -step
				}
				volume, changed, keep = min(max(volume+step, 0), 1), true, true
			}
		}
	}
	return volume, changed, keep
}

// visible decides whether the slider shows: while the pointer is over its
// button or over it, or drags it, and volumeHideAfter longer.
func (s *volumeSlider) visible(gtx layout.Context, overButton bool) bool {
	switch {
	case overButton || s.over || s.dragging:
		s.shown, s.hideAt = true, time.Time{}
	case !s.shown:
	case s.hideAt.IsZero():
		s.hideAt = gtx.Now.Add(volumeHideAfter)
		gtx.Execute(op.InvalidateCmd{At: s.hideAt})
	case !gtx.Now.Before(s.hideAt):
		s.shown, s.hideAt = false, time.Time{}
	default:
		gtx.Execute(op.InvalidateCmd{At: s.hideAt})
	}
	return s.shown
}

// layout draws the slider in size at the current origin: a track, filled
// from the bottom up to the volume, and a knob there.
func (s *volumeSlider) layout(gtx layout.Context, size image.Point, volume float64) {
	sc := scheme(gtx)
	radius := gtx.Dp(8)
	fillRounded(gtx, sc.OutlineVariant, size, radius)
	line := gtx.Dp(1)
	offset(gtx, image.Pt(line, line), func(gtx layout.Context) layout.Dimensions {
		fillRounded(gtx, sc.SurfaceContainerHigh, size.Sub(image.Pt(2*line, 2*line)), radius-line)
		return layout.Dimensions{}
	})
	pad, width := gtx.Dp(volumePanelPad), gtx.Dp(4)
	track := max(size.Y-2*pad, 1)
	x := (size.X - width) / 2
	level := pad + int(float64(track)*(1-volume)+.5)
	paint.FillShape(gtx.Ops, sc.OutlineVariant.AsNRGBA(), clip.UniformRRect(image.Rect(x, pad, x+width, pad+track), width/2).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.UniformRRect(image.Rect(x, level, x+width, pad+track), width/2).Op(gtx.Ops))
	knob := gtx.Dp(12)
	c := image.Pt(size.X/2, level)
	paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Min: c.Sub(image.Pt(knob/2, knob/2)), Max: c.Add(image.Pt(knob/2, knob/2))}.Op(gtx.Ops))
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, s)
	pointer.CursorPointer.Add(gtx.Ops)
	area.Pop()
}

// layoutAudioBar draws the bar across the top of gtx, and the volume
// slider under its button, over the page. away is set over a page that is
// not the chat's: a click on the bar opens the chat at the message.
func (p *chatPage) layoutAudioBar(gtx layout.Context, l localization.Catalog, away bool) {
	b := &p.audioBar
	if b.play == nil {
		b.previous, b.play, b.next = button.Text(), button.Text(), button.Text()
		b.speed, b.volume, b.close = button.Text(), button.Text(), button.Text()
	}
	if p.audioBarLive() {
		p.audioBarInput(gtx, away)
	}
	// What a click changed is drawn in this frame.
	live := p.audioBarLive()
	if live {
		m, state, speed, _ := p.audio.current()
		before, after := p.audio.around()
		b.shown = &audioShown{m: m, state: state, speed: speed, volume: p.audio.loudness(), before: before, after: after}
	}
	height := p.audioBarSize(gtx)
	if b.shown == nil {
		return
	}
	if !live {
		gtx = gtx.Disabled()
	}
	m, state, speed, volume := b.shown.m, b.shown.state, b.shown.speed, b.shown.volume
	before, after := b.shown.before, b.shown.after
	if live && (state.playing || state.loading) {
		// The line of what was heard moves while it plays.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(100 * time.Millisecond)})
	}

	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(audioBarHeight))
	barClip := clip.Rect(image.Rect(0, 0, size.X, height)).Push(gtx.Ops)
	fillRect(gtx, sc.Surface.Color, size)
	gtx.Constraints = layout.Exact(size)
	b.bar.Layout(gtx, size, surfaceStyle{content: sc.Surface.OnColor}, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: size}
	})
	square := gtx.Dp(48)
	// iconButton draws one of the bar's buttons in a square at x.
	iconButton := func(gtx layout.Context, x int, w layout.Widget) {
		offset(gtx, image.Pt(x, (size.Y-square)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(square, square))
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return w(gtx)
			})
		})
	}
	left := gtx.Dp(4)
	// The buttons of the tracks are there when there is another track, as
	// in Telegram Desktop; the one with nothing to go to is disabled.
	if before || after {
		btnGtx := gtx
		if !before {
			btnGtx = gtx.Disabled()
		}
		iconButton(btnGtx, left, func(gtx layout.Context) layout.Dimensions {
			return b.previous.LayoutIconOnly(gtx, l.T("audio.previous"), iconSkipPrevious)
		})
		left += square
	}
	iconButton(gtx, left, func(gtx layout.Context) layout.Dimensions {
		if state.playing {
			return b.play.LayoutIconOnly(gtx, l.T("audio.pause"), iconPauseFile)
		}
		return b.play.LayoutIconOnly(gtx, l.T("audio.play"), iconPlayFile)
	})
	left += square
	if before || after {
		btnGtx := gtx
		if !after {
			btnGtx = gtx.Disabled()
		}
		iconButton(btnGtx, left, func(gtx layout.Context) layout.Dimensions {
			return b.next.LayoutIconOnly(gtx, l.T("audio.next"), iconSkipNext)
		})
		left += square
	}
	right := size.X - gtx.Dp(4) - square
	iconButton(gtx, right, func(gtx layout.Context) layout.Dimensions {
		return b.close.LayoutIconOnly(gtx, l.T("audio.close"), iconClear)
	})
	right -= square
	volumeX := right
	// The wheel over the volume button goes to an area around the button,
	// which takes the clicks: an area beside it would get nothing.
	wheelArea := clip.Rect{Min: image.Pt(volumeX, 0), Max: image.Pt(volumeX+square, size.Y)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &b.slider.wheel)
	iconButton(gtx, volumeX, func(gtx layout.Context) layout.Dimensions {
		return b.volume.LayoutIconOnly(gtx, l.T("audio.volume"), volumeIcon(volume))
	})
	wheelArea.Pop()
	if speedChanges(m) {
		width := gtx.Dp(56)
		right -= width
		offset(gtx, image.Pt(right, 0), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(width, size.Y))
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return b.speed.Layout(gtx, speedText(speed))
			})
		})
	}
	title, subtitle := audioTitle(m, gtx.Now, l)
	textX := left + gtx.Dp(8)
	textGtx := gtx
	textGtx.Constraints = layout.Constraints{Max: image.Pt(max(right-textX-gtx.Dp(8), 0), size.Y)}
	offset(textGtx, image.Pt(textX, 0), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = size.Y
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleLabelLargeEmphasized, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, subtitle, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}),
			)
		})
	})
	// The line under the bar: what was heard, in the primary color.
	line := gtx.Dp(2)
	offset(gtx, image.Pt(0, size.Y-line), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, line))
		fillRect(gtx, sc.Primary.Color, image.Pt(int(state.progress*float32(size.X)), line))
		return layout.Dimensions{}
	})
	barClip.Pop()
	if live && b.slider.visible(gtx, b.volume.Hovered(gtx)) {
		panel := volumePanel(gtx)
		// Under its button, touching the bar, so that the pointer gets
		// from one to the other without leaving both.
		offset(gtx, image.Pt(volumeX+(square-panel.X)/2, size.Y), func(gtx layout.Context) layout.Dimensions {
			b.slider.layout(gtx, panel, volume)
			return layout.Dimensions{}
		})
	}
}
