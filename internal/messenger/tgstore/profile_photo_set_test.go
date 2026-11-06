// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// Who may set a profile's photo is as Telegram Desktop's canEditInformation tells.
func TestCanSetProfilePhoto(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1}, &tg.User{ID: 2, AccessHash: 2}}, []tg.ChatClass{
		&tg.Chat{ID: 10},
		&tg.Chat{ID: 11, DefaultBannedRights: tg.ChatBannedRights{ChangeInfo: true}},
		&tg.Chat{ID: 12, Creator: true, DefaultBannedRights: tg.ChatBannedRights{ChangeInfo: true}},
		&tg.Channel{ID: 20, AccessHash: 1, Broadcast: true},
		&tg.Channel{ID: 21, AccessHash: 1, Broadcast: true, AdminRights: tg.ChatAdminRights{ChangeInfo: true}},
		&tg.Channel{ID: 22, AccessHash: 1, Megagroup: true},
		&tg.Channel{ID: 23, AccessHash: 1, Megagroup: true, Username: "open"},
		&tg.Channel{ID: 24, AccessHash: 1, Megagroup: true, BannedRights: tg.ChatBannedRights{ChangeInfo: true}},
	})
	for _, c := range []struct {
		chat int64
		want bool
	}{
		{peerID(&tg.PeerUser{UserID: 1}), true},
		{peerID(&tg.PeerUser{UserID: 2}), false},
		{peerID(&tg.PeerChat{ChatID: 10}), true},
		{peerID(&tg.PeerChat{ChatID: 11}), false},
		{peerID(&tg.PeerChat{ChatID: 12}), true},
		{peerID(&tg.PeerChannel{ChannelID: 20}), false},
		{peerID(&tg.PeerChannel{ChannelID: 21}), true},
		{peerID(&tg.PeerChannel{ChannelID: 22}), true},
		{peerID(&tg.PeerChannel{ChannelID: 23}), false},
		{peerID(&tg.PeerChannel{ChannelID: 24}), false},
	} {
		if got := s.CanSetProfilePhoto(c.chat); got != c.want {
			t.Errorf("chat %d: %v", c.chat, got)
		}
	}
}

// The account's own photo, a group's and a channel's are each set by their own
// request, with the picture uploaded; what Telegram refuses is told as such.
func TestSetProfilePhotoAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 300, 200)))
	f.Close()
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1}}, []tg.ChatClass{&tg.Chat{ID: 10}, &tg.Channel{ID: 22, AccessHash: 3, Megagroup: true}})
	var mu sync.Mutex
	var asked []string
	var refuse error
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		mu.Lock()
		defer mu.Unlock()
		switch r := in.(type) {
		case *tg.UploadSaveFilePartRequest, *tg.UploadSaveBigFilePartRequest:
			return &tg.BoolTrue{}, nil
		case *tg.PhotosUploadProfilePhotoRequest:
			asked = append(asked, "self")
			return &tg.PhotosPhoto{Photo: &tg.PhotoEmpty{}, Users: []tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1, Photo: &tg.UserProfilePhoto{PhotoID: 77, DCID: 2}}}}, nil
		case *tg.MessagesEditChatPhotoRequest:
			asked = append(asked, "chat")
			return &tg.Updates{}, refuse
		case *tg.ChannelsEditPhotoRequest:
			if c, ok := r.Channel.(*tg.InputChannel); !ok || c.ChannelID != 22 || c.AccessHash != 3 {
				t.Errorf("channel %+v", r.Channel)
			}
			asked = append(asked, "channel")
			return &tg.Updates{}, nil
		}
		t.Errorf("asked %T", in)
		return nil, errors.New("unexpected")
	})
	ctx := context.Background()
	self := peerID(&tg.PeerUser{UserID: 1})
	for _, chat := range []int64{self, peerID(&tg.PeerChat{ChatID: 10}), peerID(&tg.PeerChannel{ChannelID: 22})} {
		if err := s.SetProfilePhoto(ctx, chat, path, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(asked) != 3 || asked[0] != "self" || asked[1] != "chat" || asked[2] != "channel" {
		t.Fatalf("asked %v", asked)
	}
	// The account's own photo is known at once: no update tells it.
	s.history.mu.Lock()
	photo := s.history.peers[self].Photo
	s.history.mu.Unlock()
	if photo == nil || photo.ID != 77 {
		t.Fatalf("own photo %+v", photo)
	}
	refuse = tgerr.New(400, "CHAT_ADMIN_REQUIRED")
	if err := s.SetProfilePhoto(ctx, peerID(&tg.PeerChat{ChatID: 10}), path, nil); !errors.Is(err, model.ErrProfilePhotoForbidden) {
		t.Fatalf("refused: %v", err)
	}
}
