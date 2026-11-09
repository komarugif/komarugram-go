// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// A pin's plate quotes the message pinned and, clicked, shows it.
func TestServicePinShowsPinned(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[0].Text = "The pinned message itself"
		pin := &messages[19]
		pin.Kind, pin.Text, pin.ReplyToMessageID = model.MessageService, "", 1
		pin.Service = &model.ServiceAction{Kind: model.ServicePin}
	})
	p := h.page
	pin, _ := p.messageByID(20)
	if got, want := p.serviceText(pin, localization.For("en")), `Ann pinned "The pinned messa…"`; got != want {
		t.Fatalf("pin reads %q, want %q", got, want)
	}
	p.list.Position.BeforeEnd = false
	h.frames(3)
	at := h.messageAt(20)
	// The last row takes the room under the history too, in the list though
	// not in the height kept: the plate is at its top.
	top := at.Y - float32(p.heights.Prefix(20)-p.heights.Prefix(19))/2
	h.press(pointer.ButtonPrimary, f32.Pt(250, top+float32(p.rows[20].bodyTop+p.rows[20].bodySize.Y/2)))
	h.frames(3)
	if p.list.Position.First != 0 {
		t.Fatalf("the click on the pin shows message %d, not the pinned one", p.messages[p.list.Position.First].Key.MessageID)
	}
}

// An action Telegram Desktop shows nothing for takes no room.
func TestServiceHiddenTakesNoRoom(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		m := &messages[5]
		m.Kind, m.Text = model.MessageService, ""
		m.Service = &model.ServiceAction{Kind: model.ServiceHidden}
	})
	if got := h.page.measures[6].HeightPx; got != 0 {
		t.Fatalf("a hidden action is %d px tall", got)
	}
}

// TestRenderService saves service messages of the demo chat, for looking
// at them: SERVICE_PNG=/tmp/service.png.
func TestRenderService(t *testing.T) {
	path := os.Getenv("SERVICE_PNG")
	if path == "" {
		t.Skip("set SERVICE_PNG to a file")
	}
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		actions := []model.ServiceAction{
			{Kind: model.ServiceAddUser, Peers: []model.ServicePeer{{ID: 7, Name: "Ольга"}, {ID: 8, Name: "Павел"}, {ID: 9, Name: "Игорь"}}},
			{Kind: model.ServicePin},
			{Kind: model.ServiceEditTitle, Title: "Команда разработки"},
			{Kind: model.ServiceTTL, Count: 7 * 86400},
			{Kind: model.ServicePhoneCall, Count: 754},
			{Kind: model.ServicePhoneCall, Reason: "missed", Video: true},
			{Kind: model.ServiceGroupCall, Count: 3600 * 3},
			{Kind: model.ServiceHidden},
		}
		for i, a := range actions {
			m := &messages[12+i]
			m.Kind, m.Text, m.Service = model.MessageService, "", &a
			m.Outgoing = i == 4
			if a.Kind == model.ServicePin {
				m.ReplyToMessageID = 11
			}
		}
		messages[10].Text = "Сборка прошла, можно выкатывать"
	})
	p := h.page
	p.list.Position.BeforeEnd = false
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 900), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}

// lookupStore finds messages the history has not loaded.
type lookupStore struct {
	*menuStore
	asked []model.MessageID
}

func (s *lookupStore) LookupMessage(chat int64, id model.MessageID) (model.Message, model.LookupState) {
	s.asked = append(s.asked, id)
	if id == 404 {
		return model.Message{}, model.LookupGone
	}
	return model.Message{Key: model.MessageKey{ChatID: chat, MessageID: id}, Text: "Far away"}, model.LookupFound
}

// What a pin or a reply names beyond the loaded history is looked up.
func TestServiceLooksUpUnloaded(t *testing.T) {
	var store *lookupStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		pin := &messages[18]
		pin.Kind, pin.Text, pin.ReplyToMessageID = model.MessageService, "", 999
		pin.Service = &model.ServiceAction{Kind: model.ServicePin}
		messages[19].ReplyToMessageID = 404
	}, func(s *menuStore) model.ConversationStore {
		store = &lookupStore{menuStore: s}
		return store
	})
	p := h.page
	pin, _ := p.messageByID(19)
	if got, want := p.serviceText(pin, localization.For("en")), `Ann pinned "Far away"`; got != want {
		t.Fatalf("pin reads %q, want %q", got, want)
	}
	unpinned := pin
	unpinned.ReplyToMessageID = 404
	if got, want := p.serviceText(unpinned, localization.For("en")), `Ann pinned Deleted message`; got != want {
		t.Fatalf("a pin of a deleted message reads %q, want %q", got, want)
	}
	found := false
	for _, id := range store.asked {
		found = found || id == 404
	}
	if !found {
		t.Fatalf("the reply's message was never looked up: %v", store.asked)
	}
}
