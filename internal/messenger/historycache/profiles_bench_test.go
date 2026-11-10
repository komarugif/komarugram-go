package historycache

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"komarugram/internal/messenger/model"
)

// BenchmarkShapeProfiles reads the profiles of 2000 rows as the history's
// skeletons ask for them: by the reserve's query, which reads the messages
// too, and by the profiles' own.
func BenchmarkShapeProfiles(b *testing.B) {
	c, _ := openHeights(b, defaultHeightLimits)
	ctx := context.Background()
	const n = 2000
	text := strings.Repeat("Some text of a post, as channels have. ", 6)
	var msgs []model.Message
	for i := 1; i <= n; i++ {
		msgs = append(msgs, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 7, MessageID: model.MessageID(i)}, Text: fmt.Sprintf("%s %d", text, i), ContentRevision: uint64(i)})
	}
	if err := c.SaveMessages(ctx, msgs); err != nil {
		b.Fatal(err)
	}
	if _, err := c.AddSpan(ctx, 7, 1, n); err != nil {
		b.Fatal(err)
	}
	env := model.RenderEnvironment{WidthPx: 500, ScaleMilli: 1000, TextScaleMilli: 1000, Locale: "en"}
	profile := append([]byte{4}, make([]byte, 90)...)
	var ls []model.MessageLayout
	for _, m := range msgs {
		ls = append(ls, model.MessageLayout{Key: m.Key, Environment: env, ContentRevision: m.ContentRevision, HeightPx: 80, Profile: profile})
	}
	if err := c.SaveView(ctx, model.Viewport{AccountID: "a", ChatID: 7, AnchorMessageID: 1}, ls); err != nil {
		b.Fatal(err)
	}
	b.Run("reserve", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if rows, err := c.Reserve(ctx, 7, 0, env, 4, n); err != nil || len(rows) == 0 {
				b.Fatal(len(rows), err)
			}
		}
	})
	b.Run("profiles", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if out, err := c.ProfilesBetween(ctx, 7, env, 4, 1, n); err != nil || len(out) != n {
				b.Fatal(len(out), err)
			}
		}
	})
}
