// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strings"
	"sync"
	"testing"
	"time"

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

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// entityStore answers what clicks on entities ask: searches in the chat,
// messages sent, phone numbers, cards and bots.
type entityStore struct {
	benchmarkHistory
	mu       sync.Mutex
	searched []string
	sent     []string
}

func (s *entityStore) SearchChat(_ context.Context, _ int64, text, _ string, _ int) (model.ChatSearchPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searched = append(s.searched, text)
	return model.ChatSearchPage{}, nil
}

func (s *entityStore) Picker(context.Context, model.PickerRequest) (model.PickerPage, error) {
	return model.PickerPage{}, nil
}

func (s *entityStore) Send(_ context.Context, _ int64, m model.OutgoingMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m.Text)
	return nil
}

func (s *entityStore) ResolvePhone(_ context.Context, phone string) (model.Chat, error) {
	if phone != "+1 555 0100" {
		return model.Chat{}, model.ErrLinkNotFound
	}
	return model.Chat{ID: 7, Title: "Anna"}, nil
}

func (s *entityStore) BankCard(context.Context, string) (model.BankCard, error) {
	return model.BankCard{Title: "Bank", Links: []model.BankCardLink{{Name: "Open in Bank", URL: "https://bank.example"}}}, nil
}

func (s *entityStore) BotUsername(id int64) string {
	if id == 8 {
		return "helper_bot"
	}
	return ""
}

func (s *entityStore) took() (searched, sent []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.searched...), append([]string(nil), s.sent...)
}

// entityHarness draws a message's text and the entity menu over it.
type entityHarness struct {
	t      *testing.T
	router input.Router
	store  *entityStore
	page   *chatPage
	row    *messageRow
	m      model.Message
	now    time.Time
	l      localization.Catalog
	// animate lays the row out as the history does when it animates.
	animate bool
}

func newEntityHarness(t *testing.T, m model.Message, kind model.ChatKind) *entityHarness {
	t.Helper()
	store := &entityStore{}
	h := &entityHarness{t: t, store: store, page: newChatPage(store, func() {}), m: m, now: time.Unix(1_790_000_000, 0), l: localization.For("en")}
	h.page.kind, h.page.chat = kind, 100
	h.row = newMessageRow(m, h.l, h.now)
	t.Cleanup(h.page.Close)
	h.frame()
	return h
}

func (h *entityHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(400, 400)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	h.page.entityMenu.watch(gtx)
	h.page.keyboardEvents(gtx)
	h.page.chatSearchUpdate(gtx)
	h.row.refreshDates(gtx, h.m, h.l)
	if h.row.article != nil {
		h.page.articleLayout(gtx, h.row, h.m, h.l, h.animate)
	} else {
		h.page.richText(gtx, h.row, h.l, h.animate)
	}
	h.page.entityMenuLayout(gtx, h.l)
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}

func (h *entityHarness) click(pos image.Point) {
	h.clickIn(pos, false)
}

// clickIn clicks pos, the press and the release in one frame when
// oneFrame is set, as a quick click often comes.
func (h *entityHarness) clickIn(pos image.Point, oneFrame bool) {
	// Clicks a moment apart, not one double click.
	h.now = h.now.Add(time.Second)
	p := f32.Pt(float32(pos.X), float32(pos.Y))
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Time: time.Duration(h.now.UnixNano()), Kind: kind, Source: pointer.Mouse, Position: p, Buttons: pointer.ButtonPrimary})
		if !oneFrame {
			h.frame()
		}
	}
	if oneFrame {
		// The press focuses the page, and Gio holds the release from the
		// same frame for the next one.
		h.frame()
		h.frame()
	}
}

// clickText clicks the middle of the first fragment of the run that says
// text, and returns where.
func (h *entityHarness) clickText(text string) image.Point {
	h.t.Helper()
	return h.clickTextIn(text, false)
}

func (h *entityHarness) clickTextIn(text string, oneFrame bool) image.Point {
	h.t.Helper()
	for _, f := range h.row.text.fragments {
		if strings.Contains(h.row.runs[f.Index].Text, text) {
			at := f.Bounds.Min.Add(f.Bounds.Size().Div(2))
			h.clickIn(at, oneFrame)
			return at
		}
	}
	h.t.Fatalf("no fragment says %q: %+v", text, h.row.runs)
	return image.Point{}
}

