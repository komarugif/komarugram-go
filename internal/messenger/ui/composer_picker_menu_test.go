// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"slices"
	"strings"
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
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// pickerMenuHarness draws the picker of a chat of the demo, open on a tab
// with the page the demo gives it.
type pickerMenuHarness struct {
	t      *testing.T
	store  *mockstore.Store
	p      *chatPage
	router input.Router
}

func newPickerMenuHarness(t *testing.T, tab model.PickerTab) *pickerMenuHarness {
	store := mockstore.New(time.Now(), 0)
	p := newChatPage(store, func() {})
	t.Cleanup(p.Close)
	p.images = &imageOps{}
	h := &pickerMenuHarness{t: t, store: store, p: p}
	h.show(tab, "")
	return h
}

// show opens the picker on tab, the page for query in it.
func (h *pickerMenuHarness) show(tab model.PickerTab, query string) {
	c := h.p.composer
	page, err := h.store.Picker(context.Background(), model.PickerRequest{Tab: tab, Query: query})
	if err != nil {
		h.t.Fatal(err)
	}
	c.pickerOpen, c.tab, c.query, c.page, c.pageLoaded = true, tab, query, page, true
	c.itemClicks = map[string]*surface{}
	h.frames(2)
}

func (h *pickerMenuHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(372, 488)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	wdk.SetAnimationsEnabled(gtx, false)
	l := localization.For("ru")
	h.p.images.BeginFrame()
	h.p.media.BeginFrame()
	h.p.composer.pickerLayout(gtx, l, h.p, false, image.Point{})
	h.p.media.EndFrame()
	h.p.images.EndFrame()
	h.router.Frame(gtx.Ops)
}

func (h *pickerMenuHarness) frames(n int) {
	for range n {
		h.frame()
	}
}

// click presses and releases button at at, in the picker.
func (h *pickerMenuHarness) click(at image.Point, button pointer.Buttons) {
	pos := f32.Pt(float32(at.X), float32(at.Y))
	h.router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: button, Position: pos},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos},
	)
	h.frames(3)
}

// tile is the middle of the tile of item id in the picker, in its row
// recent or not.
func (h *pickerMenuHarness) tile(id string, recent bool) image.Point {
	h.t.Helper()
	c := h.p.composer
	cell := 68
	if c.tab == model.PickerGIF {
		cell = 100
	}
	rows := c.pickerRows(372-20, cell, localization.For("ru"))
	for _, t := range c.preview.tiles {
		if t.item.ID == id && recent == (c.tab == model.PickerStickers && rows[t.row].pack == 0) {
			// The area of the tiles starts under the tabs and the search.
			return t.rect.Min.Add(t.rect.Size().Div(2)).Add(image.Pt(10, 44+48))
		}
	}
	h.t.Fatalf("no tile of %s", id)
	return image.Point{}
}

// choose clicks the menu's action a.
func (h *pickerMenuHarness) choose(a pickerAction) {
	h.t.Helper()
	m := &h.p.composer.itemMenu
	i := slices.Index(m.shown, a)
	if !m.open || i < 0 {
		h.t.Fatalf("menu open %v with %v, not %v", m.open, m.shown, a)
	}
	b := m.menu.bounds
	h.click(image.Pt(b.Min.X+40, b.Min.Y+menuPadding+i*menuItemHeight+menuItemHeight/2), pointer.ButtonPrimary)
}

// until draws frames until done, for at most 5 s.
func (h *pickerMenuHarness) until(what string, done func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); {
		if time.Now().After(deadline) {
			h.t.Fatal(what)
		}
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
}

func pickerIDs(items []model.PickerItem) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
}

// A right click on a saved GIF offers to delete it, and the delete takes it
// off the list and off the saved ones; one a search found is added to them,
// first.
func TestPickerMenuSavesAndDeletesGIFs(t *testing.T) {
	h := newPickerMenuHarness(t, model.PickerGIF)
	c := h.p.composer
	first := c.page.Items[0].ID
	h.click(h.tile(first, false), pointer.ButtonSecondary)
	if !c.itemMenu.open || !slices.Equal(c.itemMenu.shown, []pickerAction{pickerDeleteGIF}) {
		t.Fatalf("a saved GIF's menu: open %v, %v", c.itemMenu.open, c.itemMenu.shown)
	}
	// Its item is the message menu's Delete.
	if got := pickerDeleteGIF.label(localization.For("en")); got != "Delete" {
		t.Fatalf("delete reads %q", got)
	}
	n := len(c.page.Items)
	h.choose(pickerDeleteGIF)
	if c.itemMenu.open || len(c.page.Items) != n-1 || slices.Contains(pickerIDs(c.page.Items), first) {
		t.Fatalf("after the delete: open %v, %v", c.itemMenu.open, pickerIDs(c.page.Items))
	}
	h.until("the store keeps the GIF deleted", func() bool { return !h.store.SavedGIF(first) })

	// A click on a GIF with the menu open closes the menu and sends nothing.
	h.click(h.tile(c.page.Items[0].ID, false), pointer.ButtonSecondary)
	h.click(image.Pt(5, 5), pointer.ButtonPrimary)
	if c.itemMenu.open {
		t.Fatal("a click beside the menu leaves it open")
	}

	h.show(model.PickerGIF, "кот")
	found := c.page.Items[0].ID
	h.click(h.tile(found, false), pointer.ButtonSecondary)
	if !slices.Equal(c.itemMenu.shown, []pickerAction{pickerSaveGIF}) {
		t.Fatalf("a GIF found: %v", c.itemMenu.shown)
	}
	h.choose(pickerSaveGIF)
	h.until("the GIF found is saved", func() bool { return h.store.SavedGIF(found) })
	saved, _ := h.store.Picker(context.Background(), model.PickerRequest{Tab: model.PickerGIF})
	if saved.Items[0].ID != found {
		t.Fatalf("saved GIFs %v, the one added not first", pickerIDs(saved.Items))
	}
	// Saved, it is deleted from the same menu.
	h.click(h.tile(found, false), pointer.ButtonSecondary)
	if !slices.Equal(c.itemMenu.shown, []pickerAction{pickerDeleteGIF}) {
		t.Fatalf("a GIF found and saved: %v", c.itemMenu.shown)
	}
}

