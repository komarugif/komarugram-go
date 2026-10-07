// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// forumServer is a forum, channel 30, that answers for its topics and their
// messages and records what else is asked of it.
type forumServer struct {
	mu     sync.Mutex
	topics []tg.ForumTopicClass
	// count is how many topics the forum has in all.
	count    int
	messages []tg.MessageClass
	// requests are the topic pages asked for; sent, replies and reads what
	// went to it.
	requests []*tg.MessagesGetForumTopicsRequest
	replies  []*tg.MessagesGetRepliesRequest
	sent     []*tg.MessagesSendMessageRequest
	reads    []*tg.MessagesReadDiscussionRequest
	// replyPage, if set, answers the requests for replies; replyCount is
	// how many replies they count in all.
	replyPage  func(*tg.MessagesGetRepliesRequest) []tg.MessageClass
	replyCount int
	// searches are the searches asked for, found what they find, with
	// foundTopics, the topics of what they found.
	searches    []*tg.MessagesSearchRequest
	found       []tg.MessageClass
	foundTopics []tg.ForumTopicClass
}

func topicMessage(id, topic int, out bool, text string) tg.MessageClass {
	m := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 30}, Message: text, Date: 1000 + id, Out: out}
	if !out {
		m.FromID = &tg.PeerUser{UserID: 5}
	}
	if topic != model.GeneralTopic {
		m.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: topic, ForumTopic: true}
	}
	m.SetFlags()
	return m
}

func newForumServer() *forumServer {
	release := &tg.ForumTopic{ID: 7, Title: "Release", Pinned: true, IconColor: 0xCB86DB, TopMessage: 45, UnreadCount: 3, UnreadMentionsCount: 1}
	release.SetIconEmojiID(555)
	release.NotifySettings.SetMuteUntil(int(time.Now().Add(time.Hour).Unix()))
	return &forumServer{
		topics: []tg.ForumTopicClass{
			release,
			&tg.ForumTopic{ID: 9, Title: "Chat", IconColor: 0x6FB9F0, TopMessage: 50},
			&tg.ForumTopic{ID: 1, Title: "General", TopMessage: 40},
			&tg.ForumTopic{ID: 11, Title: "Hidden", Hidden: true, TopMessage: 41},
		},
		count: 4,
		messages: []tg.MessageClass{
			topicMessage(45, 7, false, "ship it"), topicMessage(50, 9, true, "ok"), topicMessage(40, 1, false, "welcome"),
		},
	}
}

func (f *forumServer) client() *tg.Client {
	return tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch req := in.(type) {
		case *tg.MessagesGetForumTopicsRequest:
			f.requests = append(f.requests, req)
			res := out.(*tg.MessagesForumTopics)
			res.Count, res.Topics, res.Messages = f.count, f.topics, f.messages
			res.Users = []tg.UserClass{&tg.User{ID: 5, FirstName: "Анна"}}
		case *tg.MessagesGetRepliesRequest:
			f.replies = append(f.replies, req)
			page := []tg.MessageClass{topicMessage(46, 7, false, "second"), topicMessage(45, 7, false, "first")}
			if f.replyPage != nil {
				page = f.replyPage(req)
			}
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{
				Messages: page,
				Count:    max(f.replyCount, len(page)),
				Users:    []tg.UserClass{&tg.User{ID: 5, FirstName: "Анна"}},
			}
		case *tg.MessagesSearchRequest:
			f.searches = append(f.searches, req)
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{
				Messages: f.found,
				Count:    len(f.found),
				Topics:   f.foundTopics,
				Users:    []tg.UserClass{&tg.User{ID: 5, FirstName: "Анна"}},
			}
		case *tg.MessagesSendMessageRequest:
			f.sent = append(f.sent, req)
			out.(*tg.UpdatesBox).Updates = &tg.Updates{}
		case *tg.MessagesReadDiscussionRequest:
			f.reads = append(f.reads, req)
			out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		default:
			return fmt.Errorf("unexpected request %T", in)
		}
		return nil
	}))
}

// forumStore is a store with the forum, a group of channel 30.
func forumStore(t *testing.T) (*Store, *forumServer, int64) {
	t.Helper()
	s := testStore(t)
	s.rememberPeers(nil, []tg.ChatClass{&tg.Channel{ID: 30, AccessHash: 31, Megagroup: true, Forum: true, Title: "Team"}})
	server := newForumServer()
	s.history.api = server.client()
	return s, server, peerID(&tg.PeerChannel{ChannelID: 30})
}

