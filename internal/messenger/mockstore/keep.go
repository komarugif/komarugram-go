// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"strings"
	"time"
	"unicode/utf16"

	"komarugram/internal/messenger/model"
)

var _ model.KeepStore = (*Store)(nil)

// SetKeep implements model.KeepStore: the demo keeps the one edit it has
// whatever is asked.
func (s *Store) SetKeep(model.Keep) {}

// MessageEdits implements model.KeepStore: the message of formatted text
// was written otherwise first, with formatting and a custom emoji of its
// own, which the edits history draws as the history does.
func (s *Store) MessageEdits(ctx context.Context, msg model.Message) ([]model.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if msg.EditedAt.IsZero() || !strings.HasPrefix(msg.Text, demoRichText) {
		return nil, nil
	}
	old := msg
	old.Text, old.Entities = demoRichFirst, demoEntities(demoRichFirst, [][2]string{{"bold", "Черновик"}, {"strike", "без"}, {"underline", "форматирования"}, {"emoji", "☀"}})
	old.EditedAt = time.Time{}
	return []model.Message{old}, nil
}

// demoRichText is the message of formatted text, and demoRichFirst what it
// said before its edit. demoPhotoCaption is a photo's caption, which the
// viewer draws with its formatting and custom emoji.
const (
	demoRichText     = "👋 Привет! Жирный, курсив, код и спойлер. Telegram"
	demoRichFirst    = "Черновик без форматирования ☀"
	demoPhotoCaption = "Изображение из локального кеша, а в конце сюрприз ☀"
)

// demoEntities are entities of kind over the first occurrence of each
// word of text; a custom emoji's is the demo's.
func demoEntities(text string, words [][2]string) []model.Entity {
	var out []model.Entity
	for _, pair := range words {
		i := strings.Index(text, pair[1])
		if i < 0 {
			continue
		}
		e := model.Entity{Kind: pair[0], Offset: len(utf16.Encode([]rune(text[:i]))), Length: len(utf16.Encode([]rune(pair[1])))}
		switch e.Kind {
		case "url":
			e.URL = "https://telegram.org"
		case model.EntityEmoji:
			e.DocumentID = 1
		}
		out = append(out, e)
	}
	return out
}
