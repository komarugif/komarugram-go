// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"os"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// tallVideo is a video as tall as a phone's screen, with no picture
// loaded: its tile is the plate of SurfaceContainerHigh.
func tallVideo(m *model.Message) {
	m.Kind = model.MessageVideo
	m.Media = &model.MessageMedia{ID: "document/1", MIMEType: "video/mp4", Width: 478, Height: 854, Duration: 20e9}
}

// drawHistory draws the harness's history headless, at one pixel to a dp.
func drawHistory(t *testing.T, h *menuHarness, size image.Point) *image.RGBA {
	t.Helper()
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Skip("no GPU:", err)
	}
	defer win.Release()
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Now: h.now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	// What fades is drawn as it ends.
	wdk.SetAnimationsEnabled(gtx, false)
	h.page.Layout(gtx, model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}, localization.For("en"), false)
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	return img
}

// near tells whether c is want, give or take the rounding of blending.
func near(c color.RGBA, want color.NRGBA) bool {
	d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
	return d(c.R, want.R) < 4 && d(c.G, want.G) < 4 && d(c.B, want.B) < 4
}

// A video in a bubble comes the gap of an album's tiles from the bubble's
// sides, and from its top where no name is over it, as in Telegram for
// Android; the text under it keeps the bubble's own padding.
func TestMediaInsetInBubble(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		// Joined to the first, with no name over it.
		tallVideo(&m[1])
		m[1].Text = "A"
	})
	h.frames(2)
	size := image.Pt(500, 700)
	img := drawHistory(t, h, size)
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	sc := scheme(gtx)
	tile, bubble := sc.SurfaceContainerHigh.AsNRGBA(), sc.Surface.Color.AsNRGBA()
	box := findTile(img, tile)
	if box.Dx() < 150 || box.Dy() < 300 {
		t.Fatalf("no tile found: %v", box)
	}
	if box.Dx() > 210 {
		t.Fatalf("the tile is %v: more than the video", box)
	}
	checkInsets(t, img, box, tile, bubble)
}

// findTile is the bounds of the widest tile of color in img: the longest
// run of its color across, over the composer, which is of its color too,
// then down and up from it, off the play button in its middle.
func findTile(img *image.RGBA, tile color.NRGBA) image.Rectangle {
	size := img.Bounds().Size()
	var box image.Rectangle
	for y := range size.Y - 120 {
		for x := 0; x < size.X; {
			if !near(img.RGBAAt(x, y), tile) {
				x++
				continue
			}
			end := x
			for end < size.X && near(img.RGBAAt(end, y), tile) {
				end++
			}
			if end-x > box.Dx() {
				box = image.Rect(x, y, end, y+1)
			}
			x = end
		}
	}
	if box.Empty() {
		return box
	}
	// Up and down the middle, past the play button and the plates at the
	// bottom's corners, to the last of the tile's color.
	col, row := box.Min.X+box.Dx()/2, box.Min.Y
	extend := func(dir int) int {
		last := row
		for y, gap := row+dir, 0; y >= 0 && y < size.Y && gap < 50; y += dir {
			if near(img.RGBAAt(col, y), tile) {
				last, gap = y, 0
			} else {
				gap++
			}
		}
		return last
	}
	box.Min.Y, box.Max.Y = extend(-1), extend(1)+1
	return box
}

// checkInsets checks that the bubble is 3 px on each side of the tile in
// box, and over it, and that the caption under it keeps 12 px.
func checkInsets(t *testing.T, img *image.RGBA, box image.Rectangle, tile, bubble color.NRGBA) {
	t.Helper()
	size := img.Bounds().Size()
	// run counts the bubble's pixels past (x, y), the tile's own, by step,
	// to the bubble's edge.
	run := func(x, y, dx, dy int) int {
		n := 0
		for x, y = x+dx, y+dy; x >= 0 && y >= 0 && x < size.X && y < size.Y && near(img.RGBAAt(x, y), bubble); x, y = x+dx, y+dy {
			n++
		}
		return n
	}
	mid := box.Min.Y + box.Dy()/3
	center := box.Min.X + box.Dx()/2
	// The top in the middle, off the rounded corners: over the play button.
	tileTop := box.Min.Y + box.Dy()/4
	for tileTop > 0 && near(img.RGBAAt(center, tileTop-1), tile) {
		tileTop--
	}
	left, right, top := run(box.Min.X, mid, -1, 0), run(box.Max.X-1, mid, 1, 0), run(center, tileTop, 0, -1)
	if left != 3 || right != 3 || top != 3 {
		t.Fatalf("the bubble is %d px left of the video, %d right, %d over it; want 3 each", left, right, top)
	}
	// The caption keeps the bubble's padding: its first dark pixel is 12 px
	// in from the bubble's edge.
	bubbleLeft := box.Min.X - 3
	for y := box.Max.Y; y < min(size.Y, box.Max.Y+40); y++ {
		for x := bubbleLeft; x < bubbleLeft+40; x++ {
			if c := img.RGBAAt(x, y); int(c.R)+int(c.G)+int(c.B) < 300 {
				if x-bubbleLeft < 12 {
					t.Fatalf("the caption starts %d px in from the bubble's edge", x-bubbleLeft)
				}
				return
			}
		}
	}
	t.Fatal("no caption under the video")
}

// TestRenderMediaInset saves bubbles of media, for looking at their insets:
// MEDIA_INSET_PNG=/tmp/inset.png.
func TestRenderMediaInset(t *testing.T) {
	path := os.Getenv("MEDIA_INSET_PNG")
	if path == "" {
		t.Skip("set MEDIA_INSET_PNG to a file")
	}
	h := newMenuHarness(t, func(_ *menuStore, m []model.Message) {
		for i := range m {
			m[i].SenderID, m[i].SenderName = int64(i+1), "Анна"
		}
		// With the name over it, then joined to the one before, with none.
		tallVideo(&m[0])
		m[0].Text = "Видео с подписью"
		m[1].SenderID = m[0].SenderID
		tallVideo(&m[1])
		m[2].Kind = model.MessagePhoto
		m[2].Media = &model.MessageMedia{ID: "photo/2", MIMEType: "image/jpeg", Width: 1280, Height: 960}
		m[2].Outgoing = true
		m[3].Text = "Альбом"
		for j := range 3 {
			m[3].Attachments = append(m[3].Attachments, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(100 + j)}, Kind: model.MessagePhoto,
				Media: &model.MessageMedia{ID: "photo/a", MIMEType: "image/jpeg", Width: 1280, Height: 960}})
		}
		m[4].SenderID = m[3].SenderID
		m[4].Text, m[4].ReplyToMessageID = "Ответ с фото", 1
		m[4].Kind = model.MessagePhoto
		m[4].Media = &model.MessageMedia{ID: "photo/4", MIMEType: "image/jpeg", Width: 960, Height: 1280}
		m[5].Text, m[5].Kind = "Файл", model.MessageFile
		m[5].Media = &model.MessageMedia{ID: "document/5", MIMEType: "application/pdf", FileName: "report.pdf", Size: 123456}
	})
	p := h.page
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 2400), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
