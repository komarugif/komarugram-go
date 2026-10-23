// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/audio"
	"komarugram/pkg/video"
)

// Where a video plays: the external player when chosen, or for a video
// the sandbox does not take; FFmpeg, any; the sandbox, HD MP4.
func TestVideoEngineOf(t *testing.T) {
	video := func(mime string, w, h int) model.Message {
		return model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{MIMEType: mime, Width: w, Height: h}}
	}
	for _, c := range []struct {
		name    string
		m       model.Message
		player  string
		sandbox bool
		want    string
	}{
		{"chosen external", video("video/mp4", 1280, 720), "external", false, videoExternal},
		{"FFmpeg, 4K", video("video/x-matroska", 3840, 2160), "", false, videoFFmpeg},
		{"FFmpeg chosen", video("video/mp4", 640, 360), "internal", false, videoFFmpeg},
		{"sandbox, HD", video("video/mp4", 1280, 720), "", true, videoWASM},
		{"sandbox, HD upright", video("video/mp4", 720, 1280), "", true, videoWASM},
		{"sandbox, Full HD", video("video/mp4", 1920, 1080), "", true, videoExternal},
		{"sandbox, Matroska", video("video/x-matroska", 640, 360), "", true, videoExternal},
		{"not a video", model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{MIMEType: "video/mp4", Width: 320, Height: 240}}, "", false, videoExternal},
	} {
		if got := videoEngineOf(c.m, c.player, c.sandbox); got != c.want {
			t.Errorf("%s: %s, not %s", c.name, got, c.want)
		}
	}
}

// videoGallery is a gallery whose video's file is the demo's video.
type videoGallery struct {
	*galleryStore
	data []byte
}

func (s videoGallery) Media(ctx context.Context, m model.Message) ([]byte, error) {
	if m.Media.ID == "v20/file" {
		return s.data, nil
	}
	return s.galleryStore.Media(ctx, m)
}

// videoViewer is a viewer whose second item is the demo's video, played
// with engine into a silent output; external records what goes to the
// external player, from where.
func videoViewer(t *testing.T, engine string) (*viewerHarness, model.Message, *[]time.Duration) {
	t.Helper()
	data, err := os.ReadFile("../../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	h := &viewerHarness{t: t, store: newGalleryStore(), now: time.Unix(1000, 0), changed: make(chan struct{}, 64)}
	m := &h.store.photos[1]
	m.Kind = model.MessageVideo
	m.Media = &model.MessageMedia{ID: "v20/file", MIMEType: "video/mp4", Width: 720, Height: 1280, Duration: 36 * time.Second, Size: int64(len(data)),
		Thumbnail: &model.MessageMedia{ID: "v20/t", Width: 180, Height: 320}}
	h.viewer = newMediaViewer(videoGallery{h.store, data}, &h.images, func() {
		select {
		case h.changed <- struct{}{}:
		default:
		}
	})
	t.Cleanup(h.viewer.Destroy)
	h.viewer.videoEngine = func(model.Message) string { return engine }
	h.viewer.video.play = func(src audio.Source) (audioPlayback, error) {
		return &silentPlayback{src: src, playing: true}, nil
	}
	var external []time.Duration
	h.viewer.play = func(_ layout.Context, _ model.Message, at time.Duration, _ localization.Catalog) {
		external = append(external, at)
	}
	return h, *m, &external
}

// untilLong is until with more time, for decoders that start.
func (h *viewerHarness) untilLong(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for h.frame(); !cond(); h.frame() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		select {
		case <-h.changed:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A video plays in the viewer, in the sandbox or with FFmpeg: from the
// time it was opened at, its pictures drawn; its controls pause it, it
// moves, and it goes to the external player from where it is; another
// item stops it, and lets its sound go.
func TestViewerPlaysVideo(t *testing.T) {
	for _, engine := range []string{videoWASM, videoFFmpeg} {
		t.Run(engine, func(t *testing.T) {
			switch engine {
			case videoWASM:
				roundDecoders(t)
			case videoFFmpeg:
				if video.ResolveFFmpeg("") == "" || video.ResolveFFprobe("") == "" {
					t.Skip("no FFmpeg")
				}
			}
			h, m, external := videoViewer(t, engine)
			audioPaused := false
			h.viewer.pauseAudio = func() { audioPaused = true }
			h.viewer.OpenVideo(1, m, nil, 10*time.Second)
			state := func() videoState { return h.viewer.video.state(m) }
			h.untilLong("the video plays", func() bool { return state().playing })
			if !audioPaused {
				t.Fatal("the window's audio player plays on under the video")
			}
			if s := state(); s.position < 10*time.Second || s.position > 11*time.Second || s.duration < 35*time.Second {
				t.Fatalf("it plays at %v of %v, not from 0:10", s.position, s.duration)
			}
			h.untilLong("its picture", func() bool { return h.viewer.video.picture(m, time.Now(), image800) != nil })

			// Hidden while it plays, the controls show once the pointer
			// moves; a press where they were shows them too.
			h.now = h.now.Add(videoControlsIdle)
			// They fade out.
			for range 40 {
				h.frame()
			}
			h.click(108, 480)
			if !state().playing || !h.viewer.open {
				t.Fatal("a press where the hidden controls are paused it, or closed the viewer")
			}
			// The play button of the controls, at the left of their plate:
			// the stage is 72…728 by 56…516, the plate 48 high, 12 over its
			// bottom and 12 in from its sides.
			h.click(108, 480)
			if state().playing {
				t.Fatal("the play button did not pause it")
			}
			h.click(108, 480)
			if !state().playing {
				t.Fatal("the play button did not play it on")
			}
			h.viewer.video.seek(30 * time.Second)
			h.untilLong("the move", func() bool { return state().position >= 30*time.Second })

			// The external player's button, at the right end of the plate.
			h.click(692, 480)
			if len(*external) != 1 || (*external)[0] < 30*time.Second {
				t.Fatalf("the external player got %v", *external)
			}
			if state().active {
				t.Fatal("it plays on in the viewer with the external player")
			}

			// Played again, another item stops it.
			h.viewer.OpenVideo(1, m, nil, -1)
			h.untilLong("the video plays again", func() bool { return state().playing })
			h.viewer.video.mu.Lock()
			playback := h.viewer.video.playback.(*silentPlayback)
			h.viewer.video.mu.Unlock()
			h.key(key.NameRightArrow)
			if state().active || !playback.isClosed() {
				t.Fatal("the next item did not stop it")
			}
		})
	}
}

// image800 is a box the viewer's video is drawn in.
var image800 = image.Pt(800, 600)

// A click on a video in the chat, or a timestamp of one, shows it in the
// viewer and plays it there, when the settings choose the viewer.
func TestChatVideoOpensViewer(t *testing.T) {
	p := newChatPage(nil, func() {})
	t.Cleanup(p.Close)
	m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 5}, Kind: model.MessageVideo, Media: &model.MessageMedia{MIMEType: "video/mp4", Width: 1280, Height: 720}}
	var opened []time.Duration
	p.openVideo = func(_ model.Message, at time.Duration) { opened = append(opened, at) }
	p.videoEngine = func(model.Message) string { return videoWASM }
	gtx := layout.Context{Ops: new(op.Ops)}
	p.playVideo(gtx, m, -1, localization.For("en"))
	p.playVideo(gtx, m, 42*time.Second, localization.For("en"))
	if len(opened) != 2 || opened[0] != -1 || opened[1] != 42*time.Second {
		t.Fatalf("the viewer opened at %v", opened)
	}
}
