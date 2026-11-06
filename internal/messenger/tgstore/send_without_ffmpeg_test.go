// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

const repoVideo = "../../../assets/video.mp4"

// Without FFmpeg an MP4 still goes as a video, with what its file says.
func TestVideoAttributesWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	attrs, err := uploadAttributes(context.Background(), repoVideo, "video/mp4", true, "")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := attrs[len(attrs)-1].(*tg.DocumentAttributeVideo)
	if !ok || v.W <= 0 || v.H <= 0 || v.Duration <= 0 {
		t.Fatalf("attributes %+v", attrs)
	}
	f, asFile, err := inspectOutgoing(context.Background(), repoVideo, "", false)
	if err != nil || asFile || f.Kind != sendfiles.KindVideo {
		t.Fatalf("%+v, as a file %v: %v", f, asFile, err)
	}
}

// What nothing can read goes as a file, not refused, and this is told once. Asked
// as documents, it is no news.
func TestUnreadableVideoGoesAsFile(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	clip := filepath.Join(t.TempDir(), "clip.avi")
	if err := os.WriteFile(clip, []byte("RIFF....AVI LIST and the rest of it"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f, asFile, err := inspectOutgoing(ctx, clip, "", false)
	if err != nil || !asFile || f.Kind != sendfiles.KindFile {
		t.Fatalf("%+v, as a file %v: %v", f, asFile, err)
	}
	if _, asFile, _ := inspectOutgoing(ctx, clip, "", true); asFile {
		t.Fatal("documents told of as gone as a file")
	}

	s := testStore(t)
	msg := model.OutgoingMessage{RandomID: 1, Files: &model.OutgoingFiles{Paths: []string{clip}}}
	pending, err := s.outgoingMessages(ctx, 5, msg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].msg.Kind != model.MessageFile {
		t.Fatalf("shown as %+v", pending)
	}
	if told, ok := s.TakeSendFailure(); !ok || !told.AsFile || told.NoFFprobe {
		t.Fatalf("told %+v, %v", told, ok)
	}
	if _, again := s.TakeSendFailure(); again {
		t.Fatal("told twice")
	}

	// In an album it goes apart from the media: an album of media takes no file.
	groups := sendfiles.Divide([]sendfiles.File{{Kind: sendfiles.KindVideo}, f, {Kind: sendfiles.KindVideo}}, sendfiles.Way{Group: true})
	if len(groups) != 3 {
		t.Fatalf("%d groups", len(groups))
	}
}

// An MP4 goes shown as a video, with nothing to tell of.
func TestReadableVideoShownAsVideo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := testStore(t)
	msg := model.OutgoingMessage{RandomID: 1, Files: &model.OutgoingFiles{Paths: []string{repoVideo}}}
	pending, err := s.outgoingMessages(context.Background(), 5, msg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].msg.Kind != model.MessageVideo || pending[0].msg.Media.Width <= 0 || pending[0].msg.Media.Duration <= 0 {
		t.Fatalf("shown as %+v", pending[0].msg)
	}
	if told, ok := s.TakeSendFailure(); ok {
		t.Fatalf("told %+v", told)
	}
}

// A video known to have no sound says so without FFmpeg too; one with sound does not.
func TestSilenceKnownWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if videoHasNoSound(context.Background(), repoVideo, "") {
		t.Fatal("the video in the repository has sound")
	}
	clip := filepath.Join(t.TempDir(), "clip.avi")
	if err := os.WriteFile(clip, []byte("RIFF....AVI"), 0o600); err != nil {
		t.Fatal(err)
	}
	if videoHasNoSound(context.Background(), clip, "") {
		t.Fatal("a file nothing reads is known to have no sound")
	}
}
