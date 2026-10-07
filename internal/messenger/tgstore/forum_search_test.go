// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A topic counts its messages as Telegram counts them, a new one more and
// a deleted one fewer; an edit, or a message of another topic, changes
// nothing.
func TestTopicCountsItsMessages(t *testing.T) {
	s, server, chat := forumStore(t)
	server.replyPage, server.replyCount = repliesOf(7, 8, 57), 120
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	if h := s.History(thread.ID); !h.Counted || h.Count != 120 {
		t.Fatalf("the topic counts %d, counted %v", h.Count, h.Counted)
	}
	message := func(id, topic int, text string) model.Message {
		return model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}, Text: text, Date: time.Now(), ForumTopic: true, ReplyToMessageID: model.MessageID(topic)}
	}
	s.mergeUpdate(message(500, 7, "new"))
	s.mergeUpdate(message(500, 7, "new, edited"))
	s.mergeUpdate(message(501, 9, "elsewhere"))
	// An edit of an old message, which the topic has not loaded.
	s.mergeUpdate(message(3, 7, "old, edited"))
	if h := s.History(thread.ID); h.Count != 121 {
		t.Fatalf("after a new message, an edit and another topic's: %d", h.Count)
	}
	if _, err := s.deleteMessages(context.Background(), chat, []int{500}); err != nil {
		t.Fatal(err)
	}
	if h := s.History(thread.ID); h.Count != 120 {
		t.Fatalf("after a deletion: %d", h.Count)
	}
}

// A topic is searched under its root; offline, the cache has none of its
// messages to search.
func TestSearchInTopic(t *testing.T) {
	s, server, chat := forumStore(t)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	server.mu.Lock()
	server.found = []tg.MessageClass{topicMessage(46, 7, false, "second")}
	server.mu.Unlock()
	page, err := s.SearchChat(context.Background(), thread.ID, "second", "", 50)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("found %+v, %v", page, err)
	}
	server.mu.Lock()
	req := server.searches[0]
	server.mu.Unlock()
	if top, ok := req.GetTopMsgID(); !ok || top != 7 || req.Peer.(*tg.InputPeerChannel).ChannelID != 30 {
		t.Fatalf("searched %+v", req)
	}
	s.history.api = nil
	if _, err := s.SearchChat(context.Background(), thread.ID, "second", "", 50); !errors.Is(err, model.ErrSearchOffline) {
		t.Fatalf("offline: %v", err)
	}
}

// A search's jump to a message of a topic loads the replies around it and
// opens the page there; the jump to the end, or the topic opened again,
// opens it at its end.
func TestRevealInTopic(t *testing.T) {
	s, server, chat := forumStore(t)
	server.replyPage = repliesOf(7, 8, 400)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	s.Reveal(thread.ID, 200)
	if v, ok := s.Viewport(thread.ID); !ok || v.AnchorMessageID != 200 {
		t.Fatalf("the page opens at %+v, %v", v, ok)
	}
	h := waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	first, last := h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID
	if first != 176 || last != 225 || !h.HasOlder || !h.HasNewer || h.ThreadRoot != 7 {
		t.Fatalf("around 200: %d..%d, HasOlder %v, HasNewer %v", first, last, h.HasOlder, h.HasNewer)
	}
	// Near the start, the window reaches it.
	s.Reveal(thread.ID, 10)
	h = waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 && h.Messages[0].Key.MessageID < 100 })
	if h.Messages[0].Key.MessageID != 8 || h.HasOlder || !h.HasNewer {
		t.Fatalf("near the start: from %d, HasOlder %v", h.Messages[0].Key.MessageID, h.HasOlder)
	}
	s.RevealLast(thread.ID)
	waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	if v, ok := s.Viewport(thread.ID); ok {
		t.Fatalf("the end opens at %+v", v)
	}
	s.Reveal(thread.ID, 200)
	waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	s.OpenTopic(chat, model.Topic{ID: 7, Title: "Release"})
	if v, ok := s.Viewport(thread.ID); ok {
		t.Fatalf("opened again, the topic opens at %+v", v)
	}
}

// A forum's search finds messages of all its topics, each with its topic,
// from what Telegram sends with them or the list; a topic opens at one.
func TestSearchForum(t *testing.T) {
	s, server, chat := forumStore(t)
	server.replyPage = repliesOf(9, 10, 300)
	s.OpenForum(chat)
	waitTopics(t, s, chat, func(l model.TopicList) bool { return len(l.Topics) > 0 })
	server.mu.Lock()
	server.found = []tg.MessageClass{topicMessage(45, 7, false, "deploy"), topicMessage(80, 12, false, "deploy again"), topicMessage(40, model.GeneralTopic, false, "deploy?")}
	server.foundTopics = []tg.ForumTopicClass{&tg.ForumTopic{ID: 12, Title: "Ops", IconColor: 0xFB6F5F, TopMessage: 80}}
	server.mu.Unlock()
	page, err := s.SearchForum(context.Background(), chat, "deploy", "", 50)
	if err != nil || page.Count != 3 || len(page.Found) != 3 {
		t.Fatalf("found %+v, %v", page, err)
	}
	want := []struct {
		id    model.MessageID
		topic string
	}{{80, "Ops"}, {45, "Release"}, {40, "General"}}
	for i, w := range want {
		if f := page.Found[i]; f.Message.Key.MessageID != w.id || f.Topic.Title != w.topic {
			t.Fatalf("found %d: %d in %q, want %d in %q", i, f.Message.Key.MessageID, f.Topic.Title, w.id, w.topic)
		}
	}
	server.mu.Lock()
	if _, ok := server.searches[0].GetTopMsgID(); ok {
		t.Fatal("the forum was searched under a topic")
	}
	server.mu.Unlock()
	if got := s.Topics(chat); titles(got) != "Release;Chat;General;" {
		t.Fatalf("a search changed the topics: %q", titles(got))
	}

	thread := s.OpenTopicAt(chat, model.Topic{ID: 9, Title: "Chat"}, 150)
	if v, ok := s.Viewport(thread.ID); !ok || v.AnchorMessageID != 150 {
		t.Fatalf("the topic opens at %+v", v)
	}
	h := waitThread(t, s, thread.ID, func(h model.History) bool { return len(h.Messages) > 0 })
	if first, last := h.Messages[0].Key.MessageID, h.Messages[len(h.Messages)-1].Key.MessageID; first != 126 || last != 175 || !h.Counted {
		t.Fatalf("the topic opened at %d..%d, counted %v", first, last, h.Counted)
	}
}
