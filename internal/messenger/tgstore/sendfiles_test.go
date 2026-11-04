// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// filesServer records what sending files asks of Telegram.
type filesServer struct {
	uploads     int
	parts       map[int64][]byte
	uploaded    []tg.InputMediaClass
	singles     []*tg.MessagesSendMediaRequest
	albums      []*tg.MessagesSendMultiMediaRequest
	nextPhotoID int64
}

func (f *filesServer) handle(in bin.Encoder) (bin.Encoder, error) {
	switch r := in.(type) {
	case *tg.UploadSaveFilePartRequest:
		f.uploads++
		if f.parts == nil {
			f.parts = map[int64][]byte{}
		}
		f.parts[r.FileID] = append(f.parts[r.FileID], r.Bytes...)
		return &tg.BoolTrue{}, nil
	case *tg.UploadSaveBigFilePartRequest:
		return &tg.BoolTrue{}, nil
	case *tg.MessagesUploadMediaRequest:
		f.uploaded = append(f.uploaded, r.Media)
		f.nextPhotoID++
		if _, ok := r.Media.(*tg.InputMediaUploadedPhoto); ok {
			return &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: f.nextPhotoID, AccessHash: 7, FileReference: []byte{1}, Date: 1, DCID: 2}}, nil
		}
		return &tg.MessageMediaDocument{Document: &tg.Document{ID: 100 + f.nextPhotoID, AccessHash: 8, FileReference: []byte{2}, Date: 1, MimeType: "text/plain", DCID: 2}}, nil
	case *tg.MessagesSendMediaRequest:
		f.singles = append(f.singles, r)
		return &tg.Updates{}, nil
	case *tg.MessagesSendMultiMediaRequest:
		f.albums = append(f.albums, r)
		return &tg.Updates{}, nil
	}
	return nil, fmt.Errorf("unexpected %T", in)
}

func picture(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 50, A: 255})
		}
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func filesStore(t *testing.T) (*Store, *filesServer) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	server := new(filesServer)
	s.history.api = composerAPI(server.handle)
	return s, server
}

// Photos that can be grouped go in one album, in their order, the caption
// on the first of them; a picture too large for a photo is scaled down.
func TestSendFilesAsAnAlbum(t *testing.T) {
	s, server := filesStore(t)
	dir := t.TempDir()
	paths := []string{picture(t, dir, "a.png", 2000, 1000), picture(t, dir, "b.png", 30, 20), picture(t, dir, "c.png", 40, 40)}
	err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1000, Text: " Holiday ", ReplyTo: 4,
		Files: &model.OutgoingFiles{Paths: paths, Group: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(server.singles) != 0 || len(server.albums) != 1 || server.uploads != 3 || len(server.uploaded) != 3 {
		t.Fatalf("%d singles, %d albums, %d uploads, %d uploaded", len(server.singles), len(server.albums), server.uploads, len(server.uploaded))
	}
	for i, media := range server.uploaded {
		if _, ok := media.(*tg.InputMediaUploadedPhoto); !ok {
			t.Fatalf("file %d uploaded as %T", i, media)
		}
	}
	album := server.albums[0]
	if reply, ok := album.ReplyTo.(*tg.InputReplyToMessage); !ok || reply.ReplyToMsgID != 4 {
		t.Fatalf("reply %+v", album.ReplyTo)
	}
	for i, item := range album.MultiMedia {
		photo, ok := item.Media.(*tg.InputMediaPhoto)
		if !ok || photo.ID.(*tg.InputPhoto).ID != int64(i+1) {
			t.Fatalf("item %d: %+v", i, item.Media)
		}
		if item.RandomID != 1000+int64(i) {
			t.Errorf("item %d has identity %d", i, item.RandomID)
		}
		if want := map[bool]string{true: "Holiday"}[i == 0]; item.Message != want {
			t.Errorf("item %d caption %q, want %q", i, item.Message, want)
		}
	}
	// Sending it again after a failure asks for the same identities.
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1000, Text: "Holiday", ReplyTo: 4, Files: &model.OutgoingFiles{Paths: paths, Group: true}}); err != nil {
		t.Fatal(err)
	}
	for i, item := range server.albums[1].MultiMedia {
		if item.RandomID != album.MultiMedia[i].RandomID {
			t.Fatalf("the second try has other identities: %d, %d", item.RandomID, album.MultiMedia[i].RandomID)
		}
	}
}

