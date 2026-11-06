// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
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

// The account's photos may each be deleted, a group's current one only, and any
// that is not the main one may become it, as Telegram Desktop's viewer offers.
func TestProfilePhotoEditorRights(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1, Photo: &tg.UserProfilePhoto{PhotoID: 5, DCID: 2}}, &tg.User{ID: 2, AccessHash: 2, Photo: &tg.UserProfilePhoto{PhotoID: 8, DCID: 2}}},
		[]tg.ChatClass{&tg.Chat{ID: 10, Photo: &tg.ChatPhoto{PhotoID: 6, DCID: 2}}})
	self, other, group := peerID(&tg.PeerUser{UserID: 1}), peerID(&tg.PeerUser{UserID: 2}), peerID(&tg.PeerChat{ChatID: 10})
	c := s.history
	c.mu.Lock()
	photo := func(chat int64, id int64, i int) model.Message {
		meta := c.rememberProfilePhoto(profilePhoto(id), 0)
		return model.Message{Key: model.MessageKey{ChatID: chat, MessageID: model.ProfilePhotoID(i)}, Kind: model.MessagePhoto, Media: meta}
	}
	ownCurrent, ownOld := photo(self, 5, 0), photo(self, 4, 1)
	otherOld := photo(other, 9, 1)
	groupCurrent, groupOld := photo(group, 6, 0), photo(group, 7, 1)
	c.mu.Unlock()
	for _, x := range []struct {
		name         string
		m            model.Message
		remove, main bool
	}{
		{"own current", ownCurrent, true, false},
		{"own old", ownOld, true, true},
		{"someone's", otherOld, false, false},
		{"group's current", groupCurrent, true, false},
		{"group's old", groupOld, false, true},
	} {
		if got := s.CanDeleteProfilePhoto(x.m); got != x.remove {
			t.Errorf("%s: delete %v", x.name, got)
		}
		if got := s.CanMakeProfilePhotoMain(x.m); got != x.main {
			t.Errorf("%s: main %v", x.name, got)
		}
	}
}

// Deleting the current photo takes it off the profile, another of the account's is
// deleted; making one main sets it, with its reference renewed where it expired.
func TestProfilePhotoEditorAsks(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1, Photo: &tg.UserProfilePhoto{PhotoID: 5, DCID: 2}}},
		[]tg.ChatClass{&tg.Chat{ID: 10, Photo: &tg.ChatPhoto{PhotoID: 6, DCID: 2}}})
	self, group := peerID(&tg.PeerUser{UserID: 1}), peerID(&tg.PeerChat{ChatID: 10})
	c := s.history
	c.mu.Lock()
	photo := func(chat int64, id int64, i int) model.Message {
		meta := c.rememberProfilePhoto(profilePhoto(id), 0)
		return model.Message{Key: model.MessageKey{ChatID: chat, MessageID: model.ProfilePhotoID(i)}, Kind: model.MessagePhoto, Media: meta}
	}
	ownCurrent, ownOld, groupCurrent, groupOld := photo(self, 5, 0), photo(self, 4, 1), photo(group, 6, 0), photo(group, 7, 1)
	c.mu.Unlock()
	var asked []string
	expire := true
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.PhotosUpdateProfilePhotoRequest:
			switch p := r.ID.(type) {
			case *tg.InputPhotoEmpty:
				asked = append(asked, "own off")
				return &tg.PhotosPhoto{Photo: &tg.PhotoEmpty{}, Users: []tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1}}}, nil
			case *tg.InputPhoto:
				asked = append(asked, fmt.Sprintf("own main %d", p.ID))
				return &tg.PhotosPhoto{Photo: &tg.PhotoEmpty{}, Users: []tg.UserClass{&tg.User{ID: 1, Self: true, AccessHash: 1, Photo: &tg.UserProfilePhoto{PhotoID: p.ID, DCID: 2}}}}, nil
			}
		case *tg.PhotosDeletePhotosRequest:
			p := r.ID[0].(*tg.InputPhoto)
			if expire {
				expire = false
				return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			asked = append(asked, fmt.Sprintf("own delete %d", p.ID))
			return &tg.LongVector{Elems: []int64{p.ID}}, nil
		case *tg.PhotosGetUserPhotosRequest:
			// The reference renewed.
			return &tg.PhotosPhotos{Photos: []tg.PhotoClass{profilePhoto(4)}}, nil
		case *tg.MessagesEditChatPhotoRequest:
			switch p := r.Photo.(type) {
			case *tg.InputChatPhotoEmpty:
				asked = append(asked, "group off")
			case *tg.InputChatPhoto:
				asked = append(asked, fmt.Sprintf("group main %d", p.ID.(*tg.InputPhoto).ID))
			}
			return &tg.Updates{}, nil
		}
		t.Errorf("asked %T", in)
		return nil, errors.New("unexpected")
	})
	ctx := context.Background()
	for _, step := range []func() error{
		func() error { return s.DeleteProfilePhoto(ctx, ownOld) },
		func() error { return s.MakeProfilePhotoMain(ctx, ownOld) },
		func() error { return s.DeleteProfilePhoto(ctx, groupCurrent) },
		func() error { return s.MakeProfilePhotoMain(ctx, groupOld) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	// ownOld is the main one now, and ownCurrent no more: deleting it deletes it.
	if s.CanMakeProfilePhotoMain(ownOld) || !s.CanMakeProfilePhotoMain(ownCurrent) {
		t.Error("the main photo did not change")
	}
	if err := s.DeleteProfilePhoto(ctx, ownOld); err != nil {
		t.Fatal(err)
	}
	want := []string{"own delete 4", "own main 4", "group off", "group main 7", "own off"}
	if fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
	s.history.mu.Lock()
	gone := s.history.peers[self].Photo
	s.history.mu.Unlock()
	if gone != nil {
		t.Fatalf("own photo still %+v", gone)
	}
}
