// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// A profile video whose file reference expired is renewed from the
// profile's full info, as it was first found. It was of no message, and
// was asked of messages.getMessages for message 0, which renewed nothing:
// each time the chat list showed the avatar again, upload.getFile failed,
// and messages.getMessages followed.
func TestAvatarVideoReferenceRenewed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const user = 5
	s.history.peers[user] = peerRecord{Kind: "user", ID: user, Hash: 55}
	reference := "old"
	var asked []string
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		asked = append(asked, fmt.Sprintf("%T", in))
		switch in.(type) {
		case *tg.UsersGetFullUserRequest:
			*out.(*tg.UsersUserFull) = tg.UsersUserFull{FullUser: tg.UserFull{ProfilePhoto: &tg.Photo{
				ID: 77, AccessHash: 1, DCID: 2, FileReference: []byte(reference),
				VideoSizes: []tg.VideoSizeClass{&tg.VideoSize{Type: "u", W: 160, H: 160, Size: 90000}},
			}}}
			return nil
		}
		return fmt.Errorf("unexpected %T", in)
	}))

	video, err := s.AvatarVideo(ctx, "avatar/5/77/video")
	if err != nil {
		t.Fatal(err)
	}
	reference, asked = "new", nil
	if err := s.refreshReference(ctx, video); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "*tg.UsersGetFullUserRequest" {
		t.Fatalf("asked %q, want the profile's full info", asked)
	}
	s.history.mu.Lock()
	got := string(s.history.refs[video.Media.ID].Reference)
	s.history.mu.Unlock()
	if got != "new" {
		t.Fatalf("the reference is %q, want the new one", got)
	}
	// Kept for the next start too.
	if again, err := s.AvatarVideo(ctx, "avatar/5/77/video"); err != nil || again.Media.ID != video.Media.ID {
		t.Fatalf("kept %+v (%v)", again.Media, err)
	}
	var ref fileLocation
	if _, err := s.Cache().Get(ctx, "ref/"+video.Media.ID, &ref); err != nil || string(ref.Reference) != "new" {
		t.Fatalf("the cache keeps the reference %q (%v)", ref.Reference, err)
	}
}

// An avatar whose photo Telegram no longer has (FILE_ID_INVALID), changed
// since its peer was read, is of the peer asked again: the new photo shows.
// Still the same, it is not asked for again. Each show of the avatar asked
// for the photo gone.
func TestAvatarGoneAsksForThePeer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	chat := peerID(&tg.PeerUser{UserID: 5})
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 55, Photo: &avatarRecord{ID: 77, DC: 2}}
	photo := int64(88)
	asked := 0
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.UsersGetUsersRequest); !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		asked++
		out.(*tg.UserClassVector).Elems = []tg.UserClass{&tg.User{ID: 5, AccessHash: 55, FirstName: "Ann", Photo: &tg.UserProfilePhoto{PhotoID: photo, DCID: 2}}}
		return nil
	}))
	key := func() string {
		m, ok := s.Avatar(chat)
		if !ok {
			return ""
		}
		return m.Media.ID
	}
	if k := key(); k != "avatar/5/77" {
		t.Fatalf("the avatar is %q", k)
	}
	s.avatarGone(ctx, "avatar/5/77")
	if k := key(); k != "avatar/5/88" || asked != 1 {
		t.Fatalf("the avatar is %q after %d requests, want the new photo's", k, asked)
	}
	// Telegram has this one no more either, and tells it still.
	s.avatarGone(ctx, "avatar/5/88")
	if k := key(); k != "" {
		t.Fatalf("an avatar Telegram does not have is %q", k)
	}
	// What is not a picture of an avatar is not asked of the peer.
	s.avatarGone(ctx, "avatar/5/88/video/u")
	if asked != 2 {
		t.Fatalf("%d requests, want 2", asked)
	}
}
