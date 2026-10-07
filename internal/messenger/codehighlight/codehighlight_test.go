// SPDX-License-Identifier: Unlicense OR MIT

package codehighlight

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"komarugram/pkg/prism"
)

// reset gives the test a highlighter of its own, which counts the times it
// loads the grammars: the worker of the test before goes first.
func reset(t *testing.T) *atomic.Int32 {
	t.Helper()
	h := &highlighter
	h.mu.Lock()
	h.idle = time.Nanosecond
	if h.wake != nil {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
	for h.running {
		h.mu.Unlock()
		time.Sleep(time.Millisecond)
		h.mu.Lock()
	}
	h.idle = 0
	h.cache, h.order, h.pending, h.queue = nil, nil, nil, nil
	loads := &atomic.Int32{}
	h.load = func() (*prism.Grammars, error) {
		loads.Add(1)
		return prism.Embedded(matchTimeout)
	}
	h.mu.Unlock()
	t.Cleanup(func() {
		h.mu.Lock()
		h.load, h.idle = nil, 0
		h.mu.Unlock()
	})
	return loads
}

func colorsOf(t *testing.T, language, text string) []Span {
	t.Helper()
	key := KeyOf(language, text)
	done := make(chan struct{})
	Request(key, language, text, func() { close(done) })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no colors")
	}
	spans, ok := Lookup(key)
	if !ok {
		t.Fatal("colors announced but not there")
	}
	return spans
}

func classesOf(text string, spans []Span) map[string]Class {
	out := map[string]Class{}
	for _, s := range spans {
		out[text[s.Start:s.End]] = s.Class
	}
	return out
}

// Tokens get Telegram Desktop's classes, under the names people write for
// their languages.
func TestColors(t *testing.T) {
	reset(t)
	text := "func main() { s := \"hi\" // note\n}"
	for _, language := range []string{"go", " Golang "} {
		got := classesOf(text, colorsOf(t, language, text))
		for piece, class := range map[string]Class{"func": Keyword, `"hi"`: String, "// note": Comment, "()": Punctuation, ":=": Operator} {
			if got[piece] != class {
				t.Errorf("%s: %q is %d, not %d (%v)", language, piece, got[piece], class, got)
			}
		}
	}
	code := "/** Doc. */\nclass A {}"
	if got := classesOf(code, colorsOf(t, "javascript", code)); got["/** Doc. */"] != Comment || got["A"] != ClassName {
		t.Errorf("javascript: %v", got)
	}
	if spans := colorsOf(t, "klingon", text); len(spans) != 0 {
		t.Errorf("an unknown language colored: %v", spans)
	}
}

// Blocks asked for at once are colored once, with the grammars loaded once;
// the cache keeps the last 256 blocks.
func TestRequestsShareWork(t *testing.T) {
	loads := reset(t)
	var wg sync.WaitGroup
	var calls atomic.Int32
	for range 10 {
		wg.Add(1)
		Request(KeyOf("go", "x := 1"), "go", "x := 1", func() { calls.Add(1); wg.Done() })
	}
	wg.Wait()
	if calls.Load() != 10 || loads.Load() != 1 {
		t.Fatalf("%d calls, %d loads", calls.Load(), loads.Load())
	}
	h := &highlighter
	h.mu.Lock()
	queued := len(h.order)
	h.mu.Unlock()
	if queued != 1 {
		t.Fatalf("%d blocks colored", queued)
	}
	for i := range cacheSize + 10 {
		colorsOf(t, "go", fmt.Sprintf("x := %d", i))
	}
	if _, ok := Lookup(KeyOf("go", "x := 0")); ok {
		t.Fatal("the oldest block stayed")
	}
	if _, ok := Lookup(KeyOf("go", fmt.Sprintf("x := %d", cacheSize+9))); !ok {
		t.Fatal("the newest block went")
	}
	if loads.Load() != 1 {
		t.Fatalf("%d loads", loads.Load())
	}
}

// The worker, and the grammars with it, go after a while without blocks.
func TestWorkerGoesWhenIdle(t *testing.T) {
	loads := reset(t)
	h := &highlighter
	h.mu.Lock()
	h.idle = 50 * time.Millisecond
	h.mu.Unlock()
	colorsOf(t, "go", "a")
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		running := h.running
		h.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker stays")
		}
		time.Sleep(10 * time.Millisecond)
	}
	colorsOf(t, "go", "b")
	if loads.Load() != 2 {
		t.Fatalf("%d loads", loads.Load())
	}
}
