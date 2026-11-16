// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
)

// TestRenderStickerSet saves the dialog for visual review when requested.
func TestRenderStickerSet(t *testing.T) {
	dir := os.Getenv("STICKER_SET_PNG_DIR")
	if dir == "" {
		t.Skip("set STICKER_SET_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"ru", "en"} {
		p := newChatPage(mockstore.New(time.Now(), 0), func() {})
		p.images = &imageOps{}
		emoji := lang == "en"
		title := "Коты и собаки"
		if emoji {
			title = "Cats and dogs"
		}
		p.stickers.pack = &model.StickerSet{Title: title, AuthorID: 123456789, Count: 12, Emoji: emoji, Installed: emoji, Items: []model.PickerItem{
			{Emoji: "🐈"}, {Emoji: "🐕"}, {Emoji: "🐩"}, {Emoji: "🐱"},
			{Emoji: "🐶"}, {Emoji: "🦊"}, {Emoji: "🐻"}, {Emoji: "🐼"},
			{Emoji: "🐯"}, {Emoji: "🦁"}, {Emoji: "🐮"}, {Emoji: "🐷"},
		}}
		p.stickers.modal.Open()
		renderFrames(t, image.Pt(700, 620), filepath.Join(dir, lang+"-stickers.png"), func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.stickers.layout(gtx, p, localization.For(lang))
			p.media.EndFrame()
			p.images.EndFrame()
		})
		// The other state of the set, whose button names what the set
		// holds: the stickers added, and the emoji not added yet.
		p.stickers.pack.Installed = !p.stickers.pack.Installed
		state := "-stickers-add.png"
		if p.stickers.pack.Installed {
			state = "-stickers-added.png"
		}
		renderFrames(t, image.Pt(700, 620), filepath.Join(dir, lang+state), func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.stickers.layout(gtx, p, localization.For(lang))
			p.media.EndFrame()
			p.images.EndFrame()
		})
		p.stickers.pack.Installed = !p.stickers.pack.Installed
		// What an action tells, at the dialog's bottom.
		p.stickers.modal.Toast(localization.For(lang).Format("stickers.saved", map[string]string{"path": "/home/user/Cats.zip"}))
		renderFrames(t, image.Pt(700, 620), filepath.Join(dir, lang+"-stickers-toast.png"), func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.stickers.layout(gtx, p, localization.For(lang))
			p.media.EndFrame()
			p.images.EndFrame()
		})
		p.stickers.modal.toast.Hide()
		p.stickers.menuOpen = true
		renderFrames(t, image.Pt(700, 620), filepath.Join(dir, lang+"-stickers-menu.png"), func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.stickers.layout(gtx, p, localization.For(lang))
			p.media.EndFrame()
			p.images.EndFrame()
		})
		p.stickers.pack = nil
		p.stickers.menuOpen = false
		p.stickers.busy = true
		renderFrames(t, image.Pt(700, 620), filepath.Join(dir, lang+"-stickers-loading.png"), func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.stickers.layout(gtx, p, localization.For(lang))
			p.media.EndFrame()
			p.images.EndFrame()
		})
		p.Close()
	}
}
