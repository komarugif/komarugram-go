// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// maxFloods is how many FLOOD_WAIT one download sits out before it gives
// up. gotd's reader sits out every one, the seconds Telegram names, and
// asks again: a file Telegram refused every time, with a few seconds each,
// was asked for for as long as its download ran.
const maxFloods = 3

// floodRefused is the FLOOD_WAIT a download gave up at, which gotd does
// not sit out: see noteFloodWait for what waits then.
type floodRefused struct{ wait time.Duration }

func (f *floodRefused) Error() string {
	return fmt.Sprintf("Telegram refused the file %d times, the last asking to wait %v", maxFloods, f.wait)
}

// Parallel downloaders probe past EOF to discover a file's length. A CDN can
// respond to such a probe with ReuploadNeeded, then VOLUME_LOC_NOT_FOUND. Use
// Telegram's known byte length to stop those probes before they reach the RPC.
type sizedDownload struct {
	downloader.Client
	size int64
	// floods counts the FLOOD_WAIT of the download: see maxFloods. The
	// first is told to onFlood at once, as the download may be cancelled
	// before it gives up: then the next asked for the file again at once.
	floods  *atomic.Int32
	onFlood func(time.Duration)
}

func (s sizedDownload) UploadGetFile(ctx context.Context, r *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	if s.size > 0 && r.Offset >= s.size {
		return &tg.UploadFile{Type: &tg.StorageFileUnknown{}}, nil
	}
	res, err := s.Client.UploadGetFile(ctx, r)
	wait, ok := tgerr.AsFloodWait(err)
	if !ok || s.floods == nil {
		return res, err
	}
	n := s.floods.Add(1)
	if n == 1 && s.onFlood != nil {
		s.onFlood(wait)
	}
	if n >= maxFloods {
		return nil, &floodRefused{wait}
	}
	return res, err
}

type sizedCDNDownload struct {
	sizedDownload
	provider downloader.CDNProvider
}

func (s sizedCDNDownload) CDN(ctx context.Context, dc int, max int64) (downloader.CDN, io.Closer, error) {
	client, close, err := s.provider.CDN(ctx, dc, max)
	if err != nil {
		return nil, nil, err
	}
	return sizedCDN{client, s.size}, close, nil
}

type sizedCDN struct {
	downloader.CDN
	size int64
}

func (s sizedCDN) UploadGetCDNFile(ctx context.Context, r *tg.UploadGetCDNFileRequest) (tg.UploadCDNFileClass, error) {
	if s.size > 0 && r.Offset >= s.size {
		return &tg.UploadCDNFile{}, nil
	}
	return s.CDN.UploadGetCDNFile(ctx, r)
}

// downloadPart is how much of a file one upload.getFile asks for: gotd's
// part.
const downloadPart = 512 << 10

// downloadFile downloads the file at location, of size bytes (0 when not
// known), with client into out: four parts at a time when it has more than
// one, one otherwise. The parallel download starts each of its threads at
// once at a part of its own, and only the size known keeps them from
// Telegram past the end: a small file whose size is not known, as an avatar
// of a few kilobytes, cost four upload.getFile, three of them empty.
//
// onFlood, when not nil, is told the wait of the first FLOOD_WAIT.
func downloadFile(ctx context.Context, client downloader.Client, location tg.InputFileLocationClass, size int64, out io.WriterAt, onFlood func(time.Duration)) error {
	threads := 1
	if size > downloadPart {
		threads = 4
	}
	_, err := downloader.NewDownloader().WithPartSize(downloadPart).WithAllowCDN(true).Download(sizedClient(client, size, onFlood), location).WithThreads(threads).Parallel(ctx, out)
	return err
}

func withDownloadSize(client downloader.Client, size int64) downloader.Client {
	return sizedClient(client, size, nil)
}

func sizedClient(client downloader.Client, size int64, onFlood func(time.Duration)) downloader.Client {
	base := sizedDownload{client, size, new(atomic.Int32), onFlood}
	if provider, ok := client.(downloader.CDNProvider); ok {
		return sizedCDNDownload{base, provider}
	}
	return base
}