// clickItem clicks line i of the open entity menu.
func (h *entityHarness) clickItem(i int) {
	h.t.Helper()
	m := &h.page.entityMenu
	if !m.open {
		h.t.Fatal("the menu is not open")
	}
	// The menu opens animated: its lines are where they stay once it is open.
	for range 30 {
		h.frame()
	}
	y := m.rect.Min.Y + menuPadding
	items := h.page.entityItems(h.l)
	for j := range i {
		if items[j].sub != "" {
			y += menuPackHeight
		} else {
			y += menuItemHeight
		}
	}
	h.click(image.Pt(m.rect.Min.X+40, y+menuItemHeight/2))
}

// waitFor draws frames until done reports true.
func (h *entityHarness) waitFor(what string, done func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			h.t.Fatalf("no %s", what)
		}
		time.Sleep(time.Millisecond)
		h.frame()
	}
}

func message(text string, entities ...model.Entity) model.Message {
	return model.Message{Key: model.MessageKey{ChatID: 100, MessageID: 1}, SenderID: 8, Text: text, Entities: entities, ContentRevision: 1}
}

func entityOf(text, part, kind string) model.Entity {
	i := strings.Index(text, part)
	return model.Entity{Kind: kind, Offset: utf16Length(text[:i]), Length: utf16Length(part)}
}

// A hashtag searches the chat it is clicked in, from its first page, a
// private chat too; the caret goes after it, counted in characters.
func TestHashtagSearches(t *testing.T) {
	text := "Новости #голанг дня"
	m := message(text, entityOf(text, "#голанг", "hashtag"))
	for _, kind := range []model.ChatKind{model.KindGroup, model.KindUser, -1} {
		h := newEntityHarness(t, m, kind)
		if kind == -1 {
			// A topic, or a post's comments: a thread of a group.
			h.page.kind, h.page.threadRoot = model.KindGroup, 7
		}
		h.clickText("#голанг")
		s := &h.page.chatSearch
		if !s.open || s.field.Text() != "#голанг " {
			t.Fatalf("%v: chat search open %v with %q", kind, s.open, s.field.Text())
		}
		if start, end := s.field.Selection(); start != 8 || end != 8 {
			t.Fatalf("%v: caret at %d-%d, not after the tag", kind, start, end)
		}
		h.waitFor("search", func() bool { searched, _ := h.store.took(); return len(searched) == 1 })
		if searched, _ := h.store.took(); searched[0] != "#голанг" {
			t.Fatalf("%v: searched %q", kind, searched)
		}
	}
}

// A command is sent as it is pressed; in a group, with the username of the
// bot whose message it is, unless it names one.
func TestBotCommandSends(t *testing.T) {
	for _, c := range []struct {
		kind    model.ChatKind
		command string
		want    string
	}{
		{model.KindBot, "/start", "/start"},
		{model.KindGroup, "/start", "/start@helper_bot"},
		{model.KindGroup, "/start@other_bot", "/start@other_bot"},
	} {
		text := "Press " + c.command + " now"
		h := newEntityHarness(t, message(text, entityOf(text, c.command, "bot_command")), c.kind)
		h.clickText(c.command)
		h.waitFor("message sent", func() bool { _, sent := h.store.took(); return len(sent) == 1 })
		if _, sent := h.store.took(); sent[0] != c.want {
			t.Errorf("%v %s: sent %q", c.kind, c.command, sent)
		}
	}
}

