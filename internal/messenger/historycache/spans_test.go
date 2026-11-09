// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"komarugram/internal/messenger/model"
)

func spanCache(t *testing.T, chat int64, ids ...[2]int) *Cache {
	t.Helper()
	c, e := Open(filepath.Join(t.TempDir(), "history"), "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	var msgs []model.Message
	for _, r := range ids {
		for id := r[0]; id <= r[1]; id++ {
			msgs = append(msgs, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}})
		}
	}
	if e = c.SaveMessages(context.Background(), msgs); e != nil {
		t.Fatal(e)
	}
	return c
}

func idRange(msgs []model.Message) (first, last, n int) {
	if len(msgs) == 0 {
		return 0, 0, 0
	}
	return int(msgs[0].Key.MessageID), int(msgs[len(msgs)-1].Key.MessageID), len(msgs)
}

func TestSpansJoinWhenTheyTouchOrOverlap(t *testing.T) {
	ctx := context.Background()
	c := spanCache(t, 7)
	for _, step := range []struct {
		low, high int
		want      Span
	}{
		{10, 20, Span{10, 20}},
		{30, 40, Span{30, 40}},
		{21, 25, Span{10, 25}}, // touches the first
		{26, 29, Span{10, 40}}, // fills the gap between both
		{5, 12, Span{5, 40}},   // overlaps
		{50, math.MaxInt, Span{50, math.MaxInt}},
		{41, 49, Span{5, math.MaxInt}},
	} {
		got, e := c.AddSpan(ctx, 7, step.low, step.high)
		if e != nil || got != step.want {
			t.Fatalf("AddSpan(%d, %d) = %v, %v; want %v", step.low, step.high, got, e, step.want)
		}
	}
	if s, ok, legacy, e := c.SpanOf(ctx, 7, 0); e != nil || !ok || legacy || s != (Span{5, math.MaxInt}) {
		t.Fatalf("the newest span is %v, %v, %v, %v", s, ok, legacy, e)
	}
	if _, ok, legacy, _ := c.SpanOf(ctx, 7, 3); ok || legacy {
		t.Fatalf("id 3 is in a span (%v) or the chat has none (%v)", ok, legacy)
	}
	if _, ok, legacy, _ := c.SpanOf(ctx, 8, 3); ok || !legacy {
		t.Fatal("a chat without spans is not legacy")
	}
}

// The cache has 1..200 from long ago and 421..500 read today: what lies
// between is unknown, and neither Around nor Page reaches across it.
func TestHistoryIsNotReadAcrossAGap(t *testing.T) {
	ctx := context.Background()
	c := spanCache(t, 7, [2]int{1, 200}, [2]int{421, 500})

	// Before any span the chat is read as it was cached.
	if first, last, n := idRange(must(c.Around(ctx, 7, 0, 300))); first != 1 || last != 500 || n != 280 {
		t.Fatalf("legacy around: %d..%d (%d)", first, last, n)
	}

	if _, e := c.AddSpan(ctx, 7, 1, 200); e != nil {
		t.Fatal(e)
	}
	if _, e := c.AddSpan(ctx, 7, 421, 500); e != nil {
		t.Fatal(e)
	}
	for _, step := range []struct {
		name               string
		msgs               []model.Message
		first, last, count int
	}{
		{"newest", must(c.Around(ctx, 7, 0, 200)), 421, 500, 80},
		{"around 430", must(c.Around(ctx, 7, 430, 200)), 421, 500, 80},
		{"around 190", must(c.Around(ctx, 7, 190, 200)), 91, 200, 110},
		{"around a missing one", must(c.Around(ctx, 7, 300, 200)), 0, 0, 0},
		{"older than 421", must(c.Page(ctx, 7, 421, -1, 80)), 0, 0, 0},
		{"older than 430", must(c.Page(ctx, 7, 430, -1, 80)), 421, 429, 9},
		{"newer than 190", must(c.Page(ctx, 7, 190, 1, 80)), 191, 200, 10},
		{"newer than 200", must(c.Page(ctx, 7, 200, 1, 80)), 0, 0, 0},
	} {
		if first, last, n := idRange(step.msgs); first != step.first || last != step.last || n != step.count {
			t.Errorf("%s: %d..%d (%d); want %d..%d (%d)", step.name, first, last, n, step.first, step.last, step.count)
		}
	}

	// Once the gap is read, the history is whole again.
	if _, e := c.AddSpan(ctx, 7, 200, 421); e != nil {
		t.Fatal(e)
	}
	if first, last, n := idRange(must(c.Page(ctx, 7, 421, -1, 80))); first != 121 || last != 200 || n != 80 {
		t.Fatalf("older than 421 after the gap was read: %d..%d (%d)", first, last, n)
	}
}

func must(msgs []model.Message, e error) []model.Message {
	if e != nil {
		panic(e)
	}
	return msgs
}

// Count and Have count a chat's messages, in a range or all, and leave out
// those deleted, which the cache keeps as markers, and other chats'.
func TestCountAndHave(t *testing.T) {
	const chat, other = int64(-1000000000001), int64(-1000000000002)
	c := spanCache(t, chat, [2]int{1, 10}, [2]int{20, 30})
	if err := c.SaveMessages(context.Background(), []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: other, MessageID: 5}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), chat, []int{3, 25}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		low, high, want int
	}{{0, math.MaxInt, 19}, {1, 10, 9}, {11, 19, 0}, {20, 30, 10}} {
		if n, err := c.Have(context.Background(), chat, f.low, f.high); err != nil || n != f.want {
			t.Fatalf("Have %d..%d = %d, %v, want %d", f.low, f.high, n, err, f.want)
		}
	}
	if n, err := c.Count(context.Background(), chat); err != nil || n != 19 {
		t.Fatalf("Count = %d, %v, want 19", n, err)
	}
}
