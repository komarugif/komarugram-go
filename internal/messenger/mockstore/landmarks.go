// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"slices"

	"komarugram/internal/messenger/model"
)

// LandmarkChat is the chat landmarks are looked for in: every demo chat
// but the bots has the same history.
const LandmarkChat = 2

// landmarks are the messages of the demo's history a test goes to by name
// (-demo-open), each found by what it is, so that they stay found when the
// history changes.
var landmarks = map[string]func(model.Message) bool{
	"album":         func(m model.Message) bool { return m.GroupedID != 0 },
	"sticker":       mediaIs("demo/tgs", model.MessageSticker),
	"video-sticker": mediaIs("demo/webm", model.MessageSticker),
	"gif":           mediaIs("demo/gifv", model.MessageGIF),
	"video":         mediaIs("demo/video", model.MessageVideo),
	"voice":         mediaIs("demo/voice", model.MessageVoice),
	"music":         mediaIs("demo/music", model.MessageMusic),
	"round":         mediaIs("demo/round", model.MessageRound),
	"file":          mediaIs("demo/markdown", model.MessageFile),
	"poll":          func(m model.Message) bool { return m.Kind == model.MessagePoll },
	"quiz":          func(m model.Message) bool { return m.Poll != nil && m.Poll.Quiz },
	"todo":          func(m model.Message) bool { return m.Todo != nil },
	"edited":        func(m model.Message) bool { return !m.EditedAt.IsZero() },
	"article":       func(m model.Message) bool { return m.Rich != nil },
	"service":       func(m model.Message) bool { return m.Kind == model.MessageService },
	"story":         func(m model.Message) bool { return m.StoryShown() && !m.StoryOf.Mention },
}

func mediaIs(id string, kind model.MessageKind) func(model.Message) bool {
	return func(m model.Message) bool { return m.Kind == kind && m.Media != nil && m.Media.ID == id }
}

// Landmarks are the names Landmark knows, sorted.
func Landmarks() []string {
	names := make([]string, 0, len(landmarks))
	for name := range landmarks {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Landmark is the message of the demo's history named name: the first of
// its kind in LandmarkChat.
func (s *Store) Landmark(name string) (model.MessageKey, bool) {
	is := landmarks[name]
	if is == nil {
		return model.MessageKey{}, false
	}
	for _, m := range s.History(LandmarkChat).Messages {
		if is(m) {
			return m.Key, true
		}
	}
	return model.MessageKey{}, false
}
