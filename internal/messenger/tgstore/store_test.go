// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const self = 1

// testPage is one page of dialogs: Saved Messages, a contact, a stranger, a
// bot, a muted group and a channel.
func testPage() *tg.MessagesDialogs {
	now := int(time.Now().Unix())
	dialog := func(peer tg.PeerClass, top, unread int) *tg.Dialog {
		return &tg.Dialog{Peer: peer, TopMessage: top, UnreadCount: unread}
	}
	muted := dialog(&tg.PeerChat{ChatID: 5}, 50, 3)
	muted.NotifySettings.SetMuteUntil(now + 3600)
	// Optional fields are read through their flags, so they are set the way
	// decoding sets them.
	fromAnna := &tg.Message{ID: 50, PeerID: &tg.PeerChat{ChatID: 5}, Message: "lunch?", Date: now}
	fromAnna.SetFromID(&tg.PeerUser{UserID: 2})
	pinned := dialog(&tg.PeerUser{UserID: self}, 10, 0)
	pinned.Pinned = true
	return &tg.MessagesDialogs{
		Dialogs: []tg.DialogClass{
			pinned,
			dialog(&tg.PeerUser{UserID: 2}, 20, 1),
			dialog(&tg.PeerUser{UserID: 3}, 30, 0),
			dialog(&tg.PeerUser{UserID: 4}, 40, 0),
			muted,
			dialog(&tg.PeerChannel{ChannelID: 6}, 60, 7),
		},
		Messages: []tg.MessageClass{
			&tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: self}, Message: "note", Date: now},
			&tg.Message{ID: 20, PeerID: &tg.PeerUser{UserID: 2}, Message: "hi\nthere", Date: now},
			&tg.Message{ID: 30, PeerID: &tg.PeerUser{UserID: 3}, Media: &tg.MessageMediaPhoto{}, Date: now},
			&tg.Message{ID: 40, PeerID: &tg.PeerUser{UserID: 4}, Message: "/start", Date: now},
			fromAnna,
			&tg.Message{ID: 60, PeerID: &tg.PeerChannel{ChannelID: 6}, Message: "news", Date: now},
		},
		Users: []tg.UserClass{
			&tg.User{ID: self, Self: true, FirstName: "Me"},
			&tg.User{ID: 2, Contact: true, FirstName: "Anna", LastName: "S"},
			&tg.User{ID: 3, FirstName: "Stranger"},
			&tg.User{ID: 4, Bot: true, FirstName: "Bot"},
		},
		Chats: []tg.ChatClass{
			&tg.Chat{ID: 5, Title: "Group", ParticipantsCount: 3},
			&tg.Channel{ID: 6, Title: "Channel", Broadcast: true},
		},
	}
}

func TestDialogs(t *testing.T) {
	l := newList(self)
	if added := l.add(testPage()); added != 6 {
		t.Fatalf("added %d chats, want 6", added)
	}
	chats, _ := l.snapshot(nil)
	want := []struct {
		kind   model.ChatKind
		title  string
		last   string
		sender string
	}{
		{model.KindSaved, "", "note", ""},
		{model.KindUser, "Anna S", "hi there", ""},
		{model.KindUser, "Stranger", "Фото", ""},
		{model.KindBot, "Bot", "/start", ""},
		{model.KindGroup, "Group", "lunch?", "Anna"},
		{model.KindChannel, "Channel", "news", ""},
	}
	for i, w := range want {
		c := chats[i]
		if c.Kind != w.kind || c.Title != w.title || chatText(c) != w.last || chatSender(c) != w.sender {
			t.Errorf("chat %d: %+v, want %+v", i, c, w)
		}
	}
	if !chats[0].Pinned || !chats[4].Muted || chats[5].Unread != 7 || chats[4].Members != 3 {
		t.Errorf("flags lost: %+v", chats)
	}
	if again := l.add(testPage()); again != 0 {
		t.Errorf("a repeated page added %d chats", again)
	}
}

func TestFolders(t *testing.T) {
	l := newList(self)
	l.add(testPage())
	contacts := &tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "Contacts"}, Contacts: true,
		IncludePeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 6}}}
	unmutedGroups := &tg.DialogFilter{ID: 3, Title: tg.TextWithEntities{Text: "Groups"}, Groups: true, ExcludeMuted: true}
	unreadPeople := &tg.DialogFilter{ID: 4, Contacts: true, NonContacts: true, ExcludeRead: true,
		ExcludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 2}}}
	shared := &tg.DialogFilterChatlist{ID: 5, IncludePeers: []tg.InputPeerClass{&tg.InputPeerChat{ChatID: 5}}}
	chats, folders := l.snapshot([]tg.DialogFilterClass{&tg.DialogFilterDefault{}, contacts, unmutedGroups, unreadPeople, shared})
	if len(folders) != 4 {
		t.Fatalf("%d folders, want 4 without All chats", len(folders))
	}
	members := func(f model.Folder) (titles []string) {
		for _, c := range chats {
			if f.Contains(c) {
				titles = append(titles, c.Title)
			}
		}
		return titles
	}
	for i, want := range [][]string{
		{"", "Anna S", "Channel"},
		nil,
		nil,
		{"Group"},
	} {
		got := members(folders[i])
		if len(got) != len(want) {
			t.Errorf("folder %d holds %v, want %v", i, got, want)
			continue
		}
		for j := range got {
			if got[j] != want[j] {
				t.Errorf("folder %d holds %v, want %v", i, got, want)
				break
			}
		}
	}
	if folders[0].Kinds[0] != model.KindUser {
		t.Error("a contacts folder should show the personal icon")
	}
}

// chatText and chatSender are what a Russian chat list shows of c's last
// message; topicText and topicSender, of a topic's.
func chatText(c model.Chat) string {
	_, text := localization.For("ru").Preview(c.Last, c.Title, c.Kind == model.KindChannel)
	return text
}

func chatSender(c model.Chat) string {
	sender, _ := localization.For("ru").Preview(c.Last, c.Title, c.Kind == model.KindChannel)
	return sender
}

func topicText(t model.Topic) string {
	_, text := localization.For("ru").Preview(t.Last, "", false)
	return text
}

func topicSender(t model.Topic) string {
	sender, _ := localization.For("ru").Preview(t.Last, "", false)
	return sender
}
