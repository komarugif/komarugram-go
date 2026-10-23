// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/io/key"
)

// editStore is the menu's store that edits, and keeps what it was asked.
type editStore struct {
	*menuStore
	mu    sync.Mutex
	edits []editAsked
	err   error
}

type editAsked struct {
	key      model.MessageKey
	text     string
	entities []model.Entity
}

func (s *editStore) EditMessage(_ context.Context, key model.MessageKey, text string, entities []model.Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits = append(s.edits, editAsked{key, text, entities})
	return s.err
}
func (s *editStore) EditTimeLimit() time.Duration { return 0 }

func (s *editStore) asked() []editAsked {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]editAsked(nil), s.edits...)
}

// newEditHarness is a chat whose messages 5 and 7 are the account's, 7
// with "Message" bold.
func newEditHarness(t *testing.T) (*menuHarness, *editStore) {
	var store *editStore
	h := newMenuHarnessOn(t, func(_ *menuStore, m []model.Message) {
		m[4].Outgoing = true
		m[6].Outgoing = true
		m[6].Entities = []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 7}, {Kind: "mention", Offset: 8, Length: 1}}
	}, func(s *menuStore) model.ConversationStore {
		store = &editStore{menuStore: s}
		return store
	})
	return h, store
}

func TestEditFromMenuSavesFormatting(t *testing.T) {
	h, store := newEditHarness(t)
	h.openMenu(7)
	h.choose(actionEdit)
	h.frames(3)
	d := h.page.composer.draft(1)
	if d.editing == nil || d.editing.Key.MessageID != 7 || d.editor.Text() != "Message 7" {
		t.Fatalf("not editing message 7: %v %q", d.editing, d.editor.Text())
	}
	if want := []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 7}}; !reflect.DeepEqual(d.entities, want) {
		t.Fatalf("the field has %+v, not the message's formatting", d.entities)
	}
	if c := h.page.composer; c.replies.shown == nil || !c.replies.editing {
		t.Fatal("the strip does not show the message edited")
	}
	// "7" made italic with its shortcut, then the edit saved.
	d.editor.SetCaret(8, 9)
	h.router.Queue(key.Event{Name: "I", Physical: "I", Modifiers: key.ModShortcut, State: key.Press})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frames(10)
	asked := store.asked()
	if len(asked) != 1 || asked[0].key.MessageID != 7 || asked[0].text != "Message 7" {
		t.Fatalf("edits asked: %+v", asked)
	}
	want := []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 7}, {Kind: model.EntityItalic, Offset: 8, Length: 1}}
	if !reflect.DeepEqual(asked[0].entities, want) {
		t.Fatalf("saved %+v", asked[0].entities)
	}
	if d.editing != nil || d.editor.Text() != "" || len(d.entities) != 0 {
		t.Fatalf("the field did not get back what it had: %v %q", d.editing, d.editor.Text())
	}
}

func TestUpArrowEditsLastOwnAndEscapeGivesBackTheReply(t *testing.T) {
	h, store := newEditHarness(t)
	h.openMenu(3)
	h.choose(actionReply)
	h.frames(3)
	d := h.page.composer.draft(1)
	h.router.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	h.frames(3)
	if d.editing == nil || d.editing.Key.MessageID != 7 || d.reply != nil {
		t.Fatalf("Up did not edit the last message of the account's: %v", d.editing)
	}
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(3)
	if d.editing != nil || d.reply == nil || d.reply.Key.MessageID != 3 || d.editor.Text() != "" {
		t.Fatalf("Escape did not end the edit: %v, reply %v", d.editing, d.reply)
	}
	// A field with text keeps its Up arrow.
	h.router.Queue(key.EditEvent{Text: "draft"})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	h.frames(3)
	if d.editing != nil {
		t.Fatal("Up in a field with text edited a message")
	}
	if len(store.asked()) != 0 {
		t.Fatal("an edit was saved")
	}
}

func TestEditUnchangedEndsWithoutAsking(t *testing.T) {
	h, store := newEditHarness(t)
	h.openMenu(5)
	h.choose(actionEdit)
	h.frames(3)
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frames(5)
	if d := h.page.composer.draft(1); d.editing != nil {
		t.Fatal("an edit that changed nothing stayed")
	}
	if len(store.asked()) != 0 {
		t.Fatal("Telegram was asked to save a text that did not change")
	}
}

