// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A story a message carries is its photo or video with the story's
// caption, headed by whose it is; one Telegram left out has neither yet,
// one deleted is expired; a mention is a line of the history, with the
// story's media; a reply to a story names it.
func TestMessageStories(t *testing.T) {
	names := map[int64]string{7: "Anna"}
	from := &tg.PeerUser{UserID: 7}
	msg := func(media tg.MessageMediaClass) *tg.Message {
		return &tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 5}, Message: "", Date: 100, Media: media}
	}
	m, loc := convertMessage("", msg(&tg.MessageMediaStory{Peer: from, ID: 3, Story: storyItem(3, 50)}), names)
	if m.Kind != model.MessagePhoto || m.Media == nil || m.Text != "story 3" || m.StoryOf == nil || m.StoryOf.Peer != 7 || m.StoryOf.ID != 3 || m.StoryOf.From != "Anna" || m.StoryOf.Mention {
		t.Fatalf("forwarded %+v %+v", m, m.StoryOf)
	}
	if loc == nil || loc.StoryPeer != 7 || loc.Story != 3 {
		t.Fatalf("downloads from %+v", loc)
	}
	if m, _ = convertMessage("", msg(&tg.MessageMediaStory{Peer: from, ID: 4}), names); m.Media != nil || m.StoryOf == nil || m.StoryOf.Expired || m.Kind != model.MessageText {
		t.Fatalf("left out %+v", m)
	}
	if m, _ = convertMessage("", msg(&tg.MessageMediaStory{Peer: from, ID: 5, Story: &tg.StoryItemDeleted{ID: 5}}), names); !m.StoryOf.Expired {
		t.Fatalf("deleted %+v", m.StoryOf)
	}
	m, _ = convertMessage("", msg(&tg.MessageMediaStory{Peer: from, ID: 3, ViaMention: true, Story: storyItem(3, 50)}), names)
	if m.Kind != model.MessageService || m.Service == nil || m.Service.Kind != model.ServiceStoryMention || m.Media == nil || !m.StoryShown() || m.AsStory().Kind != model.MessagePhoto {
		t.Fatalf("mention %+v", m)
	}
	reply := msg(nil)
	reply.Message, reply.ReplyTo = "nice", &tg.MessageReplyStoryHeader{Peer: from, StoryID: 3}
	if m, _ = convertMessage("", reply, names); m.ReplyToStory == nil || m.ReplyToStory.ID != 3 || m.ReplyToStory.From != "Anna" || m.ReplyToMessageID != 0 {
		t.Fatalf("reply %+v", m)
	}
	if p := messagePreview(m, false); p.Media != "" {
		t.Fatalf("a reply previewed as %q", p.Media)
	}
	forwarded, _ := convertMessage("", msg(&tg.MessageMediaStory{Peer: from, ID: 3, Story: storyItem(3, 50)}), names)
	if p := messagePreview(forwarded, false); p.Media != "preview.story" || len(p.Thumbs) != 1 || p.Text != "story 3" {
		t.Fatalf("previewed %+v", p)
	}
}

// The stories Telegram left out of a page's messages are asked for once,
// a peer's together; those gone are expired. The history's stories page
// from the cache, apart from its photos.
func TestResolveChatStories(t *testing.T) {
	s, api := storyStore(t)
	ctx := context.Background()
	chat := int64(5)
	s.history.peers[chat] = peerRecord{Kind: "user", ID: 5, Hash: 1}
	s.history.histories[chat] = &model.History{}
	from := &tg.PeerUser{UserID: 7}
	page := []tg.MessageClass{
		&tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}, Date: 10, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 900, AccessHash: 1, FileReference: []byte{1}, DCID: 2, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "s", W: 90, H: 90, Size: 900}}}}},
		&tg.Message{ID: 2, PeerID: &tg.PeerUser{UserID: 5}, Date: 11, Media: &tg.MessageMediaStory{Peer: from, ID: 1}},
		&tg.Message{ID: 3, PeerID: &tg.PeerUser{UserID: 5}, Date: 12, Media: &tg.MessageMediaStory{Peer: from, ID: 8}},
		&tg.Message{ID: 4, PeerID: &tg.PeerUser{UserID: 5}, Date: 13, Media: &tg.MessageMediaStory{Peer: from, ID: 1}},
	}
	api.byID = []tg.StoryItemClass{storyItem(1, int(time.Now().Unix())-3600)}
	if _, err := s.ingest(ctx, page, false, 0); err != nil {
		t.Fatal(err)
	}
	cache := s.history.cache
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m, ok, _ := cache.Message(ctx, chat, 3); ok && m.StoryOf.Expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if told := api.requests(); !slices.Equal(told, []string{"by id 7 [1 8]"}) {
		t.Fatalf("told %q", told)
	}
	for id, want := range map[int]bool{2: true, 3: false, 4: true} {
		m, ok, err := cache.Message(ctx, chat, id)
		if err != nil || !ok || m.StoryShown() != want || m.StoryOf.Expired == want {
			t.Fatalf("message %d: %v %v %+v %+v", id, ok, err, m.StoryOf, m.Media)
		}
	}
	// Shown again, they are not asked for again.
	if _, err := s.ingest(ctx, page[1:2], false, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if told := api.requests(); len(told) != 1 {
		t.Fatalf("asked again %q", told)
	}
	stories, err := s.ChatStories(ctx, chat, 0, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []model.MessageID
	for _, m := range stories.Messages {
		if m.Kind != model.MessagePhoto {
			t.Fatalf("shown as %v", m.Kind)
		}
		ids = append(ids, m.Key.MessageID)
	}
	if !slices.Equal(ids, []model.MessageID{2, 4}) {
		t.Fatalf("stories %v", ids)
	}
	photos, err := cache.Photos(ctx, chat, 0, 1, 10)
	if err != nil || len(photos) != 1 || photos[0].Key.MessageID != 1 {
		t.Fatalf("photos %v %+v", err, photos)
	}
}
