// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// messageRuns are the runs of m's text with its dates written as l writes
// them at now, as Telegram Desktop writes a date entity with a format, and
// when a relative one among them changes next (zero for never).
func messageRuns(m model.Message, l localization.Catalog, now time.Time) ([]model.TextRun, time.Time) {
	var next time.Time
	text, entities := model.FormatDates(m.Text, m.Entities, func(e model.Entity) string {
		if e.Date == 0 {
			return ""
		}
		s, due := formattedDate(l, time.Unix(e.Date, 0), now, e.DateFormat)
		if !due.IsZero() && (next.IsZero() || due.Before(next)) {
			next = due
		}
		return s
	})
	return model.TextRuns(text, entities), next
}

// newMessageRow is the row of m, with its text's runs.
func newMessageRow(m model.Message, l localization.Catalog, now time.Time) *messageRow {
	r := &messageRow{revision: m.ContentRevision, noCopy: m.NoForwards, sender: m.SenderID, source: m.Text, language: l.Language(), key: m.Key, streaming: m.Streaming}
	r.runs, r.datesDue = messageRuns(m, l, now)
	r.prepareArticle(m, l, now)
	return r
}

// prepareArticle prepares m's rich message, when it has one with something
// to show: its text is the row's runs then, in place of the summary's.
func (r *messageRow) prepareArticle(m model.Message, l localization.Catalog, now time.Time) {
	if m.Rich == nil {
		return
	}
	doc := prepareArticle(*m.Rich, l, now)
	if len(doc.blocks) == 0 {
		return
	}
	old := r.article
	r.article, r.runs, r.datesDue = doc, doc.runs, doc.datesDue
	if old == nil {
		return
	}
	// Written again, the article keeps what the reader did with its blocks.
	for i := range doc.leaves {
		if i < len(old.leaves) {
			doc.leaves[i].expanded, doc.leaves[i].action = old.leaves[i].expanded, old.leaves[i].action
		}
	}
}

// refreshDates writes r's dates again when a relative one has changed or
// the language has, keeping what the reader did with its blocks, and asks
// for a frame when the next one changes.
func (r *messageRow) refreshDates(gtx layout.Context, m model.Message, l localization.Catalog) {
	if r.language == l.Language() && (r.datesDue.IsZero() || gtx.Now.Before(r.datesDue)) {
		if !r.datesDue.IsZero() {
			gtx.Execute(op.InvalidateCmd{At: r.datesDue})
		}
		return
	}
	old := r.textBlocks
	r.runs, r.datesDue = messageRuns(m, l, gtx.Now)
	r.prepareArticle(m, l, gtx.Now)
	r.language = l.Language()
	r.textBlocks = nil
	r.prepareTextBlocks()
	for i := range r.textBlocks {
		if i < len(old) {
			r.textBlocks[i].expanded, r.textBlocks[i].action = old[i].expanded, old[i].action
		}
	}
	if !r.datesDue.IsZero() {
		gtx.Execute(op.InvalidateCmd{At: r.datesDue})
	}
}

// formattedDate is date written in format f, as Telegram Desktop's
// FormatDateWithFlags writes it, and when a relative date's text changes
// next.
func formattedDate(l localization.Catalog, date, now time.Time, f model.DateFormat) (string, time.Time) {
	if f&model.DateRelative != 0 {
		return relativeDate(l, date, now)
	}
	t := date.Local()
	var parts []string
	if f&model.DateDayOfWeek != 0 {
		if f&model.DateLongDate != 0 {
			parts = append(parts, l.T(fmt.Sprintf("date.weekday_full.%d", t.Weekday())))
		} else {
			parts = append(parts, l.T(fmt.Sprintf("weekday.%d", t.Weekday())))
		}
	}
	if f&model.DateLongDate != 0 {
		parts = append(parts, dayOfMonth(l, t, now, true))
	} else if f&model.DateShortDate != 0 {
		parts = append(parts, dayOfMonth(l, t, now, false))
	}
	if f&model.DateLongTime != 0 {
		parts = append(parts, t.Format("15:04:05"))
	} else if f&model.DateShortTime != 0 {
		parts = append(parts, t.Format("15:04"))
	}
	if len(parts) == 0 {
		return t.Format("02.01.2006 15:04"), time.Time{}
	}
	return strings.Join(parts, " "), time.Time{}
}

