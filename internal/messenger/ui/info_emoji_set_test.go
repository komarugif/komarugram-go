// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"testing"
	"time"

	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// statusStore tells a status the chat the info was opened with does not have,
// and the set of an emoji.
type statusStore struct {
	*mockstore.Store
	ref model.StickerSetRef
}

func (s statusStore) PeerProfile(_ context.Context, chat int64) (model.PeerProfile, error) {
	return model.PeerProfile{Badges: model.Badges{User: chat, EmojiStatus: 42}, BadgesKnown: true}, nil
}

func (s statusStore) CachedPeerProfile(int64) (model.PeerProfile, bool) {
	return model.PeerProfile{}, false
}

func (s statusStore) CustomEmoji(_ context.Context, id int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "emoji", MIMEType: "application/x-tgsticker", StickerSet: &s.ref}}, nil
}

func (s statusStore) StickerSet(_ context.Context, ref model.StickerSetRef) (model.StickerSet, error) {
	return model.StickerSet{Ref: ref, Title: "Statuses", Emoji: true}, nil
}

func (s statusStore) SetStickerSetInstalled(context.Context, model.StickerSetRef, bool) error {
	return nil
}

// The profile brings the status the person has now, which the chat of someone
// with no dialog, made of older answers, may not have; and a click on the
// status shows the set of its emoji, over the info.
func TestInfoStatusIsCurrentAndOpensItsSet(t *testing.T) {
	ref := model.StickerSetRef{Type: "id", ID: 7, AccessHash: 8}
	var images imageOps
	p := newChatInfo(statusStore{Store: mockstore.New(time.Now(), 0), ref: ref}, &images, func() {})
	defer p.Destroy()
	l := localization.For("en")
	frame := func() {
		ops := new(op.Ops)
		p.Layout(sharedContext(ops, image.Pt(900, 800)), l, false)
	}
	p.Open(model.Chat{ID: 2, Kind: model.KindUser, Title: "Svatosh", Badges: model.Badges{User: 2, EmojiStatus: 7}})
	awaitShared(t, p)
	until := time.Now().Add(3 * time.Second)
	for p.chat.Badges.EmojiStatus != 42 && time.Now().Before(until) {
		frame()
		time.Sleep(time.Millisecond)
	}
	if p.chat.Badges.EmojiStatus != 42 {
		t.Fatalf("the info shows status %d, want the one the profile brought, 42", p.chat.Badges.EmojiStatus)
	}
	p.openEmojiSet(42)
	until = time.Now().Add(3 * time.Second)
	for !p.renderer.stickers.modal.Shown() && time.Now().Before(until) {
		frame()
		time.Sleep(time.Millisecond)
	}
	if !p.renderer.stickers.modal.Shown() || p.renderer.stickers.ref != ref {
		t.Fatalf("the set was not opened: shown %v, ref %+v", p.renderer.stickers.modal.Shown(), p.renderer.stickers.ref)
	}
	frame()
	p.Close()
	if p.renderer.stickers.modal.Shown() {
		t.Fatal("the set stays open after the info is closed")
	}
}
