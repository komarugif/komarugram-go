// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"komarugram/internal/messenger/model"
)

func TestLocalPremiumEmojiNeverSentAndSurvivesReload(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[5] = &model.History{}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesSendMessageRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		if r.Message != "a 🙂" || len(r.Entities) != 1 {
			return nil, fmt.Errorf("server received local emoji: %+v", r)
		}
		if _, ok := r.Entities[0].(*tg.MessageEntityBold); !ok {
			return nil, fmt.Errorf("server received %T", r.Entities[0])
		}
		return &tg.UpdateShortSentMessage{ID: 9, Date: 10, Out: true}, nil
	})
	entities := []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 1}, {Kind: model.EntityEmoji, Offset: 2, Length: 2, DocumentID: 77}}
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 123, Text: "a 🙂", Entities: entities}); err != nil {
		t.Fatal(err)
	}
	raw := &tg.Message{ID: 9, Out: true, PeerID: &tg.PeerUser{UserID: 5}, Message: "a 🙂", Date: 10}
	messages, err := s.ingest(context.Background(), []tg.MessageClass{raw}, false, s.history.generation)
	if err != nil || len(messages) != 1 || !messages[0].LocalPremium || !slices.Contains(messages[0].Entities, entities[1]) {
		t.Fatalf("reload lost emoji: %+v, %v", messages, err)
	}
	raw.Message = "edited elsewhere"
	messages, err = s.ingest(context.Background(), []tg.MessageClass{raw}, true, 0)
	if err != nil || messages[0].LocalPremium || len(messages[0].Entities) != 0 {
		t.Fatalf("overlay survived a different server text: %+v, %v", messages, err)
	}
}

func TestLocalPremiumStickerStaysOnlyOnDevice(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history")
	s := New(nil)
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	s.SetLocalPremium(model.AllLocalPremium)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[5] = &model.History{}
	m := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "document/77", MIMEType: "image/webp", Premium: true, Emoji: "🙂"}}
	if err := s.Cache().SaveMedia(ctx, m.Media.ID, []byte("sticker bytes")); err != nil {
		t.Fatal(err)
	}
	// With no API attached, this can finish only if it sends no Telegram RPC.
	if err := s.Send(ctx, 5, model.OutgoingMessage{RandomID: 123, Item: &model.PickerItem{ID: m.Media.ID, Media: m}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = New(nil)
	if err := s.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.history.histories[5] = &model.History{}
	s.SetLocalPremium(model.AllLocalPremium)
	got := s.History(5).Messages
	if len(got) != 1 || got[0].Sending || !got[0].LocalPremium || got[0].Media.ID != m.Media.ID {
		t.Fatalf("local sticker after restart: %+v", got)
	}
	if b, err := s.Cache().LocalMedia(ctx, m.Media.ID); err != nil || string(b) != "sticker bytes" {
		t.Fatalf("sticker not retained: %q, %v", b, err)
	}
	if normal, err := s.Cache().Around(ctx, 5, 0, 10); err != nil || len(normal) != 0 {
		t.Fatal("local sticker contaminated Telegram history", normal, err)
	}
	s.CancelOutgoing(got[0].Key)
	if len(s.History(5).Messages) != 0 {
		t.Fatal("local sticker was not deleted")
	}
}

// Saved with Local Premium, a GIF is saved on Telegram as ever. What its list
// has no room for then stays here, and goes from here alone.
func TestLocalPremiumKeepsGIFsTelegramPushesOut(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	ctx := context.Background()
	api := &savedGIFsAPI{}
	s.history.api = api.client()
	s.history.refs["document/9"] = fileLocation{ID: 9, Hash: 99}
	gif := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/9", MIMEType: "video/mp4"}}
	limit := model.SavedGIFLimit(s.Premium())
	items := make([]model.PickerItem, limit)
	for i := range items {
		items[i].ID = fmt.Sprintf("document/%d", 100+i)
	}
	s.picker.mu.Lock()
	s.picker.init()
	s.picker.pages[model.PickerGIF] = model.PickerPage{Items: items}
	s.picker.mu.Unlock()
	if err := s.SaveGIF(ctx, gif, false); err != nil {
		t.Fatalf("over Telegram's limit: %v", err)
	}
	if took := api.took(); len(took) != 1 || took[0] != "gif 9/99/ unsave=false" {
		t.Fatalf("asked %v", took)
	}
	last := fmt.Sprintf("document/%d", 100+limit-1)
	if !s.SavedGIF(gif.Media.ID) || !s.SavedGIF(last) {
		t.Fatal("the GIF or the one it pushed out is not saved")
	}
	s.picker.mu.Lock()
	server := s.picker.pages[model.PickerGIF]
	s.picker.mu.Unlock()
	page := s.withLocalGIFs(server)
	if len(page.Items) != limit+1 || page.Items[0].ID != gif.Media.ID || page.Items[limit].ID != last {
		t.Fatalf("%d GIFs, %s first, %s last", len(page.Items), page.Items[0].ID, page.Items[len(page.Items)-1].ID)
	}
	// Only here: offline it goes too, and Telegram hears nothing.
	s.history.api = nil
	pushed := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: last}}
	if err := s.SaveGIF(ctx, pushed, true); err != nil {
		t.Fatal(err)
	}
	if s.SavedGIF(last) {
		t.Fatal("the local GIF was not removed")
	}
	s.SetLocalPremium(model.LocalPremium{})
	if s.Premium().Active {
		t.Fatal("local mode changed actual subscription")
	}
	if len(s.withLocalGIFs(server).Items) != limit {
		t.Fatal("without Local Premium the list is not Telegram's")
	}
}

