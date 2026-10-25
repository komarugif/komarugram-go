// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"slices"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// What others do in chats, as Telegram tells it (updateUserTyping and the
// like): typing, recording, sending a file. An action shows for a while
// after it was told, and its doer tells it again while it goes on, as
// Telegram Desktop's SendActionPainter keeps them; it ends sooner when they
// tell it is over, or when their message comes.

const (
	// activityShown is how long an action shows after it was told, as
	// Telegram Desktop's kStatusShowClientside…; a game's, gameShown.
	activityShown = 6 * time.Second
	gameShown     = 10 * time.Second
)

// activities are what others do in chats, by chat.
type activities struct {
	mu     sync.Mutex
	byChat map[int64][]model.Activity
}

// Activities implements model.ActivitySource.
func (s *Store) Activities(chat int64) []model.Activity {
	s.activity.mu.Lock()
	defer s.activity.mu.Unlock()
	return s.activity.byChat[chat]
}

// noteAction keeps what from does in peer, in the thread of root top, as an
// update told it; an action this client does not show, or its end, ends
// what from did there.
func (s *Store) noteAction(peer tg.PeerClass, top int, from tg.PeerClass, action tg.SendMessageActionClass) {
	user, ok := from.(*tg.PeerUser)
	if !ok || user.UserID == s.Me().ID {
		return
	}
	now := time.Now()
	a := model.Activity{User: peerID(user), Top: top, Until: now.Add(activityShown)}
	switch action := action.(type) {
	case *tg.SendMessageTypingAction:
		a.Action = model.ActionTyping
	case *tg.SendMessageRecordAudioAction:
		a.Action = model.ActionRecordVoice
	case *tg.SendMessageUploadAudioAction:
		a.Action, a.Progress = model.ActionUploadVoice, action.Progress
	case *tg.SendMessageRecordVideoAction:
		a.Action = model.ActionRecordVideo
	case *tg.SendMessageUploadVideoAction:
		a.Action, a.Progress = model.ActionUploadVideo, action.Progress
	case *tg.SendMessageRecordRoundAction:
		a.Action = model.ActionRecordRound
	case *tg.SendMessageUploadRoundAction:
		a.Action, a.Progress = model.ActionUploadRound, action.Progress
	case *tg.SendMessageUploadPhotoAction:
		a.Action, a.Progress = model.ActionUploadPhoto, action.Progress
	case *tg.SendMessageUploadDocumentAction:
		a.Action, a.Progress = model.ActionUploadFile, action.Progress
	case *tg.SendMessageGeoLocationAction:
		a.Action = model.ActionChooseLocation
	case *tg.SendMessageChooseContactAction:
		a.Action = model.ActionChooseContact
	case *tg.SendMessageChooseStickerAction:
		a.Action = model.ActionChooseSticker
	case *tg.SendMessageGamePlayAction:
		a.Action, a.Until = model.ActionPlayGame, now.Add(gameShown)
	case *tg.SendMessageTextDraftAction, *tg.SendMessageRichMessageDraftAction, *tg.SendMessageStopDraftAction,
		*tg.SendMessageEmojiInteraction, *tg.SendMessageEmojiInteractionSeen, *tg.SpeakingInGroupCallAction, *tg.SendMessageHistoryImportAction:
		// A bot's draft, which the history shows, and what this client does
		// not show: they leave the action shown as it is.
		return
	}
	if a.Action != model.ActionNone {
		c := s.history
		c.mu.Lock()
		p := c.peers[a.User]
		c.mu.Unlock()
		a.Name = p.First
		if a.Name == "" {
			a.Name = p.Name
		}
	}
	s.putActivity(peerID(peer), a, now)
}

// putActivity keeps a, what someone does in chat, in place of what they
// did there before; one of no action ends it. What ended is let go.
func (s *Store) putActivity(chat int64, a model.Activity, now time.Time) {
	t := &s.activity
	t.mu.Lock()
	old := t.byChat[chat]
	list := make([]model.Activity, 0, len(old)+1)
	for _, o := range old {
		if o.Until.After(now) && (o.User != a.User || o.Top != a.Top) {
			list = append(list, o)
		}
	}
	if a.Action != model.ActionNone {
		list = append(list, a)
	}
	changed := len(list) != len(old) || a.Action != model.ActionNone
	if t.byChat == nil {
		t.byChat = map[int64][]model.Activity{}
	}
	if len(list) == 0 {
		delete(t.byChat, chat)
	} else {
		t.byChat[chat] = list
	}
	t.mu.Unlock()
	if changed {
		s.changed()
	}
}

// endActivity ends what user did in chat, in any thread: their message
// came, which it was about.
func (s *Store) endActivity(chat, user int64) {
	t := &s.activity
	t.mu.Lock()
	old := t.byChat[chat]
	if !slices.ContainsFunc(old, func(a model.Activity) bool { return a.User == user }) {
		t.mu.Unlock()
		return
	}
	list := slices.DeleteFunc(slices.Clone(old), func(a model.Activity) bool { return a.User == user })
	if len(list) == 0 {
		delete(t.byChat, chat)
	} else {
		t.byChat[chat] = list
	}
	t.mu.Unlock()
	s.changed()
}
