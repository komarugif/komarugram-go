// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

func TestSnapshotMediaSpoilers(t *testing.T) {
	skipFrames(t)
	data, err := os.ReadFile(boxPaths(t, "pattern")[0])
	if err != nil {
		t.Fatal(err)
	}
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		photo := messages[1]
		photo.Text = ""
		photo.Kind = model.MessagePhoto
		photo.MediaSpoiler = true
		photo.Media = &model.MessageMedia{ID: "photo/shot-spoiler", MIMEType: "image/png", Width: 1000, Height: 800}
		messages[1] = photo
		album := &messages[2]
		album.Text = ""
		left, right := photo, photo
		left.Key = album.Key
		right.Key.MessageID = 22
		right.MediaSpoiler = false
		album.Attachments = []model.Message{left, right}
	}, func(s *menuStore) model.ConversationStore { return &tileStore{menuStore: s, data: data} })
	h.still = true
	h.settle(t)
	p := h.page
	shot := &p.shot
	label := localization.For("en").T("shot.spoilers")
	var spoilerSwitch func([]input.SemanticNode) image.Rectangle
	spoilerSwitch = func(nodes []input.SemanticNode) image.Rectangle {
		for _, node := range nodes {
			if node.Desc.Label == label {
				return node.Desc.Bounds
			}
			if bounds := spoilerSwitch(node.Children); !bounds.Empty() {
				return bounds
			}
		}
		return image.Rectangle{}
	}
	clickSpoilers := func(t *testing.T, reveal bool) {
		t.Helper()
		bounds := spoilerSwitch(h.router.AppendSemantics(nil))
		if bounds.Empty() {
			t.Fatal("the snapshot has no reveal switch")
		}
		h.press(pointer.ButtonPrimary, layout.FPt(bounds.Min.Add(image.Pt(8, 8))))
		if shot.opts.spoilers != reveal || !shot.rendering {
			t.Fatalf("switching reveal to %v: reveal=%v, rendering=%v", reveal, shot.opts.spoilers, shot.rendering)
		}
	}
	for i, name := range []string{"photo", "album"} {
		msg := p.messages[i+1]
		t.Run(name, func(t *testing.T) {
			shot.open(p, []model.Message{msg})
			waitFor(t, "covered snapshot", h.frame, func() bool { return shot.image != nil || shot.err != nil })
			if shot.err != nil {
				t.Fatal(shot.err)
			}
			covered := shot.image
			saveSpoilerShot(t, name+"-covered", shot)
			if name == "photo" {
				dots, redStripe := snapshotPhotoMarks(covered)
				if !dots || redStripe {
					t.Errorf("covered photo: visible particles=%v, sharp red stripe=%v", dots, redStripe)
				}
			}

			clickSpoilers(t, true)
			waitFor(t, "revealed snapshot", h.frame, func() bool { return shot.image != covered || shot.err != nil })
			if shot.err != nil {
				t.Fatal(shot.err)
			}
			if bytes.Equal(covered.Pix, shot.image.Pix) {
				t.Error("the reveal option left the photo covered")
			}
			if name == "photo" {
				if _, redStripe := snapshotPhotoMarks(shot.image); !redStripe {
					t.Error("the revealed photo is still blurred")
				}
			}
			saveSpoilerShot(t, name+"-revealed", shot)

			row := p.rows[msg.Key.MessageID]
			if name == "album" {
				row = row.album[msg.Attachments[0].Key.MessageID]
			}
			if row.mediaRevealed {
				t.Error("the reveal option also revealed the photo in the chat")
			}
			row.mediaRevealed = true
			revealed := shot.image
			clickSpoilers(t, false)
			waitFor(t, "a new covered snapshot", h.frame, func() bool { return shot.image != revealed || shot.err != nil })
			if shot.err != nil {
				t.Fatal(shot.err)
			}
			if !bytes.Equal(covered.Pix, shot.image.Pix) || !row.mediaRevealed {
				t.Error("the chat and the snapshot did not keep separate spoiler states")
			}
		})
	}
}

// The pattern's white tiles become darker under the cover, which adds
// bright particles. Its narrow red stripes are lost in the blur.
func snapshotPhotoMarks(img *image.RGBA) (dots, redStripe bool) {
	area := img.Bounds().Inset(40)
	area.Min.Y = max(area.Min.Y, img.Bounds().Dy()/4)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			c := img.RGBAAt(x, y)
			dots = dots || c.R > 225 && c.G > 225 && c.B > 225
			redStripe = redStripe || c.R > 180 && c.G < 70 && c.B < 70
		}
	}
	return dots, redStripe
}

// SHOT_SPOILERS_PNG_DIR saves the PNGs the snapshot dialog offers.
func saveSpoilerShot(t *testing.T, name string, shot *shotDialog) {
	t.Helper()
	dir := os.Getenv("SHOT_SPOILERS_PNG_DIR")
	if dir == "" {
		return
	}
	data, err := shot.png()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
