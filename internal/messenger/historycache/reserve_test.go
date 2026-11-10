package historycache

import (
	"context"
	"testing"

	"komarugram/internal/messenger/model"
)

// The reserve is the messages of the span after the one asked, with what
// the cache keeps of their rows in the environment: the height at its
// width, the profile at any width.
func TestReserveReadsTheSpanAfter(t *testing.T) {
	c, _ := openHeights(t, defaultHeightLimits)
	ctx := context.Background()
	var msgs []model.Message
	for i := 1; i <= 12; i++ {
		m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 7, MessageID: model.MessageID(i)}, Text: "text", ContentRevision: uint64(100 + i), Outgoing: i%2 == 0}
		if i == 6 || i == 7 {
			m.GroupedID, m.Media = 42, &model.MessageMedia{ID: "photo/p", Width: 10, Height: 10}
		}
		msgs = append(msgs, m)
	}
	if err := c.SaveMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	// The span is 1..10: 11 and 12 are past it.
	if _, err := c.AddSpan(ctx, 7, 1, 10); err != nil {
		t.Fatal(err)
	}
	env := model.RenderEnvironment{WidthPx: 500, ScaleMilli: 1000, TextScaleMilli: 1000, Locale: "en"}
	other := env
	other.WidthPx = 300
	var ls []model.MessageLayout
	for i := 4; i <= 9; i++ {
		l := model.MessageLayout{Key: msgs[i-1].Key, Environment: env, ContentRevision: msgs[i-1].ContentRevision, HeightPx: 40 + i}
		if i%3 == 0 {
			l.Environment = other
			l.Profile = []byte{4, byte(i)}
		}
		ls = append(ls, l)
	}
	if err := c.SaveView(ctx, model.Viewport{AccountID: "a", ChatID: 7, AnchorMessageID: 3}, ls); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Reserve(ctx, 7, 3, env, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 || rows[0].ID != 4 || rows[6].ID != 10 {
		t.Fatalf("the reserve after 3 is %+v", rows)
	}
	for _, r := range rows {
		id := int(r.ID)
		if r.Revision != uint64(100+id) || r.Outgoing != (id%2 == 0) || (r.GroupedID == 42) != (id == 6 || id == 7) || r.Media != (id == 6 || id == 7) {
			t.Fatalf("row %d is %+v", id, r)
		}
		switch {
		case id == 10:
			if r.Height != -1 || r.Profile != nil {
				t.Fatalf("row %d, of no layout, is %+v", id, r)
			}
		case id%3 == 0:
			// Laid out at another width: its profile, no height at env's.
			if r.Height != -1 || len(r.Profile) != 2 || r.Profile[1] != byte(id) || r.ProfileRevision != r.Revision {
				t.Fatalf("row %d is %+v", id, r)
			}
		default:
			if r.Height != 40+id || r.HeightRevision != r.Revision || r.Profile != nil {
				t.Fatalf("row %d is %+v", id, r)
			}
		}
	}
	// Profiles of another version of the encoding are as none.
	rows, _ = c.Reserve(ctx, 7, 3, env, 3, 100)
	for _, r := range rows {
		if r.Profile != nil {
			t.Fatalf("a profile of another version came: %+v", r)
		}
	}
}

// From reads the span from a message on: the message itself, and those
// after it to the span's end, never past it.
func TestFromReadsTheSpanOn(t *testing.T) {
	c, _ := openHeights(t, defaultHeightLimits)
	ctx := context.Background()
	var msgs []model.Message
	for i := 1; i <= 12; i++ {
		msgs = append(msgs, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 7, MessageID: model.MessageID(i)}, Text: "text"})
	}
	if err := c.SaveMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddSpan(ctx, 7, 1, 10); err != nil {
		t.Fatal(err)
	}
	got, err := c.From(ctx, 7, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 || got[0].Key.MessageID != 4 || got[6].Key.MessageID != 10 {
		t.Fatalf("from 4: %d messages, %+v", len(got), got)
	}
	if got, _ := c.From(ctx, 7, 4, 3); len(got) != 3 || got[2].Key.MessageID != 6 {
		t.Fatalf("3 from 4: %+v", got)
	}
	if got, _ := c.From(ctx, 7, 11, 100); len(got) != 0 {
		t.Fatalf("from 11, past the span: %+v", got)
	}
}
