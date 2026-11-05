// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// webmAPI answers as Telegram does for a WebM: its file for what asks for no
// thumbnail, FILE_REFERENCE_EXPIRED for what asks for one, and its message as a
// file, of no shape, when it is asked for. messages counts those.
func webmAPI(t *testing.T, data []byte, messages *atomic.Int32) *tg.Client {
	t.Helper()
	doc := &tg.Document{ID: 7, AccessHash: 8, FileReference: []byte{1}, DCID: 2, MimeType: "video/webm", Size: int64(len(data)),
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "clip.webm"}}}
	return tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		var answer bin.Encoder
		switch req := in.(type) {
		case *tg.UploadGetFileRequest:
			if req.Location.(*tg.InputDocumentFileLocation).ThumbSize != "" {
				return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			var part []byte
			if req.Offset < int64(len(data)) {
				part = data[req.Offset:min(req.Offset+int64(req.Limit), int64(len(data)))]
			}
			answer = &tg.UploadFile{Type: &tg.StorageFilePartial{}, Bytes: part}
		case *tg.MessagesGetMessagesRequest:
			messages.Add(1)
			answer = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 2}, Date: 1, Media: &tg.MessageMediaDocument{Document: doc}}}}
		default:
			return fmt.Errorf("unexpected %T", in)
		}
		buffer := new(bin.Buffer)
		if err := answer.Encode(buffer); err != nil {
			return err
		}
		return out.Decode(buffer)
	}))
}

// The thumbnail of a WebM is made of its start, and only of that: Telegram has none,
// and asking for one fails. Its message then comes again with the shape.
func TestWebMFirstPicture(t *testing.T) {
	data, err := os.ReadFile("../../../assets/stickers/circle.webm")
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	var messages atomic.Int32
	api := webmAPI(t, data, &messages)
	s.history.api, s.history.pool = api, testMediaPool{api}
	s.history.peers[2] = peerRecord{Kind: "user", ID: 2, Hash: 3}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sent := &tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 2}, Date: 1, Media: &tg.MessageMediaDocument{Document: &tg.Document{
		ID: 7, AccessHash: 8, FileReference: []byte{1}, DCID: 2, MimeType: "video/webm", Size: int64(len(data)),
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "clip.webm"}},
		// What the answer to a sending may still tell of: not to be asked for.
		Thumbs: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 320, H: 180, Size: 100}},
	}}}
	shown, err := s.ingest(ctx, []tg.MessageClass{sent}, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := shown[0]
	if m.Kind != model.MessageVideo || m.Media.Thumbnail == nil {
		t.Fatalf("%+v", m)
	}
	picture, err := s.Media(ctx, m.WithMedia(m.Media.Thumbnail))
	if err != nil {
		t.Fatalf("the thumbnail: %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(picture))
	if err != nil || format != "jpeg" || cfg.Width != 320 || cfg.Height != 320 {
		t.Fatalf("%s of %dx%d: %v", format, cfg.Width, cfg.Height, err)
	}
	if messages.Load() != 1 {
		t.Fatalf("its message was asked for %d times", messages.Load())
	}
	again, err := s.ingest(ctx, []tg.MessageClass{sent}, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := again[0].Media; got.Width != 512 || got.Height != 512 || got.Thumbnail.Width != 512 || got.Duration <= 0 {
		t.Fatalf("come again as %dx%d of %v, its thumbnail %dx%d", got.Width, got.Height, got.Duration, got.Thumbnail.Width, got.Thumbnail.Height)
	}
	// Kept: nothing is read again.
	if _, err := s.Media(ctx, m.WithMedia(m.Media.Thumbnail)); err != nil || messages.Load() != 1 {
		t.Fatalf("again: %v, asked %d times", err, messages.Load())
	}
	// The file itself, as a video opens it.
	r, size, closeStream, err := s.MediaStream(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStream()
	got := make([]byte, size)
	if _, err := r.ReadAt(got, 0); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the file: %v", err)
	}
}

// What the client cannot decode shows as a plate, told as no failure each time it
// shows; a file that does not come is told.
func TestWebMFirstPictureUndecodable(t *testing.T) {
	data := []byte("not a video")
	s := testStore(t)
	var messages atomic.Int32
	api := webmAPI(t, data, &messages)
	s.history.api, s.history.pool = api, testMediaPool{api}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.history.refs["document/7/first"] = fileLocation{ID: 7, DC: 2, Thumb: "first", FileSize: int64(len(data))}
	s.history.refs["document/7"] = fileLocation{ID: 7, DC: 2, FileSize: int64(len(data))}
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 5}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "document/7/first"}}
	picture, err := s.Media(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(picture)); err != nil || format != "jpeg" {
		t.Fatalf("%s: %v", format, err)
	}
	if messages.Load() != 0 {
		t.Fatal("asked for a message with no shape to bring")
	}
	s.history.refs["document/8/first"] = fileLocation{ID: 8, DC: 2, Thumb: "first"}
	if _, err := s.Media(ctx, model.Message{Media: &model.MessageMedia{ID: "document/8/first"}}); err == nil {
		t.Fatal("a picture of a file of no known size")
	}
}
