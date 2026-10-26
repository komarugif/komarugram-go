// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

const gifBotChat = 777

func botMessage(id model.MessageID, text string) model.Message {
	return model.Message{Key: model.MessageKey{ChatID: gifBotChat, MessageID: id}, Text: text}
}

// The bot's answer is read after the forward only: its status message, as
// it is edited, then the GIF it sends.
func TestWaitGIFBot(t *testing.T) {
	var w chatWatch
	messages, stop := w.add(gifBotChat)
	defer stop()
	other, stopOther := w.add(gifBotChat + 1)
	defer stopOther()
	gif := model.Message{Key: model.MessageKey{ChatID: gifBotChat, MessageID: 13}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", Size: 3 << 20}}
	forward := model.Message{Key: model.MessageKey{ChatID: gifBotChat, MessageID: 10}, Kind: model.MessageVideo, Outgoing: true, Media: &model.MessageMedia{ID: "video"}}
	for _, m := range []model.Message{
		// An edit of an answer to another video, before.
		botMessage(9, "Processing...\nProgress: 99.00%"),
		forward,
		botMessage(11, "You are in the queue. Your position: 1"),
		botMessage(11, "Downloading file..."),
		botMessage(11, "Processing...\nResolution: 1280x720 -> 1280x720\nProgress: 50.00%\nFrames 50/100"),
		botMessage(12, "Hey! Send a video to this bot and get it as a GIF."),
		gif,
	} {
		w.deliver(m)
	}
	var got []model.GIFProgress
	res, last, err := waitGIFBot(context.Background(), messages, time.Second, func(p model.GIFProgress) { got = append(got, p) })
	if err != nil || res.Media.ID != "gif" || last != 13 {
		t.Fatalf("got %+v, last %d, %v", res, last, err)
	}
	if len(got) != 3 || got[0].Stage != model.GIFQueued || got[1].Stage != model.GIFDownloading || got[2].Stage != model.GIFEncoding || got[2].Share != .5 {
		t.Fatalf("progress %+v", got)
	}
	if len(other) != 0 {
		t.Fatal("another chat's watcher got the bot's messages")
	}
	stop()
	w.deliver(gif)
	if len(w.subs) != 1 {
		t.Fatalf("watchers left: %v", w.subs)
	}
}

// The bot's failure, a GIF too large, its silence and a cancel end the
// wait with their errors.
func TestWaitGIFBotFails(t *testing.T) {
	forward := model.Message{Key: model.MessageKey{ChatID: gifBotChat, MessageID: 10}, Outgoing: true, Media: &model.MessageMedia{ID: "video"}}
	large := model.Message{Key: model.MessageKey{ChatID: gifBotChat, MessageID: 12}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif", Size: model.GIFMaxBytes + 1}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name     string
		ctx      context.Context
		messages []model.Message
		want     error
	}{
		{"failed", context.Background(), []model.Message{forward, botMessage(11, "An error occurred")}, model.ErrGIFBotFailed},
		{"busy", context.Background(), []model.Message{forward, botMessage(11, "You can send no more than 2 videos at a time!")}, model.ErrGIFBotBusy},
		{"large", context.Background(), []model.Message{forward, botMessage(11, "Sending a gif..."), large}, model.ErrGIFTooLarge},
		// What the bot says before the forward is of another video.
		{"silent", context.Background(), []model.Message{botMessage(9, "Sending a gif..."), forward}, model.ErrGIFBotSilent},
		{"cancelled", ctx, []model.Message{forward}, context.Canceled},
	} {
		ch := make(chan model.Message, len(c.messages))
		for _, m := range c.messages {
			ch <- m
		}
		_, _, err := waitGIFBot(c.ctx, ch, 50*time.Millisecond, func(model.GIFProgress) {})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

// A conversion cancelled while the bot converts sends the bot its cancel,
// after the forward, and reads the bot's answer to it.
func TestConvertGIFByBotCancels(t *testing.T) {
	s := testStore(t)
	s.SetGhost(model.Ghost{SendRead: true})
	var mu sync.Mutex
	var asked []string
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		mu.Lock()
		defer mu.Unlock()
		switch r := in.(type) {
		case *tg.MessagesForwardMessagesRequest:
			asked = append(asked, fmt.Sprintf("forward %v", r.ID))
			return &tg.Updates{}, nil
		case *tg.MessagesSendMessageRequest:
			asked = append(asked, "send "+r.Message)
			// The bot's status, as the updates bring it.
			go s.watch.deliver(model.Message{Key: model.MessageKey{ChatID: 900, MessageID: 12}, Text: "Cancelled."})
			return &tg.Updates{}, nil
		case *tg.MessagesReadHistoryRequest:
			asked = append(asked, fmt.Sprintf("read %d", r.MaxID))
			return &tg.MessagesAffectedMessages{}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	s.rememberPeers([]tg.UserClass{
		&tg.User{ID: 5, AccessHash: 55, FirstName: "Friend"},
		&tg.User{ID: 900, AccessHash: 99, Username: model.GIFBot, Bot: true},
	}, nil)
	video := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 3}, Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "video", Size: 1 << 20}}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.ConvertGIFByBot(ctx, video, func(model.GIFProgress) {
			select {
			case <-started:
			default:
				close(started)
			}
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled with %v", err)
	}
	took := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(took()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := took(); len(got) != 3 || got[0] != "forward [3]" || got[1] != "send "+model.GIFBotCancel || got[2] != "read 12" {
		t.Fatalf("asked %q", got)
	}
}

// The bot's messages are read once its GIF came, and the GIF is
// remembered as the video's: made again, the same is saved.
func TestConvertGIFByBotReadsAndRemembers(t *testing.T) {
	s := testStore(t)
	s.SetGhost(model.Ghost{SendRead: true})
	var mu sync.Mutex
	var asked []string
	gif := model.Message{Key: model.MessageKey{ChatID: 900, MessageID: 14}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/77", Size: 1 << 20}}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		mu.Lock()
		defer mu.Unlock()
		switch r := in.(type) {
		case *tg.MessagesForwardMessagesRequest:
			asked = append(asked, "forward")
			go func() {
				s.watch.deliver(model.Message{Key: model.MessageKey{ChatID: 900, MessageID: 12}, Outgoing: true, Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "video"}})
				s.watch.deliver(model.Message{Key: model.MessageKey{ChatID: 900, MessageID: 13}, Text: "Downloading file..."})
				s.watch.deliver(gif)
			}()
			return &tg.Updates{}, nil
		case *tg.MessagesReadHistoryRequest:
			asked = append(asked, fmt.Sprintf("read %d", r.MaxID))
			return &tg.MessagesAffectedMessages{}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	s.rememberPeers([]tg.UserClass{
		&tg.User{ID: 5, AccessHash: 55, FirstName: "Friend"},
		&tg.User{ID: 900, AccessHash: 99, Username: model.GIFBot, Bot: true},
	}, nil)
	video := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 3}, Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "document/50", Size: 1 << 20}}
	got, err := s.ConvertGIFByBot(context.Background(), video, func(model.GIFProgress) {})
	if err != nil || got.Media.ID != "document/77" {
		t.Fatalf("got %+v, %v", got, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(asked)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if len(asked) != 2 || asked[1] != "read 14" {
		t.Errorf("asked %q", asked)
	}
	mu.Unlock()
	// The same video, sent elsewhere, is the same document.
	again := video
	again.Key = model.MessageKey{ChatID: 6, MessageID: 8}
	if made, ok := s.MadeGIF(context.Background(), again); !ok || made.Media.ID != "document/77" {
		t.Fatalf("made %+v, %v", made, ok)
	}
}

// A GIF made here is uploaded as Telegram Desktop uploads one, which
// Telegram keeps animated: with nosound_video it keeps a video, and
// messages.saveGif refuses it with GIF_ID_INVALID.
func TestSaveGIFFile(t *testing.T) {
	s := testStore(t)
	path := filepath.Join(t.TempDir(), "made.mp4")
	if err := os.WriteFile(path, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	gifs := map[int64]bool{}
	var saved []int64
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		mu.Lock()
		defer mu.Unlock()
		switch r := in.(type) {
		case *tg.UploadSaveFilePartRequest:
			return &tg.BoolTrue{}, nil
		case *tg.MessagesUploadMediaRequest:
			up := r.Media.(*tg.InputMediaUploadedDocument)
			if _, ok := r.Peer.(*tg.InputPeerSelf); !ok {
				t.Errorf("uploaded to %T", r.Peer)
			}
			// As Telegram does: the attributes asked for, Animated
			// dropped by nosound_video.
			d := &tg.Document{ID: 41, AccessHash: 4141, FileReference: []byte("ref"), MimeType: up.MimeType, Size: 3}
			for _, a := range up.Attributes {
				if _, animated := a.(*tg.DocumentAttributeAnimated); animated && up.NosoundVideo {
					continue
				}
				d.Attributes = append(d.Attributes, a)
			}
			kind, _, _ := documentMedia(d)
			gifs[d.ID] = kind == model.MessageGIF && d.MimeType == "video/mp4"
			return &tg.MessageMediaDocument{Document: d}, nil
		case *tg.MessagesSaveGifRequest:
			doc := r.ID.(*tg.InputDocument)
			if !gifs[doc.ID] || doc.AccessHash != 4141 || string(doc.FileReference) != "ref" {
				return nil, tgerr.New(400, "GIF_ID_INVALID")
			}
			saved = append(saved, doc.ID)
			return &tg.BoolTrue{}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	source := model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "document/50"}}
	if err := s.SaveGIFFile(context.Background(), source, path, 640, 360, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0] != 41 {
		t.Fatalf("saved %v", saved)
	}
	if ids := savedIDs(s); len(ids) == 0 || ids[0] != "document/41" {
		t.Fatalf("saved GIFs %v", ids)
	}
	if made, ok := s.MadeGIF(context.Background(), source); !ok || made.Media.ID != "document/41" {
		t.Fatalf("made %+v, %v", made, ok)
	}
}
