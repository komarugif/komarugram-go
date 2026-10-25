// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sync/atomic"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// refusingSource refuses every picture, as Telegram refuses a file, and
// counts how often it was asked.
type refusingSource struct{ asked atomic.Int32 }

func (s *refusingSource) Media(context.Context, model.Message) ([]byte, error) {
	s.asked.Add(1)
	return nil, errors.New("refused")
}

// A thumbnail that failed is not asked for again each time it is shown,
// as after the window was hidden, but after a wait that doubles.
func TestPreviewThumbFailureWaits(t *testing.T) {
	source := new(refusingSource)
	pm := &previewMedia{media: chatmedia.NewPreviews(source, func() {}), images: new(imageOps)}
	defer pm.media.Close()
	l := newChatList()
	l.previews = pm
	id := "photo/1/s"
	chats := []model.Chat{{ID: 1, Title: "Photo", Last: model.Preview{Media: "preview.photo", Thumbs: []model.PreviewThumb{{ID: id, Message: 1}}}}}
	var ops op.Ops
	catalog := localization.For("en")
	frame := func(now time.Time) {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Now: now, Constraints: layout.Exact(image.Pt(360, 200)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		pm.images.BeginFrame()
		pm.media.BeginFrame()
		l.Layout(gtx, section{kind: sectionAll}, nil, chats, 0, false, catalog)
		pm.media.EndFrame()
		pm.images.EndFrame()
	}
	// failedAt draws frames at now until the thumbnail has failed n times.
	failedAt := func(now time.Time, n int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); pm.failed[id].times < n; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("failed %d times, not %d; asked %d", pm.failed[id].times, n, source.asked.Load())
			}
			frame(now)
		}
	}
	start := time.Now()
	failedAt(start, 1)
	// Hidden and shown again, before its time: not asked.
	pm.media.Release()
	for range 20 {
		frame(start.Add(previewThumbRetry / 2))
		time.Sleep(time.Millisecond)
	}
	if n := source.asked.Load(); n != 1 {
		t.Fatalf("asked %d times before its time", n)
	}
	// Its time comes: asked once more, and then it waits twice as long.
	again := start.Add(previewThumbRetry)
	failedAt(again, 2)
	if n := source.asked.Load(); n != 2 {
		t.Fatalf("asked %d times at its time", n)
	}
	if want := again.Add(2 * previewThumbRetry); !pm.failed[id].retry.Equal(want) {
		t.Fatalf("next try at %v, want %v", pm.failed[id].retry.Sub(start), want.Sub(start))
	}
	pm.media.Release()
	for range 20 {
		frame(again.Add(previewThumbRetry))
		time.Sleep(time.Millisecond)
	}
	if n := source.asked.Load(); n != 2 {
		t.Fatalf("asked %d times before its doubled wait", n)
	}
}

// The same message, as the store hands it again in new slices, is the same
// line: its custom emoji, which play twice from when it was first drawn,
// do not start over. Another message is a new line.
func TestPreviewLineSameMessage(t *testing.T) {
	var p previewLine
	c := model.Chat{ID: 1, Last: model.Preview{Text: "a b", Entities: []model.Entity{{Kind: "italic", Offset: 2, Length: 1}}}}
	l := localization.For("en")
	p.update(c, "x", l, 17)
	// Drawn once: its emoji play from then.
	shown := time.Unix(1, 0)
	p.shown = shown
	c.Last.Entities = []model.Entity{{Kind: "italic", Offset: 2, Length: 1}}
	p.update(c, "x", l, 17)
	if !p.shown.Equal(shown) || len(p.runs) != 2 {
		t.Fatal("the same message started over")
	}
	c.Last.Text = "a c"
	p.update(c, "x", l, 17)
	if p.runs[1].Text != "c" || !p.runs[1].Italic || !p.shown.IsZero() {
		t.Fatalf("not made again for another message: %+v", p.runs)
	}
}

// strippedThumb is a picture as Telegram strips its inline ones: the size
// and the scan of a JPEG whose header is the one they all share, which Go's
// encoder writes but for the tables of its quality.
func strippedThumb(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			im.Set(x, y, color.RGBA{200, uint8(4 * x), 60, 0xff})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	sos := []byte{0xff, 0xda, 0, 12}
	at := bytes.Index(b.Bytes(), sos)
	if at < 0 {
		t.Fatal("no scan")
	}
	scan := b.Bytes()[at+len(sos)+10 : b.Len()-2]
	return append([]byte{1, 30, 40}, scan...)
}

// The inline picture Telegram sends with the message is drawn in the
// thumbnail's place, and the thumbnail is not downloaded: only one without
// it is.
func TestPreviewThumbInlineOnly(t *testing.T) {
	source := new(refusingSource)
	pm := &previewMedia{media: chatmedia.NewPreviews(source, func() {}), images: new(imageOps)}
	defer pm.media.Close()
	var p previewLine
	c := model.Chat{ID: 1, Last: model.Preview{Media: "preview.photo", Thumbs: []model.PreviewThumb{
		{ID: "photo/1/s", Message: 1, Blurred: strippedThumb(t)},
		{ID: "photo/2/s", Message: 2},
	}}}
	p.update(c, "x", localization.For("en"), 17)
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Now: time.Now(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	for deadline := time.Now().Add(5 * time.Second); source.asked.Load() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the thumbnail without an inline picture was not asked for")
		}
		pm.media.BeginFrame()
		im := p.thumbImage(gtx, pm, 0, image.Pt(18, 18))
		p.thumbImage(gtx, pm, 1, image.Pt(18, 18))
		pm.media.EndFrame()
		if im == nil || im.Bounds().Dx() != 40 {
			t.Fatalf("drawn %v", im)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := source.asked.Load(); n != 1 {
		t.Fatalf("asked %d times, the inline picture's thumbnail too", n)
	}
}
