// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// audioFrame draws p showing chat, and returns the height of its bar of
// what plays. Animations are off: the bar is there at once, or gone.
func audioFrame(p *chatPage, chat int64) int {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 820)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{wdk.AnimationsNamespace: false}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	if p.images == nil {
		p.images = &imageOps{}
	}
	p.images.BeginFrame()
	p.media.BeginFrame()
	p.Layout(gtx, model.Chat{ID: chat}, localization.For("ru"), false)
	p.media.EndFrame()
	p.images.EndFrame()
	return p.audioBarSize(gtx)
}

// What plays goes on when the page shows another chat, and the bar tells
// of it there; the bar's button ends it.
func TestAudioGoesOnInAnotherChat(t *testing.T) {
	p, played, find := audioHarnessPage(t)
	t.Chdir("../../..")
	if audioFrame(p, 2) != 0 {
		t.Fatal("a bar while nothing plays")
	}
	m := find("demo/voice")
	p.audio.toggle(p, m, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if audioFrame(p, 2) == 0 {
		t.Fatal("no bar in the chat of what plays")
	}
	if audioFrame(p, 3) == 0 {
		t.Fatal("no bar in another chat")
	}
	if !p.audio.state(m).playing || played(0).isClosed() {
		t.Fatal("another chat stopped it")
	}
	got, _, _, ok := p.audio.current()
	if !ok || got.Key != m.Key {
		t.Fatalf("the player tells of %+v", got.Key)
	}
	p.audio.stop()
	if audioFrame(p, 3) != 0 {
		t.Fatal("the bar stays after the end")
	}
}

// A voice message that played to its end is followed by the next one of
// the chat, as in Telegram Desktop; music is not a voice message.
func TestAudioPlaysNextAfterTheEnd(t *testing.T) {
	p, played, find := audioHarnessPage(t)
	first, second, third := find("demo/voice"), find("demo/voice-bare"), find("demo/voice-mp3")
	p.chat = 2
	p.audio.toggle(p, first, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(first).playing })
	// The page shows another chat meanwhile: what follows is of the chat
	// the message is in.
	p.chat = 3
	played(0).end()
	waitAudio(t, "the next one did not start", func() bool { return p.audio.state(second).playing })
	if !played(0).isClosed() {
		t.Fatal("the one that ended was not let go")
	}
	played(1).end()
	waitAudio(t, "the third one did not start", func() bool { return p.audio.state(third).playing })

	messages := p.source.History(2).Messages
	if next, ok := nextAudio(messages, third); !ok || next.Media.ID != "demo/voice-m4a" {
		t.Fatalf("after the MP3 voice message comes %+v, not the voice message after the music", next.Media)
	}
	if next, ok := nextAudio(messages, find("demo/music")); ok {
		t.Fatalf("music is followed by %+v", next.Media)
	}
	if _, ok := nextAudio(messages, find("demo/voice-m4a")); ok {
		t.Fatal("something follows the last voice message")
	}
	if _, ok := nextAudio(nil, first); ok {
		t.Fatal("something follows a message that is not in the history")
	}
}

// After the last one the player has nothing, and the bar goes.
func TestAudioEndsAfterTheLast(t *testing.T) {
	p, played, find := audioHarnessPage(t)
	music := find("demo/music")
	p.chat = 2
	p.audio.toggle(p, music, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(music).playing })
	played(0).end()
	waitAudio(t, "the player kept the music that ended", func() bool {
		_, _, _, ok := p.audio.current()
		return !ok
	})
	if !played(0).isClosed() {
		t.Fatal("the output was not closed")
	}
}

