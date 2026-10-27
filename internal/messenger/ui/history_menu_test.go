// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// menuStore is a chat whose account may do everything but what a test
// forbids, and whose custom emoji come from sets named after their ids.
type menuStore struct {
	benchmarkHistory
	mu         sync.Mutex
	sent       []model.OutgoingMessage
	muted      bool
	noForwards bool
	// forwarded are the forwards asked for, as "from:ids→to".
	forwarded []string
	// sets maps a custom emoji to its set.
	sets map[int64]int64
}

func (s *menuStore) Picker(context.Context, model.PickerRequest) (model.PickerPage, error) {
	return model.PickerPage{}, nil
}
func (s *menuStore) Send(_ context.Context, _ int64, m model.OutgoingMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	return nil
}
func (s *menuStore) MessageRights(int64, []model.Message) model.MessageRights {
	return model.MessageRights{Forward: !s.noForwards, Save: !s.noForwards, Delete: true}
}
func (s *menuStore) CanSend(int64) bool { return !s.muted }
func (s *menuStore) ForwardMessages(_ context.Context, from int64, ids []model.MessageID, to int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forwarded = append(s.forwarded, fmt.Sprintf("%d:%v→%d", from, ids, to))
	return nil
}
func (s *menuStore) DeleteMessages(context.Context, int64, []model.MessageID, bool) error {
	return nil
}
func (s *menuStore) MessageLink(chat int64, id model.MessageID) (string, bool, bool) {
	return fmt.Sprintf("https://t.me/c/1/%d", id), false, true
}
func (s *menuStore) CustomEmoji(_ context.Context, id int64) (model.Message, error) {
	return model.Message{Media: &model.MessageMedia{StickerSet: &model.StickerSetRef{Type: "id", ID: s.sets[id]}}}, nil
}
func (s *menuStore) StickerSet(_ context.Context, ref model.StickerSetRef) (model.StickerSet, error) {
	return model.StickerSet{Ref: ref, Title: fmt.Sprintf("Set %d", ref.ID), Emoji: true}, nil
}
func (s *menuStore) SetStickerSetInstalled(context.Context, model.StickerSetRef, bool) error {
	return nil
}

type menuHarness struct {
	t      *testing.T
	router input.Router
	store  *menuStore
	page   *chatPage
	now    time.Time
	// still turns animations off.
	still bool
}

func newMenuHarness(t *testing.T, edit func(*menuStore, []model.Message)) *menuHarness {
	return newMenuHarnessOn(t, edit, nil)
}

// newMenuHarnessOn is newMenuHarness on the store wrap makes of the menu
// store, such as one that also takes reactions.
func newMenuHarnessOn(t *testing.T, edit func(*menuStore, []model.Message), wrap func(*menuStore) model.ConversationStore) *menuHarness {
	t.Helper()
	h := &menuHarness{t: t, now: time.Unix(1000, 0)}
	var messages []model.Message
	for i := 1; i <= 20; i++ {
		messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(i)}, Date: h.now, Text: fmt.Sprintf("Message %d", i), SenderName: "Ann", ContentRevision: 1})
	}
	h.store = &menuStore{}
	if edit != nil {
		edit(h.store, messages)
	}
	h.store.benchmarkHistory = benchmarkHistory{h: model.History{Messages: messages, Revision: 1}}
	var source model.ConversationStore = h.store
	if wrap != nil {
		source = wrap(h.store)
	}
	h.page = newChatPage(source, func() {})
	h.page.images = &imageOps{}
	t.Cleanup(h.page.Close)
	h.frame()
	h.page.list.Position.First, h.page.list.Position.Offset, h.page.list.Position.BeforeEnd = 0, 0, true
	h.frame()
	return h
}
func (h *menuHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 700)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	if h.still {
		wdk.SetAnimationsEnabled(gtx, false)
	}
	h.page.images.BeginFrame()
	h.page.media.BeginFrame()
	h.page.Layout(gtx, model.Chat{ID: 1, Title: "Chat", Kind: model.KindGroup}, localization.For("en"), false)
	h.page.layoutDialogs(gtx, localization.For("en"))
	h.page.media.EndFrame()
	h.page.images.EndFrame()
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}
func (h *menuHarness) frames(n int) {
	for range n {
		h.frame()
	}
}
func (h *menuHarness) press(button pointer.Buttons, pos f32.Point) {
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: button, Position: pos, Time: time.Duration(h.now.UnixNano())})
		h.frame()
	}
}

// messageAt is a point on message id, in the page.
func (h *menuHarness) messageAt(id model.MessageID) f32.Point {
	p := h.page
	for i, m := range p.messages {
		if m.Key.MessageID == id {
			y := p.heights.Prefix(i) - p.heights.Prefix(p.list.Position.First) - int64(p.list.Position.Offset)
			return f32.Pt(120, float32(y)+float32(p.heights.Prefix(i+1)-p.heights.Prefix(i))/2)
		}
	}
	h.t.Fatalf("message %d not shown", id)
	return f32.Point{}
}

