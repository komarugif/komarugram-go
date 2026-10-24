// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// What others do shows until it is over: told to end, or by their
// message; what this client does not show leaves it as it is.
func TestActivities(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 7, FirstName: "Anna", LastName: "Smith"}, &tg.User{ID: 8, FirstName: "Boris"}}, nil)
	group := &tg.PeerChat{ChatID: 3}
	chat := peerID(group)
	s.noteAction(group, 0, &tg.PeerUser{UserID: 7}, &tg.SendMessageTypingAction{})
	s.noteAction(group, 0, &tg.PeerUser{UserID: 8}, &tg.SendMessageUploadPhotoAction{Progress: 30})
	acts := s.Activities(chat)
	if len(acts) != 2 || acts[0].Name != "Anna" || acts[0].Action != model.ActionTyping || acts[1].Action != model.ActionUploadPhoto || acts[1].Progress != 30 {
		t.Fatalf("activities %+v", acts)
	}
	if until := time.Until(acts[0].Until); until < 5*time.Second || until > activityShown {
		t.Fatalf("shown for %v", until)
	}
	// Told again, it replaces what Anna did; an emoji's interaction is not
	// shown, and leaves it.
	s.noteAction(group, 0, &tg.PeerUser{UserID: 7}, &tg.SendMessageRecordAudioAction{})
	s.noteAction(group, 0, &tg.PeerUser{UserID: 7}, &tg.SendMessageEmojiInteractionSeen{Emoticon: "🎉"})
	if acts = s.Activities(chat); len(acts) != 2 || acts[1].User != 7 || acts[1].Action != model.ActionRecordVoice {
		t.Fatalf("again %+v", acts)
	}
	s.noteAction(group, 0, &tg.PeerUser{UserID: 7}, &tg.SendMessageCancelAction{})
	if acts = s.Activities(chat); len(acts) != 1 || acts[0].User != 8 {
		t.Fatalf("after the end %+v", acts)
	}
	// Boris's message ends what he did to send it.
	msg := &tg.Message{ID: 1, PeerID: group, FromID: &tg.PeerUser{UserID: 8}, Message: "photo", Date: int(time.Now().Unix())}
	if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: msg}}); err != nil {
		t.Fatal(err)
	}
	if acts = s.Activities(chat); len(acts) != 0 {
		t.Fatalf("after the message %+v", acts)
	}
	// The account's own, from another device, is not shown.
	s.me.ID = 7
	s.noteAction(group, 0, &tg.PeerUser{UserID: 7}, &tg.SendMessageTypingAction{})
	if acts = s.Activities(chat); len(acts) != 0 {
		t.Fatalf("own %+v", acts)
	}
}

// actionAPI records the actions told to Telegram.
type actionAPI struct {
	mu   sync.Mutex
	told []string
}

