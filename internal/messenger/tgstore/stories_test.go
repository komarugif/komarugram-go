// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// storyAPI answers the stories' requests and records them.
type storyAPI struct {
	mu   sync.Mutex
	told []string
	// all answers getAllStories, by the state it is asked with; byID,
	// getStoriesByID; react fails a reaction when set.
	all   map[string]tg.StoriesAllStoriesClass
	byID  []tg.StoryItemClass
	react error
}

func (a *storyAPI) client() *tg.Client {
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch r := in.(type) {
		case *tg.StoriesGetAllStoriesRequest:
			a.told = append(a.told, fmt.Sprintf("all next=%v state=%q", r.Next, r.State))
			return a.all[r.State], nil
		case *tg.StoriesGetStoriesByIDRequest:
			a.told = append(a.told, fmt.Sprintf("by id %d %v", peerOf(r.Peer), r.ID))
			return &tg.StoriesStories{Stories: a.byID}, nil
		case *tg.StoriesReadStoriesRequest:
			a.told = append(a.told, fmt.Sprintf("read %d %d", peerOf(r.Peer), r.MaxID))
			return &tg.IntVector{}, nil
		case *tg.StoriesSendReactionRequest:
			emoji := ""
			if e, ok := r.Reaction.(*tg.ReactionEmoji); ok {
				emoji = e.Emoticon
			}
			a.told = append(a.told, fmt.Sprintf("react %d %d %q", peerOf(r.Peer), r.StoryID, emoji))
			if a.react != nil {
				return nil, a.react
			}
			return &tg.Updates{}, nil
		case *tg.MessagesSendMediaRequest:
			story := r.Media.(*tg.InputMediaStory)
			a.told = append(a.told, fmt.Sprintf("share %d %d to %d", peerOf(story.Peer), story.ID, peerOf(r.Peer)))
			return &tg.Updates{}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

func (a *storyAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.told)
}

// storyItem is story id, a photo, posted at date for a day.
func storyItem(id, date int) *tg.StoryItem {
	return &tg.StoryItem{ID: id, Date: date, ExpireDate: date + 86400, Caption: fmt.Sprint("story ", id),
		Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(100 + id), AccessHash: 1, FileReference: []byte{1}, DCID: 2, Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 90, H: 160, Size: 900}, &tg.PhotoSize{Type: "y", W: 720, H: 1280, Size: 90000}}}}}
}

