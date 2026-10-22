// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"testing"

	"komarugram/internal/messenger/model"
)

// A GIF the user chose to play with FFmpeg leaves the sandbox; the others
// stay in it, as the settings have them.
func TestPlayGIFWithFFmpeg(t *testing.T) {
	m := New(&fakeSource{}, func() {})
	defer m.Close()
	m.ConfigureDecoders(false, true, "")
	gif := func(id string) model.Message {
		return model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: id, MIMEType: "video/mp4"}}
	}
	chosen, other := gif("gif/chosen"), gif("gif/other")
	m.PlayGIFWithFFmpeg(chosen)
	m.BeginFrame()
	m.StatusFit(chosen, false, image.Pt(100, 100), true)
	m.StatusFit(other, false, image.Pt(100, 100), true)
	m.EndFrame()
	if e := m.entries["gif/chosen"]; e == nil || e.sandboxGIF {
		t.Error("the GIF chosen for FFmpeg plays in the sandbox")
	}
	if e := m.entries["gif/other"]; e == nil || !e.sandboxGIF {
		t.Error("another GIF left the sandbox")
	}
}
