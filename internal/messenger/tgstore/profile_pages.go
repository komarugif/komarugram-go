// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tgerr"
)

// profilePageTTL is how long a page of a profile's photos is kept. Opening
// an avatar again and again asks Telegram for the same thing each time,
// channels.getFullChannel among it, which Telegram limits hard; a change
// of the photos by this client forgets the pages at once.
const profilePageTTL = time.Minute

type profilePageKey struct {
	chat   int64
	offset string
	limit  int
}

// profilePage is a page being read or read. Whoever asks for the page that
// is being read waits for it, so the request is made once, and goes on
// when the one who asked has gone: the page is kept for the next.
type profilePage struct {
	done chan struct{}
	page model.GalleryPage
	err  error
	// at is when the page was read; until, when Telegram let the request be
	// made again after a FLOOD_WAIT, which the next ones are told of
	// without asking.
	at, until time.Time
}

// profilePages are the pages of profile photos read lately.
type profilePages struct {
	mu    sync.Mutex
	pages map[profilePageKey]*profilePage
}

// get returns the page for key, read by read unless one is kept or on its
// way. The page is a copy, which the caller may change.
func (p *profilePages) get(ctx context.Context, key profilePageKey, read func(context.Context) (model.GalleryPage, error)) (model.GalleryPage, error) {
	p.mu.Lock()
	now := time.Now()
	for k, e := range p.pages {
		if finished(e) && now.Sub(e.at) > profilePageTTL && now.After(e.until) {
			delete(p.pages, k)
		}
	}
	e := p.pages[key]
	if e != nil && finished(e) && e.err != nil && now.After(e.until) {
		// A failure is not kept, but for the wait Telegram asked for.
		e = nil
	}
	if e == nil {
		e = &profilePage{done: make(chan struct{})}
		if p.pages == nil {
			p.pages = map[profilePageKey]*profilePage{}
		}
		p.pages[key] = e
		go func() {
			defer close(e.done)
			defer crash.Recover("profile photos", func(c *crash.Panic) { e.err = c })
			// Not the caller's context: its end must not fail what others wait for.
			page, err := read(context.Background())
			e.page, e.err, e.at = page, err, time.Now()
			if wait, ok := tgerr.AsFloodWait(err); ok {
				e.until = e.at.Add(wait)
			}
		}()
	}
	p.mu.Unlock()
	select {
	case <-e.done:
	case <-ctx.Done():
		return model.GalleryPage{}, ctx.Err()
	}
	page := e.page
	page.Messages = slices.Clone(page.Messages)
	return page, e.err
}

func finished(e *profilePage) bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// forget drops the pages of chat's photos, after a change of them.
func (p *profilePages) forget(chat int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.pages {
		if k.chat == chat {
			delete(p.pages, k)
		}
	}
}
