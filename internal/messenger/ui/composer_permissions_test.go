package ui

import (
	"context"
	"gioui.org/layout"
	"gioui.org/op"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"sync"
	"testing"
)

type restrictedComposerStore struct {
	model.ComposerStore
	rights model.SendPermissions
	sends  int
	// muted is what SetChatMuted set.
	mu    sync.Mutex
	muted map[int64]bool
}

func (s *restrictedComposerStore) SendPermissions(int64) model.SendPermissions { return s.rights }

func (s *restrictedComposerStore) DirectMessages(ctx context.Context, chat int64) (model.Chat, error) {
	return model.Chat{ID: s.rights.DirectMessagesID, Title: "Direct", Kind: model.KindGroup}, ctx.Err()
}
func (s *restrictedComposerStore) ChatMuted(chat int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.muted[chat]
}
func (s *restrictedComposerStore) SetChatMuted(_ context.Context, chat int64, muted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.muted == nil {
		s.muted = map[int64]bool{}
	}
	s.muted[chat] = muted
	return nil
}
func restrictComposer(h *composerHarness, p model.SendPermissions) *restrictedComposerStore {
	s := &restrictedComposerStore{ComposerStore: h.p.composer.source, rights: p}
	h.p.composer.source = s
	return s
}
func TestComposerRevokedPermissions(t *testing.T) {
	h := newComposerHarness(t)
	c := h.p.composer
	s := restrictComposer(h, model.SendPermissions{})
	c.draft(h.chat).editor.SetText("keep draft")
	c.pickerOpen = true
	c.tab = model.PickerStickers
	c.attachOpen = true
	s.rights = model.SendPermissions{Unavailable: true, Broadcast: true}
	h.frame()
	if c.pickerOpen || c.attachOpen {
		t.Fatal("revoked controls remain open")
	}
	c.submit(h.chat, model.OutgoingMessage{Text: "forbidden"})
	if c.draft(h.chat).sending || c.draft(h.chat).err == nil {
		t.Fatal("revoked text sent")
	}
	if c.draft(h.chat).editor.Text() != "keep draft" {
		t.Fatal("draft lost")
	}
}
func TestComposerSeparateMediaPermissions(t *testing.T) {
	h := newComposerHarness(t)
	c := h.p.composer
	restrictComposer(h, model.SendPermissions{Default: model.SendVoice | model.SendSticker | model.SendGIF})
	if c.canRecord(c.draft(h.chat)) {
		t.Fatal("forbidden microphone available")
	}
	if !c.pickerAllowed(model.PickerEmoji) || c.pickerAllowed(model.PickerStickers) || c.pickerAllowed(model.PickerGIF) {
		t.Fatal("incorrect picker tabs")
	}
	if c.chooseIn(layout.Context{Ops: new(op.Ops)}, model.PickerStickers, model.PickerItem{}) {
		t.Fatal("forbidden sticker selected")
	}
	if !c.permissions(h.chat).Allows(model.SendPhoto) {
		t.Fatal("unrelated photo permission lost")
	}
}
func TestComposerRestrictionExplanation(t *testing.T) {
	p := model.SendPermissions{Default: model.SendPhoto, Personal: model.SendText, Until: 1800000000}
	l := localization.For("ru")
	if restrictionText(p, model.SendText, l) == restrictionText(p, model.SendPhoto, l) {
		t.Fatal("personal deadline and group ban conflated")
	}
}
