// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
)

// tileColors are the colors of a tile of media not loaded and of a
// received message's bubble.
func tileColors() (tile, bubble color.NRGBA) {
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	sc := scheme(gtx)
	return sc.SurfaceContainerHigh.AsNRGBA(), sc.Surface.Color.AsNRGBA()
}

// timeShown tells whether the corner of box, inset in, has a plate on it:
// pixels of another color than the tile's, the plate's or its text's.
func timeShown(img *image.RGBA, box image.Rectangle, tile color.NRGBA, inset int) bool {
	other := 0
	for y := box.Max.Y - inset - 16; y < box.Max.Y-inset-4; y++ {
		for x := box.Max.X - inset - 30; x < box.Max.X-inset-4; x++ {
			if !near(img.RGBAAt(x, y), tile) {
				other++
			}
		}
	}
	return other > 50
}

// hover moves the pointer to pos, and lays the page out after it.
func (h *menuHarness) hover(pos f32.Point) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Time: time.Duration(h.now.UnixNano())})
	h.frames(2)
}

// The time of a bubble that ends in media goes on a plate in the media's
// corner, the bubble ending mediaInset under them, as Telegram for Android
// draws it; on a video it fades out while the pointer is on the video, as
// the video's own plate does.
func TestTimeOnVideo(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		// Joined to the first, with no name over it, and no caption.
		tallVideo(&m[1])
		m[1].Text = ""
	})
	h.frames(2)
	r := h.page.rows[2]
	if r == nil || !r.time.drawn {
		t.Fatal("the time is not on the video")
	}
	// 360 px of video, 3 over it and 3 under it.
	if r.bodySize.Y != 366 {
		t.Fatalf("the bubble is %d px high, want 366: the video and 3 px over and under it", r.bodySize.Y)
	}
	tile, bubble := tileColors()
	img := drawHistory(t, h, image.Pt(500, 700))
	box := findTile(img, tile)
	if box.Dx() < 150 || box.Dy() < 300 {
		t.Fatalf("no tile found: %v", box)
	}
	// Under the video, 3 px of the bubble.
	center := box.Min.X + box.Dx()/2
	under := 0
	for y := box.Max.Y; y < box.Max.Y+20 && near(img.RGBAAt(center, y), bubble); y++ {
		under++
	}
	if under != 3 {
		t.Fatalf("the bubble goes %d px under the video, want 3", under)
	}
	if !timeShown(img, box, tile, 8) {
		t.Fatal("no time in the video's corner")
	}
	h.hover(f32.Pt(float32(center), float32(box.Min.Y+box.Dy()/4)))
	if img := drawHistory(t, h, image.Pt(500, 700)); timeShown(img, box, tile, 8) {
		t.Fatal("the time stays on the video under the pointer")
	}
}

// A GIF that plays in the chat shows the time only under the pointer, as
// its own time.
func TestTimeOnGIF(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text, m[1].Kind = "", model.MessageGIF
		m[1].Media = &model.MessageMedia{ID: "document/3", MIMEType: "video/mp4", Width: 478, Height: 854, Size: 1000, Duration: 3e9}
	})
	h.frames(2)
	r := h.page.rows[2]
	if r == nil || !r.time.drawn {
		t.Fatal("the time is not on the GIF")
	}
	tile, _ := tileColors()
	img := drawHistory(t, h, image.Pt(500, 700))
	box := findTile(img, tile)
	if timeShown(img, box, tile, 8) {
		t.Fatal("the time shows on a GIF off the pointer")
	}
	h.hover(f32.Pt(float32(box.Min.X+box.Dx()/2), float32(box.Min.Y+box.Dy()/4)))
	if img := drawHistory(t, h, image.Pt(500, 700)); !timeShown(img, box, tile, 8) {
		t.Fatal("the time does not show on the GIF under the pointer")
	}
}

// On a photo the time fades out under the pointer, for the picture to be
// seen whole.
func TestTimeOnPhotoHides(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text, m[1].Kind = "", model.MessagePhoto
		m[1].Media = &model.MessageMedia{ID: "photo/1", MIMEType: "image/jpeg", Width: 478, Height: 854}
	})
	h.frames(2)
	tile, _ := tileColors()
	img := drawHistory(t, h, image.Pt(500, 700))
	box := findTile(img, tile)
	if box.Dx() < 150 {
		t.Fatalf("no photo found: %v", box)
	}
	if !timeShown(img, box, tile, 8) {
		t.Fatal("no time on the photo")
	}
	h.hover(f32.Pt(float32(box.Min.X+box.Dx()/2), float32(box.Min.Y+box.Dy()/4)))
	if img := drawHistory(t, h, image.Pt(500, 700)); timeShown(img, box, tile, 8) {
		t.Fatal("the time stays on the photo under the pointer")
	}
}

