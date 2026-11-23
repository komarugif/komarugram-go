// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/io/pointer"
)

func TestHistoryMediaSpoilers(t *testing.T) {
	t.Run("animations off", func(t *testing.T) { checkHistoryMediaSpoilers(t, "", false) })
	t.Run("wave", func(t *testing.T) { checkHistoryMediaSpoilers(t, "", true) })
}

func checkHistoryMediaSpoilers(t *testing.T, dir string, animate bool) {
	t.Helper()
	skipFrames(t)
	data, err := os.ReadFile(boxPaths(t, "pattern")[0])
	if err != nil {
		t.Fatal(err)
	}
	photo := model.Message{Kind: model.MessagePhoto, MediaSpoiler: true,
		Media: &model.MessageMedia{ID: "photo/spoiler", MIMEType: "image/png", Width: 1000, Height: 800}}
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		album := &messages[1]
		album.Text = ""
		left, right := photo, photo
		left.Key = album.Key
		right.Key = model.MessageKey{ChatID: album.Key.ChatID, MessageID: 22}
		album.Attachments = []model.Message{left, right}
	}, func(s *menuStore) model.ConversationStore { return &tileStore{menuStore: s, data: data} })
	h.still, h.animate = !animate, animate
	h.fx = new(effects)
	h.settle(t)
	var opened []model.MessageID
	h.page.openPhoto = func(m model.Message) { opened = append(opened, m.Key.MessageID) }
	var shots *effectsShots
	if dir != "" {
		shots = newEffectsShots(t, h, dir)
		shots.save("media-spoilers-covered", shots.take())
	}
	box := h.rowBox(2)
	click := func(x int) {
		h.press(pointer.ButtonPrimary, f32.Pt(float32(x), float32(box.Min.Y+box.Dy()/3)))
	}
	left, right := box.Min.X+box.Dx()/8, box.Max.X-box.Dx()/4
	click(left)
	if len(opened) != 0 {
		t.Fatalf("the first click opened %v", opened)
	}
	tile := h.page.rows[2].album[2]
	if animate {
		if tile.mediaReveal.started.IsZero() {
			t.Fatal("the click did not start the wave")
		}
		// A second click during the wave must not open a still-covered tile.
		click(left)
		if len(opened) != 0 {
			t.Fatalf("a click during the wave opened %v", opened)
		}
		if shots != nil {
			var frames []*image.RGBA
			for range 5 {
				frames = append(frames, shots.take())
				h.frames(10)
			}
			shots.save("media-spoilers-wave", strip(frames, box.Inset(-4)))
		}
		h.now = h.now.Add(spoilerMaxDuration)
		h.frame()
		if !tile.mediaReveal.started.IsZero() {
			t.Fatal("the wave did not finish")
		}
	} else if !tile.mediaReveal.started.IsZero() {
		t.Fatal("the wave started with animations off")
	}
	if shots != nil {
		shots.save("media-spoilers-one-revealed", shots.take())
	}
	h.now = h.now.Add(time.Second)
	click(left)
	if len(opened) != 1 || opened[0] != 2 {
		t.Fatalf("the next click opened %v, want the revealed photo", opened)
	}
	click(right)
	if len(opened) != 1 {
		t.Fatalf("revealing one tile also revealed its neighbour: %v", opened)
	}
}
