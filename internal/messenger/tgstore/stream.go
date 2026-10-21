// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/dcpool"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const (
	streamPartSize = 128 << 10
	// streamAhead parts past the one read are fetched before they are asked
	// for, streamParallel at once: a part a round trip to Telegram is slower
	// than a video plays, and a player that waits for each one stutters.
	streamAhead    = 8
	streamParallel = 4
)

// rangeReader fetches only aligned ranges requested by the external player (including seeks),
// and the parts that follow them.
// The decrypted working set is bounded independently of the file's total size.
type rangeReader struct {
	// cdnMu serializes the reads of cdn, whose verified window is shared;
	// it is taken before mu.
	cdnMu sync.Mutex
	// mu guards what follows; it is never held across a fetch.
	mu       sync.Mutex
	cdn      *cdnRange
	ctx      context.Context
	cancel   context.CancelFunc
	store    *Store
	message  model.Message
	location fileLocation
	size     int64
	chunks   map[int64][]byte
	order    []int64
	// pending are the parts being fetched, which a read of one waits for
	// rather than fetching it again.
	pending map[int64]*pendingPart
	// ahead holds a slot for each part fetched ahead.
	ahead chan struct{}
	// refreshMu lets one of the fetches that found the file reference
	// expired refresh it; refreshed counts the refreshes, for the others to
	// see theirs done.
	refreshMu sync.Mutex
	refreshed int
}

// pendingPart is a part being fetched: done closes when b or err is set.
type pendingPart struct {
	done chan struct{}
	b    []byte
	err  error
}