func waitTopics(t *testing.T, s *Store, chat int64, cond func(model.TopicList) bool) model.TopicList {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := s.Topics(chat)
		if !list.Loading && cond(list) {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("topics never came: %+v", list)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func titles(list model.TopicList) string {
	out := ""
	for _, topic := range list.Topics {
		out += topic.Title + ";"
	}
	return out
}

// The forum's topics come in the server's order, each with what the list
// shows of it; a hidden one is left out.
func TestForumTopics(t *testing.T) {
	s, server, chat := forumStore(t)
	if got := s.Topics(chat); len(got.Topics) != 0 {
		t.Fatalf("topics before opening: %+v", got)
	}
	s.OpenForum(chat)
	list := waitTopics(t, s, chat, func(l model.TopicList) bool { return len(l.Topics) > 0 })
	if got := titles(list); got != "Release;Chat;General;" {
		t.Fatalf("topics %q", got)
	}
	release, chatTopic, general := list.Topics[0], list.Topics[1], list.Topics[2]
	if !release.Pinned || release.Unread != 3 || release.Mentions != 1 || !release.Muted || release.IconColor != 0xCB86DB || release.IconEmoji != 555 ||
		release.LastMessage != "ship it" || release.LastSender != "Анна" || release.LastTime.IsZero() {
		t.Fatalf("release %+v", release)
	}
	if chatTopic.LastSender != "Вы" || chatTopic.LastMessage != "ok" || chatTopic.Muted || chatTopic.Pinned {
		t.Fatalf("chat %+v", chatTopic)
	}
	if !general.General || general.ID != model.GeneralTopic {
		t.Fatalf("general %+v", general)
	}
	if list.More || list.Err != nil {
		t.Fatalf("more %v, err %v", list.More, list.Err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests) != 1 || server.requests[0].Limit != topicPage || server.requests[0].OffsetID != 0 {
		t.Fatalf("requests %+v", server.requests)
	}
	if p, ok := server.requests[0].Peer.(*tg.InputPeerChannel); !ok || p.ChannelID != 30 || p.AccessHash != 31 {
		t.Fatalf("asked of %+v", server.requests[0].Peer)
	}
	// Opened again soon, it is not read again.
	server.mu.Unlock()
	s.OpenForum(chat)
	time.Sleep(20 * time.Millisecond)
	server.mu.Lock()
	if len(server.requests) != 1 {
		t.Fatalf("read again at once: %d requests", len(server.requests))
	}
}

// A forum with more topics than a page has says so, and the next page is
// asked for after the last topic.
func TestForumTopicsPages(t *testing.T) {
	s, server, chat := forumStore(t)
	server.topics = nil
	for i := 0; i < topicPage; i++ {
		server.topics = append(server.topics, &tg.ForumTopic{ID: 100 + i, Title: fmt.Sprintf("Topic %d", i), TopMessage: 200 + i})
	}
	server.messages = []tg.MessageClass{topicMessage(200+topicPage-1, 100+topicPage-1, false, "last")}
	server.count = topicPage + 2
	s.OpenForum(chat)
	list := waitTopics(t, s, chat, func(l model.TopicList) bool { return len(l.Topics) == topicPage })
	if !list.More {
		t.Fatal("no more topics offered")
	}
	server.mu.Lock()
	server.topics = []tg.ForumTopicClass{
		&tg.ForumTopic{ID: 300, Title: "Older", TopMessage: 301}, &tg.ForumTopic{ID: 100, Title: "Repeated", TopMessage: 200},
	}
	server.messages = nil
	server.mu.Unlock()
	s.LoadMoreTopics(chat)
	list = waitTopics(t, s, chat, func(l model.TopicList) bool { return len(l.Topics) > topicPage })
	if len(list.Topics) != topicPage+1 || list.More || list.Topics[topicPage].Title != "Older" {
		t.Fatalf("%d topics after the second page, more %v", len(list.Topics), list.More)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests) != 2 || server.requests[1].OffsetTopic != 100+topicPage-1 || server.requests[1].OffsetID != 200+topicPage-1 {
		t.Fatalf("the second page was asked for with %+v", server.requests[len(server.requests)-1])
	}
}

func openedTopic(t *testing.T, s *Store, chat int64, id int) model.Chat {
	t.Helper()
	list := waitTopics(t, s, chat, func(l model.TopicList) bool { return len(l.Topics) > 0 })
	var topic model.Topic
	for _, one := range list.Topics {
		if one.ID == id {
			topic = one
		}
	}
	thread := s.OpenTopic(chat, topic)
	deadline := time.Now().Add(5 * time.Second)
	for s.History(thread.ID).LoadingOlder {
		if time.Now().After(deadline) {
			t.Fatal("the topic never loaded")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return thread
}

// A topic opens as a chat of its own, whose messages are the topic's and
// whose messages go into it.
func TestOpenTopic(t *testing.T) {
	s, server, chat := forumStore(t)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	if !isThread(thread.ID) || thread.Title != "Release" {
		t.Fatalf("topic chat %+v", thread)
	}
	h := s.History(thread.ID)
	if len(h.Messages) != 2 || h.Messages[0].Text != "first" || h.Messages[1].Text != "second" || h.ThreadRoot != 7 || h.Err != nil {
		t.Fatalf("topic history %+v", h)
	}
	if !h.Messages[0].ForumTopic || h.Messages[0].TopicID() != 7 {
		t.Fatalf("the topic of a message: %+v", h.Messages[0])
	}
	server.mu.Lock()
	if len(server.replies) != 1 || server.replies[0].MsgID != 7 {
		t.Fatalf("replies asked as %+v", server.replies)
	}
	if p := server.replies[0].Peer.(*tg.InputPeerChannel); p.ChannelID != 30 {
		t.Fatalf("asked of %+v", p)
	}
	server.mu.Unlock()
	// Opened again, it is the same chat.
	if again := s.OpenTopic(chat, model.Topic{ID: 7, Title: "Release"}); again.ID != thread.ID {
		t.Fatalf("another chat %d for the same topic, first %d", again.ID, thread.ID)
	}

	// What is sent replies to the topic's root; a reply in it names the root
	// too.
	if err := s.Send(context.Background(), thread.ID, model.OutgoingMessage{RandomID: 1, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), thread.ID, model.OutgoingMessage{RandomID: 2, Text: "yes", ReplyTo: 46}); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.sent) != 2 {
		t.Fatalf("%d messages sent", len(server.sent))
	}
	first := server.sent[0].ReplyTo.(*tg.InputReplyToMessage)
	second := server.sent[1].ReplyTo.(*tg.InputReplyToMessage)
	if first.ReplyToMsgID != 7 || first.TopMsgID != 0 {
		t.Fatalf("first reply %+v", first)
	}
	if top, _ := second.GetTopMsgID(); second.ReplyToMsgID != 46 || top != 7 {
		t.Fatalf("second reply %+v", second)
	}
}

// The General topic has no root to reply to.
func TestGeneralTopicSendsWithoutARoot(t *testing.T) {
	s, server, chat := forumStore(t)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, model.GeneralTopic)
	if err := s.Send(context.Background(), thread.ID, model.OutgoingMessage{RandomID: 1, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), thread.ID, model.OutgoingMessage{RandomID: 2, Text: "yes", ReplyTo: 44}); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	sent := append([]*tg.MessagesSendMessageRequest(nil), server.sent...)
	server.mu.Unlock()
	if len(sent) != 2 || sent[0].ReplyTo != nil {
		t.Fatalf("sent %+v", sent)
	}
	if reply := sent[1].ReplyTo.(*tg.InputReplyToMessage); reply.ReplyToMsgID != 44 || reply.TopMsgID != 0 {
		t.Fatalf("reply in General %+v", reply)
	}
	// Its messages carry no header, yet they are its own; those of the other
	// topics are not.
	general := len(s.History(thread.ID).Messages)
	s.mergeUpdate(model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: 80}, Text: "plain", Date: time.Now()})
	s.mergeUpdate(model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: 81}, Text: "elsewhere", Date: time.Now(), ForumTopic: true, ReplyToMessageID: 9})
	s.mergeUpdate(model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: 82}, Text: "answer", Date: time.Now(), ForumTopic: true, ReplyToMessageID: 80, ReplyToTopID: 9})
	msgs := s.History(thread.ID).Messages
	if len(msgs) != general+1 || msgs[len(msgs)-1].Text != "plain" {
		t.Fatalf("General has %d messages, had %d", len(msgs), general)
	}
}

