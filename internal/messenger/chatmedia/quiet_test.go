// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"komarugram/internal/messenger/model"
)

// Quiet, the manager starts no load and counts nothing as loading: rows
// measured off screen ask for media no one sees. What has loaded shows.
func TestQuietStartsNothing(t *testing.T) {
	m, source, loaded := loadedManager(t)
	calls := source.calls.Load()
	m.LoadingSeen()
	other := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "two", MIMEType: "image/png"}}
	m.SetQuiet(true)
	m.BeginFrame()
	st := m.StatusFit(other, true, image.Pt(64, 64), false)
	shown := m.Status(loaded, false)
	m.EndFrame()
	m.SetQuiet(false)
	if !st.Loading || st.Frame != nil {
		t.Fatalf("media not loaded is %+v while quiet", st)
	}
	if shown.Frame == nil {
		t.Fatal("media loaded shows nothing while quiet")
	}
	if n := m.LoadingSeen(); n != 0 {
		t.Fatalf("%d media counted as loading while quiet", n)
	}
	if m.entries["two"] != nil || source.calls.Load() != calls {
		t.Fatal("a load started while quiet")
	}
	m.Status(other, false)
	if m.entries["two"] == nil {
		t.Fatal("no load started once loud again")
	}
}

// Deferring, the manager starts no load for media it was not asked for
// before, which show as loading all the same; those loaded show, and the
// rest load once it is off.
func TestDeferringHoldsBackNewLoads(t *testing.T) {
	m, source, loaded := loadedManager(t)
	calls := source.calls.Load()
	other := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "two", MIMEType: "image/png"}}
	m.SetDeferring(true)
	m.BeginFrame()
	st := m.StatusFit(other, true, image.Pt(64, 64), false)
	shown := m.Status(loaded, false)
	m.EndFrame()
	m.SetDeferring(false)
	if !st.Loading || st.Frame != nil {
		t.Fatalf("media not asked for before is %+v while deferring", st)
	}
	if shown.Frame == nil {
		t.Fatal("media loaded shows nothing while deferring")
	}
	if m.entries["two"] != nil || source.calls.Load() != calls {
		t.Fatal("a load started while deferring")
	}
	if n := m.Deferred(); n != 1 {
		t.Fatalf("%d media counted as held back, want 1", n)
	}
	m.Status(other, false)
	if m.entries["two"] == nil {
		t.Fatal("no load started once not deferring")
	}
}

// Deferring holds back the load, not the blurred preview the message
// carries: it costs no download.
func TestDeferringShowsThePreview(t *testing.T) {
	m, source, _ := loadedManager(t)
	calls := source.calls.Load()
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 40, 30)), nil); err != nil {
		t.Fatal(err)
	}
	other := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "two", MIMEType: "image/png", Preview: jpg.Bytes()}}
	m.SetDeferring(true)
	m.BeginFrame()
	st := m.StatusFit(other, true, image.Pt(64, 64), false)
	m.EndFrame()
	m.SetDeferring(false)
	if !st.Loading || st.Preview == nil {
		t.Fatalf("media held back is %+v, want loading with its preview", st)
	}
	if m.entries["two"] != nil || source.calls.Load() != calls {
		t.Fatal("a load started while deferring")
	}
	// Once the load starts, the preview does not go: the next frame shows it
	// before the load brings its own.
	if st := m.StatusFit(other, true, image.Pt(64, 64), false); st.Preview == nil {
		t.Fatal("the preview blinked off as the load began")
	}
}