func (s *Store) MediaStream(ctx context.Context, m model.Message) (io.ReaderAt, int64, func(), error) {
	if m.Media == nil || m.Media.Size <= 0 {
		return nil, 0, nil, errors.New("video size unavailable")
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	ref, ok := c.refs[m.Media.ID]
	c.mu.Unlock()
	if cache == nil {
		return nil, 0, nil, errors.New("cache unavailable")
	}
	if b, e := cache.Media(ctx, m.Media.ID); e != nil {
		return nil, 0, nil, e
	} else if len(b) > 0 {
		return bytes.NewReader(b), int64(len(b)), func() {}, nil
	}
	if !ok {
		var e error
		ok, e = cache.Get(ctx, "ref/"+m.Media.ID, &ref)
		if e != nil {
			return nil, 0, nil, e
		}
		if !ok {
			return nil, 0, nil, errors.New("video location unavailable")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &rangeReader{ctx: ctx, cancel: cancel, store: s, message: m, location: ref, size: m.Media.Size,
		chunks: map[int64][]byte{}, pending: map[int64]*pendingPart{}, ahead: make(chan struct{}, streamParallel)}
	return r, r.size, func() {
		cancel()
		r.cdnMu.Lock()
		defer r.cdnMu.Unlock()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.cdn != nil {
			r.cdn.close()
		}
	}, nil
}
func (r *rangeReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative video offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= r.size {
		return 0, io.EOF
	}
	n := 0
	last := int64(-1)
	for len(p) > 0 && off < r.size {
		part := off / int64(streamPartSize) * int64(streamPartSize)
		if last < 0 {
			// The part the read begins in is asked for first, then the ones
			// after it, while it downloads.
			r.mu.Lock()
			r.start(part, false)
			r.mu.Unlock()
			r.readAhead(part)
		}
		last = part
		b, e := r.chunk(part)
		if e != nil {
			return n, e
		}
		inside := int(off - part)
		if inside >= len(b) {
			return n, io.ErrUnexpectedEOF
		}
		take := min(len(p), len(b)-inside)
		take = min(take, int(r.size-off))
		copy(p, b[inside:inside+take])
		n += take
		off += int64(take)
		p = p[take:]
	}
	if last >= 0 {
		r.readAhead(last)
	}
	if len(p) > 0 {
		return n, io.EOF
	}
	return n, nil
}

// readAhead starts fetching the streamAhead parts after part that are
// neither kept nor being fetched.
func (r *rangeReader) readAhead(part int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := int64(1); i <= streamAhead; i++ {
		next := part + i*int64(streamPartSize)
		if next >= r.size {
			break
		}
		r.start(next, true)
	}
}

// start begins fetching the part at offset unless it is kept or being
// fetched; ahead fetches wait for a slot. It runs with mu held.
func (r *rangeReader) start(offset int64, ahead bool) {
	if r.chunks[offset] != nil || r.pending[offset] != nil || r.ctx.Err() != nil {
		return
	}
	p := &pendingPart{done: make(chan struct{})}
	r.pending[offset] = p
	go func() {
		defer close(p.done)
		if ahead {
			select {
			case r.ahead <- struct{}{}:
				defer func() { <-r.ahead }()
			case <-r.ctx.Done():
				p.err = r.ctx.Err()
			}
		}
		if p.err == nil {
			p.b, p.err = r.load(offset)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.pending, offset)
		if p.err == nil {
			r.keep(offset, p.b)
		}
	}()
}

// keep holds the part at offset in memory, the oldest dropped past 64. It
// runs with mu held.
func (r *rangeReader) keep(offset int64, b []byte) {
	if r.chunks[offset] != nil {
		return
	}
	if len(r.order) >= 64 {
		delete(r.chunks, r.order[0])
		r.order = r.order[1:]
	}
	r.order = append(r.order, offset)
	r.chunks[offset] = b
}

// chunk is the part at offset, kept, or fetched with the reads that wait for
// it.
func (r *rangeReader) chunk(offset int64) ([]byte, error) {
	r.mu.Lock()
	if e := r.ctx.Err(); e != nil {
		r.mu.Unlock()
		return nil, e
	}
	if b := r.chunks[offset]; b != nil {
		r.mu.Unlock()
		return b, nil
	}
	r.start(offset, false)
	p := r.pending[offset]
	r.mu.Unlock()
	if p == nil {
		// Fetched between the two looks.
		return r.chunk(offset)
	}
	select {
	case <-p.done:
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
	if p.err != nil {
		if e := r.ctx.Err(); e != nil {
			return nil, e
		}
		return nil, p.err
	}
	return p.b, nil
}

// load reads the part at offset from the cache, or downloads and caches it.
func (r *rangeReader) load(offset int64) ([]byte, error) {
	if e := r.ctx.Err(); e != nil {
		return nil, e
	}
	c := r.store.history
	c.mu.Lock()
	cache, pool := c.cache, c.pool
	c.mu.Unlock()
	key := fmt.Sprintf("%s/range/%d", r.message.Media.ID, offset)
	b, e := cache.Media(r.ctx, key)
	if e != nil || len(b) > 0 {
		return b, e
	}
	if pool == nil {
		return nil, errors.New("video range is not cached; reconnect to Telegram")
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	r.mu.Lock()
	dc := r.location.DC
	r.mu.Unlock()
	origin := dcpool.DownloadClient(pool, ctx, dc)
	var refreshed int
	fetch := func() (tg.UploadFileClass, error) {
		r.mu.Lock()
		location := r.location.input()
		refreshed = r.refreshed
		r.mu.Unlock()
		return origin.UploadGetFile(ctx, &tg.UploadGetFileRequest{Precise: true, CDNSupported: true, Location: location, Offset: offset, Limit: streamPartSize})
	}
	for attempt := 0; attempt < 3; attempt++ {
		r.mu.Lock()
		cdn := r.cdn
		r.mu.Unlock()
		if cdn != nil {
			r.cdnMu.Lock()
			b, e = cdn.read(ctx, offset, int(min(int64(streamPartSize), r.size-offset)))
			if invalidCDNToken(e) {
				r.mu.Lock()
				if r.cdn == cdn {
					cdn.close()
					r.cdn = nil
				}
				r.mu.Unlock()
				r.cdnMu.Unlock()
				continue
			}
			r.cdnMu.Unlock()
			break
		}
		file, err := fetch()
		e = err
		if tgerr.Is(e, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_EMPTY", "FILE_REFERENCE_INVALID") {
			if e = r.refresh(ctx, refreshed); e != nil {
				return nil, e
			}
			continue
		}
		if e != nil {
			return nil, e
		}
		switch f := file.(type) {
		case *tg.UploadFile:
			b = f.Bytes
		case *tg.UploadFileCDNRedirect:
			provider, ok := origin.(downloader.CDNProvider)
			if !ok {
				return nil, errors.New("CDN transport unavailable")
			}
			r.mu.Lock()
			if r.cdn == nil {
				r.cdn = &cdnRange{origin: origin, provider: provider, redirect: f}
			}
			r.mu.Unlock()
			continue
		default:
			return nil, fmt.Errorf("unexpected file response %T", file)
		}
		break
	}
	if e != nil {
		return nil, e
	}
	if len(b) > streamPartSize {
		return nil, errors.New("oversized video range")
	}
	if len(b) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	if e = cache.SaveMedia(ctx, key, b); e != nil {
		return nil, e
	}
	return b, nil
}

// refresh asks Telegram for the file reference again, unless another fetch
// did since this one read it, at seen refreshes.
func (r *rangeReader) refresh(ctx context.Context, seen int) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.Lock()
	done := r.refreshed != seen
	r.mu.Unlock()
	if done {
		return nil
	}
	if e := r.store.refreshReference(ctx, r.message); e != nil {
		return e
	}
	c := r.store.history
	c.mu.Lock()
	location := c.refs[r.message.Media.ID]
	c.mu.Unlock()
	r.mu.Lock()
	r.location = location
	r.refreshed++
	r.mu.Unlock()
	return nil
}