// A message that comes is shown by its topic, counted unread when it is new,
// and put where its topic goes; the topic open takes only its own.
func TestTopicsFollowMessages(t *testing.T) {
	s, server, chat := forumStore(t)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	message := func(id, topic int, text string, out bool) model.Message {
		m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}, Text: text, Date: time.Now(), Outgoing: out}
		if topic != model.GeneralTopic {
			m.ForumTopic, m.ReplyToMessageID = true, model.MessageID(topic)
		}
		return m
	}
	s.mergeUpdate(message(60, 9, "hi", false))
	list := s.Topics(chat)
	if got := titles(list); got != "Release;Chat;General;" || list.Topics[1].LastMessage != "hi" || list.Topics[1].Unread != 1 || list.Topics[1].LastSender != "" {
		t.Fatalf("after a message in Chat: %q %+v", titles(list), list.Topics[1])
	}
	// A newer message in General puts it before Chat, after the pinned.
	s.mergeUpdate(message(61, model.GeneralTopic, "news", false))
	list = s.Topics(chat)
	if got := titles(list); got != "Release;General;Chat;" || list.Topics[1].Unread != 1 {
		t.Fatalf("after a message in General: %q", got)
	}
	// One of the account's own is not unread.
	s.mergeUpdate(message(62, 9, "mine", true))
	if got := s.Topics(chat); got.Topics[0].Title != "Chat" && got.Topics[1].Title != "Chat" || got.Topics[0].Unread != 3 {
		t.Fatalf("own message: %+v", got.Topics)
	}
	for _, topic := range s.Topics(chat).Topics {
		if topic.Title == "Chat" && topic.Unread != 1 {
			t.Fatalf("own message counted unread: %d", topic.Unread)
		}
	}
	// Only the topic that is open takes a message.
	before := len(s.History(thread.ID).Messages)
	s.mergeUpdate(message(63, 7, "for the release", false))
	after := s.History(thread.ID).Messages
	if len(after) != before+1 || after[len(after)-1].Text != "for the release" {
		t.Fatalf("the open topic has %d messages, had %d", len(after), before)
	}
	s.mergeUpdate(message(64, 9, "elsewhere", false))
	s.mergeUpdate(message(65, model.GeneralTopic, "general", false))
	if got := len(s.History(thread.ID).Messages); got != before+1 {
		t.Fatalf("messages of other topics went into the open one: %d", got)
	}

	// Telegram's dates are whole seconds: of two messages of one second the
	// later, which has the greater id, puts its topic first.
	second := time.Now().Add(time.Hour).Truncate(time.Second)
	for id, topic := range []int{9, model.GeneralTopic} {
		m := message(66+id, topic, "same second", true)
		m.Date = second
		s.mergeUpdate(m)
	}
	if got := titles(s.Topics(chat)); got != "Release;General;Chat;" {
		t.Fatalf("after messages of one second: %q", got)
	}

	// A message of a topic the list lacks asks for the list again.
	server.mu.Lock()
	asked := len(server.requests)
	server.mu.Unlock()
	s.mergeUpdate(message(70, 12, "new topic", false))
	waitTopics(t, s, chat, func(model.TopicList) bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.requests) > asked
	})
}

