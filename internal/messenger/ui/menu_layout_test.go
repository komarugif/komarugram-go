// SPDX-License-Identifier: Unlicense OR MIT
package ui

import (
	"image"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"
	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

func TestMenuSettingsDragAndHidePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	prefs, err := preferences.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	catalog := menuCatalog{"example", "Example", []menuChoice{{"a", "First", iconReply}, {"b", "Second", iconCopy}, {"c", "Third", iconDelete}}}
	view := newMenuSettings(func() preferences.Menus { return prefs.Global().Menus }, prefs.SetMenus, func(localization.Catalog) []menuCatalog { return []menuCatalog{catalog} }, nil)
	var router input.Router
	now := time.Unix(1000, 0)
	frame := func() {
		gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: now, Constraints: layout.Exact(image.Pt(500, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		view.Update(gtx, localization.For("en"))
		view.layoutCard(gtx, catalog, localization.For("en"))
		router.Frame(gtx.Ops)
		now = now.Add(16 * time.Millisecond)
	}
	frame()
	c := view.state(catalog.id)
	send := func(kind pointer.Kind, x, y int) {
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(float32(x), float32(y))})
		frame()
	}
	send(pointer.Press, 20, c.startY+24)
	send(pointer.Move, 20, c.startY+2*menuSettingsRowHeight+24)
	send(pointer.Release, 20, c.startY+2*menuSettingsRowHeight+24)
	for range 20 {
		frame()
	}
	if got := prefs.Global().Menus[catalog.id].Order; !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("drag: %v", got)
	}
	send(pointer.Press, 476, c.startY+24)
	send(pointer.Release, 476, c.startY+24)
	frame()
	saved, err := preferences.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Global().Menus[catalog.id].Arrange(catalogIDs(catalog), false); !reflect.DeepEqual(got, []string{"c", "a"}) {
		t.Fatalf("hidden: %v", got)
	}
	send(pointer.Press, 476, c.startY+24)
	send(pointer.Release, 476, c.startY+24)
	frame()
	if got := prefs.Global().Menus[catalog.id].Arrange(catalogIDs(catalog), false); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("unhidden: %v", got)
	}
	// Cancel restores the previous order without saving a partial drag.
	send(pointer.Press, 20, c.startY+24)
	send(pointer.Move, 20, c.startY+120)
	send(pointer.Cancel, 20, c.startY+120)
	if got := prefs.Global().Menus[catalog.id].Order; !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("cancel: %v", got)
	}
}

func TestMenuChoicesRespectContext(t *testing.T) {
	h := newMenuHarness(t, nil)
	choices := preferences.Menus{menuMessage: {Order: []string{"delete", "copy_link", "reply"}, Hidden: []string{"copy", "forward"}}, menuChatRow: {Order: []string{"read", "pin"}}, menuChatHeader: {Order: []string{"leave", "info"}, Hidden: []string{"search"}}, menuAttachment: {Order: []string{"tasks", "photo"}, Hidden: []string{"file"}}, "entity_date": {Order: []string{"calendar", "copy"}, Hidden: []string{"copy"}}}
	h.page.menus = func() preferences.Menus { return choices }
	h.store.noForwards = true
	actions := h.page.menuActions(h.page.messages[0])
	if slices.Contains(actions, actionForward) || slices.Contains(actions, actionCopyText) {
		t.Fatal(actions)
	}
	if got := h.page.chatMenuActions(); len(got) == 0 || got[0] != chatMenuInfo || slices.Contains(got, chatMenuLeave) || slices.Contains(got, chatMenuSearch) {
		t.Fatal(got)
	}
	h.page.composer.menus = h.page.menus
	if got := h.page.composer.attachmentOrder(); !reflect.DeepEqual(got, []int{2, 0}) {
		t.Fatal(got)
	}
	h.page.entityMenu.target = entityTarget{kind: "date", date: 1}
	if got := h.page.entityItems(localization.For("en")); len(got) != 1 || got[0].group != "calendar" {
		t.Fatal(got)
	}
	if navigationID(drawerKey{section: section{kind: sectionFolder, folder: 1}}, "first") == navigationID(drawerKey{section: section{kind: sectionFolder, folder: 1}}, "second") {
		t.Fatal("folder choices crossed accounts")
	}
}

func TestMenuRowMotionTracksAndSettles(t *testing.T) {
	gtx := heightContext(time.Unix(1000, 0))
	var row menuRowMotion
	if got := row.Position(gtx, 48, false); got != 48 {
		t.Fatal("initial position animated", got)
	}
	if got := row.Position(gtx, 0, false); got != 48 {
		t.Fatal("neighbor jumped", got)
	}
	gtx.Now = gtx.Now.Add(80 * time.Millisecond)
	mid := row.Position(gtx, 0, false)
	if mid <= 0 || mid >= 48 {
		t.Fatal("neighbor did not slide", mid)
	}
	if got := row.Position(gtx, 70, true); got != 70 {
		t.Fatal("grabbed row lagged behind pointer", got)
	}
	if got := row.Position(gtx, 96, false); got != 70 {
		t.Fatal("release jumped", got)
	}
	gtx.Now = gtx.Now.Add(time.Second)
	if got := row.Position(gtx, 96, false); got != 96 {
		t.Fatal("drop did not settle", got)
	}
	row.Position(gtx, 0, false)
	wdk.SetAnimationsEnabled(gtx, false)
	if got := row.Position(gtx, 0, false); got != 0 {
		t.Fatal("ignored reduced motion", got)
	}
	wdk.SetAnimationsEnabled(gtx, true)
	gtx.Constraints.Max.X += 20
	if got := row.Position(gtx, 48, false); got != 48 {
		t.Fatal("resize animated old geometry", got)
	}
}