// dayOfMonth is t's day and month, its month's name in full or short, and
// its year when it is far from now's, as Telegram Desktop's langDayOfMonth
// and langDayOfMonthFull write it.
func dayOfMonth(l localization.Catalog, t, now time.Time, full bool) string {
	month := l.T(fmt.Sprintf("date.month_short.%d", t.Month()))
	if full {
		month = l.T(fmt.Sprintf("date.month_of.%d", t.Month()))
	}
	args := map[string]string{"{month}": month, "{day}": strconv.Itoa(t.Day()), "{year}": strconv.Itoa(t.Year())}
	key := "date.month_day"
	if farYear(t, now.Local()) {
		key = "date.month_day_year"
	}
	return replaceAll(l.T(key), args)
}

// farYear reports whether t is far enough from now for its year to show:
// another year, unless it is next to now's within three months.
func farYear(t, now time.Time) bool {
	y, m, ny, nm := t.Year(), int(t.Month()), now.Year(), int(now.Month())
	if y == ny {
		return false
	}
	return y > ny+1 || ny > y+1 || y == ny+1 && m+12 > nm+3 || ny == y+1 && nm+12 > m+3
}

func replaceAll(s string, args map[string]string) string {
	pairs := make([]string, 0, 2*len(args))
	for k, v := range args {
		pairs = append(pairs, k, v)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// relativeDate is date from now, as Telegram Desktop's FormatDateRelative
// writes it: "in 3 days", "5 minutes ago", and when that changes. Telegram
// Desktop changes "in 3 days" a day late, when 2 are left; this changes it
// when fewer than 3 are.
func relativeDate(l localization.Catalog, date, now time.Time) (string, time.Time) {
	d, n := date.Unix(), now.Unix()
	delta := d - n
	abs := max(delta, -delta)
	future := delta > 0
	if abs < 1 {
		return l.T("date.now"), time.Unix(n+1, 0)
	}
	units := []struct {
		size      int64
		past, in  string
		limit     int64
		perSecond bool
	}{
		{1, "date.seconds_ago", "date.in_seconds", 60, true},
		{60, "date.minutes_ago", "date.in_minutes", 3600, false},
		{3600, "date.hours_ago", "date.in_hours", 86400, false},
		{86400, "date.days_ago", "date.in_days", 30 * 86400, false},
		{30 * 86400, "date.months_ago", "date.in_months", 365 * 86400, false},
		{365 * 86400, "date.years_ago", "date.in_years", 0, false},
	}
	for _, u := range units {
		if u.limit != 0 && abs >= u.limit {
			continue
		}
		count := abs / u.size
		key := u.past
		if future {
			key = u.in
		}
		text := l.Count(key, int(count), nil)
		var next int64
		switch {
		case u.perSecond:
			next = n + 1
		case future:
			next = d - count*u.size + 1
		default:
			next = d + (count+1)*u.size
		}
		return text, time.Unix(next, 0)
	}
	return "", time.Time{}
}

// longDateTime is t in full, for copying a date: the weekday, the date with
// its year, and the time to the second.
func longDateTime(l localization.Catalog, t time.Time) string {
	t = t.Local()
	date := replaceAll(l.T("date.month_day_year"), map[string]string{
		"{month}": l.T(fmt.Sprintf("date.month_of.%d", t.Month())), "{day}": strconv.Itoa(t.Day()), "{year}": strconv.Itoa(t.Year()),
	})
	return l.T(fmt.Sprintf("date.weekday_full.%d", t.Weekday())) + ", " + date + " " + t.Format("15:04:05")
}

// activateRun does what a click on run of r does: opens its link, searches
// its hashtag, sends its command, or opens the menu of its number or date.
func (p *chatPage) activateRun(gtx layout.Context, r *messageRow, run model.TextRun) {
	switch run.Action {
	case "hashtag", "cashtag":
		p.searchTag(gtx, run.Value)
		return
	case "bot_command":
		p.sendBotCommand(r, run.Value)
		return
	case "bank_card":
		p.openEntityMenu(gtx, entityTarget{kind: "bank_card", value: run.Value})
		return
	case "date":
		if run.Date != 0 {
			p.openEntityMenu(gtx, entityTarget{kind: "date", date: run.Date, text: r.source})
		}
		return
	case "button":
		// A button in an article's text, pressed as one under a message.
		if run.Button != nil {
			a := articleDraw{p: p, r: r, m: model.Message{Key: r.key}, l: localization.For(string(r.language))}
			a.press(gtx, -1, 0, *run.Button)
		}
		return
	}
	if name, ok := anchorTarget(run.URL); ok && r.article != nil {
		p.goToAnchor(r, name, localization.For(string(r.language)))
		return
	}
	raw := strings.TrimSpace(run.URL)
	switch scheme, rest, _ := strings.Cut(raw, ":"); strings.ToLower(scheme) {
	case "tel":
		if phone := strings.TrimSpace(rest); phone != "" {
			p.openEntityMenu(gtx, entityTarget{kind: "phone", value: phone})
		}
	case "mailto":
		p.askMail(rest)
	default:
		p.askLink(raw)
	}
}

// askMail asks to open the mail program with a letter to address, as a
// link: mailto: and the address alone, which the parser takes for one.
func (p *chatPage) askMail(address string) {
	a, err := mail.ParseAddress(address)
	if err != nil || a.Name != "" || strings.ContainsAny(a.Address, "?&#/\\ ") {
		return
	}
	p.link = "mailto:" + a.Address
	p.linkModal.Open()
}

// searchTag searches a hashtag or a cashtag in the chat it was clicked in,
// a topic or a post's comments included, from the first page. Telegram
// Desktop searches all chats from a private chat, and the whole forum from
// a topic.
func (p *chatPage) searchTag(gtx layout.Context, tag string) {
	if !p.canSearchChat() {
		return
	}
	p.openChatSearch(gtx)
	s := &p.chatSearch
	s.field.SetText(tag + " ")
	end := utf8.RuneCountInString(tag) + 1
	s.field.SetCaret(end, end)
	s.typed, s.due = tag, time.Time{}
	s.start(p, tag)
}

// sendBotCommand sends command to the chat, as Telegram Desktop sends a
// command pressed in a message: in a group, with the username of the bot
// whose message it is, unless it names a bot already.
func (p *chatPage) sendBotCommand(r *messageRow, command string) {
	if p.composer == nil || command == "" {
		return
	}
	if p.kind == model.KindGroup && strings.Index(command, "@") < 2 {
		if bots, ok := p.source.(model.BotUsernames); ok && r.sender != 0 {
			if name := bots.BotUsername(r.sender); name != "" {
				command += "@" + name
			}
		}
	}
	p.composer.submit(p.chat, model.OutgoingMessage{Text: command})
}

// entityTarget is what an entity menu is for: a phone number, a card's
// number or a date, and the text of the date's message.
type entityTarget struct {
	kind, value string
	date        int64
	text        string
}

// entityMenu is the menu a phone number, a bank card's number or a date
// opens when clicked, as Telegram Desktop's: what can be copied, and what
// Telegram says of the number, asked for when the menu opens.
type entityMenu struct {
	menu   contextMenu
	open   bool
	target entityTarget
	at     image.Point
	rect   image.Rectangle
	corner menuCorner
	items  []surface
	// dismiss takes clicks around the open menu; panel, those on it;
	// presses, every press on the page, which press is the last of.
	dismiss, panel, presses byte
	press                   image.Point
	// results is where the lookup of the open menu answers, a channel of
	// each menu's own, so that a late answer for a menu closed cannot
	// take the place of the open one's; cancel stops the lookup.
	results chan entityLookup
	cancel  context.CancelFunc
	// loading is set while the lookup runs; chat is who has the phone
	// number, card what Telegram knows of the card.
	loading bool
	chat    *model.Chat
	card    *model.BankCard
}

type entityLookup struct {
	chat *model.Chat
	card *model.BankCard
}

// entityItem is a line of the menu: its text, a line under it, and what
// choosing it does; nil for a line that only tells.
type entityItem struct {
	text, sub string
	icon      wdk.IconWidget
	do        func(gtx layout.Context)
}

// openEntityMenu opens the menu of t where the pointer was last pressed.
func (p *chatPage) openEntityMenu(gtx layout.Context, t entityTarget) {
	m := &p.entityMenu
	m.close()
	m.open, m.target, m.at = true, t, m.press
	m.menu = contextMenu{}
	m.results = nil
	invalidate := p.invalidate
	var lookup func(context.Context) entityLookup
	switch t.kind {
	case "phone":
		if r, ok := p.source.(model.PhoneResolver); ok {
			lookup = func(ctx context.Context) entityLookup {
				chat, err := r.ResolvePhone(ctx, t.value)
				if err != nil {
					return entityLookup{}
				}
				return entityLookup{chat: &chat}
			}
		}
	case "bank_card":
		if s, ok := p.source.(model.BankCardSource); ok {
			lookup = func(ctx context.Context) entityLookup {
				card, err := s.BankCard(ctx, t.value)
				if err != nil {
					return entityLookup{}
				}
				return entityLookup{card: &card}
			}
		}
	}
	if lookup != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		results := make(chan entityLookup, 1)
		m.cancel, m.loading, m.results = cancel, true, results
		go func() {
			defer cancel()
			results <- lookup(ctx)
			if invalidate != nil {
				invalidate()
			}
		}()
	}
	gtx.Execute(op.InvalidateCmd{})
}

// close stops the menu's lookup and forgets what it found.
func (m *entityMenu) close() {
	m.open = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading, m.chat, m.card, m.results = false, nil, nil, nil
}

// entityItems are the lines of the open menu, as Telegram Desktop's menus
// of a phone number, a card and a date have them.
func (p *chatPage) entityItems(l localization.Catalog) []entityItem {
	m := &p.entityMenu
	t := m.target
	copyText := func(text, told string) func(layout.Context) {
		return func(gtx layout.Context) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
			if told != "" {
				p.toast.Show(told)
			}
		}
	}
	var items []entityItem
	switch t.kind {
	case "phone":
		items = append(items, entityItem{text: l.T("phone.copy"), icon: iconCopy, do: copyText(strings.TrimSpace(t.value), "")})
		if _, ok := p.source.(model.PhoneResolver); ok {
			switch {
			case m.loading:
				items = append(items, entityItem{text: l.T("phone.loading"), icon: iconProfile})
			case m.chat != nil:
				chat := *m.chat
				items = append(items, entityItem{text: chat.Title, sub: l.T("phone.profile"), icon: iconProfile, do: func(layout.Context) {
					if p.openChat != nil {
						p.openChat(chat, 0)
					}
				}})
			default:
				items = append(items, entityItem{text: l.T("phone.not_telegram"), icon: iconProfile})
			}
		}
	case "bank_card":
		copyCard := copyText(t.value, l.T("card.copied"))
		items = append(items, entityItem{text: l.T("card.copy"), icon: iconCopy, do: copyCard})
		if m.loading {
			items = append(items, entityItem{text: l.T("phone.loading"), icon: iconOpenInBrowser})
		} else if m.card != nil {
			for _, link := range m.card.Links {
				url := link.URL
				items = append(items, entityItem{text: link.Name, icon: iconOpenInBrowser, do: func(layout.Context) { p.askLink(url) }})
			}
			if m.card.Title != "" {
				items = append(items, entityItem{text: m.card.Title, do: copyCard})
			}
		}
	case "date":
		date := time.Unix(t.date, 0)
		items = append(items,
			entityItem{text: l.T("date.copy"), icon: iconCopy, do: copyText(longDateTime(l, date), l.T("date.copied"))},
			entityItem{text: l.T("date.calendar"), icon: iconCalendar, do: func(layout.Context) { p.addToCalendar(t.date, longDateTime(l, date), t.text, l) }},
		)
	}
	return items
}

