// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"slices"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// demoActivity is a scene of the demo: who does what in a chat, from a
// moment of its cycle, for as long as Telegram shows an action told once.
type demoActivity struct {
	at   time.Duration
	chat int64
	acts []model.Activity
}

// demoActivityCycle is how long the demo's scenes take before they begin
// again.
const demoActivityCycle = 40 * time.Second

var demoActivities = []demoActivity{
	{2 * time.Second, 2, []model.Activity{{User: 2, Name: "Анна", Action: model.ActionTyping}}},
	{9 * time.Second, 3, []model.Activity{{User: 101, Name: "Игорь", Action: model.ActionTyping}, {User: 102, Name: "Ольга", Action: model.ActionTyping}}},
	{16 * time.Second, 7, []model.Activity{{User: 103, Name: "Ольга", Action: model.ActionRecordVoice}}},
	{23 * time.Second, 9, []model.Activity{{User: 9, Name: "Дмитрий", Action: model.ActionUploadPhoto, Progress: 40}}},
	{30 * time.Second, 14, []model.Activity{{User: 104, Name: "Павел", Action: model.ActionChooseSticker}}},
}

// StartActivities plays the demo's scenes of what others do in its chats,
// over and over, telling of each as the store changes; it runs until the
// process ends.
func (s *Store) StartActivities() {
	go func() {
		defer crash.Recover("demo activities", nil)
		for {
			start := time.Now()
			for _, scene := range demoActivities {
				time.Sleep(time.Until(start.Add(scene.at)))
				until := time.Now().Add(6 * time.Second)
				acts := slices.Clone(scene.acts)
				for i := range acts {
					acts[i].Until = until
				}
				s.mu.Lock()
				if s.activities == nil {
					s.activities = map[int64][]model.Activity{}
				}
				s.activities[scene.chat] = acts
				changed := s.changed
				s.mu.Unlock()
				if changed != nil {
					changed()
				}
			}
			time.Sleep(time.Until(start.Add(demoActivityCycle)))
		}
	}()
}

// Activities implements model.ActivitySource.
func (s *Store) Activities(chat int64) []model.Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activities[chat]
}

// SetActivities makes acts what others do in chat, for a render.
func (s *Store) SetActivities(chat int64, acts []model.Activity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activities == nil {
		s.activities = map[int64][]model.Activity{}
	}
	s.activities[chat] = acts
}

// SaveDraft implements model.DraftStore: the demo keeps the draft, as
// Telegram would for the account's other devices.
func (s *Store) SaveDraft(chat int64, d *model.Draft) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.chats, func(c model.Chat) bool { return c.ID == chat })
	if i < 0 || s.chats[i].Draft.Equal(d) {
		return
	}
	if d != nil {
		saved := *d
		saved.Date = time.Now()
		d = &saved
	}
	chats := slices.Clone(s.chats)
	chats[i].Draft = d
	model.SortChats(chats)
	s.chats = chats
}