// A video message has the time it was sent inside, under the circle's
// right, as its duration is under its left, someone else's as the
// account's own, as Telegram Desktop draws it: nothing goes beside it.
func TestRoundTimeInside(t *testing.T) {
	round := func(m *model.Message) {
		m.Text, m.Kind = "", model.MessageRound
		m.Media = &model.MessageMedia{ID: "document/7", MIMEType: "video/mp4", Width: 384, Height: 384, Duration: 5e9}
	}
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		round(&m[1])
		round(&m[2])
		m[2].Outgoing = true
	})
	h.frames(2)
	theirs, ours := h.page.rows[2], h.page.rows[3]
	if theirs == nil || ours == nil {
		t.Fatal("rows not laid out")
	}
	if theirs.bodySize != ours.bodySize {
		t.Fatalf("someone else's video message takes %v, the account's own %v: its time is beside it", theirs.bodySize, ours.bodySize)
	}
}

// Media the time's plate does not fit on keep it on a line of their own,
// as media with a caption under them do.
func TestTimeUnderNarrowMedia(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		// A strip as narrow as the plate is wide.
		m[1].Text, m[1].Kind = "", model.MessagePhoto
		m[1].Media = &model.MessageMedia{ID: "photo/1", MIMEType: "image/jpeg", Width: 40, Height: 400}
		tallVideo(&m[2])
	})
	h.frames(2)
	narrow, captioned := h.page.rows[2], h.page.rows[3]
	if narrow == nil || captioned == nil {
		t.Fatal("rows not laid out")
	}
	if narrow.time.drawn {
		t.Fatal("the time is on a strip narrower than it")
	}
	if captioned.time.drawn {
		t.Fatal("the time is on a video with a caption under it")
	}
}

// The time over the corner of a sticker the account sent fades out while
// the pointer is on the sticker, for it to be seen whole.
func TestStickerTimeHides(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		m[1].Text, m[1].Kind, m[1].Outgoing = "", model.MessageSticker, true
		m[1].Media = &model.MessageMedia{ID: "document/4", MIMEType: "image/webp", Width: 512, Height: 512}
	})
	h.frames(2)
	tile, _ := tileColors()
	img := drawHistory(t, h, image.Pt(500, 700))
	box := findTile(img, tile)
	if box.Dx() < 100 {
		t.Fatalf("no sticker found: %v", box)
	}
	if !timeShown(img, box, tile, 4) {
		t.Fatal("no time over the sticker")
	}
	h.hover(f32.Pt(float32(box.Min.X+box.Dx()/2), float32(box.Min.Y+box.Dy()/2)))
	if img := drawHistory(t, h, image.Pt(500, 700)); timeShown(img, box, tile, 4) {
		t.Fatal("the time stays over the sticker under the pointer")
	}
}

// TestRenderMediaTime saves bubbles that end in media, the time on them,
// for looking at them: MEDIA_TIME_PNG=/tmp/time.png.
func TestRenderMediaTime(t *testing.T) {
	path := os.Getenv("MEDIA_TIME_PNG")
	if path == "" {
		t.Skip("set MEDIA_TIME_PNG to a file")
	}
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		for i := range m {
			m[i].SenderID, m[i].SenderName = int64(i+1), "Анна"
		}
		// A video with the name over it, one joined to it, a narrow one.
		tallVideo(&m[0])
		m[0].Text = ""
		m[1].SenderID = m[0].SenderID
		tallVideo(&m[1])
		m[1].Text = ""
		m[2].Text, m[2].Outgoing = "", true
		m[2].Kind, m[2].Media = model.MessagePhoto, &model.MessageMedia{ID: "photo/2", MIMEType: "image/jpeg", Width: 1280, Height: 960}
		m[2].EditedAt = time.Unix(1, 0)
		m[3].Text = ""
		for j := range 3 {
			kind, mime := model.MessagePhoto, "image/jpeg"
			if j == 2 {
				kind, mime = model.MessageVideo, "video/mp4"
			}
			m[3].Attachments = append(m[3].Attachments, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(100 + j)}, Kind: kind,
				Media: &model.MessageMedia{ID: "photo/a", MIMEType: mime, Width: 1280, Height: 960, Duration: 41e9}})
		}
		// A narrow video: its own plate goes to the top.
		m[4].Text, m[4].Kind = "", model.MessageVideo
		m[4].Media = &model.MessageMedia{ID: "document/5", MIMEType: "video/mp4", Width: 300, Height: 854, Duration: 41e9}
		m[5].Text, m[5].Kind, m[5].Outgoing = "", model.MessageSticker, true
		m[5].Media = &model.MessageMedia{ID: "document/6", MIMEType: "image/webp", Width: 512, Height: 512}
		tallVideo(&m[6])
		m[6].Text = "С подписью"
		// Video messages, someone else's and the account's own.
		for i := 7; i <= 8; i++ {
			m[i].Text, m[i].Kind = "", model.MessageRound
			m[i].Media = &model.MessageMedia{ID: "document/7", MIMEType: "video/mp4", Width: 384, Height: 384, Duration: 5e9}
		}
		m[8].Outgoing = true
	})
	p := h.page
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 3800), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