// A right click on a recent sticker offers to take it off the recent;
// one of a pack offers nothing.
func TestPickerMenuRemovesRecentStickers(t *testing.T) {
	h := newPickerMenuHarness(t, model.PickerStickers)
	c := h.p.composer
	recent := c.page.Recent[1].ID
	// The sticker of a pack: no menu, as it has none of its actions.
	h.click(h.tile(c.page.Packs[0].Items[1].ID, false), pointer.ButtonSecondary)
	if c.itemMenu.open {
		t.Fatalf("a pack's sticker opened %v", c.itemMenu.shown)
	}
	h.click(h.tile(recent, true), pointer.ButtonSecondary)
	if !slices.Equal(c.itemMenu.shown, []pickerAction{pickerRemoveRecent}) {
		t.Fatalf("a recent sticker's menu: open %v, %v", c.itemMenu.open, c.itemMenu.shown)
	}
	h.choose(pickerRemoveRecent)
	if slices.Contains(pickerIDs(c.page.Recent), recent) {
		t.Fatalf("still recent: %v", pickerIDs(c.page.Recent))
	}
	h.until("the store keeps the sticker off the recent", func() bool {
		page, _ := h.store.Picker(context.Background(), model.PickerRequest{Tab: model.PickerStickers})
		return len(page.Recent) > 0 && !slices.Contains(pickerIDs(page.Recent), recent)
	})
}

// The menu of a GIF message adds it to the saved GIFs, first, as Telegram
// Desktop's does.
func TestMessageMenuSavesGIF(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	p := newChatPage(store, func() {})
	t.Cleanup(p.Close)
	gif := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 5}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "demo/gifv", MIMEType: "video/mp4"}}
	if !slices.Contains(p.menuActions(gif), actionSaveGIF) {
		t.Fatalf("a GIF's menu: %v", p.menuActions(gif))
	}
	text := gif
	text.Kind, text.Media, text.Text = model.MessageText, nil, "text"
	if slices.Contains(p.menuActions(text), actionSaveGIF) {
		t.Fatal("a text offers to be saved as a GIF")
	}
	protected := gif
	protected.NoForwards = true
	if slices.Contains(p.menuActions(protected), actionSaveGIF) {
		t.Fatal("a GIF of a chat that keeps its media offers to be saved")
	}
	p.menuDo(layout.Context{Ops: new(op.Ops)}, actionSaveGIF, gif, localization.For("ru"))
	deadline := time.Now().Add(5 * time.Second)
	for !store.SavedGIF("demo/gifv") {
		if time.Now().After(deadline) {
			t.Fatal("the GIF is not saved")
		}
		time.Sleep(5 * time.Millisecond)
	}
	page, _ := store.Picker(context.Background(), model.PickerRequest{Tab: model.PickerGIF})
	if page.Items[0].ID != "demo/gifv" || page.Items[0].Media.Kind != model.MessageGIF {
		t.Fatalf("saved GIFs %v", pickerIDs(page.Items))
	}
}

// TestRenderPickerMenu saves the menu of a recent sticker and of a saved
// GIF over the picker, beside MENU_PNG as -picker.png and -picker-gif.png.
func TestRenderPickerMenu(t *testing.T) {
	path := os.Getenv("MENU_PNG")
	if path == "" {
		t.Skip("set MENU_PNG to a file")
	}
	for _, tab := range []model.PickerTab{model.PickerStickers, model.PickerGIF} {
		h := newPickerMenuHarness(t, tab)
		c := h.p.composer
		file := strings.TrimSuffix(path, ".png") + "-picker.png"
		if tab == model.PickerGIF {
			file = strings.TrimSuffix(path, ".png") + "-picker-gif.png"
			h.click(h.tile(c.page.Items[1].ID, false), pointer.ButtonSecondary)
		} else {
			h.click(h.tile(c.page.Recent[1].ID, true), pointer.ButtonSecondary)
		}
		// Under the row of the item, which the render's scale moves.
		c.itemMenu.at = image.Pt(70, 250)
		l := localization.For("ru")
		renderFrames(t, image.Pt(372, 488), file, func(gtx layout.Context) {
			h.p.images.BeginFrame()
			h.p.media.BeginFrame()
			c.pickerLayout(gtx, l, h.p, false, image.Point{})
			h.p.media.EndFrame()
			h.p.images.EndFrame()
		})
	}
}
