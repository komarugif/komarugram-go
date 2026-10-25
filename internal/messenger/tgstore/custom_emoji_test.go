// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"sync"
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

// Custom emoji asked for together go in one request, an emoji on its way
// is not asked twice, and one Telegram does not know is not asked again.
func TestCustomEmojiAskedTogether(t *testing.T) {
	s := testStore(t)
	var mu sync.Mutex
	var asked [][]int64
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		req, ok := in.(*tg.MessagesGetCustomEmojiDocumentsRequest)
		if !ok {
			t.Errorf("unexpected request %T", in)
			return nil, nil
		}
		mu.Lock()
		asked = append(asked, slices.Clone(req.DocumentID))
		mu.Unlock()
		var docs []tg.DocumentClass
		for _, id := range req.DocumentID {
			if id == 9 {
				continue
			}
			docs = append(docs, &tg.Document{ID: id, AccessHash: 1, FileReference: []byte{1}, MimeType: "image/webp", Size: 10, DCID: 2,
				Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeCustomEmoji{Alt: "🙂", Stickerset: &tg.InputStickerSetEmpty{}}}})
		}
		return &tg.DocumentClassVector{Elems: docs}, nil
	})
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i, id := range []int64{5, 6, 5, 9} {
		wg.Go(func() { _, errs[i] = s.CustomEmoji(ctx, id) })
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || errs[2] != nil || errs[3] == nil {
		t.Fatalf("errors %v", errs)
	}
	if _, err := s.CustomEmoji(ctx, 9); err == nil {
		t.Fatal("missing emoji found")
	}
	if _, err := s.CustomEmoji(ctx, 6); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || !slices.Equal(asked[0], []int64{5, 6, 9}) {
		t.Fatalf("asked %v", asked)
	}
}
