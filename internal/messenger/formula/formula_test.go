// SPDX-License-Identifier: Unlicense OR MIT

package formula

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"komarugram/pkg/ratex"
)

// wait requests source and waits for it to be laid out.
func wait(t *testing.T, source string, display bool) Result {
	t.Helper()
	key := KeyOf(source, display)
	done := make(chan struct{})
	Request(key, source, display, func() { close(done) })
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%q was not laid out", source)
	}
	r, ok := Lookup(key)
	if !ok {
		t.Fatalf("%q is not kept", source)
	}
	return r
}

// fakeLayouter lays every formula out as an empty list.
type fakeLayouter struct{}

func (fakeLayouter) Layout(context.Context, string, bool) (*ratex.List, error) {
	return &ratex.List{}, nil
}
func (fakeLayouter) Close(context.Context) error { return nil }

// Memory is asked back once the module is opened, which leaves the
// garbage of compiling it, and once it is let go. It runs before the
// other tests, while no module is open.
func TestReleasesMemory(t *testing.T) {
	var releases atomic.Int32
	l := &layouts
	l.mu.Lock()
	l.open = func() (layouter, error) { return fakeLayouter{}, nil }
	l.mu.Unlock()
	SetRelease(func() { releases.Add(1) })
	saved := idle
	idle = 50 * time.Millisecond
	t.Cleanup(func() {
		SetRelease(nil)
		l.mu.Lock()
		l.open = nil
		l.mu.Unlock()
		idle = saved
	})
	wait(t, `y`, false)
	if n := releases.Load(); n != 1 {
		t.Fatalf("opening the module asked %d times", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.mu.Lock()
		running := l.running
		l.mu.Unlock()
		if !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if n := releases.Load(); n != 2 {
		t.Fatalf("letting the module go asked %d times in all", n)
	}
	l.mu.Lock()
	delete(l.cache, KeyOf(`y`, false))
	l.mu.Unlock()
}

// A formula is laid out off the caller's goroutine and kept, and one that
// cannot be is kept with its error; the styles are told apart.
func TestRequest(t *testing.T) {
	if r := wait(t, `x^2`, false); r.Err != nil || r.List == nil || len(r.List.Items) == 0 {
		t.Fatalf("x^2: %+v", r)
	}
	if r := wait(t, `\frac{1}{`, false); r.Err == nil || r.List != nil {
		t.Fatalf("an unclosed fraction: %+v", r)
	}
	inline, display := wait(t, `\sum_{i=1}^n i`, false), wait(t, `\sum_{i=1}^n i`, true)
	if inline.List == nil || display.List == nil || display.List.Height <= inline.List.Height {
		t.Fatalf("the sum in text style is %+v high, in display style %+v", inline.List, display.List)
	}
	called := false
	Request(KeyOf(`x^2`, false), `x^2`, false, func() { called = true })
	if !called {
		t.Fatal("a formula laid out is not told of at once")
	}
}

// Wait returns a formula once it is laid out, and gives up with its
// context.
func TestWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Wait(ctx, `\sqrt{w}`, true)
	if err != nil || r.List == nil || len(r.List.Items) == 0 {
		t.Fatalf("%+v, %v", r, err)
	}
	done, stop := context.WithCancel(context.Background())
	stop()
	if _, err := Wait(done, `\sqrt{never}`, true); err != context.Canceled {
		t.Fatalf("a context ended: %v", err)
	}
}
