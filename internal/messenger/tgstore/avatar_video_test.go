// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
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

// A file Telegram said to wait for (FLOOD_WAIT) is not asked for again
// before the wait is over: it was on every show of what draws it, and was
// told to wait again. Other files are asked for meanwhile.
func TestMediaWaitsAsTelegramSaid(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	waited := model.Message{Media: &model.MessageMedia{ID: "avatar/5/77"}}
	other := model.Message{Media: &model.MessageMedia{ID: "avatar/6/78"}}
	s.noteFloodWait(waited.Media.ID, 30*time.Second)
	_, err := s.Media(ctx, waited)
	if err == nil || !strings.Contains(err.Error(), "wait") {
		t.Fatalf("asked again during the wait: %v", err)
	}
	// Without a connection, the other is only not there.
	if _, err := s.Media(ctx, other); err == nil || strings.Contains(err.Error(), "wait") {
		t.Fatalf("another file waits too: %v", err)
	}
}

// A file refused again waits longer each time: Telegram may name a few
// seconds every time, and refuse it still.
func TestMediaRefusedAgainWaitsLonger(t *testing.T) {
	s := testStore(t)
	const key = "avatar/5/77"
	wait := func() time.Duration {
		s.history.mu.Lock()
		defer s.history.mu.Unlock()
		return time.Until(s.history.floodUntil[key].until)
	}
	s.noteFloodWait(key, 3*time.Second)
	first := wait()
	s.noteFloodWait(key, 3*time.Second)
	second := wait()
	if first < floodBackoff-time.Second || second < 2*floodBackoff-time.Second {
		t.Fatalf("waits %v, then %v; want %v, then twice that", first, second, floodBackoff)
	}
	for range 20 {
		s.noteFloodWait(key, 3*time.Second)
	}
	if w := wait(); w > floodBackoffMost {
		t.Fatalf("waits %v, more than %v", w, floodBackoffMost)
	}
}