// A topic read up to its last message has no unread count left, and it is
// told to Telegram only with Ghost's leave or when asked.
func TestTopicRead(t *testing.T) {
	s, server, chat := forumStore(t)
	s.OpenForum(chat)
	thread := openedTopic(t, s, chat, 7)
	unread := func() int {
		for _, topic := range s.Topics(chat).Topics {
			if topic.ID == 7 {
				return topic.Unread
			}
		}
		return -1
	}
	reads := func() int {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.reads)
	}
	s.MarkRead(thread.ID, 45, false)
	time.Sleep(30 * time.Millisecond)
	if reads() != 0 || unread() != 3 {
		t.Fatalf("read without Ghost's leave: %d reads, %d unread", reads(), unread())
	}
	s.SetGhost(model.Ghost{SendRead: true})
	s.MarkRead(thread.ID, 44, false)
	deadline := time.Now().Add(5 * time.Second)
	for reads() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nothing was told")
		}
		time.Sleep(2 * time.Millisecond)
	}
	server.mu.Lock()
	read := server.reads[0]
	server.mu.Unlock()
	if p := read.Peer.(*tg.InputPeerChannel); p.ChannelID != 30 || read.MsgID != 7 || read.ReadMaxID != 44 {
		t.Fatalf("told %+v %+v", p, read)
	}
	if unread() != 3 {
		t.Fatalf("read up to 44 of 45 left %d unread", unread())
	}
	s.MarkRead(thread.ID, 45, false)
	for unread() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the unread count stayed")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
