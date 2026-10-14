// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A custom emoji keeps Telegram's mark of the text's color; one cached
// before the mark was kept is asked for again.
func TestCustomEmojiTextColor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	stale := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "doc/5", MIMEType: "image/webp"}}
	if err := s.history.cache.Put(ctx, "emoji/5", stale); err != nil {
		t.Fatal(err)
	}
	asked := 0
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if _, ok := in.(*tg.MessagesGetCustomEmojiDocumentsRequest); !ok {
			t.Fatalf("unexpected request %T", in)
		}
		asked++
		return &tg.DocumentClassVector{Elems: []tg.DocumentClass{&tg.Document{ID: 5, AccessHash: 1, FileReference: []byte{1}, Date: 1, MimeType: "image/webp", Size: 10, DCID: 2,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeCustomEmoji{TextColor: true, Alt: "💫", Stickerset: &tg.InputStickerSetEmpty{}}}}}}, nil
	})
	for range 2 {
		m, err := s.CustomEmoji(ctx, 5)
		if err != nil {
			t.Fatal(err)
		}
		if m.Media == nil || !m.Media.TextColor {
			t.Fatalf("media %+v", m.Media)
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times", asked)
	}
}