func TestEditForbiddenIsTold(t *testing.T) {
	h, store := newEditHarness(t)
	store.err = model.ErrEditForbidden
	h.openMenu(5)
	h.choose(actionEdit)
	h.frames(3)
	h.router.Queue(key.EditEvent{Text: "!"})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frames(10)
	d := h.page.composer.draft(1)
	if d.editing == nil || d.err == nil {
		t.Fatal("a refused edit ended")
	}
	if got := composerErrorText(d.err, localization.For("en")); got != "You can't edit this message." {
		t.Fatalf("told %q", got)
	}
}

func TestMenuOffersEditOnlyForOwn(t *testing.T) {
	h, _ := newEditHarness(t)
	h.openMenu(6)
	for _, a := range h.page.messageMenu.shown {
		if a == actionEdit {
			t.Fatal("others' message offers Edit")
		}
	}
}

func TestShiftEntitiesKeepsFormatting(t *testing.T) {
	bold := []model.Entity{{Kind: model.EntityBold, Offset: 2, Length: 4}} // "cdef" of "abcdefgh"
	for name, c := range map[string]struct {
		old, new string
		caret    int
		want     []model.Entity
	}{
		"typed inside":      {"abcdefgh", "abcdXefgh", 5, []model.Entity{{Kind: model.EntityBold, Offset: 2, Length: 5}}},
		"typed before":      {"abcdefgh", "Xabcdefgh", 1, []model.Entity{{Kind: model.EntityBold, Offset: 3, Length: 4}}},
		"typed right after": {"abcdefgh", "abcdefXgh", 7, bold},
		"deleted inside":    {"abcdefgh", "abcfgh", 3, []model.Entity{{Kind: model.EntityBold, Offset: 2, Length: 2}}},
		"deleted across":    {"abcdefgh", "abgh", 2, nil},
		"cut its start":     {"abcdefgh", "abefgh", 2, []model.Entity{{Kind: model.EntityBold, Offset: 2, Length: 2}}},
		"equal letters":     {"abccdefgh", "abcccdefgh", 4, nil},
	} {
		entities := bold
		if name == "equal letters" {
			// "cc" bold; a "c" typed between its two goes in it.
			entities = []model.Entity{{Kind: model.EntityBold, Offset: 2, Length: 2}}
			c.want = []model.Entity{{Kind: model.EntityBold, Offset: 2, Length: 3}}
		}
		if got := shiftEntities(c.old, c.new, entities, c.caret); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestToggleFormat(t *testing.T) {
	var e []model.Entity
	e = toggleFormat(e, model.EntityBold, 0, 4)
	e = toggleFormat(e, model.EntityBold, 3, 8)
	if want := []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 8}}; !reflect.DeepEqual(e, want) {
		t.Fatalf("not merged: %+v", e)
	}
	e = toggleFormat(e, model.EntityBold, 2, 4)
	if want := []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 2}, {Kind: model.EntityBold, Offset: 4, Length: 4}}; !reflect.DeepEqual(e, want) {
		t.Fatalf("not taken off the middle: %+v", e)
	}
	e = append(e, model.Entity{Kind: model.EntityEmoji, Offset: 5, Length: 2, DocumentID: 1})
	e = clearFormat(e, 0, 8)
	if want := []model.Entity{{Kind: model.EntityEmoji, Offset: 5, Length: 2, DocumentID: 1}}; !reflect.DeepEqual(e, want) {
		t.Fatalf("clearing took more or less: %+v", e)
	}
	e = setLink(e, 0, 3, "https://telegram.org")
	if linkAt(e, 1, 2) != "https://telegram.org" || linkAt(e, 4, 5) != "" {
		t.Fatalf("link %+v", e)
	}
}

// A quote of a message, over a reply and in the strip over the field,
// keeps the message's custom emoji, which plain text would draw as their
// fallback emoji.
func TestQuoteKeepsCustomEmoji(t *testing.T) {
	l := localization.For("en")
	m := model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "p"}, Text: "hi 🙂\nthere", Entities: []model.Entity{{Kind: model.EntityBold, Offset: 0, Length: 2}, {Kind: model.EntityEmoji, Offset: 3, Length: 2, DocumentID: 77}}}
	runs := quoteRuns(m, l)
	var text string
	emoji := false
	for _, r := range runs {
		text += r.Text
		if r.Emoji == 77 && r.Text == "🙂" {
			emoji = true
		}
		if r.Bold {
			t.Fatal("a quote has the message's formatting")
		}
	}
	if !emoji || text != "Photo, hi 🙂 there" {
		t.Fatalf("quote %q, custom emoji kept: %v", text, emoji)
	}
}
