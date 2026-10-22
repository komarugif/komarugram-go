// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/io/pointer"
)

// The menu opened on a part of an album asks whether to save that part or
// the album; opened beside the parts, it saves the album. A right click on
// an album while a menu is open only closes it, as it could not tell which.
func TestAlbumSaveMenu(t *testing.T) {
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		for _, i := range []int{0, 1} {
			messages[i].Text = ""
			messages[i].Kind, messages[i].GroupedID = model.MessagePhoto, 7
			messages[i].Media = &model.MessageMedia{ID: "p", Width: 400, Height: 300, Size: 10}
		}
	}, func(s *menuStore) model.ConversationStore { return noMedia{s} })
	l := localization.For("en")
	at := h.messageAt(1)
	h.openMenu(1)
	part, ok := h.page.menuPart(h.page.messages[0])
	if !ok || h.page.menuLabel(actionSave, l) != l.T("menu.save") {
		t.Fatalf("the menu of a part is not of it: %v, %q", ok, h.page.menuLabel(actionSave, l))
	}
	// A right click on the album, the menu open, only closes it.
	beside := at
	for beside.X = 480; image.Pt(int(beside.X), int(beside.Y)).In(h.page.messageMenu.rect); beside.X -= 40 {
	}
	h.press(pointer.ButtonSecondary, beside)
	h.frames(5)
	if h.page.messageMenu.open {
		t.Fatal("a menu opened on an album over another")
	}
	h.openMenu(1)
	h.choose(actionSave)
	h.frames(5)
	if !h.page.albumSave.modal.Shown() || h.page.albumSave.part.Key != part.Key || len(h.page.albumSave.album.Attachments) != 2 {
		t.Fatal("the dialog does not ask what to save")
	}
	h.page.albumSave = albumSaveDialog{}
	// Beside the album, at the right edge of its row.
	h.frame()
	h.press(pointer.ButtonSecondary, f32.Pt(495, at.Y))
	h.frames(30)
	if !h.page.messageMenu.open || h.page.messageMenu.id != 1 {
		t.Fatal("no menu beside the album")
	}
	if _, ok := h.page.menuPart(h.page.messages[0]); ok || h.page.menuLabel(actionSave, l) != l.T("menu.save_album") {
		t.Fatalf("the menu beside the album is of a part: %q", h.page.menuLabel(actionSave, l))
	}
}

// noMedia is the menu's store, whose media do not load.
type noMedia struct{ *menuStore }

func (noMedia) Media(context.Context, model.Message) ([]byte, error) {
	return nil, errors.New("no media")
}

// The whole album is saved at once: its photo into the pictures, its video
// into the downloads, and the notice tells how many and where.
func TestSaveAlbum(t *testing.T) {
	t.Chdir("../../..")
	p := newChatPage(mockstore.New(time.Now(), 0), func() {})
	date := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	album := []model.Message{
		{Key: model.MessageKey{ChatID: 1, MessageID: 30}, Kind: model.MessagePhoto, Date: date, Media: &model.MessageMedia{ID: "demo/photo", MIMEType: "image/jpeg", Size: 1}},
		{Key: model.MessageKey{ChatID: 1, MessageID: 31}, Kind: model.MessageVideo, Date: date, Media: &model.MessageMedia{ID: "demo/video", MIMEType: "video/mp4", Size: 7789765}},
	}
	if !p.canSave(model.Message{Media: album[0].Media, Attachments: album}) {
		t.Fatal("the album cannot be saved")
	}
	p.saveAlbum(album, localization.For("en"))
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		p.errorMu.Lock()
		notice, err := p.notice, p.mediaError
		p.errorMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if notice != "" {
			if !strings.Contains(notice, "2 files") || !strings.Contains(notice, picturesDir()) || !strings.Contains(notice, appDownloadsDir()) {
				t.Fatal(notice)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not saved")
		}
	}
	if _, err := os.Stat(filepath.Join(appDownloadsDir(), "video_2026-10-22_12-00-00.mp4")); err != nil {
		t.Fatal(err)
	}
}