// openMenu right-clicks message id and waits for the menu to open.
func (h *menuHarness) openMenu(id model.MessageID) {
	h.t.Helper()
	// Input goes by the last frame's areas: one without what a test hid.
	h.frame()
	h.press(pointer.ButtonSecondary, h.messageAt(id))
	h.frames(30)
	if !h.page.messageMenu.open || h.page.messageMenu.id != id {
		h.t.Fatalf("menu of message %d did not open: %+v", id, h.page.messageMenu.id)
	}
}

// choose clicks action a of the open menu.
func (h *menuHarness) choose(a menuAction) {
	h.t.Helper()
	m := &h.page.messageMenu
	y := m.rect.Min.Y + menuPadding
	if strip := m.reactions.height(layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}); strip > 0 {
		y = m.rect.Min.Y + strip
	}
	for _, shown := range m.shown {
		height := menuItemHeight
		if shown == actionEmojiPacks {
			y += 9
			height = menuPackHeight
		}
		if shown == a {
			h.press(pointer.ButtonPrimary, f32.Pt(float32(m.rect.Min.X+60), float32(y+height/2)))
			h.frame()
			return
		}
		y += height
	}
	h.t.Fatalf("menu has no %d: %v", a, m.shown)
}

func TestMessageMenuShowsTelegramActions(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(3)
	want := []menuAction{actionReply, actionCopyText, actionCopyLink, actionForward, actionDelete, actionSelect, actionRepeat}
	if !reflect.DeepEqual(h.page.messageMenu.shown, want) {
		t.Fatalf("actions %v, want %v", h.page.messageMenu.shown, want)
	}
	h.choose(actionCopyText)
	if _, text, ok := h.router.WriteClipboard(); !ok || string(text) != "Message 3" {
		t.Fatalf("copied %q", text)
	}
	if h.page.messageMenu.open {
		t.Fatal("menu stayed open")
	}
	h.openMenu(4)
	h.choose(actionCopyLink)
	if _, text, ok := h.router.WriteClipboard(); !ok || string(text) != "https://t.me/c/1/4" {
		t.Fatalf("copied link %q", text)
	}
	if h.page.toast.Text() == "" {
		t.Fatal("private link copied without its notice")
	}
}

func TestMessageMenuFollowsRights(t *testing.T) {
	h := newMenuHarness(t, func(s *menuStore, _ []model.Message) { s.muted, s.noForwards = true, true })
	h.openMenu(3)
	for _, a := range h.page.messageMenu.shown {
		if a == actionReply || a == actionForward || a == actionRepeat {
			t.Fatalf("forbidden action shown: %v", h.page.messageMenu.shown)
		}
	}
	h2 := newMenuHarness(t, func(_ *menuStore, m []model.Message) { m[2].NoForwards = true })
	h2.openMenu(3)
	for _, a := range h2.page.messageMenu.shown {
		if a == actionCopyText {
			t.Fatal("protected text may be copied")
		}
	}
}

