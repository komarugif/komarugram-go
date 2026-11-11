// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/ffmpegtest"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/program"
	"komarugram/pkg/video"
)

func editedPhotoFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 40, 30)), nil); err != nil {
		t.Fatal(err)
	}
	return path
}

// A replacement uploads the file, tells how far it went, and edits it into the
// message, whose caption and formatting go with it as they are.
func TestReplaceMediaKeepsCaption(t *testing.T) {
	s := testStore(t)
	chat := int64(5)
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	var parts int
	var got *tg.MessagesEditMessageRequest
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.UploadSaveFilePartRequest:
			parts++
			return &tg.BoolTrue{}, nil
		case *tg.MessagesEditMessageRequest:
			got = r
			return &tg.Updates{}, nil
		case *tg.MessagesSetTypingRequest:
			return &tg.BoolTrue{}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	m := model.Message{Key: model.MessageKey{ChatID: chat, MessageID: 17}, Text: "caption", Entities: []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 4}}}
	var told int64
	if _, err := s.ReplaceMedia(context.Background(), m, editedPhotoFile(t), "", func(done, _ int64) { told = done }); err != nil {
		t.Fatal(err)
	}
	if parts == 0 || told == 0 {
		t.Errorf("%d parts, told %d", parts, told)
	}
	if got == nil || got.ID != 17 || got.Message != "caption" || len(got.Entities) != 1 {
		t.Fatalf("request %+v", got)
	}
	media, ok := got.Media.(*tg.InputMediaUploadedPhoto)
	if !ok || media.File == nil {
		t.Fatalf("media %#v", got.Media)
	}
}

// What comes back has the new file, as the update brought it, under the key it
// was asked by: a viewer shows that, not the file it has.
func TestReplaceMediaReturnsTheNewFile(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	chat := int64(5)
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[chat] = &model.History{}
	photo := func(id int64) *tg.MessageMediaPhoto {
		return &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: id, AccessHash: 1, FileReference: []byte{1}, Date: 10, DCID: 2,
			Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 800, H: 600, Size: 1000}}}}
	}
	raw := &tg.Message{ID: 17, Out: true, PeerID: &tg.PeerUser{UserID: 5}, Message: "caption", Date: 10, Media: photo(1)}
	msgs, err := s.ingest(ctx, []tg.MessageClass{raw}, false, 0)
	if err != nil || len(msgs) != 1 || msgs[0].Media == nil {
		t.Fatal(msgs, err)
	}
	old := msgs[0]
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.MessagesEditMessageRequest:
			edited := *raw
			edited.EditDate, edited.Media = 11, photo(2)
			return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &edited}}}, nil
		}
		return &tg.BoolTrue{}, nil
	})
	got, err := s.ReplaceMedia(ctx, old, editedPhotoFile(t), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Media == nil || got.Media.ID == old.Media.ID {
		t.Fatalf("the file stayed: %+v", got.Media)
	}
	if got.Key != old.Key || got.Text != "caption" {
		t.Errorf("came back as %+v", got)
	}
}

// A GIF replaced goes as a GIF, its attributes those a GIF is told by: Telegram
// would keep it a video otherwise.
func TestReplaceMediaKeepsAGIF(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	path := filepath.Join(t.TempDir(), "gif.mp4")
	if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.3:r=25", "-c:v", "libx264", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	var got *tg.MessagesEditMessageRequest
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if r, ok := in.(*tg.MessagesEditMessageRequest); ok {
			got = r
			return &tg.Updates{}, nil
		}
		return &tg.BoolTrue{}, nil
	})
	m := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 1}, Kind: model.MessageGIF}
	if _, err := s.ReplaceMedia(context.Background(), m, path, ffmpeg, nil); err != nil {
		t.Fatal(err)
	}
	media, ok := got.Media.(*tg.InputMediaUploadedDocument)
	if !ok || media.ForceFile {
		t.Fatalf("media %#v", got.Media)
	}
	var animated, sized bool
	for _, a := range media.Attributes {
		switch v := a.(type) {
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeVideo:
			sized = v.W == 160 && v.H == 120
		}
	}
	if !animated || !sized {
		t.Errorf("attributes %#v", media.Attributes)
	}
}

// Files sent together go each as it was asked: the video at a place named Animated
// as a GIF, the one beside it as a video, and what bans GIFs bans that one.
func TestSendFilesAnimatedByPlace(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")}
	for _, path := range paths {
		if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.3:r=25", "-c:v", "libx264", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	s, server := filesStore(t)
	animated := map[bool]int{}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if r, ok := in.(*tg.MessagesSendMediaRequest); ok {
			media, ok := r.Media.(*tg.InputMediaUploadedDocument)
			if !ok {
				return nil, fmt.Errorf("media %T", r.Media)
			}
			isGIF := false
			for _, a := range media.Attributes {
				if _, ok := a.(*tg.DocumentAttributeAnimated); ok {
					isGIF = true
				}
			}
			animated[isGIF]++
			return &tg.UpdateShortSentMessage{ID: int(r.RandomID), Date: 10}, nil
		}
		return server.handle(in)
	})
	msg := model.OutgoingMessage{RandomID: 700, FFmpeg: ffmpeg, Files: &model.OutgoingFiles{Paths: paths, Animated: []int{1}}}
	if err := s.Send(context.Background(), 5, msg); err != nil {
		t.Fatal(err)
	}
	if animated[true] != 1 || animated[false] != 1 {
		t.Errorf("GIFs and videos: %v", animated)
	}
	p := s.history.peers[5]
	p.Rights.Default = model.SendGIF
	s.history.peers[5] = p
	if err := s.checkSend(5, msg); err == nil {
		t.Error("a GIF went where they are banned")
	}
	msg.Files.Animated = nil
	if err := s.checkSend(5, msg); err != nil {
		t.Errorf("videos where only GIFs are banned: %v", err)
	}
}