// Without grouping, each file is a message; the caption goes on the last,
// and a file that is no photo goes as a document.
func TestSendFilesOneByOne(t *testing.T) {
	s, server := filesStore(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 50, Text: "Look",
		Files: &model.OutgoingFiles{Paths: []string{picture(t, dir, "a.png", 30, 20), text}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(server.albums) != 0 || len(server.singles) != 2 {
		t.Fatalf("%d singles, %d albums", len(server.singles), len(server.albums))
	}
	if _, ok := server.singles[0].Media.(*tg.InputMediaUploadedPhoto); !ok || server.singles[0].Message != "" || server.singles[0].RandomID != 50 {
		t.Fatalf("the photo: %+v", server.singles[0])
	}
	doc, ok := server.singles[1].Media.(*tg.InputMediaUploadedDocument)
	if !ok || !doc.ForceFile || doc.MimeType != "text/plain" || server.singles[1].Message != "Look" || server.singles[1].RandomID != 51 {
		t.Fatalf("the document: %+v", server.singles[1])
	}
	if name := doc.Attributes[0].(*tg.DocumentAttributeFilename).FileName; name != "notes.txt" {
		t.Fatalf("named %q", name)
	}
}

// "Send as documents" keeps photos as they are, as files.
func TestSendPhotosAsDocuments(t *testing.T) {
	s, server := filesStore(t)
	path := picture(t, t.TempDir(), "raw.png", 3000, 2000)
	err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 9, Files: &model.OutgoingFiles{Paths: []string{path}, Documents: true}})
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := server.singles[0].Media.(*tg.InputMediaUploadedDocument)
	if !ok || !doc.ForceFile || doc.MimeType != "image/png" {
		t.Fatalf("sent %+v", server.singles[0].Media)
	}
	if server.singles[0].Message != "" {
		t.Fatalf("a caption out of nowhere: %q", server.singles[0].Message)
	}
}

func TestSendFilesTellsWhichFileFailed(t *testing.T) {
	s, server := filesStore(t)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 9, Files: &model.OutgoingFiles{Paths: []string{picture(t, dir, "ok.png", 8, 8), empty}}})
	if err == nil || !strings.Contains(err.Error(), "empty.png") {
		t.Fatalf("error %v", err)
	}
	if len(server.singles) != 0 || server.uploads != 0 {
		t.Fatal("something was sent before the files were checked")
	}
	if err := (model.OutgoingMessage{RandomID: 1, Files: &model.OutgoingFiles{}}).Validate(); err == nil {
		t.Fatal("no files validated")
	}
}

// song writes an MP3 of n silent frames, 1152 samples each at 44.1 kHz,
// whose ID3v2.3 tag names it and holds a cover when cover is set.
func song(t *testing.T, dir, name, title string, n int, cover []byte) string {
	t.Helper()
	frame := func(id string, data []byte) []byte {
		return append(append([]byte(id), binary.BigEndian.AppendUint32(nil, uint32(len(data)))...), append([]byte{0, 0}, data...)...)
	}
	body := append(frame("TIT2", append([]byte{3}, title...)), frame("TPE1", []byte("\x03Исполнитель"))...)
	if cover != nil {
		body = append(body, frame("APIC", append([]byte("\x00image/png\x00\x03\x00"), cover...))...)
	}
	size := len(body)
	data := append([]byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}, body...)
	audio := make([]byte, 417)
	copy(audio, []byte{0xff, 0xfb, 0x90, 0x64})
	data = append(data, bytes.Repeat(audio, n)...)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Music goes as a track, as Telegram Desktop sends it: its tags and length