// A phone number's menu copies it and finds who has it; a card's copies it
// and lists its bank's pages.
func TestPhoneAndCardMenus(t *testing.T) {
	text := "Call +1 555 0100 or pay 4242 4242 4242 4242"
	h := newEntityHarness(t, message(text, entityOf(text, "+1 555 0100", "phone"), entityOf(text, "4242 4242 4242 4242", "bank_card")), model.KindUser)
	var opened model.Chat
	h.page.openChat = func(c model.Chat, _ model.MessageID) { opened = c }
	at := h.clickText("+1 555 0100")
	m := &h.page.entityMenu
	if !m.open || m.target.kind != "phone" || m.target.value != "+1 555 0100" || m.at != at {
		t.Fatalf("phone menu %+v at %v, clicked at %v", m.target, m.at, at)
	}
	h.waitFor("the number's user", func() bool { return m.chat != nil })
	h.clickItem(0)
	if _, copied, ok := h.router.WriteClipboard(); !ok || string(copied) != "+1 555 0100" || m.open {
		t.Fatalf("copied %q, menu open %v", copied, m.open)
	}
	// Escape closes the menu, though the text clicked has the keyboard, and
	// the next click is the text's.
	h.clickText("+1 555 0100")
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	if m.open {
		t.Fatal("Escape left the menu open")
	}
	h.clickText("+1 555 0100")
	h.waitFor("the number's user", func() bool { return m.chat != nil })
	h.clickItem(1)
	if opened.ID != 7 {
		t.Fatalf("opened %+v", opened)
	}

	h.clickText("4242")
	h.waitFor("the card's bank", func() bool { return m.card != nil })
	items := h.page.entityItems(h.l)
	if len(items) != 3 || items[1].text != "Open in Bank" || items[2].text != "Bank" {
		t.Fatalf("card menu %+v", items)
	}
	h.clickItem(0)
	if _, copied, ok := h.router.WriteClipboard(); !ok || string(copied) != "4242 4242 4242 4242" || h.page.toast.Text() != h.l.T("card.copied") {
		t.Fatalf("copied %q, told %q", copied, h.page.toast.Text())
	}
}

// A date with a format is written as the reader's language writes it, a
// relative one changes as time goes, and its menu copies it in full.
func TestFormattedDateEntity(t *testing.T) {
	text := "Starts 2026-09-21 18:00, ends then"
	start := entityOf(text, "2026-09-21 18:00", "date")
	start.Date, start.DateFormat = 1_790_000_000+125, model.DateRelative
	end := entityOf(text, "then", "date")
	end.Date, end.DateFormat = 1_790_000_000, model.DateLongDate|model.DateDayOfWeek|model.DateShortTime
	quote := model.Entity{Kind: "quote", Offset: 0, Length: utf16Length(text), Collapsed: true}
	h := newEntityHarness(t, message(text, start, end, quote), model.KindUser)
	when := time.Unix(1_790_000_000, 0).Local()
	full := h.l.T("date.weekday_full."+string(rune('0'+when.Weekday()))) + " " + dayOfMonth(h.l, when, when, true) + " " + when.Format("15:04")
	if got := runsText(h.row.runs); got != "Starts in 2 minutes, ends "+full {
		t.Fatalf("text %q", got)
	}
	h.row.textBlocks[0].expanded = true
	h.now = h.now.Add(time.Minute)
	h.frame()
	if got := runsText(h.row.runs); !strings.HasPrefix(got, "Starts in 1 minute,") || !h.row.textBlocks[0].expanded {
		t.Fatalf("a minute later %q, quote expanded %v", got, h.row.textBlocks[0].expanded)
	}
	// A quick click: the menu opens where it was, though the release
	// comes in the frame of the press.
	at := h.clickTextIn(full[:3], true)
	if m := &h.page.entityMenu; !m.open || m.target.date != end.Date || m.target.text != text || m.at != at {
		t.Fatalf("date menu %+v at %v, clicked at %v", m.target, m.at, at)
	}
	h.clickItem(0)
	if _, copied, ok := h.router.WriteClipboard(); !ok || string(copied) != longDateTime(h.l, when) || h.page.toast.Text() != h.l.T("date.copied") {
		t.Fatalf("copied %q, told %q", copied, h.page.toast.Text())
	}
}

func runsText(runs []model.TextRun) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

