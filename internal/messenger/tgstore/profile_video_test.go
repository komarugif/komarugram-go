// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"testing"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func videoPhoto(id int64) *tg.Photo {
	p := profilePhoto(id)
	p.VideoSizes = []tg.VideoSizeClass{
		&tg.VideoSize{Type: "p", W: 160, H: 160, Size: 10000},
		&tg.VideoSizeEmojiMarkup{EmojiID: 1},
		&tg.VideoSize{Type: "u", W: 800, H: 800, Size: 90000},
	}
	return p
}

// A photo set by a video tells it, the largest of it, downloaded by the photo's own
// reference; one without tells none, and an emoji's markup is no video.
func TestProfilePhotoTellsItsVideo(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 5, AccessHash: 6, FirstName: "Ann"}}, nil)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		return &tg.PhotosPhotos{Photos: []tg.PhotoClass{videoPhoto(1), profilePhoto(2)}}, nil
	})
	page, err := s.ProfilePhotos(context.Background(), 5, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("%d photos", len(page.Messages))
	}
	motion := page.Messages[0].Media.Motion
	if motion == nil || motion.MIMEType != "video/mp4" || motion.Width != 800 || motion.Size != 90000 {
		t.Fatalf("%+v", motion)
	}
	if page.Messages[1].Media.Motion != nil {
		t.Fatalf("a photo that is only that plays: %+v", page.Messages[1].Media.Motion)
	}
	ref := s.history.refs[motion.ID]
	if !ref.Photo || ref.ID != 1 || ref.Thumb != "u" || len(ref.Reference) == 0 {
		t.Fatalf("%+v", ref)
	}
	markup := profilePhoto(3)
	markup.VideoSizes = []tg.VideoSizeClass{&tg.VideoSizeEmojiMarkup{EmojiID: 1}}
	if largestVideoSize(markup) != nil {
		t.Fatal("an emoji's markup plays")
	}
}

// Its reference is renewed with the photo's, by which refreshProfilePhoto goes.
func TestProfileVideoReferenceRenewed(t *testing.T) {
	s := testStore(t)
	c := s.history
	c.mu.Lock()
	meta := c.rememberProfilePhoto(videoPhoto(1), 0)
	renewed := videoPhoto(1)
	renewed.FileReference = []byte("new")
	c.rememberProfilePhoto(renewed, 0)
	ref := c.refs[meta.Motion.ID]
	c.mu.Unlock()
	if string(ref.Reference) != "new" {
		t.Fatalf("%q", ref.Reference)
	}
}

// The photo a user shows now is told where Telegram lists it, which need not be first.
func TestProfilePhotosTellTheCurrent(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 5, AccessHash: 6, FirstName: "Ann", Photo: &tg.UserProfilePhoto{PhotoID: 3, DCID: 2}}}, nil)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		return &tg.PhotosPhotos{Photos: []tg.PhotoClass{profilePhoto(1), profilePhoto(2), videoPhoto(3)}}, nil
	})
	page, err := s.ProfilePhotos(context.Background(), 5, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Current != model.ProfilePhotoID(2) {
		t.Fatalf("current %v", page.Current)
	}
}