func (a *actionAPI) client() *tg.Client {
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch r := in.(type) {
		case *tg.MessagesSetTypingRequest:
			a.told = append(a.told, fmt.Sprintf("%d %T", peerOf(r.Peer), r.Action))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesSaveDraftRequest:
			a.told = append(a.told, fmt.Sprintf("draft %d %q", peerOf(r.Peer), r.Message))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesSendMessageRequest:
			a.told = append(a.told, fmt.Sprintf("send %q clear %v", r.Message, r.ClearDraft))
			return &tg.UpdateShortSentMessage{ID: 60, Date: 10}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

func peerOf(p tg.InputPeerClass) int64 {
	switch p := p.(type) {
	case *tg.InputPeerUser:
		return p.UserID
	case *tg.InputPeerChannel:
		return p.ChannelID
	case *tg.InputPeerChat:
		return p.ChatID
	}
	return 0
}

// settle returns what was told once nothing more comes.
func (a *actionAPI) settle() []string {
	for last := -1; ; time.Sleep(30 * time.Millisecond) {
		a.mu.Lock()
		n := len(a.told)
		a.mu.Unlock()
		if n == last {
			break
		}
		last = n
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.told)
}

// An action is told as Ghost Mode allows, every few seconds at most, and
// only where someone sees it, as Telegram Desktop tells it: not in Saved
// Messages, a channel or a bot's chat, nor to a user long offline.
func TestSendActionWhereSeen(t *testing.T) {
	s := testStore(t)
	api := &actionAPI{}
	s.history.api = api.client()
	peers := map[int64]peerRecord{
		5: {Kind: "user", ID: 5, Hash: 1},
		6: {Kind: "user", ID: 6, Hash: 1, Rights: peerRights{Self: true}},
		7: {Kind: "user", ID: 7, Hash: 1, Rights: peerRights{Bot: true}},
		8: {Kind: "user", ID: 8, Hash: 1},
		9: {Kind: "user", ID: 9, Hash: 1},
	}
	channel, group := peerID(&tg.PeerChannel{ChannelID: 20}), peerID(&tg.PeerChannel{ChannelID: 21})
	peers[channel] = peerRecord{Kind: "channel", ID: 20, Hash: 1, Rights: peerRights{Broadcast: true}}
	peers[group] = peerRecord{Kind: "channel", ID: 21, Hash: 1}
	for id, p := range peers {
		s.history.peers[id] = p
	}
	now := int(time.Now().Unix())
	s.setPresence(8, &tg.UserStatusOffline{WasOnline: now - 600})
	s.setPresence(9, &tg.UserStatusOffline{WasOnline: now - 10})

	s.SetGhost(model.Ghost{SendTyping: true, SendUploads: true})
	for _, chat := range []int64{5, 6, 7, 8, 9, channel, group} {
		s.SendAction(chat, model.ActionTyping)
	}
	s.SendAction(5, model.ActionTyping)          // too soon
	s.SendAction(5, model.ActionRecordVoice)     // Ghost Mode hides it
	s.sendAction(5, model.ActionUploadPhoto, 40) // another action
	told := api.settle()
	slices.Sort(told)
	want := []string{"21 *tg.SendMessageTypingAction", "5 *tg.SendMessageTypingAction", "5 *tg.SendMessageUploadPhotoAction", "9 *tg.SendMessageTypingAction"}
	if !slices.Equal(told, want) {
		t.Fatalf("told\n%q\nwant\n%q", told, want)
	}
}

// A chat's draft comes with the dialogs and their updates; one saved is
// told to Telegram once, and the next message sent clears it.
func TestCloudDrafts(t *testing.T) {
	s := testStore(t)
	api := &actionAPI{}
	s.history.api = api.client()
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 1}
	old, recent := time.Unix(1000, 0), time.Unix(5000, 0)
	s.chats = []model.Chat{{ID: 4, LastTime: recent}, {ID: 5, LastTime: old}}

	s.applyDraft(&tg.UpdateDraftMessage{Peer: &tg.PeerUser{UserID: 5}, Draft: &tg.DraftMessage{Message: "later", Date: 9000,
		Entities: []tg.MessageEntityClass{&tg.MessageEntityItalic{Offset: 0, Length: 5}}}})
	chats := s.Chats()
	if chats[0].ID != 5 || chats[0].Draft == nil || chats[0].Draft.Text != "later" || len(chats[0].Draft.Entities) != 1 {
		t.Fatalf("a draft newer than the list did not take the chat up: %+v", chats)
	}
	// A thread's is not the chat's.
	s.applyDraft(&tg.UpdateDraftMessage{Peer: &tg.PeerUser{UserID: 5}, TopMsgID: 3, Draft: &tg.DraftMessageEmpty{}})
	if s.draft(5) == nil {
		t.Fatal("a thread's draft cleared the chat's")
	}

	s.SaveDraft(5, &model.Draft{Text: "later", Entities: []model.Entity{{Kind: "italic", Offset: 0, Length: 5}}}) // the same
	s.SaveDraft(5, &model.Draft{Text: "sooner"})
	s.SaveDraft(5, &model.Draft{Text: "sooner"}) // saved already
	if told := api.settle(); !slices.Equal(told, []string{`draft 5 "sooner"`}) {
		t.Fatalf("told %q", told)
	}
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{Text: "sent", RandomID: 1}); err != nil {
		t.Fatal(err)
	}
	if told := api.settle(); told[len(told)-1] != `send "sent" clear true` || s.draft(5) != nil {
		t.Fatalf("told %q, draft %+v", told, s.draft(5))
	}
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{Text: "again", RandomID: 2}); err != nil {
		t.Fatal(err)
	}
	if told := api.settle(); told[len(told)-1] != `send "again" clear false` {
		t.Fatalf("told %q", told)
	}
	s.applyDraft(&tg.UpdateDraftMessage{Peer: &tg.PeerUser{UserID: 5}, Draft: &tg.DraftMessage{Message: "  ", Date: 9500}})
	if s.draft(5) != nil {
		t.Fatal("a draft of spaces is a draft")
	}
}
