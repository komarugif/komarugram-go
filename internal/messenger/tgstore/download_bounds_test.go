// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// fileServer serves one file over upload.getFile and counts the requests.
type fileServer struct {
	downloader.Client
	data  []byte
	mu    sync.Mutex
	asked int
	// refuse is how many requests are refused with FLOOD_WAIT, -1 for all.
	refuse int
}

func (f *fileServer) UploadGetFile(_ context.Context, r *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	f.mu.Lock()
	f.asked++
	refused := f.refuse < 0 || f.asked <= f.refuse
	f.mu.Unlock()
	if refused {
		return nil, tgerr.New(420, "FLOOD_WAIT_0")
	}
	from := min(int(r.Offset), len(f.data))
	to := min(from+r.Limit, len(f.data))
	return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: f.data[from:to]}, nil
}

// A file of one part costs one upload.getFile, its size known or not: the
// threads of a parallel download each asked for a part at once, and an
// avatar of a few kilobytes, whose size is not known, cost four. A larger
// file is still read four parts at a time.
func TestDownloadAsksOncePerPart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		size, told int
		want       int
	}{
		{"an avatar", 20 << 10, 0, 1},
		{"a small file", 20 << 10, 20 << 10, 1},
		{"a large file", 3*downloadPart + 100, 3*downloadPart + 100, 4},
	} {
		srv := &fileServer{data: bytes.Repeat([]byte{7}, tc.size)}
		out := &cappedFile{}
		if err := downloadFile(context.Background(), srv, &tg.InputPeerPhotoFileLocation{}, int64(tc.told), out, nil); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !bytes.Equal(out.data, srv.data) {
			t.Fatalf("%s: %d bytes downloaded of %d", tc.name, len(out.data), tc.size)
		}
		if srv.asked != tc.want {
			t.Errorf("%s: %d upload.getFile, want %d", tc.name, srv.asked, tc.want)
		}
	}
}

// A download Telegram refuses with FLOOD_WAIT every time gives up after a
// few: gotd sat out each, and asked again, for as long as the download
// ran. One refused once is waited for, and goes on.
func TestDownloadGivesUpOnARefusedFile(t *testing.T) {
	// gotd sits out a second past what Telegram names.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv := &fileServer{data: bytes.Repeat([]byte{7}, 20<<10), refuse: -1}
	err := downloadFile(ctx, srv, &tg.InputPeerPhotoFileLocation{}, 0, &cappedFile{}, nil)
	var refused *floodRefused
	if !errors.As(err, &refused) || srv.asked != maxFloods {
		t.Fatalf("%d requests, and %v; want %d, and the file refused", srv.asked, err, maxFloods)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv = &fileServer{data: bytes.Repeat([]byte{7}, 20<<10), refuse: 1}
	out := &cappedFile{}
	if err := downloadFile(ctx, srv, &tg.InputPeerPhotoFileLocation{}, 0, out, nil); err != nil || len(out.data) != len(srv.data) {
		t.Fatalf("a file refused once: %d bytes, %v", len(out.data), err)
	}
}

// The first FLOOD_WAIT of a download is told at once: a download cancelled
// before it gave up, as one of a row scrolled away, told nothing, and the
// next asked for the file again at once.
func TestDownloadTellsTheFirstRefusal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &fileServer{data: bytes.Repeat([]byte{7}, 20<<10), refuse: -1}
	var told []time.Duration
	err := downloadFile(ctx, srv, &tg.InputPeerPhotoFileLocation{}, 0, &cappedFile{}, func(wait time.Duration) {
		told = append(told, wait)
		cancel() // scrolled away
	})
	if !errors.Is(err, context.Canceled) || len(told) != 1 || srv.asked != 1 {
		t.Fatalf("%v after %d requests, told %v; want the first refusal told", err, srv.asked, told)
	}
}
