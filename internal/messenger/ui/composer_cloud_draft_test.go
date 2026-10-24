// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// draftStore keeps drafts, and counts the actions told.
type draftStore struct {
	model.ConversationStore
	chats []model.Chat
	saved []*model.Draft
	acted []model.ChatAction
}

func (s *draftStore) Picker(context.Context, model.PickerRequest) (model.PickerPage, error) {
	return model.PickerPage{}, nil
}
func (s *draftStore) Send(context.Context, int64, model.OutgoingMessage) error { return nil }
func (s *draftStore) Me() model.Profile                                        { return model.Profile{} }
func (s *draftStore) Folders() []model.Folder                                  { return nil }
func (s *draftStore) Chats() []model.Chat                                      { return s.chats }
func (s *draftStore) SaveDraft(chat int64, d *model.Draft) {
	s.saved = append(s.saved, d)
	for i := range s.chats {
		if s.chats[i].ID == chat {
			s.chats[i].Draft = d
		}
	}
}
func (s *draftStore) SetGhost(model.Ghost)                   {}
func (s *draftStore) Ghost() model.Ghost                     { return model.Ghost{} }
func (s *draftStore) MarkRead(int64, model.MessageID, bool)  {}
func (s *draftStore) SetOnline(bool)                         {}
func (s *draftStore) SendAction(_ int64, a model.ChatAction) { s.acted = append(s.acted, a) }

// The field of a chat begins with its draft, which is not typing; what it
// holds when the chat is left is the draft, and a cleared field none.
func TestComposerCloudDraft(t *testing.T) {
	store := &draftStore{chats: []model.Chat{{ID: 5, Draft: &model.Draft{Text: "later", Date: time.Unix(100, 0)}}}}
	c := newMessageComposer(store, func() {})
	defer c.cancel()
	c.takeDraft(5)
	d := c.draft(5)
	if d.editor.Text() != "later" {
		t.Fatalf("field %q", d.editor.Text())
	}
	c.textChanged(d)
	if len(store.acted) != 0 {
		t.Fatalf("the draft taken was told as typing: %v", store.acted)
	}
	d.editor.SetText("sooner")
	c.textChanged(d)
	c.keepDraft(5)
	if len(store.saved) != 1 || store.saved[0] == nil || store.saved[0].Text != "sooner" {
		t.Fatalf("saved %+v", store.saved)
	}
	// Taken already, it is not put in the field again once cleared.
	d.editor.SetText("")
	c.textChanged(d)
	store.chats[0].Draft = &model.Draft{Text: "sooner", Date: time.Unix(100, 0)}
	c.keepDraft(5)
	if len(store.saved) != 2 || store.saved[1] != nil {
		t.Fatalf("a cleared field saved %+v", store.saved)
	}
	// A message being edited is not a draft.
	d.editor.SetText("edit")
	d.editing = &model.Message{}
	c.keepDraft(5)
	if len(store.saved) != 2 {
		t.Fatalf("an edit saved as a draft: %+v", store.saved[2])
	}
}
