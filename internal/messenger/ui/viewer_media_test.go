// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// A video in the gallery shows its thumbnail, never downloads the video,
// and plays in the external player by a click, as the viewer is told to
// play videos without a videoEngine.
func TestMediaViewerPlaysVideos(t *testing.T) {
	h := newViewerHarness(t)
	video := &h.store.photos[1]
	video.Kind = model.MessageVideo
	video.Media = &model.MessageMedia{ID: "v20/file", Width: 1280, Height: 720, Thumbnail: &model.MessageMedia{ID: "v20/t", Width: 320, Height: 180}}
	var played []model.MessageID
	h.viewer.play = func(_ layout.Context, m model.Message, _ time.Duration, _ localization.Catalog) {
		played = append(played, m.Key.MessageID)
	}

	h.viewer.Open(1, h.store.photos[0], nil)
	h.until("the video in the gallery", func() bool {
		items, _, _, _, _ := h.viewer.snapshot()
		// indexOf clamps a missing ID to the nearest item; it is not
		// a membership check. Wait for the asynchronously fetched video.
		return slices.ContainsFunc(items, func(m model.Message) bool { return m.Key.MessageID == 20 })
	})
	h.key(key.NameRightArrow)
	h.until("the thumbnail", func() bool { return slices.Contains(h.store.read(), "v20/t") })
	if canKeep(*video) {
		t.Fatal("a video offers save and copy")
	}
	h.click(400, 300)
	if !slices.Equal(played, []model.MessageID{20}) {
		t.Fatalf("played %v", played)
	}
	if slices.Contains(h.store.read(), "v20/file") {
		t.Fatal("the video was downloaded")
	}
}

// A GIF opens alone: no gallery is read around it.
func TestMediaViewerOpensGIFAlone(t *testing.T) {
	h := newViewerHarness(t)
	gif := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 25}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "g25", MIMEType: "image/gif", Width: 200, Height: 100}}
	h.viewer.OpenAlone(1, gif)
	for range 5 {
		h.frame()
	}
	items, _, _, _, _ := h.viewer.snapshot()
	if len(items) != 1 || items[0].Key.MessageID != 25 || !h.viewer.open {
		t.Fatalf("%d items, open %v", len(items), h.viewer.open)
	}
}
