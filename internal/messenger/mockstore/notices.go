// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"time"

	"komarugram/internal/messenger/model"
)

// demoIncoming are the messages Receive brings, in turn.
var demoIncoming = []struct {
	chat         int64
	sender, text string
}{
	{2, "", "Ты уже дома? Позвоню через пять минут"},
	{3, "Игорь", "Ревью готово, глянь, когда будет минутка"},
	{8, "", "Новые токены для уведомлений и бейджей"},
	{4, "", "Канал без звука: уведомления не будет"},
}

func (s *Store) SetNotices(f func(model.MessageNotice)) {
	s.mu.Lock()
	s.notices = f
	s.mu.Unlock()
}

// Receive brings the next of the demo's incoming messages, as if written
// just now, and tells of it.
func (s *Store) Receive() {
	s.mu.Lock()
	in := demoIncoming[s.received%len(demoIncoming)]
	s.received++
	s.mu.Unlock()
	s.History(in.chat)
	s.mu.Lock()
	h := s.histories[in.chat]
	id := model.MessageID(1)
	if len(h.Messages) > 0 {
		id = h.Messages[len(h.Messages)-1].Key.MessageID + 1
	}
	m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: in.chat, MessageID: id}, Text: in.text, SenderID: 42, SenderName: in.sender, Date: time.Now(), ContentRevision: 1}
	h.Messages = append(append([]model.Message(nil), h.Messages...), m)
	h.Revision++
	s.histories[in.chat] = h
	var chat model.Chat
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == in.chat {
			chats[i].Last, chats[i].LastTime = model.Preview{Text: in.text, Sender: in.sender}, m.Date
			chats[i].Unread++
			chat = chats[i]
		}
	}
	model.SortChats(chats)
	s.chats = chats
	notices := s.notices
	s.mu.Unlock()
	if notices != nil && chat.ID != 0 {
		notices(model.MessageNotice{Chat: chat, Message: model.Preview{Text: in.text, Sender: in.sender}})
	}
}