// The stories over the list come in pages the first time, and then only
// what changed since the state Telegram told; they are listed as Telegram
// Desktop lists them, the account's first, those with unread ones before
// the others, and those the account moved to the archive not at all. The
// cache keeps them for the next start.
func TestStoriesLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	s := New(nil)
	if err := s.Configure(context.Background(), "a", path, nil); err != nil {
		t.Fatal(err)
	}
	now := int(time.Now().Unix())
	api := &storyAPI{all: map[string]tg.StoriesAllStoriesClass{
		"": &tg.StoriesAllStories{HasMore: true, State: "s1",
			Users: []tg.UserClass{&tg.User{ID: 6, Self: true, FirstName: "Me"}, &tg.User{ID: 7, FirstName: "Anna", LastName: "Smith"}, &tg.User{ID: 8, FirstName: "Boris", StoriesHidden: true}, &tg.User{ID: 9, FirstName: "Vera"}},
			PeerStories: []tg.PeerStories{
				{Peer: &tg.PeerUser{UserID: 9}, MaxReadID: 4, Stories: []tg.StoryItemClass{storyItem(4, now-60)}},
				{Peer: &tg.PeerUser{UserID: 7}, MaxReadID: 1, Stories: []tg.StoryItemClass{storyItem(1, now-7200), &tg.StoryItemSkipped{ID: 2, Date: now - 3600, ExpireDate: now + 3600}}},
			}},
		"s1": &tg.StoriesAllStories{State: "s2", PeerStories: []tg.PeerStories{
			{Peer: &tg.PeerUser{UserID: 6}, Stories: []tg.StoryItemClass{storyItem(3, now-9000)}},
			{Peer: &tg.PeerUser{UserID: 8}, Stories: []tg.StoryItemClass{storyItem(5, now-30)}},
		}},
		"s2": &tg.StoriesAllStoriesNotModified{State: "s3"},
	}}
	s.history.api = api.client()
	ctx := context.Background()
	if err := s.loadStories(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	peers := s.StoryPeers()
	var got []string
	for _, p := range peers {
		got = append(got, fmt.Sprintf("%s %d/%d open %d self=%v", p.Title, p.Unread, p.Count, model.StoryID(p.Open), p.Self))
	}
	want := []string{"Me 1/1 open 3 self=true", "Anna 1/2 open 2 self=false", "Vera 0/1 open 4 self=false"}
	if !slices.Equal(got, want) {
		t.Fatalf("listed\n%q\nwant\n%q", got, want)
	}
	if err := s.loadStories(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	if told := api.requests(); !slices.Equal(told, []string{`all next=false state=""`, `all next=true state="s1"`, `all next=false state="s2"`}) {
		t.Fatalf("asked %q", told)
	}
	if s.stories.state != "s3" {
		t.Fatalf("state %q", s.stories.state)
	}
	if err := s.persistDialogs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again := New(nil)
	if err := again.Configure(ctx, "a", path, nil); err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if kept := again.StoryPeers(); len(kept) != 3 || kept[1].Title != "Anna" || kept[1].Unread != 1 || again.stories.state != "s3" {
		t.Fatalf("kept %+v, state %q", kept, again.stories.state)
	}
}

// storyStore is a store with Anna's stories 1 and 2, the second told only
// in part.
func storyStore(t *testing.T) (*Store, *storyAPI) {
	s := testStore(t)
	api := &storyAPI{}
	s.history.api = api.client()
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 7, AccessHash: 1, FirstName: "Anna"}, &tg.User{ID: 9, AccessHash: 1, FirstName: "Vera"}}, nil)
	now := int(time.Now().Unix())
	s.applyStory(&tg.UpdateStory{Peer: &tg.PeerUser{UserID: 7}, Story: storyItem(1, now-7200)})
	s.applyStory(&tg.UpdateStory{Peer: &tg.PeerUser{UserID: 7}, Story: &tg.StoryItemSkipped{ID: 2, Date: now - 3600, ExpireDate: now + 3600}})
	api.byID = []tg.StoryItemClass{storyItem(2, now-3600)}
	return s, api
}