// Repeat sends the message again to its own chat, as a forward, as
// AyuGram's does; a channel's posts are not repeated.
func TestMessageMenuRepeats(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(3)
	h.choose(actionRepeat)
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.store.mu.Lock()
		forwarded := append([]string(nil), h.store.forwarded...)
		h.store.mu.Unlock()
		if len(forwarded) > 0 {
			if !reflect.DeepEqual(forwarded, []string{"1:[3]→1"}) {
				t.Fatalf("forwarded %v", forwarded)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing repeated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m := h.page.messages[2]
	h.page.kind = model.KindChannel
	if h.page.canRepeat(m) {
		t.Fatal("a channel's post may be repeated")
	}
	h.page.kind = model.KindGroup
	m.Kind = model.MessageService
	if h.page.canRepeat(m) {
		t.Fatal("a service message may be repeated")
	}
}

// A right click is not a left one: it neither selects nor drags.
func TestRightClickLeavesSelection(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(3)
	if len(h.page.selection.selected) != 0 || h.page.selection.dragging {
		t.Fatal("right click selected")
	}
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(20)
	if h.page.messageMenu.open {
		t.Fatal("Escape left the menu open")
	}
	// A click beside the menu closes it and does nothing else.
	h.openMenu(3)
	h.press(pointer.ButtonPrimary, f32.Pt(490, 60))
	if h.page.messageMenu.open || len(h.page.selection.selected) != 0 {
		t.Fatal("click outside did not just close the menu")
	}
}

func TestMessageMenuSelection(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(2)
	h.choose(actionSelect)
	if !h.page.selection.selected[2] {
		t.Fatal("Select did not select")
	}
	h.openMenu(2)
	want := []menuAction{actionReply, actionCopyLink, actionForwardSelected, actionDeleteSelected, actionClearSelection}
	if !reflect.DeepEqual(h.page.messageMenu.shown, want) {
		t.Fatalf("selected actions %v, want %v", h.page.messageMenu.shown, want)
	}
	// Escape closes the menu before it clears the selection.
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(20)
	if h.page.messageMenu.open || !h.page.selection.selected[2] {
		t.Fatal("Escape did not close only the menu")
	}
	// Forwarding another message leaves the selection alone.
	h.openMenu(5)
	h.choose(actionForward)
	f := &h.page.forwarding
	if !f.modal.Shown() || !reflect.DeepEqual(f.ids, []model.MessageID{5}) || f.selection {
		t.Fatalf("forward of one message: %v %v", f.ids, f.selection)
	}
	f.modal.Hide()
	h.openMenu(6)
	h.choose(actionDelete)
	if d := &h.page.deletion; !d.modal.Shown() || !reflect.DeepEqual(d.ids, []model.MessageID{6}) {
		t.Fatalf("delete of one message: %v", d.ids)
	}
	h.page.deletion.modal.Hide()
	h.openMenu(2)
	h.choose(actionClearSelection)
	if len(h.page.selection.selected) != 0 {
		t.Fatal("selection not cleared")
	}
}

func TestMessageMenuReply(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(3)
	h.choose(actionReply)
	d := h.page.composer.draft(1)
	if d.reply == nil || d.reply.Key.MessageID != 3 {
		t.Fatal("no reply to message 3")
	}
	h.frame()
	h.router.Queue(key.EditEvent{Text: "yes"})
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frames(10)
	h.store.mu.Lock()
	sent := append([]model.OutgoingMessage(nil), h.store.sent...)
	h.store.mu.Unlock()
	if len(sent) != 1 || sent[0].Text != "yes" || sent[0].ReplyTo != 3 {
		t.Fatalf("sent %+v", sent)
	}
	if d.reply != nil {
		t.Fatal("reply kept after it was sent")
	}
}

// The reply strip is as tall as its name and text: at 44 dp it cut the
// text's last pixels off (37 px of them in 34).
func TestReplyStripHoldsItsLines(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(3)
	h.choose(actionReply)
	h.frames(40)
	c := h.page.composer
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: h.now}
	inner := c.replyHeight(gtx, 1, false) - replyGap - 2*replyPad
	if c.replies.lines == 0 || inner < c.replies.lines {
		t.Fatalf("%d px for lines %d px tall", inner, c.replies.lines)
	}
}

func TestReplyStripCancels(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(3)
	h.choose(actionReply)
	h.frames(5)
	// The floating capsule of a 500×700 page spans y 632–688 from x 16 to
	// 484; the strip is over it, and its cross is at its end.
	composer := h.page.composer
	cross := f32.Pt(462, 604)
	h.press(pointer.ButtonPrimary, f32.Pt(200, 604))
	if composer.draft(1).reply == nil {
		t.Fatal("a click on the strip stopped replying")
	}
	h.press(pointer.ButtonPrimary, cross)
	if composer.draft(1).reply != nil {
		t.Fatal("the cross did not stop replying")
	}
}

func TestMessageMenuEmojiPacks(t *testing.T) {
	one := []model.Entity{{Kind: "emoji", DocumentID: 1}, {Kind: "emoji", DocumentID: 2}}
	h := newMenuHarness(t, func(s *menuStore, m []model.Message) {
		s.sets = map[int64]int64{1: 7, 2: 7, 3: 8}
		m[2].Entities = one
		m[3].Entities = append(one, model.Entity{Kind: "emoji", DocumentID: 3})
	})
	h.openMenu(3)
	shown := h.page.messageMenu.shown
	if shown[len(shown)-1] != actionEmojiPacks {
		t.Fatalf("no emoji packs item: %v", shown)
	}
	if got := h.page.menuLabel(actionEmojiPacks, localization.For("en")); got != "This message contains emoji from Set 7 pack." {
		t.Fatalf("label %q", got)
	}
	h.choose(actionEmojiPacks)
	if !h.page.stickers.modal.Shown() || h.page.stickers.ref.ID != 7 {
		t.Fatal("the set did not open")
	}
	h.page.stickers.stop()
	h.openMenu(4)
	if got := h.page.menuLabel(actionEmojiPacks, localization.For("en")); got != "This message contains emoji from 2 packs." {
		t.Fatalf("label %q", got)
	}
	h.choose(actionEmojiPacks)
	if !h.page.emojiPacks.modal.Shown() || len(h.page.emojiPacks.refs) != 2 {
		t.Fatal("the sets did not open")
	}
	// Without custom emoji there is no such item.
	h.page.emojiPacks.stop()
	h.openMenu(5)
	for _, a := range h.page.messageMenu.shown {
		if a == actionEmojiPacks {
			t.Fatal("emoji packs of a message without them")
		}
	}
}

// With a word selected in a message, the menu copies it rather than the
// whole text; and Escape, with the history focused, closes the menu first.
func TestMessageMenuCopiesSelectedText(t *testing.T) {
	h := newMenuHarness(t, nil)
	h.openMenu(2)
	h.choose(actionSelect)
	h.frame()
	at := h.messageAt(7)
	var row *messageRow
	for x := float32(20); x < 300 && row == nil; x += 4 {
		at.X = x
		h.press(pointer.ButtonPrimary, at)
		h.press(pointer.ButtonPrimary, at)
		if h.page.activeText != nil && h.page.activeText.selectedText() != "" {
			row = h.page.activeText
		}
	}
	if row == nil {
		t.Fatal("no word selected by a double click")
	}
	word := row.selectedText()
	h.press(pointer.ButtonSecondary, at)
	h.frames(30)
	shown := h.page.messageMenu.shown
	if len(shown) < 2 || shown[1] != actionCopySelected {
		t.Fatalf("actions %v", shown)
	}
	h.choose(actionCopySelected)
	if _, text, ok := h.router.WriteClipboard(); !ok || string(text) != word {
		t.Fatalf("copied %q, want %q", text, word)
	}
	h.press(pointer.ButtonSecondary, at)
	h.frames(30)
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(20)
	if h.page.messageMenu.open || !h.page.selection.selected[2] || h.page.activeText == nil {
		t.Fatal("Escape did more than close the menu")
	}
}

// TestRenderMessageMenu saves a message's menu, its emoji packs item
// included, over a chat replying to a message, for visual review when
// requested.
func TestRenderMessageMenu(t *testing.T) {
	path := os.Getenv("MENU_PNG")
	if path == "" {
		t.Skip("set MENU_PNG to a file")
	}
	h := newMenuHarness(t, nil)
	p := h.page
	replied, _ := p.messageByID(17)
	p.composer.draft(1).reply = &replied
	// At the end of the history, whose last message stays over the strip.
	p.list.Position.BeforeEnd = false
	m := &p.messageMenu
	m.open, m.id, m.at, m.top = true, 18, image.Pt(140, 300), 34
	m.packs = emojiPackLookup{id: 18, found: emojiPacks{refs: []model.StickerSetRef{{Type: "id", ID: 1}}, title: "Котики"}}
	l := localization.For("ru")
	for _, classic := range []bool{false, true} {
		p.classic = func() bool { return classic }
		file := path
		if classic {
			file = strings.TrimSuffix(path, ".png") + "-classic.png"
		}
		renderFrames(t, image.Pt(500, 700), file, func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindChannel}, l, false)
			p.layoutDialogs(gtx, l)
			p.media.EndFrame()
			p.images.EndFrame()
		})
	}
}

