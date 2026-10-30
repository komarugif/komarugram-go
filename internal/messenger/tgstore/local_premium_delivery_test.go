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
	s.SetLocalPremium(true)
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
	s.SetLocalPremium(true)
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

func TestLocalPremiumCollectionsSurviveServerRefresh(t *testing.T) {
	s := pinStore(t)
	s.SetLocalPremium(true)
	ctx := context.Background()
	if err := s.PinChat(ctx, 3, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderPinned(ctx, []int64{1, 3, 2}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(model.PinnedOrder(s.Chats()), []int64{1, 3, 2}) {
		t.Fatal("local pin order lost")
	}
	s.SetLocalPremium(false)
	if !slices.Equal(model.PinnedOrder(s.Chats()), []int64{1, 2}) {
		t.Fatal("local pin changed server pins")
	}
	s.SetLocalPremium(true)
	gif := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/7", MIMEType: "video/mp4"}}
	if err := s.SaveGIF(ctx, gif, false); err != nil {
		t.Fatal(err)
	}
	if !s.SavedGIF(gif.Media.ID) {
		t.Fatal("local GIF not saved")
	}
	page := s.withLocalGIFs(model.PickerPage{Items: []model.PickerItem{{ID: "server"}}})
	if len(page.Items) != 2 || page.Items[0].ID != gif.Media.ID {
		t.Fatalf("server refresh removed local GIF: %+v", page)
	}
	if err := s.SaveGIF(ctx, gif, true); err != nil {
		t.Fatal(err)
	}
	if s.SavedGIF(gif.Media.ID) {
		t.Fatal("local GIF not removed")
	}
	if s.Premium().Active {
		t.Fatal("local mode changed actual subscription")
	}
}

func TestLocalPremiumKeepsFreeEmojiOnServer(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(true)
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
	s.SetLocalPremium(true)
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
	s.SetLocalPremium(true)
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
