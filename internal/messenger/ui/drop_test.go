// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/unit"

	"komarugram/internal/messenger/sendfiles"
)

// Files dropped on the chat go to the box for sending files: as documents
// on the area of documents, as photos on the other; a folder goes nowhere.
func TestDroppedFilesGoToTheBox(t *testing.T) {
	for _, c := range []struct {
		name      string
		at        f32.Point
		documents bool
	}{
		{"on the area of photos", f32.Pt(340, 600), false},
		{"on the area of documents", f32.Pt(340, 100), true},
	} {
		h := newComposerHarness(t)
		h.p.composer.pickerOpen = false
		h.frame()
		d := &fileDrop{page: h.p, area: image.Rectangle{Max: h.size}, metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
		paths := boxPaths(t, "photo", "photo")
		d.take(app.DropEvent{Kind: app.DropEnter, Position: c.at})
		if !d.active || d.state != sendfiles.DropNone {
			t.Fatalf("%s: before the files are known: active %v, state %d", c.name, d.active, d.state)
		}
		d.take(app.DropEvent{Kind: app.DropMove, Position: c.at, Paths: paths})
		if d.state != sendfiles.DropPhotos {
			t.Fatalf("%s: state %d", c.name, d.state)
		}
		d.take(app.DropEvent{Kind: app.Drop, Position: c.at, Paths: paths})
		box := &h.p.composer.files
		if d.active || !box.Shown() || len(box.files) != 2 || box.way.Documents != c.documents {
			t.Fatalf("%s: drag active %v, box shown %v with %d files, as documents %v", c.name, d.active, box.Shown(), len(box.files), box.way.Documents)
		}
		waitFor(t, "files to be read", func() { h.frame() }, box.ready)
		if len(box.mode.tabs) != 2 || box.isMediaTile(box.files[0]) == c.documents {
			t.Fatalf("%s: the preview did not open on the tab for the drop area", c.name)
		}
	}

	h := newComposerHarness(t)
	h.frame()
	d := &fileDrop{page: h.p, area: image.Rectangle{Max: h.size}, metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	folder := []string{t.TempDir()}
	d.take(app.DropEvent{Kind: app.DropEnter, Position: f32.Pt(100, 100), Paths: folder})
	d.take(app.DropEvent{Kind: app.Drop, Position: f32.Pt(100, 100), Paths: folder})
	if h.p.composer.files.Shown() {
		t.Fatal("a folder opened the box")
	}
	d.take(app.DropEvent{Kind: app.DropEnter, Position: f32.Pt(100, 100), Paths: boxPaths(t, "file")})
	d.take(app.DropEvent{Kind: app.DropLeave})
	if d.active || d.paths != nil {
		t.Fatal("the drag stays after it left")
	}
	// A chat that takes no files takes no drop.
	d.page = nil
	d.take(app.DropEvent{Kind: app.Drop, Position: f32.Pt(100, 100), Paths: boxPaths(t, "file")})
	if h.p.composer.files.Shown() {
		t.Fatal("a drop without a chat opened the box")
	}
}