// With confirmations on, a sticker picked is sent only once confirmed, as
// AyuGram asks; a GIF without its confirmation is sent at once.
func TestStickerWaitsForConfirmation(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.confirmations = func() (bool, bool) { return true, false }
	sent := func() []model.OutgoingMessage {
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		return append([]model.OutgoingMessage(nil), h.store.sent...)
	}
	wait := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(sent()) < n {
			if time.Now().After(deadline) {
				t.Fatalf("sent %d, want %d", len(sent()), n)
			}
			h.frame()
			time.Sleep(5 * time.Millisecond)
		}
	}
	gtx := layout.Context{Ops: new(op.Ops)}
	c.pickerOpen = true
	c.chooseIn(gtx, model.PickerStickers, model.PickerItem{ID: "sticker/1"})
	h.frames(20)
	if len(sent()) != 0 || !c.sendConfirm.modal.Shown() || c.pickerOpen {
		t.Fatalf("sticker not held for confirmation: sent %d, asked %v", len(sent()), c.sendConfirm.modal.Shown())
	}
	c.sendConfirm.cancel.click.Click()
	h.frames(30)
	if len(sent()) != 0 || c.sendConfirm.modal.Shown() {
		t.Fatal("cancelled sticker sent")
	}
	c.chooseIn(gtx, model.PickerStickers, model.PickerItem{ID: "sticker/2"})
	h.frames(20)
	c.sendConfirm.send.click.Click()
	wait(1)
	if got := sent()[0]; got.Item == nil || got.Item.ID != "sticker/2" {
		t.Fatalf("sent %+v", got)
	}
	for deadline := time.Now().Add(5 * time.Second); c.draft(1).sending && time.Now().Before(deadline); {
		h.frame()
	}
	c.chooseIn(gtx, model.PickerGIF, model.PickerItem{ID: "gif/1"})
	wait(2)
}