// in an audio attribute, its cover as the thumbnail; even with "send as
// documents", and tracks grouped go in an album of their own.
func TestSendMusicAsTracks(t *testing.T) {
	s, server := filesStore(t)
	dir := t.TempDir()
	cover, err := os.ReadFile(picture(t, dir, "cover.png", 500, 500))
	if err != nil {
		t.Fatal(err)
	}
	// 1915 frames are 50.02 s: whole seconds are 50.
	one := song(t, dir, "one.mp3", "Первая", 1915, cover)
	err = s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 70, Text: "Послушай", Files: &model.OutgoingFiles{Paths: []string{one}, Documents: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(server.singles) != 1 {
		t.Fatalf("%d singles", len(server.singles))
	}
	doc, ok := server.singles[0].Media.(*tg.InputMediaUploadedDocument)
	if !ok || doc.ForceFile || doc.MimeType != "audio/mpeg" || doc.Thumb == nil || server.singles[0].Message != "Послушай" {
		t.Fatalf("the track: %+v", server.singles[0].Media)
	}
	if name := doc.Attributes[0].(*tg.DocumentAttributeFilename).FileName; name != "one.mp3" {
		t.Fatalf("named %q", name)
	}
	audio, ok := doc.Attributes[1].(*tg.DocumentAttributeAudio)
	if !ok || audio.Voice || audio.Title != "Первая" || audio.Performer != "Исполнитель" || audio.Duration != 50 {
		t.Fatalf("audio %+v", doc.Attributes[1])
	}

	two := song(t, dir, "two.mp3", "Вторая", 100, nil)
	err = s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 80, Files: &model.OutgoingFiles{Paths: []string{one, two, picture(t, dir, "a.png", 30, 20)}, Group: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(server.albums) != 1 || len(server.albums[0].MultiMedia) != 2 || len(server.singles) != 2 {
		t.Fatalf("%d albums, %d singles", len(server.albums), len(server.singles))
	}
	for i, media := range server.uploaded {
		doc, ok := media.(*tg.InputMediaUploadedDocument)
		if !ok || len(doc.Attributes) != 2 {
			t.Fatalf("track %d uploaded as %+v", i, media)
		}
		if _, ok := doc.Attributes[1].(*tg.DocumentAttributeAudio); !ok {
			t.Fatalf("track %d without an audio attribute", i)
		}
	}
	if doc := server.uploaded[1].(*tg.InputMediaUploadedDocument); doc.Thumb != nil {
		t.Fatal("a track without a cover sent with a thumbnail")
	}
}

// clip makes a short video of the size with FFmpeg.
func clip(t *testing.T, ffmpeg, name, size string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=s="+size+":d=0.2", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	return path
}

// sentThumbnail is the size of the JPEG doc was sent with.
func sentThumbnail(t *testing.T, server *filesServer, doc *tg.InputMediaUploadedDocument) image.Point {
	t.Helper()
	file, ok := doc.Thumb.(*tg.InputFile)
	if !ok {
		t.Fatalf("thumbnail %+v", doc.Thumb)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(server.parts[file.ID]))
	if err != nil || format != "jpeg" {
		t.Fatalf("thumbnail is %q: %v", format, err)
	}
	return image.Pt(cfg.Width, cfg.Height)
}

// A video goes with its first picture as the thumbnail, in a square of 320
// pixels, as media, as a file and in an album: Telegram makes none itself of
// a large file, which shows as an empty box.
func TestSendVideoWithThumbnail(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	s, server := filesStore(t)
	wide, tall := clip(t, ffmpeg, "wide.mp4", "640x360"), clip(t, ffmpeg, "tall.webm", "360x640")
	send := func(random int64, documents, group bool, paths ...string) {
		t.Helper()
		err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: random, FFmpeg: ffmpeg, Files: &model.OutgoingFiles{Paths: paths, Documents: documents, Group: group}})
		if err != nil {
			t.Fatal(err)
		}
	}
	send(1, false, false, wide)
	doc := server.singles[0].Media.(*tg.InputMediaUploadedDocument)
	if got := sentThumbnail(t, server, doc); doc.ForceFile || got != image.Pt(320, 180) {
		t.Fatalf("as media, force file %v, thumbnail %v", doc.ForceFile, got)
	}
	send(2, false, false, tall)
	doc = server.singles[1].Media.(*tg.InputMediaUploadedDocument)
	if got := sentThumbnail(t, server, doc); got != image.Pt(180, 320) {
		t.Fatalf("tall thumbnail %v", got)
	}
	send(3, true, false, wide)
	doc = server.singles[2].Media.(*tg.InputMediaUploadedDocument)
	if got := sentThumbnail(t, server, doc); !doc.ForceFile || got != image.Pt(320, 180) {
		t.Fatalf("as a file, force file %v, thumbnail %v", doc.ForceFile, got)
	}
	send(10, false, true, wide, tall)
	if len(server.uploaded) != 2 || len(server.albums) != 1 {
		t.Fatalf("%d uploaded, %d albums", len(server.uploaded), len(server.albums))
	}
	for _, media := range server.uploaded {
		sentThumbnail(t, server, media.(*tg.InputMediaUploadedDocument))
	}
}

// A video a phone recorded turned says the size it shows at, the thumbnail's
// shape, not the size it is stored at.
func TestSendTurnedVideoSaysItsShape(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	stored := clip(t, ffmpeg, "stored.mp4", "640x360")
	turned := filepath.Join(t.TempDir(), "turned.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-display_rotation", "90", "-i", stored, "-c", "copy", turned).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot turn a video: %v %s", err, out)
	}
	s, server := filesStore(t)
	err = s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, FFmpeg: ffmpeg, Files: &model.OutgoingFiles{Paths: []string{turned}}})
	if err != nil {
		t.Fatal(err)
	}
	doc := server.singles[0].Media.(*tg.InputMediaUploadedDocument)
	if got := sentThumbnail(t, server, doc); got != image.Pt(180, 320) {
		t.Fatalf("thumbnail %v", got)
	}
	if v, ok := doc.Attributes[1].(*tg.DocumentAttributeVideo); !ok || v.W != 360 || v.H != 640 {
		t.Fatalf("attributes %+v", doc.Attributes)
	}
}

// Sending as a file asks nothing of FFmpeg: without one, or with a video it
// cannot read, the file goes without a thumbnail.
func TestSendVideoFileWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s, server := filesStore(t)
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, Files: &model.OutgoingFiles{Paths: []string{path}, Documents: true}})
	if err != nil {
		t.Fatal(err)
	}
	if doc := server.singles[0].Media.(*tg.InputMediaUploadedDocument); !doc.ForceFile || doc.Thumb != nil {
		t.Fatalf("sent %+v", doc)
	}
}
