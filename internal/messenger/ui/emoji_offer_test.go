// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/localization"
)

// offerCatalog is a catalog of a sprite pack under the offered pack's ID.
func offerCatalog(t *testing.T) string {
	t.Helper()
	catalog := t.TempDir()
	picture := filepath.Join(t.TempDir(), "emoji_1.png")
	f, err := os.Create(picture)
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 8, 8)))
	f.Close()
	if _, err := emojipacks.BuildSprites(catalog, emojipacks.Pack{ID: offeredEmojiPack, Name: "Apple", License: "Apple"}, emojipacks.Sprites{Cell: 8, Columns: 1, Rows: 1}, []string{picture}, [][]string{{"\U0001F600"}}); err != nil {
		t.Fatal(err)
	}
	return catalog
}

// offerFrame runs a frame of a window with the settings closed: the offer
// and the downloads it started.
func offerFrame(h *emojiSettingsHarness, o *emojiOffer, toasts *[]string) {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 900)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	l := localization.For("ru")
	h.s.drain()
	o.Update(gtx, h.s, func(s string) { *toasts = append(*toasts, s) }, l, func() {})
	o.Layout(gtx, h.s, l)
}

func offerUntil(t *testing.T, h *emojiSettingsHarness, o *emojiOffer, toasts *[]string, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s", what)
		}
		offerFrame(h, o, toasts)
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEmojiOfferDownloadsAndChoosesThePack checks that where the system
// has no emoji the offer comes, and that its download goes on and the
// pack is chosen with the settings closed.
func TestEmojiOfferDownloadsAndChoosesThePack(t *testing.T) {
	h := newEmojiSettingsHarness(t, offerCatalog(t))
	o := newEmojiOffer()
	o.systemHasEmoji = func() bool { return false }
	var toasts []string
	offerUntil(t, h, o, &toasts, "the offer", func() bool { return o.modal.Shown() })
	o.answer(h.s, true, func(s string) { toasts = append(toasts, s) }, localization.For("ru"))
	if !h.files.EmojiOffered {
		t.Error("the answer is not kept")
	}
	offerUntil(t, h, o, &toasts, "the pack chosen", func() bool { return h.files.EmojiPack == offeredEmojiPack })
	if !h.installed(offeredEmojiPack) {
		t.Error("the pack chosen is not installed")
	}
	if len(toasts) != 1 {
		t.Errorf("toasts %q, want the one of the download", toasts)
	}
}

// TestEmojiOfferNotWhereTheSystemHasEmoji checks that a system with an
// emoji font is offered nothing, and the offer is not taken as answered.
func TestEmojiOfferNotWhereTheSystemHasEmoji(t *testing.T) {
	h := newEmojiSettingsHarness(t, offerCatalog(t))
	o := newEmojiOffer()
	o.systemHasEmoji = func() bool { return true }
	var toasts []string
	offerUntil(t, h, o, &toasts, "the check", func() bool { return o.state == emojiOfferDone })
	if o.modal.Shown() || h.files.EmojiOffered || h.files.EmojiPack != "" {
		t.Errorf("shown %v, answered %v, pack %q", o.modal.Shown(), h.files.EmojiOffered, h.files.EmojiPack)
	}
}

// TestEmojiOfferNotAgain checks that "not now" is kept: the next window,
// or start, offers nothing, and nothing was downloaded.
func TestEmojiOfferNotAgain(t *testing.T) {
	h := newEmojiSettingsHarness(t, offerCatalog(t))
	o := newEmojiOffer()
	o.systemHasEmoji = func() bool { return false }
	var toasts []string
	offerUntil(t, h, o, &toasts, "the offer", func() bool { return o.modal.Shown() })
	o.answer(h.s, false, nil, localization.For("ru"))
	again := newEmojiOffer()
	again.systemHasEmoji = func() bool { return false }
	for range 5 {
		offerFrame(h, again, &toasts)
	}
	if again.modal.Shown() || h.installed(offeredEmojiPack) || len(toasts) != 0 {
		t.Errorf("after not now: shown again %v, installed %v, toasts %q", again.modal.Shown(), h.installed(offeredEmojiPack), toasts)
	}
}

// TestEmojiOfferChoosesAnInstalledPack checks that a pack installed
// before, but not chosen, is chosen without downloading it again.
func TestEmojiOfferChoosesAnInstalledPack(t *testing.T) {
	h := newEmojiSettingsHarness(t, offerCatalog(t))
	o := newEmojiOffer()
	o.systemHasEmoji = func() bool { return false }
	var toasts []string
	offerUntil(t, h, o, &toasts, "the offer", func() bool { return o.modal.Shown() })
	if err := h.s.store.Install(context.Background(), h.s.source, o.pack, nil); err != nil {
		t.Fatal(err)
	}
	o.answer(h.s, true, func(s string) { toasts = append(toasts, s) }, localization.For("ru"))
	if h.files.EmojiPack != offeredEmojiPack || h.s.row(offeredEmojiPack).stop != nil || len(toasts) != 0 {
		t.Errorf("chosen %q, downloading %v, toasts %q", h.files.EmojiPack, h.s.row(offeredEmojiPack).stop != nil, toasts)
	}
}
