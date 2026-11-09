// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
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
