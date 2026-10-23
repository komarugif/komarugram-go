// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"komarugram/internal/messenger/model"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type testMediaPool struct{ api *tg.Client }

func (p testMediaPool) Client(context.Context, int) *tg.Client  { return p.api }
func (p testMediaPool) Default(context.Context) *tg.Client      { return p.api }
func (p testMediaPool) Takeout(context.Context, int) *tg.Client { return p.api }
func (p testMediaPool) Close() error                            { return nil }
func TestVideoRangesSeekCacheAndCancellation(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := model.Message{Media: &model.MessageMedia{ID: "video", Size: 2 << 30}}
	var mu sync.Mutex
	var requests []int64
	s.history.refs["video"] = fileLocation{ID: 1, DC: 2}
	s.history.pool = testMediaPool{tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.UploadGetFileRequest)
		if !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		mu.Lock()
		requests = append(requests, req.Offset)
		mu.Unlock()
		if req.Offset%streamPartSize != 0 || req.Limit != streamPartSize {
			t.Fatal("unaligned request", req)
		}
		data := bytes.Repeat([]byte{byte(req.Offset / streamPartSize)}, streamPartSize)
		buffer := new(bin.Buffer)
		if e := (&tg.UploadFile{Type: &tg.StorageFileMp4{}, Bytes: data}).Encode(buffer); e != nil {
			return e
		}
		return out.Decode(buffer)
	}))}
	reader, _, close, e := s.MediaStream(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	b := make([]byte, 16)
	off := int64(123*streamPartSize + streamPartSize - 8)
	if n, e := reader.ReadAt(b, off); n != 16 || e != nil {
		t.Fatal(n, e)
	}
	if !bytes.Equal(b[:8], bytes.Repeat([]byte{123}, 8)) || !bytes.Equal(b[8:], bytes.Repeat([]byte{124}, 8)) {
		t.Fatal("wrong range")
	}
	// The two parts read, and those after them fetched ahead: nothing
	// before them, nothing past the parts ahead.
	waitStream(t, reader)
	mu.Lock()
	seen := map[int64]bool{}
	for _, off := range requests {
		if off < 123*streamPartSize || off > (124+streamAhead)*streamPartSize || seen[off] {
			t.Fatal("downloaded beyond requested ranges", requests)
		}
		seen[off] = true
	}
	if !seen[123*streamPartSize] || !seen[124*streamPartSize] {
		t.Fatal("parts read not downloaded", requests)
	}
	mu.Unlock()
	// A second reader must work offline from encrypted-cache-compatible chunks.
	s.history.pool = nil
	again, _, done, e := s.MediaStream(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	defer done()
	if n, e := again.ReadAt(b, off); n != 16 || e != nil {
		t.Fatal("offline range", n, e)
	}
	if _, e := again.ReadAt(b, 0); e == nil {
		t.Fatal("uncached range silently succeeded")
	}
	if n, e := reader.ReadAt(b, m.Media.Size); n != 0 || e != io.EOF {
		t.Fatal("EOF", n, e)
	}
	close()
	if _, e := reader.ReadAt(b, off); e != context.Canceled {
		t.Fatal("cancelled cached read", e)
	}
}

// waitStream waits for the parts reader fetches ahead.
func waitStream(t *testing.T, reader io.ReaderAt) {
	t.Helper()
	r := reader.(*rangeReader)
	for range 500 {
		r.mu.Lock()
		n := len(r.pending)
		r.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("parts still fetched")
}

// A video read as it plays, over a connection with a round trip of 50 ms,
// does not wait for each part in turn: the parts after the one read are
// fetched meanwhile, several at once.
func TestVideoRangesAhead(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const parts = 24
	m := model.Message{Media: &model.MessageMedia{ID: "video", Size: parts * streamPartSize}}
	var inFlight, most atomic.Int32
	s.history.refs["video"] = fileLocation{ID: 1, DC: 2}
	s.history.pool = testMediaPool{tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.UploadGetFileRequest)
		if !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		buffer := new(bin.Buffer)
		if e := (&tg.UploadFile{Type: &tg.StorageFileMp4{}, Bytes: bytes.Repeat([]byte{byte(req.Offset / streamPartSize)}, streamPartSize)}).Encode(buffer); e != nil {
			return e
		}
		return out.Decode(buffer)
	}))}
	reader, _, done, e := s.MediaStream(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	defer done()
	start := time.Now()
	b := make([]byte, 32<<10)
	for off := int64(0); off < m.Media.Size; off += int64(len(b)) {
		if n, e := reader.ReadAt(b, off); n != len(b) || e != nil {
			t.Fatal(off, n, e)
		}
		if b[0] != byte(off/streamPartSize) {
			t.Fatal("wrong part at", off)
		}
	}
	took := time.Since(start)
	t.Logf("%d parts in %v, %d fetched at once at most", parts, took, most.Load())
	if most.Load() < 2 {
		t.Fatal("parts fetched one at a time")
	}
	if serial := parts * 50 * time.Millisecond; took > serial/2 {
		t.Fatalf("%v to read what a part at a time takes %v to", took, serial)
	}
	if most.Load() > streamParallel+1 {
		t.Fatalf("%d parts fetched at once", most.Load())
	}
}

func TestAvatarLocationAndThumbnailMetadata(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 42, AccessHash: 55, Photo: &tg.UserProfilePhoto{PhotoID: 9, DCID: 2, HasVideo: true}}}, nil)
	msg, ok := s.Avatar(42)
	if !ok || msg.Media.Thumbnail == nil {
		t.Fatal("video metadata missing")
	}
	ref := s.history.refs[msg.Media.ID]
	loc, ok := ref.input().(*tg.InputPeerPhotoFileLocation)
	if !ok || loc.PhotoID != 9 {
		t.Fatal("invalid avatar location")
	}
	if loc.Peer.(*tg.InputPeerUser).AccessHash != 55 {
		t.Fatal("missing avatar peer hash")
	}
	_, meta, _ := documentMedia(&tg.Document{ID: 1, MimeType: "video/mp4", Thumbs: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 320, H: 180, Size: 2000}}, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 1920, H: 1080}}})
	if meta.Width != 1920 || meta.Thumbnail == nil || meta.Thumbnail.Width != 320 {
		t.Fatal("video preview geometry")
	}
}
