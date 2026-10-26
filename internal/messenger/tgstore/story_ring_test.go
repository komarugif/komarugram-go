// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"testing"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// The ring of a header tells the stories of a peer known, also of one whose
// stories the account archived, which the list over the chats leaves out;
// of one Telegram only told has stories, one unread, its latest, while they
// are asked for once; of one with none, nothing.
func TestPeerStoryRing(t *testing.T) {
	s, _ := storyStore(t)
	anna := peerID(&tg.PeerUser{UserID: 7})
	ring, ok := s.PeerStoryRing(anna)
	if !ok || ring.Count != 2 || ring.Unread != 2 || model.StoryID(ring.Open) != 1 {
		t.Fatalf("Anna's ring %+v, %v", ring, ok)
	}

	s.rememberPeers([]tg.UserClass{&tg.User{ID: 7, AccessHash: 1, FirstName: "Anna", StoriesHidden: true}}, nil)
	if _, ok := s.PeerStoryRing(anna); !ok {
		t.Fatal("archived stories have no ring")
	}
	for _, p := range s.StoryPeers() {
		if p.ID == anna {
			t.Fatal("archived stories are in the list")
		}
	}

	boris := peerID(&tg.PeerUser{UserID: 8})
	s.history.api = nil // asked for, the stories wait for a connection
	u := &tg.User{ID: 8, AccessHash: 1, FirstName: "Boris"}
	var recent tg.RecentStory
	recent.SetMaxID(12)
	u.SetStoriesMaxID(recent)
	s.rememberPeers([]tg.UserClass{u}, nil)
	ring, ok = s.PeerStoryRing(boris)
	if !ok || ring.Count != 1 || ring.Unread != 1 || model.StoryID(ring.Open) != 12 {
		t.Fatalf("Boris's ring %+v, %v", ring, ok)
	}
	if !s.stories.ringAsked[boris] {
		t.Fatal("Boris's stories were not asked for")
	}

	vera := peerID(&tg.PeerUser{UserID: 9})
	if ring, ok := s.PeerStoryRing(vera); ok {
		t.Fatalf("Vera has no stories, but a ring %+v", ring)
	}
}

