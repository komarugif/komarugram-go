// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
)

// TestRenderAudio draws the demo's voice messages and music in the history:
// voice messages with the waveform Telegram sends and without, whose
// waveforms are worked out as they show (the M4A one's decoder is fetched),
// and music with its bar, paused at 60%: one plays at a time, and the bar
// over the chat tells which; and the video message, before it plays and
// while it does (its decoders are fetched). In the light and the dark
// theme, into AUDIO_PNG_DIR:
//
//	AUDIO_PNG_DIR=/tmp/voice go test ./internal/messenger/ui -run RenderAudio
func TestRenderAudio(t *testing.T) {
	dir := os.Getenv("AUDIO_PNG_DIR")
	if dir == "" {
		t.Skip("set AUDIO_PNG_DIR to a directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir("../../..")
	l := localization.For("ru")
	for _, dark := range []bool{false, true} {
		p, _, find := audioHarnessPage(t)
		p.images = &imageOps{}
		voiced := find("demo/voice")
		// Scroll so that the voice messages are at the top.
		draw := func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.Layout(gtx, model.Chat{ID: 2}, l, false)
			p.media.EndFrame()
			p.images.EndFrame()
		}
		renderToast(t, filepath.Join(dir, "scratch.png"), image.Pt(600, 820), dark, draw)
		for i, m := range p.messages {
			if m.Key == voiced.Key {
				p.list.Position = layout.Position{First: i, BeforeEnd: true}
			}
		}
		music := find("demo/music")
		p.audio.toggle(p, music, 0.6)
		waitAudio(t, "the music did not start", func() bool { return p.audio.state(music).playing })
		p.audio.toggle(p, music, -1)
		theme := "light"
		if dark {
			theme = "dark"
		}
		renderToast(t, filepath.Join(dir, "audio-"+theme+".png"), image.Pt(600, 820), dark, draw)
		// A voice message at one and a half its speed: the bar over the
		// chat has the button of the speed.
		p.audio.nextSpeed()
		p.audio.toggle(p, voiced, 0.3)
		waitAudio(t, "the voice message did not start", func() bool { return p.audio.state(voiced).playing })
		p.audio.toggle(p, voiced, -1)
		// With the volume slider held at 60%.
		p.audio.setVolume(0.6, false)
		p.audioBar.slider.dragging = true
		renderToast(t, filepath.Join(dir, "audio-voice-"+theme+".png"), image.Pt(600, 820), dark, draw)
		p.audioBar.slider.dragging = false
		p.audio.setVolume(1, false)

		// A video message: its thumbnail while it does not play, not seen
		// yet; then paused at 40%, its picture and the ring of what was
		// seen, the bar over the chat telling of it.
		round := find("demo/round")
		p.audio.stop()
		for i, m := range p.messages {
			if m.Key == round.Key {
				p.list.Position = layout.Position{First: max(0, i-2), BeforeEnd: true}
			}
		}
		renderToast(t, filepath.Join(dir, "scratch.png"), image.Pt(600, 820), dark, draw)
		waitAudio(t, "the thumbnail did not load", func() bool {
			target := round
			target.Kind, target.Media = model.MessagePhoto, round.Media.Thumbnail
			return p.media.StatusFit(target, false, image.Pt(240, 240), true).Frame != nil
		})
		renderToast(t, filepath.Join(dir, "round-idle-"+theme+".png"), image.Pt(600, 820), dark, draw)
		p.audio.toggle(p, round, 0.4)
		waitAudio(t, "the video message did not start", func() bool { return p.audio.state(round).playing })
		p.audio.toggle(p, round, -1)
		waitAudio(t, "the video message has no picture", func() bool {
			renderToast(t, filepath.Join(dir, "scratch.png"), image.Pt(600, 820), dark, draw)
			p.audio.mu.Lock()
			video := p.audio.video
			p.audio.mu.Unlock()
			return video != nil && time.Duration(video.shownAt.Load()) > 14*time.Second
		})
		renderToast(t, filepath.Join(dir, "round-"+theme+".png"), image.Pt(600, 820), dark, draw)
		p.audio.stop()
	}
	os.Remove(filepath.Join(dir, "scratch.png"))
}
