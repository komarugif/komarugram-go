// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// openStickerSet shows pack in the harness's chat, fully faded in.
func openStickerSet(h *composerHarness, pack model.StickerSet) {
	h.p.stickers.chat = h.chat
	h.p.stickers.pack = &pack
	h.p.stickers.cells = make([]surface, len(pack.Items))
	h.p.stickers.modal.Open()
	for range 30 {
		h.frame()
	}
}

// A sticker of a set that is not added is sent from its dialog, as in
// Telegram Desktop.
func TestStickerSetSendsSticker(t *testing.T) {
	h := newComposerHarness(t)
	openStickerSet(h, model.StickerSet{Title: "Cats", Count: 2, Items: []model.PickerItem{
		{ID: "sticker/1", Emoji: "🐈"}, {ID: "sticker/2", Emoji: "🐕"},
	}})
	h.click(175, 350)
	// The demo store sends at once; a sent sticker becomes a recent one.
	if recent := h.p.composer.recent[model.PickerStickers]; len(recent) == 0 || recent[0].ID != "sticker/1" {
		t.Fatalf("sticker not sent: %+v", recent)
	}
	if !h.p.stickers.modal.closing {
		t.Fatal("dialog stayed open after sending")
	}
}

// An emoji of a set goes into the draft, and its grid is denser than a
// sticker set's.
func TestStickerSetInsertsEmoji(t *testing.T) {
	h := newComposerHarness(t)
	openStickerSet(h, model.StickerSet{Title: "Cats", Count: 2, Emoji: true, Items: []model.PickerItem{
		{ID: "emoji/1", Emoji: "🐈", DocumentID: 1, Custom: true}, {ID: "emoji/2", Emoji: "🐕", DocumentID: 2, Custom: true},
	}})
	h.click(175, 365)
	d := h.p.composer.draft(h.chat)
	if d.pending != nil || d.editor.Text() != "🐈" || len(d.entities) != 1 || d.entities[0].DocumentID != 1 {
		t.Fatalf("emoji not inserted: %q %+v %+v", d.editor.Text(), d.entities, d.pending)
	}
}

func TestStickerSetMoreMenuOpensAndEscapeClosesIt(t *testing.T) {
	h := newComposerHarness(t)
	openStickerSet(h, model.StickerSet{Title: "Cats", Count: 1, Items: []model.PickerItem{{ID: "sticker/1", Emoji: "🐈"}}})
	h.click(495, 290)
	if !h.p.stickers.menuOpen {
		t.Fatal("the three-dot button did not open the set menu")
	}
	h.click(200, 400)
	if h.p.stickers.menuOpen || h.p.stickers.modal.closing {
		t.Fatal("click outside the menu should dismiss only the menu")
	}
	h.click(495, 290)
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	if h.p.stickers.menuOpen || !h.p.stickers.modal.Shown() {
		t.Fatal("Escape should close the menu and keep the set dialog open")
	}
}

func TestStickerSetMenuDownloadsArchive(t *testing.T) {
	t.Chdir("../../..")
	h := newComposerHarness(t)
	openStickerSet(h, model.StickerSet{Title: "Cats", Count: 1, Items: []model.PickerItem{{
		ID: "demo/tgs", Emoji: "🐈", Media: model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker"}},
	}}})
	path := filepath.Join(t.TempDir(), "Cats.zip")
	h.p.stickers.chooseArchive = func(context.Context, string) (string, error) { return path, nil }
	h.click(495, 290)
	for range 30 {
		h.frame()
	}
	h.click(400, 345)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.frame()
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("menu did not download the set: open=%t busy=%t toast=%q", h.p.stickers.menuOpen, h.p.stickers.busy, h.p.stickers.modal.toast.Text())
}

// A sticker of a set held shows large, the one under the pointer as it
// moves; released, it closes and sends nothing, the dialog staying open.
func TestStickerSetHoldPreview(t *testing.T) {
	h := newComposerHarness(t)
	openStickerSet(h, model.StickerSet{Title: "Cats", Count: 2, Items: []model.PickerItem{
		{ID: "sticker/1", Emoji: "🐈"}, {ID: "sticker/2", Emoji: "🐕"},
	}})
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(175, 350)})
	h.frame()
	h.now = h.now.Add(holdPreviewDelay + 50*time.Millisecond)
	h.frame()
	d := &h.p.stickers
	if !d.preview.Shown() || d.preview.item.ID != "sticker/1" {
		t.Fatalf("held: shown %v, %q", d.preview.Shown(), d.preview.item.ID)
	}
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(245, 350)})
	h.frame()
	if !d.preview.Shown() || d.preview.item.ID != "sticker/2" {
		t.Fatalf("moved: shown %v, %q", d.preview.Shown(), d.preview.item.ID)
	}
	// Back over the one pressed, whose click the release would be.
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(175, 350)})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(175, 350)})
	h.frame()
	h.frame()
	if d.preview.Shown() || len(h.p.composer.recent[model.PickerStickers]) != 0 || d.modal.closing {
		t.Fatalf("released: shown %v, sent %+v, closing %v", d.preview.Shown(), h.p.composer.recent[model.PickerStickers], d.modal.closing)
	}
}