// A video without sound in an album is asked to stay a video; alone, or as a file,
// it is sent as before.
func TestAlbumVideoStaysAVideo(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	dir := t.TempDir()
	clip := filepath.Join(dir, "silent.mp4")
	if out, err := program.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.3:r=25", "-c:v", "libx264", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	photo := picture(t, dir, "photo.png", 30, 20)
	send := func(id int64, files *model.OutgoingFiles) (nosound, seen bool) {
		t.Helper()
		s, server := filesStore(t)
		look := func(media tg.InputMediaClass) {
			if doc, ok := media.(*tg.InputMediaUploadedDocument); ok {
				nosound, seen = doc.NosoundVideo, true
			}
		}
		s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
			switch r := in.(type) {
			case *tg.MessagesUploadMediaRequest:
				look(r.Media)
			case *tg.MessagesSendMediaRequest:
				look(r.Media)
			}
			return server.handle(in)
		})
		if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: id, FFmpeg: ffmpeg, Files: files}); err != nil {
			t.Fatal(err)
		}
		return nosound, seen
	}
	if nosound, seen := send(710, &model.OutgoingFiles{Paths: []string{clip, photo}, Group: true}); !seen || !nosound {
		t.Errorf("in an album: seen %v, nosound %v", seen, nosound)
	}
	if nosound, seen := send(720, &model.OutgoingFiles{Paths: []string{clip}, Group: true}); !seen || nosound {
		t.Errorf("alone: seen %v, nosound %v", seen, nosound)
	}
	if nosound, seen := send(730, &model.OutgoingFiles{Paths: []string{clip, clip}, Group: true, Documents: true}); !seen || nosound {
		t.Errorf("as files: seen %v, nosound %v", seen, nosound)
	}
}

// A video alone goes as Telegram takes it, a GIF when it has no sound, unless the
// settings choose a video; one with sound never says it has none.
func TestSilentVideoAsChosen(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	dir := t.TempDir()
	silent, sounding := filepath.Join(dir, "silent.mp4"), filepath.Join(dir, "sounding.mp4")
	for _, args := range [][]string{
		{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.3:r=25", "-c:v", "libx264", "-pix_fmt", "yuv420p", silent},
		{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=160x120:d=0.3:r=25", "-f", "lavfi", "-t", "0.3", "-i", "sine", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", sounding},
	} {
		if out, err := program.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	for _, c := range []struct {
		path         string
		chosen, want bool
		documents    bool
	}{
		{silent, false, false, false},
		{silent, true, true, false},
		{sounding, true, false, false},
		{silent, true, false, true},
	} {
		s, server := filesStore(t)
		seen := false
		nosound := false
		s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
			if r, ok := in.(*tg.MessagesSendMediaRequest); ok {
				if doc, ok := r.Media.(*tg.InputMediaUploadedDocument); ok {
					seen, nosound = true, doc.NosoundVideo
				}
			}
			return server.handle(in)
		})
		msg := model.OutgoingMessage{RandomID: 740, FFmpeg: ffmpeg, Files: &model.OutgoingFiles{Paths: []string{c.path}, SilentAsVideo: c.chosen, Documents: c.documents}}
		if err := s.Send(context.Background(), 5, msg); err != nil {
			t.Fatal(err)
		}
		if !seen || nosound != c.want {
			t.Errorf("%s chosen %v documents %v: seen %v, nosound %v", filepath.Base(c.path), c.chosen, c.documents, seen, nosound)
		}
	}
}

// The time run out shows only once the file is up, and comes back as a reason.
func TestReplaceMediaErrors(t *testing.T) {
	for _, c := range []struct {
		tg   string
		want error
	}{
		{"MESSAGE_EDIT_TIME_EXPIRED", model.ErrEditForbidden},
		{"MEDIA_NEW_INVALID", model.ErrMediaInvalid},
	} {
		s := testStore(t)
		s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
		s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
			if _, ok := in.(*tg.MessagesEditMessageRequest); ok {
				return nil, tgerr.New(400, c.tg)
			}
			return &tg.BoolTrue{}, nil
		})
		m := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 1}}
		if _, err := s.ReplaceMedia(context.Background(), m, editedPhotoFile(t), "", nil); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.tg, err)
		}
	}
}

// The chat is told of a photo or a video going as such, and of a file otherwise.
func TestUploadAction(t *testing.T) {
	photo, clip := sendfiles.File{Kind: sendfiles.KindPhoto}, sendfiles.File{Kind: sendfiles.KindVideo}
	if got := uploadAction(photo, sendfiles.Way{}); got != model.ActionUploadPhoto {
		t.Errorf("photo: %v", got)
	}
	if got := uploadAction(clip, sendfiles.Way{}); got != model.ActionUploadVideo {
		t.Errorf("video: %v", got)
	}
	if got := uploadAction(clip, sendfiles.Way{Documents: true}); got != model.ActionUploadFile {
		t.Errorf("video as a file: %v", got)
	}
}
