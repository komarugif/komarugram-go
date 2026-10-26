// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"komarugram/internal/messenger/model"
	"math"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
)

// barHarness draws the bar of what a page's player plays, 600 wide at one
// pixel to the dp, and takes the pointer to it: the track buttons are at
// 4, 52 and 100, the volume button at 500, and its slider under it, its
// track from 60 down to 160.
type barHarness struct {
	p      *chatPage
	router input.Router
	now    time.Time
	away   bool
}

func (h *barHarness) frame() {
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(600, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	h.p.layoutAudioBar(gtx, localization.For("en"), h.away)
	h.router.Frame(ops)
}

func (h *barHarness) pointer(kind pointer.Kind, x, y float32) {
	e := pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(x, y)}
	if kind == pointer.Press {
		e.Buttons = pointer.ButtonPrimary
	}
	h.router.Queue(e)
	h.frame()
}

func (h *barHarness) click(x, y float32) {
	h.pointer(pointer.Move, x, y)
	h.pointer(pointer.Press, x, y)
	h.pointer(pointer.Release, x, y)
	h.frame()
}

// The bar's buttons go to the voice message before what plays and to the
// one after it; at the first one there is nothing before.
func TestAudioBarSwitchesTracks(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	first, second, third := find("demo/voice"), find("demo/voice-bare"), find("demo/voice-mp3")
	p.chat = 2
	h := &barHarness{p: p, now: time.Now()}
	p.audio.toggle(p, second, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(second).playing })
	h.frame()
	if before, after := p.audio.around(); !before || !after {
		t.Fatalf("around the second voice message: %v %v", before, after)
	}
	h.click(28, 24)
	waitAudio(t, "the one before did not start", func() bool { return p.audio.state(first).playing })
	if before, after := p.audio.around(); before || !after {
		t.Fatalf("around the first voice message: %v %v", before, after)
	}
	// Nothing is before it: the button does nothing.
	h.click(28, 24)
	h.click(124, 24)
	waitAudio(t, "the one after did not start", func() bool { return p.audio.state(second).playing })
	h.click(124, 24)
	waitAudio(t, "the third did not start", func() bool { return p.audio.state(third).playing })
	// The button between them pauses.
	h.click(76, 24)
	if p.audio.state(third).playing {
		t.Fatal("the play button did not pause")
	}

	messages := p.source.History(2).Messages
	if prev, ok := neighborAudio(messages, find("demo/voice-m4a"), -1); !ok || prev.Key != third.Key {
		t.Fatalf("before the last voice message is %+v: the music between them is not one", prev.Media)
	}
	if _, ok := neighborAudio(messages, find("demo/music"), -1); ok {
		t.Fatal("something precedes the only music")
	}
}

// The volume button shows a slider while the pointer is over either; the
// slider and the wheel set the volume, a click silences it and brings it
// back, and the choice is kept, for the next track too.
func TestAudioBarVolume(t *testing.T) {
	p, played, find := audioHarnessPage(t)
	p.chat = 2
	var saved []float64
	p.audio.saveVolume = func(volume float64) { saved = append(saved, volume) }
	h := &barHarness{p: p, now: time.Now()}
	m := find("demo/voice")
	p.audio.toggle(p, m, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	volume := func() float64 {
		s := played(0)
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.volume
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 0.011 }
	if volume() != 1 || p.audio.loudness() != 1 {
		t.Fatalf("it starts at %v", volume())
	}
	h.frame()
	slider := &p.audioBar.slider
	if slider.shown {
		t.Fatal("the slider shows before the pointer came")
	}
	h.pointer(pointer.Move, 524, 24)
	h.frame()
	if !slider.shown {
		t.Fatal("no slider under the pointer over the button")
	}
	// Down to the slider and along it: halfway, then a quarter.
	h.pointer(pointer.Move, 524, 110)
	h.pointer(pointer.Press, 524, 110)
	if !near(volume(), 0.5) || len(saved) != 0 {
		t.Fatalf("pressed halfway: volume %v, kept %v", volume(), saved)
	}
	h.drag(524, 135)
	h.pointer(pointer.Release, 524, 135)
	if !near(volume(), 0.25) || len(saved) != 1 || !near(saved[0], 0.25) {
		t.Fatalf("released at a quarter: volume %v, kept %v", volume(), saved)
	}
	// The wheel over the button.
	h.pointer(pointer.Move, 524, 24)
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(524, 24), Scroll: f32.Pt(0, -1)})
	h.frame()
	if !near(volume(), 0.3) {
		t.Fatalf("after the wheel up: %v", volume())
	}
	// A click silences, another brings the volume back.
	h.click(524, 24)
	if volume() != 0 {
		t.Fatalf("after a click: %v", volume())
	}
	h.click(524, 24)
	if !near(volume(), 0.3) {
		t.Fatalf("after another click: %v", volume())
	}
	// The slider stays a little after the pointer left, and goes.
	h.pointer(pointer.Move, 100, 300)
	h.frame()
	if !slider.shown {
		t.Fatal("the slider went at once")
	}
	h.now = h.now.Add(volumeHideAfter + time.Millisecond)
	h.frame()
	if slider.shown {
		t.Fatal("the slider stays")
	}
	// What plays next is as loud.
	other := find("demo/voice-bare")
	p.audio.toggle(p, other, -1)
	waitAudio(t, "the other did not start", func() bool { return p.audio.state(other).playing })
	s := played(1)
	s.mu.Lock()
	next := s.volume
	s.mu.Unlock()
	if !near(next, 0.3) {
		t.Fatalf("the next one plays at %v", next)
	}
	if volumeIcon(0) == nil || volumeIcon(0.5) == nil || volumeIcon(1) == nil {
		t.Fatal("a volume without an icon")
	}
}

// drag moves the pointer with its button held.
func (h *barHarness) drag(x, y float32) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)})
	h.frame()
}

// Over a page that is not the chat's, or over another chat, a click on the
// bar opens the chat at what plays; over the chat it scrolls there.
func TestAudioBarAwayOpensTheChat(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	voice := find("demo/voice")
	p.chat = 2
	var opened []model.MessageKey
	p.openAudio = func(m model.Message) { opened = append(opened, m.Key) }
	h := &barHarness{p: p, now: time.Now(), away: true}
	p.audio.toggle(p, voice, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(voice).playing })
	h.frame()
	h.click(300, 24)
	if len(opened) != 1 || opened[0] != voice.Key {
		t.Fatalf("opened %v", opened)
	}
	h.away = false
	h.click(300, 24)
	if len(opened) != 1 {
		t.Fatalf("over the chat the bar opened %v", opened)
	}
	p.chat = 3
	h.click(300, 24)
	if len(opened) != 2 || opened[1] != voice.Key {
		t.Fatalf("over another chat the bar opened %v", opened)
	}
}