// A story seen is told to Telegram as Ghost Mode allows, once, of the last
// one seen; hidden, it is still seen here.
func TestReadStory(t *testing.T) {
	s, api := storyStore(t)
	anna := peerID(&tg.PeerUser{UserID: 7})
	s.SetGhost(model.Ghost{})
	s.ReadStory(anna, model.StoryMessage(1))
	s.tellStoriesRead(anna)
	if p := s.StoryPeers(); len(p) != 1 || p[0].Unread != 1 || model.StoryID(p[0].Open) != 2 {
		t.Fatalf("seen here %+v", p)
	}
	s.SetGhost(model.Ghost{SendStories: true})
	s.ReadStory(anna, model.StoryMessage(2))
	s.ReadStory(anna, model.StoryMessage(1)) // seen already
	s.tellStoriesRead(anna)
	s.tellStoriesRead(anna)
	deadline := time.Now().Add(5 * time.Second)
	for len(api.requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if told := api.requests(); !slices.Equal(told, []string{"read 7 2"}) {
		t.Fatalf("told %q", told)
	}
	if p := s.StoryPeers(); p[0].Unread != 0 || model.StoryID(p[0].Open) != 1 {
		t.Fatalf("all seen %+v", p)
	}
}

// A heart is shown at once, and taken back when Telegram refuses it; a
// story is shared as media; those told in part are asked for together,
// and where their media downloads from is kept.
func TestStoryLikeShareAndFull(t *testing.T) {
	s, api := storyStore(t)
	anna := peerID(&tg.PeerUser{UserID: 7})
	ctx := context.Background()
	if err := s.LikeStory(ctx, anna, model.StoryMessage(1), true); err != nil {
		t.Fatal(err)
	}
	if m, ok := s.StoryNow(anna, model.StoryMessage(1)); !ok || !m.Story.Liked || m.Story.Likes != 1 {
		t.Fatalf("liked %+v", m.Story)
	}
	api.react = errors.New("REACTION_INVALID")
	if err := s.LikeStory(ctx, anna, model.StoryMessage(1), false); err == nil {
		t.Fatal("refused, and no error")
	}
	if m, _ := s.StoryNow(anna, model.StoryMessage(1)); !m.Story.Liked || m.Story.Likes != 1 {
		t.Fatalf("refused, and taken back %+v", m.Story)
	}
	if err := s.ShareStory(ctx, anna, model.StoryMessage(1), peerID(&tg.PeerUser{UserID: 9})); err != nil {
		t.Fatal(err)
	}
	stories, err := s.PeerStories(ctx, anna)
	if err != nil {
		t.Fatal(err)
	}
	if len(stories) != 2 || model.StoryID(stories[0].Key.MessageID) != 1 || stories[1].Text != "story 2" || stories[1].Key.ChatID != anna {
		t.Fatalf("stories %+v", stories)
	}
	if told := api.requests(); !slices.Equal(told, []string{`react 7 1 "❤"`, `react 7 1 ""`, "share 7 1 to 9", "by id 7 [2]"}) {
		t.Fatalf("told %q", told)
	}
	var ref fileLocation
	if ok, err := s.history.cache.Get(ctx, "ref/"+stories[1].Media.ID, &ref); !ok || err != nil || ref.ID != 102 {
		t.Fatalf("kept %v %v %+v", ok, err, ref)
	}
	// Known in full, they are not asked for again.
	if _, err := s.PeerStories(ctx, anna); err != nil || len(api.requests()) != 4 {
		t.Fatalf("asked again: %v %q", err, api.requests())
	}
}

// A story's file reference is renewed by the story itself, whatever
// message shows it: the viewer keys the stories of a row by ids of its own.
func TestStoryReferenceRenewed(t *testing.T) {
	s, api := storyStore(t)
	anna := peerID(&tg.PeerUser{UserID: 7})
	stories := s.knownStories(anna)
	if len(stories) != 1 {
		t.Fatalf("known %+v", stories)
	}
	api.byID = []tg.StoryItemClass{storyItem(1, int(time.Now().Unix())-7200)}
	shown := stories[0]
	shown.Key.MessageID = -1<<30 - 5 // the viewer's key
	if err := s.refreshReference(context.Background(), shown); err != nil {
		t.Fatal(err)
	}
	if told := api.requests(); !slices.Equal(told, []string{"by id 7 [1]"}) {
		t.Fatalf("told %q", told)
	}
}

// A story told in full in an earlier run is kept without where its media
// download from, unless it was opened then: opened in the next, it is
// asked for again, and its media are located, not "location unavailable".
func TestStoryKeptWithoutLocationIsAskedAgain(t *testing.T) {
	s, api := storyStore(t)
	anna := peerID(&tg.PeerUser{UserID: 7})
	now := int(time.Now().Unix())
	api.byID = []tg.StoryItemClass{storyItem(1, now-7200), storyItem(2, now-3600)}
	// The next run: the store knows story 1 in full, from the cache, and
	// nothing of where its media are.
	s.history.mu.Lock()
	clear(s.history.refs)
	s.history.mu.Unlock()
	s.stories.mu.Lock()
	clear(s.stories.unsaved)
	s.stories.mu.Unlock()
	stories, err := s.PeerStories(context.Background(), anna)
	if err != nil {
		t.Fatal(err)
	}
	if told := api.requests(); !slices.Equal(told, []string{"by id 7 [2 1]"}) {
		t.Fatalf("told %q", told)
	}
	if len(stories) != 2 {
		t.Fatalf("%d stories", len(stories))
	}
	for _, m := range stories {
		if !s.storyLocated(context.Background(), m.Media) {
			t.Fatalf("story %d's media has no location", model.StoryID(m.Key.MessageID))
		}
	}
}
