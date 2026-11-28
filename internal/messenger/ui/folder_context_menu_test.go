// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

type busyUnreadFolder struct{ model.FolderActions }

func (s busyUnreadFolder) UnreadInFolder(id int64) []model.FolderReadChat {
	chats := s.FolderActions.UnreadInFolder(id)
	if len(chats) > 0 {
		chats[0].Unread = 1001
	}
	return chats
}

type folderMenuFixture struct {
	app     *App
	gtx     *layout.Context
	frame   func()
	capture func(string)
	click   func(string, pointer.Buttons)
}

func newFolderMenuFixture(t *testing.T, variant string, size image.Point) *folderMenuFixture {
	w := newSettingsWindow(t, "ru", preferences.ThemeLight, size)
	t.Cleanup(w.close)
	a := w.a
	a.section = section{kind: sectionFolder, folder: 2}
	a.listRequest = 520
	now := time.Unix(1000, 0)
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Source: w.router.Source(), Now: now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	theme := testTheme(gtx, schemes.SchemeBaselineLight())
	frame := func() {
		ops.Reset()
		gtx.Now = gtx.Now.Add(time.Second / 60)
		wdk.InitMaterialThemeInContext(gtx, theme)
		wdk.SetAnimationsEnabled(gtx, false)
		a.Update(gtx)
		a.Layout(gtx)
		w.router.Frame(&ops)
	}
	capture := func(name string) {
		dir := os.Getenv("FOLDER_MENU_PNG_DIR")
		if dir == "" {
			return
		}
		img, err := renderSnapshot(&ops, size)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name+"-"+variant+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	var bounds func([]input.SemanticNode, string) image.Rectangle
	bounds = func(nodes []input.SemanticNode, label string) image.Rectangle {
		for _, n := range slices.Backward(nodes) {
			if n.Desc.Label == label {
				return n.Desc.Bounds
			}
			if r := bounds(n.Children, label); !r.Empty() {
				return r
			}
		}
		return image.Rectangle{}
	}
	click := func(label string, button pointer.Buttons) {
		t.Helper()
		r := bounds(w.router.AppendSemantics(nil), label)
		if r.Empty() {
			t.Fatalf("missing %q", label)
		}
		// Labels may fill the remaining width. Aim just inside their leading edge.
		at := layout.FPt(image.Pt(r.Min.X+min(5, r.Dx()/2), r.Min.Y+min(8, r.Dy()/2)))
		for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
			buttons := button
			if kind == pointer.Release {
				buttons = 0
			}
			w.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: at})
			frame()
		}
	}

	return &folderMenuFixture{app: a, gtx: &gtx, frame: frame, capture: capture, click: click}
}

// Drive both real navigation layouts. A secondary click must never select the
// folder; the editor, read and deletion must still act on that folder.
func TestFolderContextMenu(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    image.Point
		compact bool
	}{
		{"sidebar", image.Pt(1150, 850), false}, {"tabs", image.Pt(900, 600), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFolderMenuFixture(t, tc.name, tc.size)
			a, frame, click, capture := f.app, f.frame, f.click, f.capture
			frame()
			frame()
			if a.compact != tc.compact {
				t.Fatal("wrong navigation layout")
			}
			selected := a.section
			open := func() {
				t.Helper()
				click("Личные", pointer.ButtonSecondary)
				if !a.folderMenu.open || a.folderMenu.folder.ID != 1 || a.section != selected {
					t.Fatalf("right click: open=%v target=%v selection=%v", a.folderMenu.open, a.folderMenu.folder.ID, a.section)
				}
				frame()
			}
			open()
			capture("folder-menu")
			click("Настроить", pointer.ButtonPrimary)
			if a.settings.section != settingsFolders || !a.settings.folders.draft || a.settings.folders.base.ID != 1 {
				t.Fatal("wrong editor")
			}
			a.openSection(*f.gtx, selected)
			frame()
			if !tc.compact {
				store := a.folderMenu.store
				a.folderMenu.store = busyUnreadFolder{store}
				open()
				click("Прочитать", pointer.ButtonPrimary)
				if !a.folderMenu.confirm.Shown() || a.folderMenu.confirmAction != folderRead || a.folderMenu.pending != nil {
					t.Fatal("large read did not wait for confirmation")
				}
				a.folderMenu.cancel.click.Click()
				frame()
				frame()
				if len(store.UnreadInFolder(1)) == 0 {
					t.Fatal("cancelling marked the folder read")
				}
				a.folderMenu.store = store
			}
			open()
			click("Прочитать", pointer.ButtonPrimary)
			waitFor(t, "folder read", frame, func() bool { return a.folderMenu.pending == nil })
			if a.section != selected || len(a.folderMenu.store.UnreadInFolder(1)) != 0 || len(a.folderMenu.store.UnreadInFolder(2)) == 0 {
				t.Fatal("read reached wrong folder")
			}
			before := a.store.Chats()
			open()
			click("Удалить", pointer.ButtonPrimary)
			frame()
			if !a.folderMenu.confirm.Shown() {
				t.Fatal("no removal confirmation")
			}
			capture("folder-delete")
			a.folderMenu.cancel.click.Click()
			frame()
			frame()
			if !slices.Contains(model.FolderIDs(a.store.Folders()), int64(1)) {
				t.Fatal("cancel deleted folder")
			}
			if tc.compact {
				selected = section{kind: sectionFolder, folder: 1}
				a.openSection(*f.gtx, selected)
				frame()
			}
			open()
			click("Удалить", pointer.ButtonPrimary)
			a.folderMenu.accept.click.Click()
			frame()
			waitFor(t, "folder removed", frame, func() bool { return a.folderMenu.pending == nil })
			frame()
			if slices.Contains(model.FolderIDs(a.store.Folders()), int64(1)) || !slices.EqualFunc(before, a.store.Chats(), func(x, y model.Chat) bool { return x.ID == y.ID }) {
				t.Fatal("deletion changed chats or kept folder")
			}
			if tc.compact && a.section.kind != sectionAll || !tc.compact && a.section != selected {
				t.Fatalf("selection after delete: %v", a.section)
			}
		})
	}
}