func TestLocalPremiumKeepsFreeEmojiOnServer(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[5] = &model.History{}
	ctx := context.Background()
	if err := s.Cache().Put(ctx, emojiKey(3), model.Message{Media: &model.MessageMedia{PremiumKnown: true}}); err != nil {
		t.Fatal(err)
	}
	free := &tg.MessageEntityCustomEmoji{Offset: 2, Length: 2, DocumentID: 3}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r := in.(*tg.MessagesSendMessageRequest)
		if len(r.Entities) != 1 {
			return nil, fmt.Errorf("unexpected wire entities: %+v", r.Entities)
		}
		if entity, ok := r.Entities[0].(*tg.MessageEntityCustomEmoji); !ok || entity.DocumentID != 3 {
			return nil, fmt.Errorf("free emoji was not sent: %+v", r.Entities)
		}
		return &tg.UpdateShortSentMessage{ID: 9, Date: 10, Entities: []tg.MessageEntityClass{free}}, nil
	})
	entities := []model.Entity{{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 7}, {Kind: model.EntityEmoji, Offset: 2, Length: 2, DocumentID: 3}}
	if err := s.Send(ctx, 5, model.OutgoingMessage{RandomID: 1, Text: "🙂🙂", Entities: entities}); err != nil {
		t.Fatal(err)
	}
	got := s.History(5).Messages
	if len(got) != 1 || len(got[0].Entities) != 2 {
		t.Fatalf("overlay replaced a free emoji: %+v", got)
	}
}

func TestLocalPremiumResolvesOldStickerBeforeSending(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[5] = &model.History{}
	var calls int
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if _, ok := in.(*tg.MessagesGetStickerSetRequest); !ok {
			return nil, fmt.Errorf("old Premium sticker reached send API: %T", in)
		}
		calls++
		return &tg.MessagesStickerSet{Set: tg.StickerSet{ID: 1, AccessHash: 2, Title: "pack", ShortName: "pack", Count: 1}, Documents: []tg.DocumentClass{&tg.Document{ID: 77, MimeType: "application/x-tgsticker", VideoThumbs: []tg.VideoSizeClass{&tg.VideoSize{Type: "f", W: 1, H: 1, Size: 1}}, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Alt: "🙂", Stickerset: &tg.InputStickerSetID{ID: 1, AccessHash: 2}}}}}}, nil
	})
	item := model.PickerItem{ID: "document/77", Media: model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "document/77", StickerSet: &model.StickerSetRef{Type: "id", ID: 1, AccessHash: 2}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Send(ctx, 5, model.OutgoingMessage{RandomID: 1, Item: &item}); err != nil {
		t.Fatal(err)
	}
	if got := s.History(5).Messages; calls != 1 || len(got) != 1 || !got[0].LocalPremium || !got[0].Media.Premium {
		t.Fatalf("old sticker not resolved: %+v, %d calls", got, calls)
	}
}