// watch takes the presses on the page before the messages take their
// clicks, so that a menu a click opens opens where it was pressed, even
// when the press and the release come in one frame.
func (m *entityMenu) watch(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.presses, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			m.press = image.Pt(int(e.Position.X), int(e.Position.Y))
		}
	}
}

// entityMenuLayout draws the open entity menu over the page, and does what
// is chosen in it.
func (p *chatPage) entityMenuLayout(gtx layout.Context, l localization.Catalog) {
	m := &p.entityMenu
	watch := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.presses)
	area.Pop()
	watch.Pop()
	select {
	case r := <-m.results:
		m.loading, m.chat, m.card, m.results = false, r.chat, r.card, nil
	default:
	}
	if m.open {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press})
			if !ok {
				break
			}
			if _, ok := ev.(pointer.Event); ok {
				m.close()
			}
		}
		for m.open {
			ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				m.close()
			}
		}
	}
	items := p.entityItems(l)
	if len(m.items) < len(items) {
		m.items = append(m.items, make([]surface, len(items)-len(m.items))...)
	}
	if m.open {
		for i, it := range items {
			if m.items[i].Clicked(gtx) && it.do != nil {
				it.do(gtx)
				m.close()
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
	size := gtx.Constraints.Max
	heights := make([]int, len(items))
	h := 2 * gtx.Dp(menuPadding)
	for i, it := range items {
		heights[i] = gtx.Dp(menuItemHeight)
		if it.sub != "" {
			heights[i] = gtx.Dp(menuPackHeight)
		}
		h += heights[i]
	}
	if m.open {
		margin := gtx.Dp(8)
		w := min(gtx.Dp(menuWidth), max(0, size.X-2*margin))
		m.rect, m.corner = m.menu.Place(gtx, m.at, size, image.Pt(w, min(h, max(0, size.Y-2*margin))))
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		event.Op(gtx.Ops, &m.dismiss)
		area.Pop()
	} else {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	radius := gtx.Dp(12)
	m.menu.Layout(gtx, m.open, m.rect, m.corner, radius, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		menuSize := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menuSize}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, p.menuBackdrop(), menuSize, m.menu.bounds.Min, sc.SurfaceContainerHigh, radius)
		event.Op(gtx.Ops, &m.panel)
		y := gtx.Dp(menuPadding)
		for i, it := range items {
			inRect(gtx, image.Rect(0, y, menuSize.X, y+heights[i]), func(gtx layout.Context) layout.Dimensions {
				row := gtx.Constraints.Max
				content := sc.Surface.OnColor
				if it.do == nil {
					content = sc.SurfaceVariant.OnColor
				}
				style := surfaceStyle{background: content.SetOpacity(0), content: content, button: it.text}
				draw := func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if it.icon == nil {
									return layout.Dimensions{Size: image.Pt(gtx.Dp(24), 0)}
								}
								return it.icon(gtx, sc.SurfaceVariant.OnColor)
							}),
							layout.Rigid(layout.Spacer{Width: 12}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Point{}
									if it.sub == "" {
										return label(gtx, it.text, token.TypestyleBodyMedium, content, 1)
									}
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return label(gtx, it.text, token.TypestyleBodyMedium, content, 1)
										}),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return label(gtx, it.sub, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
										}),
									)
								})
							}),
						)
					})
				}
				if it.do == nil {
					return draw(gtx)
				}
				return m.items[i].Layout(gtx, row, style, draw)
			})
			y += heights[i]
		}
		return layout.Dimensions{Size: menuSize}
	})
}