// Dates are written as Telegram Desktop's FormatDateWithFlags writes them.
func TestFormattedDates(t *testing.T) {
	ru, en := localization.For("ru"), localization.For("en")
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		l    localization.Catalog
		date time.Time
		f    model.DateFormat
		want string
		next time.Time
	}{
		{ru, now, model.DateRelative, "сейчас", now.Add(time.Second)},
		{ru, now.Add(-21 * time.Second), model.DateRelative, "21 секунду назад", now.Add(time.Second)},
		{en, now.Add(-3 * time.Minute), model.DateRelative, "3 minutes ago", now.Add(time.Minute)},
		{ru, now.Add(2*time.Hour + 30*time.Minute), model.DateRelative, "через 2 часа", now.Add(30*time.Minute + time.Second)},
		{ru, now.Add(5*24*time.Hour + 12*time.Hour), model.DateRelative, "через 5 дней", now.Add(12*time.Hour + time.Second)},
		{en, now.Add(-400 * 24 * time.Hour), model.DateRelative, "1 year ago", now.Add(330 * 24 * time.Hour)},
		{ru, now, model.DateShortDate, "5 окт", time.Time{}},
		{ru, now, model.DateLongDate | model.DateDayOfWeek | model.DateShortTime, "понедельник 5 октября 12:00", time.Time{}},
		{en, now, model.DateDayOfWeek | model.DateShortDate, "Mon Oct 5", time.Time{}},
		{en, now, model.DateLongTime, "12:00:00", time.Time{}},
		// Another year shows when it is far from now's.
		{en, time.Date(2027, 2, 1, 9, 0, 0, 0, time.Local), model.DateLongDate, "February 1, 2027", time.Time{}},
		{ru, time.Date(2027, 1, 1, 9, 0, 0, 0, time.Local), model.DateShortDate, "1 янв", time.Time{}},
	} {
		got, next := formattedDate(c.l, c.date, now, c.f)
		if got != c.want || !next.Equal(c.next) {
			t.Errorf("%v %b: %q, next %v; want %q, %v", c.date, c.f, got, next, c.want, c.next)
		}
	}
}

// An event exported to a calendar escapes the message's text as iCalendar
// asks.
func TestCalendarEvent(t *testing.T) {
	event := calendarEvent(1_790_000_000, "Monday, 21", "a;b,c\\d\nnext", time.Unix(1_789_000_000, 0))
	for _, line := range []string{
		"DTSTART:20260921T141320Z", "DTEND:20260921T151320Z", "DTSTAMP:20260910T002640Z",
		`SUMMARY:Monday\, 21`, `DESCRIPTION:a\;b\,c\\d\nnext`,
	} {
		if !strings.Contains(event, line+"\r\n") {
			t.Errorf("no %q in\n%s", line, event)
		}
	}
	if strings.Count(event, "\n") != strings.Count(event, "\r\n") {
		t.Error("a line ends without CR")
	}
}

// gatedPhones answers each number when its gate opens.
type gatedPhones struct {
	*entityStore
	gates map[string]chan struct{}
}

func (s gatedPhones) ResolvePhone(_ context.Context, phone string) (model.Chat, error) {
	<-s.gates[phone]
	return model.Chat{ID: 1, Title: phone}, nil
}

// The answer for a menu closed while it was asked for cannot take the
// place of the open menu's.
func TestLateLookupKeepsToItsMenu(t *testing.T) {
	text := "Call +1 555 0100 or +1 555 0199"
	h := newEntityHarness(t, message(text, entityOf(text, "+1 555 0100", "phone"), entityOf(text, "+1 555 0199", "phone")), model.KindUser)
	gates := map[string]chan struct{}{"+1 555 0100": make(chan struct{}), "+1 555 0199": make(chan struct{})}
	h.page.source = gatedPhones{h.store, gates}
	h.clickText("+1 555 0100")
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame()
	h.clickText("+1 555 0199")
	// Both answer before the next frame: the closed menu's first.
	close(gates["+1 555 0100"])
	time.Sleep(50 * time.Millisecond)
	close(gates["+1 555 0199"])
	time.Sleep(50 * time.Millisecond)
	m := &h.page.entityMenu
	h.waitFor("the open menu's answer", func() bool { return !m.loading })
	if m.chat == nil || m.chat.Title != "+1 555 0199" {
		t.Fatalf("the menu of +1 555 0199 found %+v", m.chat)
	}
}