// The speed chosen is kept, and changes a voice message's speed at once,
// but not a song's.
func TestAudioSpeed(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	var saved []float64
	p.audio.saveSpeed = func(speed float64) { saved = append(saved, speed) }
	tempo := func() float64 {
		p.audio.mu.Lock()
		defer p.audio.mu.Unlock()
		return p.audio.stretch.Tempo()
	}
	voiced := find("demo/voice")
	p.audio.toggle(p, voiced, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(voiced).playing })
	if tempo() != 1 {
		t.Fatalf("tempo %v before a speed was chosen", tempo())
	}
	for _, want := range []float64{1.5, 2, 1, 1.5} {
		p.audio.nextSpeed()
		if _, _, speed, _ := p.audio.current(); speed != want || tempo() != want {
			t.Fatalf("speed %v, tempo %v, want %v", speed, tempo(), want)
		}
	}
	if len(saved) != 4 || saved[3] != 1.5 {
		t.Fatalf("saved %v", saved)
	}
	// The next voice message starts at the speed chosen; a song does not.
	other := find("demo/voice-bare")
	p.audio.toggle(p, other, -1)
	waitAudio(t, "the other did not start", func() bool { return p.audio.state(other).playing })
	if tempo() != 1.5 {
		t.Fatalf("the next voice message plays at %v", tempo())
	}
	music := find("demo/music")
	if speedChanges(music) {
		t.Fatal("a song of five seconds changes its speed")
	}
	p.audio.toggle(p, music, -1)
	waitAudio(t, "the music did not start", func() bool { return p.audio.state(music).playing })
	p.audio.nextSpeed()
	if tempo() != 1 {
		t.Fatalf("a song plays at %v", tempo())
	}
	long := music
	media := *music.Media
	media.Duration = speedMinLength
	long.Media = &media
	if !speedChanges(long) {
		t.Fatal("music of twenty minutes keeps its speed")
	}
}

// A voice message nobody listened to loses its mark once it plays.
func TestVoiceListenedWhenPlayed(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	m := find("demo/voice-bare")
	if !m.MediaUnread {
		t.Fatal("the voice message of the demo is listened to already")
	}
	p.audio.toggle(p, m, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if find("demo/voice-bare").MediaUnread {
		t.Fatal("played, it is still marked")
	}
}

func TestAudioTitle(t *testing.T) {
	l := localization.For("en")
	now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
	voiced := model.Message{Kind: model.MessageVoice, SenderName: "Anna", Date: now.Add(-3 * time.Hour), Media: &model.MessageMedia{}}
	for want, change := range map[string]func(*model.Message){
		"Anna|today at 15:00":          func(*model.Message) {},
		"Anna|yesterday at 15:00":      func(m *model.Message) { m.Date = m.Date.AddDate(0, 0, -1) },
		"Anna|20.09.2026 at 15:00":     func(m *model.Message) { m.Date = m.Date.AddDate(0, 0, -10) },
		"You|today at 15:00":           func(m *model.Message) { m.Outgoing = true },
		"Voice message|today at 15:00": func(m *model.Message) { m.SenderName = "" },
		"Night Tram|Demo Orchestra": func(m *model.Message) {
			m.Kind, m.Media = model.MessageMusic, &model.MessageMedia{Title: "Night Tram", Performer: "Demo Orchestra"}
		},
	} {
		msg := voiced
		change(&msg)
		if title, subtitle := audioTitle(msg, now, l); title+"|"+subtitle != want {
			t.Errorf("%q, want %q", title+"|"+subtitle, want)
		}
	}
	if speedText(1.5) != "1.5×" || speedText(2) != "2×" {
		t.Errorf("speeds %q %q", speedText(1.5), speedText(2))
	}
}

// While the bar closes, the space it leaves is still the bar, as it was
// when it closed: not the window under it.
func TestAudioBarClosesWithWhatItShowed(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	t.Chdir("../../..")
	size := image.Pt(600, 48)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	background := color.NRGBA{R: 255, B: 255, A: 255}
	now := time.Now()
	frame := func() {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		paint.Fill(gtx.Ops, background)
		if p.audioBarSize(gtx) > 0 {
			p.layoutAudioBar(gtx, localization.For("ru"), false)
		}
	}
	m := find("demo/voice")
	p.audio.toggle(p, m, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	frame()
	now = now.Add(heightDuration + time.Millisecond)
	frame()
	p.audio.stop()
	frame()
	now = now.Add(heightDuration / 3)
	frame()
	if p.audioBar.height.value == 0 {
		t.Fatal("the bar closed at once")
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(img.At(size.X/2, 1)).(color.NRGBA); got == background {
		t.Fatal("the closing bar shows the window under it")
	}
	now = now.Add(heightDuration)
	frame()
	if p.audioBar.height.value != 0 || p.audioBar.shown != nil {
		t.Fatal("the bar keeps what it showed after it closed")
	}
}