func TestLocalPremiumEmojiCanBeEditedWithoutServerChanges(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[5] = &model.History{}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesSendMessageRequest:
			return &tg.UpdateShortSentMessage{ID: 9, Date: 10, Out: true}, nil
		case *tg.MessagesEditMessageRequest:
			if len(r.Entities) > 0 {
				return nil, fmt.Errorf("local emoji leaked in edit: %+v", r.Entities)
			}
			return nil, tgerr.New(400, "MESSAGE_NOT_MODIFIED")
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	ctx := context.Background()
	entity := model.Entity{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 7}
	if err := s.Send(ctx, 5, model.OutgoingMessage{RandomID: 1, Text: "🙂", Entities: []model.Entity{entity}}); err != nil {
		t.Fatal(err)
	}
	key := model.MessageKey{AccountID: "a", ChatID: 5, MessageID: 9}
	entity.DocumentID = 8
	if err := s.EditMessage(ctx, key, "🙂", []model.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	if got := s.History(5).Messages; len(got) != 1 || !slices.Contains(got[0].Entities, entity) {
		t.Fatalf("local appearance not edited: %+v", got)
	}
	if err := s.EditMessage(ctx, key, "🙂", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.History(5).Messages; len(got) != 1 || got[0].LocalPremium || len(got[0].Entities) > 0 {
		t.Fatalf("local emoji not removed: %+v", got)
	}
}

func TestLocalPremiumRetainsEmojiLoadedAfterSend(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.keepLocalEmoji(ctx, 5, 9, "🙂", []model.Entity{{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 7}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Cache().SaveMedia(ctx, "document/7", []byte("late emoji")); err != nil {
		t.Fatal(err)
	}
	// Emoji decoders resolve the document separately from its message, so the
	// media request itself carries no LocalPremium flag.
	m := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "document/7", Size: 10}}
	if _, err := s.Media(ctx, m); err != nil {
		t.Fatal(err)
	}
	if b, err := s.Cache().LocalMedia(ctx, "document/7"); err != nil || string(b) != "late emoji" {
		t.Fatalf("late emoji was not retained: %q, %v", b, err)
	}
}

// With Local Premium's media off the chat shows what the other side sees: the
// ordinary emoji, and no local sticker. Everything comes back when it is on.
func TestLocalPremiumMediaOffShowsWhatTelegramHas(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.SetLocalPremium(model.AllLocalPremium)
	const chat = int64(5)
	emoji := model.Entity{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 7}
	free := model.Entity{Kind: model.EntityEmoji, Offset: 3, Length: 2, DocumentID: 8}
	const text = "🙂 🙂"
	if err := s.Cache().Put(ctx, localEmojiKey(chat, 9), localEmoji{Text: text, Entities: []model.Entity{emoji}}); err != nil {
		t.Fatal(err)
	}
	key := model.MessageKey{AccountID: "a", ChatID: chat, MessageID: 9}
	shown := s.applyLocalEmoji(ctx, model.Message{Key: key, Text: text, Entities: []model.Entity{free}})
	if !shown.LocalPremium || !slices.Contains(shown.Entities, emoji) {
		t.Fatalf("on: %+v", shown)
	}
	sticker := model.Message{Key: model.MessageKey{ChatID: chat, MessageID: -5}, Date: time.Unix(5, 0), LocalPremium: true}
	// The same message in a chat without a sticker, as a thread's history is.
	const plain = int64(-77)
	s.history.mu.Lock()
	s.history.histories[chat] = &model.History{Messages: []model.Message{shown}}
	s.history.histories[plain] = &model.History{Messages: []model.Message{shown}}
	s.history.localStickers = []model.Message{sticker}
	revision := s.history.histories[plain].Revision
	s.history.mu.Unlock()
	if got := s.History(chat).Messages; len(got) != 2 {
		t.Fatalf("on: %d messages", len(got))
	}
	s.SetLocalPremium(model.LocalPremium{})
	waitStorage(t, "the overlay to go", func() bool {
		got := s.History(chat).Messages
		return len(got) == 1 && !got[0].LocalPremium && !slices.Contains(got[0].Entities, emoji)
	})
	got := s.History(chat)
	if !slices.Contains(got.Messages[0].Entities, free) || got.Messages[0].Text != text {
		t.Fatalf("off: what Telegram has went too: %+v", got.Messages[0])
	}
	if s.History(plain).Revision == revision {
		t.Fatal("the history did not tell it changed")
	}
	// Another chat's message on its way has the previews looked over.
	s.history.mu.Lock()
	s.history.pending.list = append(s.history.pending.list, &pendingMessage{chat: 6, msg: model.Message{Key: model.MessageKey{ChatID: 6, MessageID: -9}, Date: time.Unix(9, 0), Sending: true}})
	s.history.mu.Unlock()
	if slices.ContainsFunc(s.withOutgoingPreviews(nil), func(c model.Chat) bool { return c.ID == chat }) {
		t.Fatal("the hidden sticker is the chat's last message")
	}
	s.history.mu.Lock()
	s.history.pending.list = nil
	s.history.mu.Unlock()
	// Read from the cache, as it was shown when on.
	again := s.applyLocalEmoji(ctx, shown)
	if again.LocalPremium || slices.Contains(again.Entities, emoji) {
		t.Fatalf("off, from the cache: %+v", again)
	}
	// A message delivered meanwhile keeps its overlay for later, unshown.
	s.history.mu.Lock()
	s.history.histories[chat].Messages = append(s.history.histories[chat].Messages, model.Message{Key: model.MessageKey{ChatID: chat, MessageID: 10}, Text: "🙂"})
	s.history.mu.Unlock()
	if err := s.keepLocalEmoji(ctx, chat, 10, "🙂", []model.Entity{emoji}); err != nil {
		t.Fatal(err)
	}
	if got := s.History(chat).Messages[1]; got.LocalPremium || len(got.Entities) != 0 {
		t.Fatalf("off, delivered: %+v", got)
	}
	s.SetLocalPremium(model.LocalPremium{Media: true})
	waitStorage(t, "the overlay to return", func() bool {
		got := s.History(chat).Messages
		return len(got) == 3 && got[0].LocalPremium && slices.Contains(got[0].Entities, emoji)
	})
}

// In the field, only a paid emoji the account could not really send is hidden.
func TestHidesCustomEmoji(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for id, paid := range map[int64]bool{1: true, 2: false} {
		if err := s.Cache().Put(ctx, emojiKey(id), model.Message{Media: &model.MessageMedia{PremiumKnown: true, Premium: paid}}); err != nil {
			t.Fatal(err)
		}
	}
	const chat = int64(5)
	if !s.HidesCustomEmoji(chat, 1) || s.HidesCustomEmoji(chat, 2) || !s.HidesCustomEmoji(chat, 3) {
		t.Fatal("off: a paid or unknown emoji is hidden, a free one is not")
	}
	s.me.ID = 9
	if s.HidesCustomEmoji(9, 1) || !s.HidesCustomEmoji(chat, 1) {
		t.Fatal("Saved Messages hide nothing, and the others still do")
	}
	s.SetLocalPremium(model.LocalPremium{Media: true})
	if s.HidesCustomEmoji(chat, 1) || s.HidesCustomEmoji(chat, 3) {
		t.Fatal("with the media on nothing is hidden")
	}
}

// The chat list follows the setting at once: the last message's overlay goes when
// it is off and comes when it is on, and a list of dialogs, which tells none, gets
// it too. Saved Messages keep theirs.
func TestLocalPremiumPreviewFollowsMedia(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.SetLocalPremium(model.AllLocalPremium)
	emoji := model.Entity{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 7}
	free := model.Entity{Kind: model.EntityEmoji, Offset: 3, Length: 2, DocumentID: 8}
	const text = "🙂 🙂"
	const saved, other = int64(9), int64(5)
	s.me.ID = saved
	for _, chat := range []int64{saved, other} {
		if err := s.Cache().Put(ctx, localEmojiKey(chat, 3), localEmoji{Text: text, Entities: []model.Entity{emoji}}); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	s.chats = []model.Chat{
		{ID: saved, Last: model.Preview{ID: 3, Text: text, Entities: []model.Entity{free}}},
		{ID: other, Last: model.Preview{ID: 3, Text: text, Entities: []model.Entity{free}}},
	}
	s.mu.Unlock()
	hasEmoji := func(chat int64) (fake, kept bool) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, c := range s.chats {
			if c.ID == chat {
				return slices.Contains(c.Last.Entities, emoji), slices.Contains(c.Last.Entities, free)
			}
		}
		t.Fatalf("no chat %d", chat)
		return
	}
	s.dialogsRead()
	if fake, kept := hasEmoji(other); !fake || !kept {
		t.Fatalf("dialogs read, on: %v %v", fake, kept)
	}
	s.SetLocalPremium(model.LocalPremium{})
	waitStorage(t, "the overlay to go", func() bool {
		fake, kept := hasEmoji(other)
		return !fake && kept
	})
	if fake, _ := hasEmoji(saved); !fake {
		t.Fatal("Saved Messages lost theirs")
	}
	s.SetLocalPremium(model.LocalPremium{Media: true})
	waitStorage(t, "the overlay to return", func() bool {
		fake, kept := hasEmoji(other)
		return fake && kept
	})
}

// In Saved Messages a paid emoji goes as it is, Local Premium or not; elsewhere
// Local Premium keeps it from Telegram.
func TestLocalPremiumSavedMessagesSendRealEmoji(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	s.me.ID = 9
	s.history.peers[9] = peerRecord{Kind: "user", ID: 9, Rights: peerRights{Self: true}}
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.histories[9] = &model.History{}
	s.history.histories[5] = &model.History{}
	sent := 0
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesSendMessageRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		sent = len(r.Entities)
		return &tg.UpdateShortSentMessage{ID: 7, Date: 10, Out: true}, nil
	})
	entities := []model.Entity{{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 7}}
	ctx := context.Background()
	if err := s.Send(ctx, 9, model.OutgoingMessage{RandomID: 1, Text: "🙂", Entities: entities}); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("Saved Messages: %d entities on the wire", sent)
	}
	if err := s.Send(ctx, 5, model.OutgoingMessage{RandomID: 2, Text: "🙂", Entities: entities}); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("elsewhere: %d entities on the wire", sent)
	}
}

// Only what could not be sent is out of the panel.
func TestCannotSendPaid(t *testing.T) {
	s := testStore(t)
	s.me.ID = 9
	if !s.CannotSendPaid(5, false) || !s.CannotSendPaid(5, true) {
		t.Fatal("with nothing to send them, they cannot be sent")
	}
	if !s.CannotSendPaid(9, false) || s.CannotSendPaid(9, true) {
		t.Fatal("Saved Messages take paid emoji, not stickers")
	}
	s.SetLocalPremium(model.LocalPremium{Media: true})
	if s.CannotSendPaid(5, false) || s.CannotSendPaid(5, true) {
		t.Fatal("Local Premium's media sends them")
	}
}
