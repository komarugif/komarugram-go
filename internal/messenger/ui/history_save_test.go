// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// noDownloads is the demo's store without its downloader.
type noDownloads struct{ model.ConversationStore }

// A photo saves into the pictures, a video, a GIF or a document through the
// downloader, the rest where the user chooses; nothing of a chat that
// protects its content, nor an album at once.
func TestSaveWay(t *testing.T) {
	p := newChatPage(mockstore.New(time.Now(), 0), func() {})
	media := &model.MessageMedia{ID: "m", Size: 10}
	for kind, want := range map[model.MessageKind]saveWay{
		model.MessagePhoto: saveToPictures, model.MessageVideo: saveToDownloads, model.MessageGIF: saveToDownloads,
		model.MessageFile: saveToDownloads, model.MessageVoice: saveAs, model.MessageMusic: saveAs,
		model.MessageRound: saveAs, model.MessageSticker: saveAs, model.MessageText: saveNone,
	} {
		if got := p.saveWayOf(model.Message{Kind: kind, Media: media}); got != want {
			t.Errorf("kind %d: %d, want %d", kind, got, want)
		}
	}
	if p.saveWayOf(model.Message{Kind: model.MessageVideo, Media: media, NoForwards: true}) != saveNone {
		t.Error("a protected video saves")
	}
	if p.saveWayOf(model.Message{Kind: model.MessagePhoto, Media: media, Attachments: make([]model.Message, 2)}) != saveNone {
		t.Error("an album saves")
	}
	if p.saveWayOf(model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "s", MIMEType: "application/x-tgsticker"}}) != saveNone {
		t.Error("a Lottie sticker saves as it is")
	}
	other := newChatPage(noDownloads{mockstore.New(time.Now(), 0)}, func() {})
	if other.saveWayOf(model.Message{Kind: model.MessageVideo, Media: media}) != saveAs {
		t.Error("a video without a downloader is not saved where the user chooses")
	}
}

// Download puts a video in the downloads and tells where; Save As writes
// the file whole where it was chosen, and nothing beside it.
func TestSaveMessage(t *testing.T) {
	t.Chdir("../../..")
	store := mockstore.New(time.Now(), 0)
	p := newChatPage(store, func() {})
	l := localization.For("en")
	video := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 4}, Kind: model.MessageVideo, Date: time.Date(2026, 10, 21, 19, 14, 15, 0, time.Local),
		Media: &model.MessageMedia{ID: "demo/video", MIMEType: "video/mp4", Size: 7789765}}
	p.saveMessage(video, saveToDownloads, l)
	want, _ := os.ReadFile("assets/video.mp4")
	path := filepath.Join(appDownloadsDir(), "video_2026-10-21_19-14-15.mp4")
	notice := ""
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		p.errorMu.Lock()
		notice = p.notice
		err := p.mediaError
		p.errorMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if notice != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not downloaded")
		}
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) || !strings.Contains(notice, path) {
		t.Fatalf("%q, %d bytes", notice, len(got))
	}
	if list, _ := store.Downloads(context.Background()); len(list) != 1 || list[0].Kind != model.MessageVideo {
		t.Fatal("not in the downloads:", list)
	}
	dir := t.TempDir()
	voice := model.Message{Kind: model.MessageVoice, Media: &model.MessageMedia{ID: "demo/voice", MIMEType: "audio/ogg", Size: 1}}
	target := filepath.Join(dir, "voice.ogg")
	if err := p.writeMedia(context.Background(), voice, target); err != nil {
		t.Fatal(err)
	}
	wantVoice, _ := store.Media(context.Background(), voice)
	if got, _ := os.ReadFile(target); !bytes.Equal(got, wantVoice) {
		t.Fatal("the voice message saved is not the one sent")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatal("files left beside it:", len(entries))
	}
}
