// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

type fakeCDNPool struct {
	testMediaPool
	cdn   *tg.Client
	opens atomic.Int32
}

func (p *fakeCDNPool) CDN(context.Context, int, int64) (downloader.CDN, io.Closer, error) {
	p.opens.Add(1)
	return p.cdn, io.NopCloser(bytes.NewReader(nil)), nil
}

// newFakeCDN serves a file through a CDN; with nominal, the hash of its
// last part gives the whole size of a part, as a CDN may, whose bytes end
// earlier.
func newFakeCDN(t *testing.T, tamper, nominal bool) (*fakeCDNPool, []byte, *atomic.Int32) {
	t.Helper()
	plain := make([]byte, streamPartSize*2+123)
	for i := range plain {
		plain[i] = byte(i*37 + 19)
	}
	key := bytes.Repeat([]byte{41}, 32)
	iv := bytes.Repeat([]byte{17}, 16)
	var hashes []tg.FileHash
	for off := 0; off < len(plain); off += streamPartSize {
		end := min(len(plain), off+streamPartSize)
		hash := sha256.Sum256(plain[off:end])
		limit := end - off
		if nominal {
			limit = streamPartSize
		}
		hashes = append(hashes, tg.FileHash{Offset: int64(off), Limit: limit, Hash: hash[:]})
	}
	reuploads := new(atomic.Int32)
	requested := new(atomic.Bool)
	respond := func(out bin.Decoder, value bin.Encoder) error {
		b := new(bin.Buffer)
		if e := value.Encode(b); e != nil {
			return e
		}
		return out.Decode(b)
	}
	pool := new(fakeCDNPool)
	pool.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.UploadGetFileRequest:
			if req.Offset >= int64(len(plain)) {
				return fmt.Errorf("VOLUME_LOC_NOT_FOUND: origin read past known EOF")
			}
			if !req.CDNSupported {
				return fmt.Errorf("CDN support flag missing")
			}
			return respond(out, &tg.UploadFileCDNRedirect{DCID: 203, FileToken: []byte("token"), EncryptionKey: key, EncryptionIv: iv, FileHashes: hashes[:1]})
		case *tg.UploadGetCDNFileHashesRequest:
			// Vector result has a generated wrapper rather than a TL constructor.
			result := out.(*tg.FileHashVector)
			result.Elems = append(result.Elems[:0], hashes...)
			return nil
		case *tg.UploadReuploadCDNFileRequest:
			reuploads.Add(1)
			result := out.(*tg.FileHashVector)
			result.Elems = append(result.Elems[:0], hashes...)
			return nil
		default:
			return fmt.Errorf("unexpected origin RPC %T", req)
		}
	}))
	pool.cdn = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.UploadGetCDNFileRequest)
		if !ok {
			return fmt.Errorf("unexpected CDN RPC %T", in)
		}
		if !requested.Swap(true) {
			return respond(out, &tg.UploadCDNFileReuploadNeeded{RequestToken: []byte("reupload")})
		}
		if req.Offset >= int64(len(plain)) {
			return fmt.Errorf("VOLUME_LOC_NOT_FOUND: CDN read past known EOF")
		}
		end := min(len(plain), int(req.Offset)+req.Limit)
		data := append([]byte(nil), plain[int(req.Offset):end]...)
		block, _ := aes.NewCipher(key)
		counter := append([]byte(nil), iv...)
		binary.BigEndian.PutUint32(counter[12:], uint32(req.Offset/16))
		cipher.NewCTR(block, counter).XORKeyStream(data, data)
		if tamper && len(data) > 0 {
			data[0] ^= 1
		}
		return respond(out, &tg.UploadCDNFile{Bytes: data})
	}))
	return pool, plain, reuploads
}
func TestCDNMediaAndStreamingVerifyBeforeCaching(t *testing.T) {
	for _, c := range []struct{ stream, tamper, nominal bool }{
		{false, false, false}, {false, true, false}, {true, false, false}, {true, true, false},
		{false, false, true}, {true, false, true}, {true, true, true},
	} {
		stream, tamper := c.stream, c.tamper
		{
			t.Run(fmt.Sprintf("stream=%v/tamper=%v/nominal=%v", stream, tamper, c.nominal), func(t *testing.T) {
				s := testStore(t)
				pool, plain, reuploads := newFakeCDN(t, tamper, c.nominal)
				s.history.pool = pool
				s.history.refs["cdn"] = fileLocation{ID: 1, DC: 2}
				msg := model.Message{Media: &model.MessageMedia{ID: "cdn", Size: int64(len(plain))}}
				var data []byte
				var err error
				if stream {
					reader, _, close, e := s.MediaStream(context.Background(), msg)
					if e != nil {
						t.Fatal(e)
					}
					defer close()
					// Seek into a hash window missing from the redirect, then cross into EOF.
					offset := int64(streamPartSize + 31)
					data = make([]byte, len(plain)-int(offset))
					_, err = reader.ReadAt(data, offset)
					plain = plain[offset:]
				} else {
					data, err = s.Media(context.Background(), msg)
				}
				if tamper {
					if err == nil {
						t.Fatal("accepted corrupted CDN bytes")
					}
					if b, _ := s.Cache().Media(context.Background(), "cdn"); len(b) > 0 {
						t.Fatal("cached unauthenticated file")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(data, plain) {
					t.Fatal("wrong decrypted content")
				}
				if pool.opens.Load() == 0 || reuploads.Load() == 0 {
					t.Fatal("CDN/reupload path not exercised")
				}
			})
		}
	}
}
