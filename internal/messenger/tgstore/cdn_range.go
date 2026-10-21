// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// A streaming seek may start inside a CDN hash window. Fetch and authenticate
// the whole window before returning any bytes, bounded to one MiB per window.
// Calls are serialized by rangeReader.cdnMu. Keys/tokens never enter disk caches.
type cdnRange struct {
	origin         downloader.Client
	provider       downloader.CDNProvider
	redirect       *tg.UploadFileCDNRedirect
	client         downloader.CDN
	closer         io.Closer
	verifiedOffset int64
	verified       []byte
}

func (c *cdnRange) close() {
	if c.closer != nil {
		c.closer.Close()
		c.closer = nil
	}
}
func (c *cdnRange) read(ctx context.Context, off int64, limit int) ([]byte, error) {
	if c.client == nil {
		var e error
		c.client, c.closer, e = c.provider.CDN(ctx, c.redirect.DCID, 2)
		if e != nil {
			return nil, e
		}
	}
	out := make([]byte, 0, limit)
	for len(out) < limit {
		pos := off + int64(len(out))
		if pos >= c.verifiedOffset && pos < c.verifiedOffset+int64(len(c.verified)) {
			start := int(pos - c.verifiedOffset)
			n := min(limit-len(out), len(c.verified)-start)
			out = append(out, c.verified[start:start+n]...)
			continue
		}
		h, err := c.hash(ctx, pos)
		if err != nil {
			return nil, err
		}
		data, err := c.window(ctx, h)
		if err != nil {
			return nil, err
		}
		c.verifiedOffset, c.verified = h.Offset, data
		if len(data) == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		// A final window may end before the requested range. File size bounds the
		// caller's ReadAt; returning a short chunk at EOF is legitimate.
		start := int(pos - h.Offset)
		n := min(limit-len(out), len(data)-start)
		if n <= 0 {
			return nil, io.ErrUnexpectedEOF
		}
		out = append(out, data[start:start+n]...)
	}
	return out, nil
}
func (c *cdnRange) hash(ctx context.Context, pos int64) (tg.FileHash, error) {
	find := func(hashes []tg.FileHash) (tg.FileHash, bool) {
		for _, h := range hashes {
			if h.Offset <= pos && h.Limit > 0 && pos-h.Offset < int64(h.Limit) {
				return h, true
			}
		}
		return tg.FileHash{}, false
	}
	if h, ok := find(c.redirect.FileHashes); ok {
		return h, nil
	}
	hashes, e := c.origin.UploadGetCDNFileHashes(ctx, &tg.UploadGetCDNFileHashesRequest{FileToken: c.redirect.FileToken, Offset: pos})
	if e != nil {
		return tg.FileHash{}, e
	}
	if h, ok := find(hashes); ok {
		return h, nil
	}
	return tg.FileHash{}, errors.New("CDN hash missing for requested range")
}
func (c *cdnRange) window(ctx context.Context, h tg.FileHash) ([]byte, error) {
	if h.Offset < 0 || h.Offset%4096 != 0 || h.Limit <= 0 || h.Limit > 1<<20 || h.Offset/(1<<20) != (h.Offset+int64(h.Limit)-1)/(1<<20) || len(h.Hash) != sha256.Size {
		return nil, errors.New("invalid CDN hash window")
	}
	if len(c.redirect.EncryptionKey) != 32 || len(c.redirect.EncryptionIv) != aes.BlockSize {
		return nil, errors.New("invalid CDN encryption parameters")
	}
	block, e := aes.NewCipher(c.redirect.EncryptionKey)
	if e != nil {
		return nil, e
	}
	data := make([]byte, 0, h.Limit)
	for len(data) < h.Limit {
		offset := h.Offset + int64(len(data))
		// 4 KiB reads fit every legal hash window and never cross a MiB boundary.
		limit := 4096
		if offset%streamPartSize == 0 && h.Limit-len(data) >= streamPartSize {
			limit = streamPartSize
		}
		var encrypted []byte
		for attempt := 0; attempt < 3; attempt++ {
			result, err := c.client.UploadGetCDNFile(ctx, &tg.UploadGetCDNFileRequest{FileToken: c.redirect.FileToken, Offset: offset, Limit: limit})
			if err != nil {
				return nil, err
			}
			switch result := result.(type) {
			case *tg.UploadCDNFile:
				encrypted = result.Bytes
			case *tg.UploadCDNFileReuploadNeeded:
				hashes, err := c.origin.UploadReuploadCDNFile(ctx, &tg.UploadReuploadCDNFileRequest{FileToken: c.redirect.FileToken, RequestToken: result.RequestToken})
				if err != nil {
					return nil, err
				}
				// Reupload hashes are trusted only as received from the origin DC.
				_ = hashes
				continue
			default:
				return nil, fmt.Errorf("unexpected CDN response %T", result)
			}
			break
		}
		if len(encrypted) > limit {
			return nil, errors.New("oversized CDN part")
		}
		// Fewer bytes than asked for are the end of the file, as Telegram
		// Desktop takes them: the hash of the last window may give a whole
		// part's size, and only the bytes there tell whether it ends.
		want := min(limit, h.Limit-len(data))
		take := min(len(encrypted), want)
		iv := append([]byte(nil), c.redirect.EncryptionIv...)
		if offset/16 > int64(^uint32(0)) {
			return nil, errors.New("CDN counter overflow")
		}
		binary.BigEndian.PutUint32(iv[12:], uint32(offset/16))
		plain := make([]byte, take)
		cipher.NewCTR(block, iv).XORKeyStream(plain, encrypted[:take])
		data = append(data, plain...)
		if take < want {
			break
		}
	}
	digest := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(digest[:], h.Hash) != 1 {
		return nil, errors.New("CDN SHA-256 verification failed")
	}
	return data, nil
}
func invalidCDNToken(err error) bool {
	return tgerr.Is(err, "FILE_TOKEN_INVALID", "REQUEST_TOKEN_INVALID")
}