// addToCalendar opens an event at date in the system's calendar, as
// Telegram Desktop does: an hour long, named by the date, with the
// message's text, in an iCalendar file it opens.
func (p *chatPage) addToCalendar(date int64, summary, text string, l localization.Catalog) {
	if p.files == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p.files = &attachmentFiles{ctx: ctx, cancel: cancel}
	}
	f := p.files
	content := calendarEvent(date, summary, text, time.Now())
	f.wg.Go(func() {
		dir, err := f.directory()
		if err == nil {
			path := filepath.Join(dir, fmt.Sprintf("event_%d.ics", date))
			if err = os.WriteFile(path, []byte(content), 0o600); err == nil {
				err = openBrowser(path)
			}
		}
		if err != nil {
			p.reportMedia(errors.New(l.T("date.calendar_failed")))
		}
	})
}

// calendarEvent is an iCalendar file of one event, an hour from date, as
// Telegram Desktop's ExportToCalendar writes it.
func calendarEvent(date int64, summary, text string, now time.Time) string {
	escape := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	stamp := func(t time.Time) string { return t.UTC().Format("20060102T150405Z") }
	var uid [8]byte
	_, _ = rand.Read(uid[:])
	start := time.Unix(date, 0)
	return "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//KomaruGram//EN\r\n" +
		"BEGIN:VEVENT\r\n" +
		"DTSTART:" + stamp(start) + "\r\n" +
		"DTEND:" + stamp(start.Add(time.Hour)) + "\r\n" +
		"DTSTAMP:" + stamp(now) + "\r\n" +
		fmt.Sprintf("UID:telegram-%d-%x@telegram.org\r\n", date, binary.LittleEndian.Uint64(uid[:])) +
		"SUMMARY:" + escape.Replace(summary) + "\r\n" +
		"DESCRIPTION:" + escape.Replace(text) + "\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
}
