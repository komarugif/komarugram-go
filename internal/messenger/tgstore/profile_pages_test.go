// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// profilePagesStore is a store of a group whose full info is asked for
// through full, and which counts what it is asked.
func profilePagesStore(t *testing.T, full func() (bin.Encoder, error)) (s *Store, group int64, asked *atomic.Int32) {
	t.Helper()
	s = testStore(t)
	s.rememberPeers(nil, []tg.ChatClass{&tg.Channel{ID: 9, Megagroup: true, Title: "group", AccessHash: 7}})
	group = peerID(&tg.PeerChannel{ChannelID: 9})
	asked = new(atomic.Int32)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.ChannelsGetFullChannelRequest:
			asked.Add(1)
			return full()
		case *tg.MessagesSearchRequest:
			return &tg.MessagesChannelMessages{Count: 1, Messages: []tg.MessageClass{
				&tg.MessageService{ID: 5, PeerID: &tg.PeerChannel{ChannelID: 9}, Action: &tg.MessageActionChatEditPhoto{Photo: profilePhoto(5)}},
			}}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	return s, group, asked
}

func currentPhoto() (bin.Encoder, error) {
	return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ChatPhoto: profilePhoto(3)}}, nil
}

// Opening an avatar again and again asks Telegram once: its full info is
// limited hard, and the pages are the same.
func TestProfilePhotosAskedOnce(t *testing.T) {
	s, group, asked := profilePagesStore(t, currentPhoto)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if page, err := s.ProfilePhotos(context.Background(), group, "", 100); err != nil || len(page.Messages) != 2 {
				t.Errorf("%d photos: %v", len(page.Messages), err)
			}
		})
	}
	wg.Wait()
	page, err := s.ProfilePhotos(context.Background(), group, "", 100)
	if err != nil || len(page.Messages) != 2 {
		t.Fatalf("%d photos: %v", len(page.Messages), err)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("the full info was asked for %d times", n)
	}
	// A change of the photos by the client is read at once.
	s.profilePages.forget(group)
	if _, err := s.ProfilePhotos(context.Background(), group, "", 100); err != nil || asked.Load() != 2 {
		t.Fatalf("after a change: asked %d times: %v", asked.Load(), err)
	}
}

// A caller that has gone does not fail the page the others wait for.
func TestProfilePhotosOutliveTheCaller(t *testing.T) {
	release := make(chan struct{})
	s, group, asked := profilePagesStore(t, func() (bin.Encoder, error) {
		<-release
		return currentPhoto()
	})
	gone, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := s.ProfilePhotos(gone, group, "", 100)
		result <- err
	}()
	for asked.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("a caller that has gone got a page")
	}
	close(release)
	page, err := s.ProfilePhotos(context.Background(), group, "", 100)
	if err != nil || len(page.Messages) != 2 || asked.Load() != 1 {
		t.Fatalf("%d photos, asked %d times: %v", len(page.Messages), asked.Load(), err)
	}
}

// What Telegram told to wait for is not asked again meanwhile; a failure
// of another kind is tried again at once.
func TestProfilePhotosFloodWait(t *testing.T) {
	failure := tgerr.New(420, "FLOOD_WAIT_3600")
	s, group, asked := profilePagesStore(t, func() (bin.Encoder, error) { return nil, failure })
	for range 3 {
		if _, err := s.ProfilePhotos(context.Background(), group, "", 100); !tgerr.Is(err, "FLOOD_WAIT") {
			t.Fatalf("error %v", err)
		}
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times while Telegram said to wait", n)
	}

	other := tgerr.New(500, "INTERNAL")
	s, group, asked = profilePagesStore(t, func() (bin.Encoder, error) { return nil, other })
	for range 3 {
		if _, err := s.ProfilePhotos(context.Background(), group, "", 100); err == nil {
			t.Fatal("no error")
		}
	}
	if n := asked.Load(); n != 3 {
		t.Fatalf("asked %d times", n)
	}
}
