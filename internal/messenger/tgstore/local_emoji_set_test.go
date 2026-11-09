package tgstore

import (
	"context"
	"testing"

	"komarugram/internal/messenger/model"
)

// The messages with local emoji are known by one read of their keys: one of
// them gets its emoji, the others are left as they are without a read
// each, and a chat whose id begins another's is not taken for it.
func TestLocalEmojiAreKnownByTheirKeys(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	cache := s.Cache()
	emoji := model.Entity{Kind: "custom_emoji", Offset: 0, Length: 2, DocumentID: 77}
	if err := s.putLocalEmoji(ctx, cache, 55, 9, localEmoji{Text: "hi", Entities: []model.Entity{emoji}}); err != nil {
		t.Fatal(err)
	}
	if s.hasLocalEmoji(ctx, cache, 5, 9) {
		t.Fatal("chat 5 is taken for chat 55")
	}
	if !s.hasLocalEmoji(ctx, cache, 55, 9) || s.hasLocalEmoji(ctx, cache, 55, 10) {
		t.Fatal("the messages of chat 55 with local emoji are not known")
	}
	// Kept after the chat's set was read: it is known too.
	if err := s.putLocalEmoji(ctx, cache, 55, 12, localEmoji{Text: "yo", Entities: []model.Entity{emoji}}); err != nil {
		t.Fatal(err)
	}
	if !s.hasLocalEmoji(ctx, cache, 55, 12) {
		t.Fatal("local emoji kept later are not known")
	}
}

// BenchmarkApplyLocalEmoji applies local emoji to a page of 600 messages,
// none of which have any, as a leap of the window brings.
func BenchmarkApplyLocalEmoji(b *testing.B) {
	s := testStore(b)
	ctx := context.Background()
	msgs := make([]model.Message, 600)
	for i := range msgs {
		msgs[i] = model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 5, MessageID: model.MessageID(i + 1)}, Text: "text"}
	}
	b.Run("a read each", func(b *testing.B) {
		b.ReportAllocs()
		cache := s.Cache()
		for b.Loop() {
			for _, m := range msgs {
				var local localEmoji
				_, _ = cache.Get(ctx, localEmojiKey(m.Key.ChatID, int(m.Key.MessageID)), &local)
			}
		}
	})
	b.Run("apply", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, m := range msgs {
				s.applyLocalEmoji(ctx, m)
			}
		}
	})
}
