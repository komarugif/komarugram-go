// SPDX-License-Identifier: Unlicense OR MIT

// Package formula lays out the formulas of messages: RaTeX (pkg/ratex) on
// a goroutine of its own, as codehighlight colors code, so that a frame
// never waits for a formula; the formulas laid out last are kept. The
// module is let go after a while without formulas, and its memory with
// it.
package formula

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"komarugram/pkg/ratex"
)

// cacheSize is how many formulas are kept laid out.
const cacheSize = 512

// idle is how long the module stays after the last formula; tests shorten
// it.
var idle = 2 * time.Minute

// Key identifies a formula: its source and its style.
type Key uint64

// KeyOf is the key of source laid out in display style or in text style.
func KeyOf(source string, display bool) Key {
	h := fnv.New64a()
	if display {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write([]byte(source))
	return Key(h.Sum64())
}

// Result is a formula laid out, or why it could not be: List is nil then.
type Result struct {
	List *ratex.List
	Err  error
}

type request struct {
	key     Key
	source  string
	display bool
}

// layouts is the process's: what it laid out, and what it is asked to.
var layouts struct {
	mu      sync.Mutex
	cache   map[Key]Result
	order   []Key
	pending map[Key][]func()
	queue   []request
	running bool
	wake    chan struct{}
	// open is how the module is opened; tests replace it.
	open func() (layouter, error)
	// release gives memory back to the system later (SetRelease).
	release func()
}

// SetRelease sets what gives memory back to the system a while later, as
// appwindow's ReleaseMemoryLater does. It is asked for once the module is
// opened, which leaves the garbage of compiling or loading it, 79 MB the
// first time and 24 MB from the cache, and once it is let go: the Go
// runtime returns such memory only gradually.
func SetRelease(release func()) {
	l := &layouts
	l.mu.Lock()
	l.release = release
	l.mu.Unlock()
}

// released asks for memory to be given back, if it can be.
func released() {
	l := &layouts
	l.mu.Lock()
	release := l.release
	l.mu.Unlock()
	if release != nil {
		release()
	}
}

// layouter is what lays formulas out: a ratex.Runtime.
type layouter interface {
	Layout(ctx context.Context, source string, display bool) (*ratex.List, error)
	Close(ctx context.Context) error
}

// Lookup returns the formula of key, if it is laid out or failed.
func Lookup(key Key) (Result, bool) {
	l := &layouts
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.cache[key]
	return r, ok
}

// Request asks for source to be laid out, in display style or not, under
// key. done runs, on another goroutine, once Lookup has it; at once when
// it does.
func Request(key Key, source string, display bool, done func()) {
	l := &layouts
	l.mu.Lock()
	if _, ok := l.cache[key]; ok {
		l.mu.Unlock()
		done()
		return
	}
	if l.pending == nil {
		l.pending = map[Key][]func(){}
	}
	waiting, queued := l.pending[key]
	l.pending[key] = append(waiting, done)
	if l.wake == nil {
		l.wake = make(chan struct{}, 1)
	}
	if !queued {
		l.queue = append(l.queue, request{key, source, display})
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
	start := !l.running
	l.running = true
	l.mu.Unlock()
	if start {
		go work()
	}
}

// work lays out what is queued, and lets the module go when nothing has
// been for a while.
func work() {
	l := &layouts
	ctx := context.Background()
	var module layouter
	var failed error
	defer func() {
		if module != nil {
			module.Close(ctx)
			released()
		}
	}()
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		l.mu.Lock()
		if len(l.queue) == 0 {
			wake := l.wake
			l.mu.Unlock()
			timer.Reset(idle)
			select {
			case <-wake:
			case <-timer.C:
				l.mu.Lock()
				if len(l.queue) == 0 {
					l.running = false
					l.mu.Unlock()
					return
				}
				l.mu.Unlock()
			}
			continue
		}
		r := l.queue[0]
		l.queue = l.queue[1:]
		open := l.open
		l.mu.Unlock()
		if module == nil && failed == nil {
			if open == nil {
				open = func() (layouter, error) { return ratex.NewRuntime(ctx, ratex.DefaultLimits) }
			}
			module, failed = open()
			released()
		}
		var result Result
		if failed != nil {
			result.Err = failed
		} else {
			result.List, result.Err = module.Layout(ctx, r.source, r.display)
		}
		l.mu.Lock()
		if l.cache == nil {
			l.cache = map[Key]Result{}
		}
		if _, ok := l.cache[r.key]; !ok {
			l.order = append(l.order, r.key)
		}
		l.cache[r.key] = result
		for len(l.order) > cacheSize {
			delete(l.cache, l.order[0])
			l.order = l.order[1:]
		}
		done := l.pending[r.key]
		delete(l.pending, r.key)
		l.mu.Unlock()
		for _, f := range done {
			f()
		}
	}
}

// Pending counts the formulas asked for and not laid out yet.
func Pending() int {
	l := &layouts
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending)
}

// Wait lays source out as Request does, and returns it once it is, or
// ctx's error when ctx ends first: for what needs a formula drawn now, as
// saving a message as HTML does.
func Wait(ctx context.Context, source string, display bool) (Result, error) {
	key := KeyOf(source, display)
	if r, ok := Lookup(key); ok {
		return r, nil
	}
	done := make(chan struct{})
	var once sync.Once
	Request(key, source, display, func() { once.Do(func() { close(done) }) })
	select {
	case <-done:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	r, ok := Lookup(key)
	if !ok {
		// Laid out, and let go from the cache since: lay it out again.
		return Wait(ctx, source, display)
	}
	return r, nil
}
