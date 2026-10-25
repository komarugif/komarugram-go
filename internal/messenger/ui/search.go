// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"
	"image"
	"strconv"
	"strings"
	"time"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	searchTabsHeight  = unit.Dp(48)
	searchChipsHeight = unit.Dp(44)
	// searchMoreAhead is how close to the end of the results the next page
	// is asked for.
	searchMoreAhead = 5
)

// searchPanel is the search of the chat list: tabs that pick between the
// messages saved on this computer and Telegram, capsules that pick what to
// look for, as the tabs of Telegram Desktop's search, and the results.
type searchPanel struct {
	searcher model.Searcher
	// told is the failure told in the list's toast last.
	told error
	// tabs pick this computer (0) or Telegram (1).
	tabs     tabRow
	global   bool
	chips    map[model.SearchSection]*folderChip
	chipList scroll.List
	section  model.SearchSection
	found    map[model.MessageKey]*surface
	// action is the button of the status line: search locally, retry, or
	// spend a free search of public posts.
	action  surface
	loader  loadingIndicator
	results model.SearchResults
	// text is what is typed; recent, the search history shown while it is
	// empty.
	text   string
	recent recentSearch
}

func newSearchPanel(searcher model.Searcher) *searchPanel {
	p := &searchPanel{
		searcher: searcher,
		global:   true,
		chips:    map[model.SearchSection]*folderChip{},
		chipList: scroll.List{List: layout.List{Axis: layout.Horizontal}},
		found:    map[model.MessageKey]*surface{},
	}
	p.recent.store, _ = searcher.(model.RecentChats)
	return p
}

// sections are the capsules offered: public posts only from Telegram.
func (p *searchPanel) sections() []model.SearchSection {
	var out []model.SearchSection
	for _, s := range model.SearchSections {
		if p.isGlobal() || s.Local() {
			out = append(out, s)
		}
	}
	return out
}

func (p *searchPanel) isGlobal() bool { return p.global }

// tab is the index of the active tab.
func (p *searchPanel) tab() int {
	if p.global {
		return 1
	}
	return 0
}

func (p *searchPanel) chip(s model.SearchSection) *folderChip {
	c := p.chips[s]
	if c == nil {
		c = new(folderChip)
		p.chips[s] = c
	}
	return c
}

// setGlobal picks Telegram or this computer.
func (p *searchPanel) setGlobal(global bool) {
	p.global = global
	if !p.section.Local() && !global {
		p.section = model.SearchChats
	}
}

// Update handles the tabs, the capsules and the status line's button, and
// starts the search of text.
func (p *searchPanel) Update(gtx layout.Context, text string) {
	if i, ok := p.tabs.Clicked(gtx, p.tab(), 2); ok {
		p.setGlobal(i == 1)
	}
	for _, s := range p.sections() {
		if p.chip(s).Clicked(gtx) {
			p.section = s
		}
	}
	if p.action.Clicked(gtx) {
		switch {
		case p.results.Posts != nil:
			p.searcher.SpendPostsSearch()
		case errors.Is(p.results.Err, model.ErrSearchOffline):
			p.tabs.Switch(p.tab(), 0)
			p.setGlobal(false)
		case p.results.Err != nil:
			// The same query is not searched twice in a row: another one
			// in between searches it again.
			p.searcher.Search(model.SearchQuery{})
		}
	}
	p.searcher.Search(model.SearchQuery{Text: text, Section: p.section, Global: p.isGlobal()})
	p.results = p.searcher.SearchResults()
	p.text = text
	p.recent.refresh(text, p.section)
	p.recent.update(gtx)
}

// headerHeight is the height of the tabs and the capsules.
func (p *searchPanel) headerHeight(gtx layout.Context) int {
	return gtx.Dp(searchTabsHeight) + gtx.Dp(searchChipsHeight)
}

// layoutHeader draws the tabs and the capsules in the width of gtx.
func (p *searchPanel) layoutHeader(gtx layout.Context, l localization.Catalog) {
	width := gtx.Constraints.Max.X
	tabs := gtx
	tabs.Constraints = layout.Exact(image.Pt(width, gtx.Dp(searchTabsHeight)))
	p.tabs.Layout(tabs, []string{l.T("search.local"), l.T("search.global")}, p.tab())
	offset(gtx, image.Pt(0, gtx.Dp(searchTabsHeight)-gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, scheme(gtx).OutlineVariant, image.Pt(width, gtx.Dp(1)))
		return layout.Dimensions{}
	})
	sections := p.sections()
	chips := gtx
	chips.Constraints = layout.Exact(image.Pt(width, gtx.Dp(searchChipsHeight)))
	offset(chips, image.Pt(gtx.Dp(8), gtx.Dp(searchTabsHeight)), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(width-gtx.Dp(8), gtx.Dp(searchChipsHeight)))
		size := gtx.Constraints.Max
		return p.chipList.Layout(gtx, len(sections), func(gtx layout.Context, i int) layout.Dimensions {
			s := sections[i]
			gap := gtx.Dp(folderChipGap)
			dims := offset(gtx, image.Pt(gap, (size.Y-gtx.Dp(folderChipHeight))/2), func(gtx layout.Context) layout.Dimensions {
				return p.chip(s).Layout(gtx, l.T(sectionLabel(s)), s == p.section, 0)
			})
			return layout.Dimensions{Size: image.Pt(dims.Size.X+gap, size.Y)}
		})
	})
}

// sectionLabel is the localization key of a capsule.
func sectionLabel(s model.SearchSection) string {
	return [...]string{
		model.SearchChats:    "search.chats",
		model.SearchChannels: "search.channels",
		model.SearchPosts:    "search.posts",
		model.SearchPhotos:   "search.photos",
		model.SearchVideos:   "search.videos",
		model.SearchLinks:    "search.links",
		model.SearchFiles:    "search.files",
		model.SearchMusic:    "search.music",
		model.SearchVoice:    "search.voice",
	}[s]
}

// searchItem is a line of the results: a chat, a message, the heading over
// the messages or the status line at the end.
type searchItem struct {
	chat    *model.Chat
	found   *model.FoundMessage
	heading string
	status  bool
	// recent marks a chat of the search history, and recentHeading the
	// heading over them.
	recent, recentHeading bool
}

// items are the lines of the current results.
func (p *searchPanel) items(l localization.Catalog) []searchItem {
	r := p.results
	var out []searchItem
	if recent := p.recent.shown; len(recent) > 0 {
		out = append(out, searchItem{recentHeading: true})
		for i := range recent {
			out = append(out, searchItem{chat: &recent[i], recent: true})
		}
		return out
	}
	for i := range r.Chats {
		out = append(out, searchItem{chat: &r.Chats[i]})
	}
	if len(r.Messages) > 0 {
		if len(r.Chats) > 0 {
			out = append(out, searchItem{heading: l.T("search.messages")})
		}
		for i := range r.Messages {
			out = append(out, searchItem{found: &r.Messages[i]})
		}
	}
	if r.Loading || p.statusText(l) != "" {
		out = append(out, searchItem{status: true})
	}
	return out
}

// statusText tells why the search found nothing or waits; while it loads,
// the status line is a ring instead.
func (p *searchPanel) statusText(l localization.Catalog) string {
	r := p.results
	switch {
	case r.Loading:
		return "" // A ring: see layoutStatus.
	case r.Posts != nil && (r.Posts.Free || r.Posts.Remains > 0):
		return l.Count("search.posts_remaining", r.Posts.Remains, nil)
	case r.Posts != nil:
		return l.T("search.posts_limit") + " · " + l.Format("search.posts_unlocks", map[string]string{"duration": untilText(r.Posts.NextFree)})
	case errors.Is(r.Err, model.ErrSearchOffline):
		return l.T("search.offline")
	case r.Err != nil:
		// Why, the list's toast tells: see failure.
		return l.T("search.failed")
	case r.Query.Global && r.Query.Text == "" && !r.Query.Section.Media() && r.Query.Section != model.SearchPosts:
		return l.T("search.hint")
	case len(r.Chats) == 0 && len(r.Messages) == 0:
		return l.T("chat.not_found")
	}
	return ""
}

// actionText is the label of the status line's button, empty for none.
func (p *searchPanel) actionText(l localization.Catalog) string {
	r := p.results
	switch {
	case r.Loading:
		return ""
	case r.Posts != nil && (r.Posts.Free || r.Posts.Remains > 0):
		return l.Format("search.posts_spend", map[string]string{"query": r.Query.Text})
	case errors.Is(r.Err, model.ErrSearchOffline):
		return l.T("search.go_local")
	case r.Err != nil:
		return l.T("history.retry")
	}
	return ""
}

// untilText is how long until t, as hours and minutes.
func untilText(t time.Time) string {
	d := max(time.Until(t), 0).Round(time.Minute)
	return strconv.Itoa(int(d.Hours())) + ":" + strconv.Itoa(int(d.Minutes())%60/10) + strconv.Itoa(int(d.Minutes())%10)
}

// moreNear asks for the next page once the list shows the end.
func (p *searchPanel) moreNear(position layout.Position, count int) {
	if p.results.More && !p.results.Loading && position.First+position.Count >= count-searchMoreAhead {
		p.searcher.SearchMore()
	}
}

func (p *searchPanel) foundRow(key model.MessageKey) *surface {
	r := p.found[key]
	if r == nil {
		r = new(surface)
		p.found[key] = r
	}
	return r
}

// layoutStatus draws the status line and its button.
func (p *searchPanel) layoutStatus(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if p.results.Loading {
		return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return p.loader.centered(gtx, l, 24)
		})
	}
	return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return centeredLabel(gtx, p.statusText(l), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 3)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				text := p.actionText(l)
				if text == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Point{}
						return textButton(gtx, &p.action, text)
					})
				})
			}),
		)
	})
}

// layoutHeading draws the heading over the messages.
func layoutHeading(gtx layout.Context, text string) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Inset{Top: 12, Bottom: 4, Left: chatAvatarInset, Right: chatAvatarInset}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return label(gtx, text, token.TypestyleLabelLarge, sc.Primary.Color, 1)
	})
}

// foundText is the line under a found message's chat: its sender, what
// media it has, and its text on one line.
func foundText(m model.Message, l localization.Catalog) string {
	text := strings.Join(strings.Fields(m.Text), " ")
	var media string
	switch m.Kind {
	case model.MessagePhoto:
		media = l.T("history.photo")
	case model.MessageVideo:
		media = l.T("history.video")
	case model.MessageGIF:
		media = l.T("history.gif")
	case model.MessageRound:
		media = l.T("preview.round")
	case model.MessageSticker:
		media = l.T("history.sticker")
		if m.Media != nil && m.Media.Emoji != "" {
			// Before the word, as in the list (Catalog.PreviewLine).
			media = m.Media.Emoji + " " + media
		}
	case model.MessageFile, model.MessageMusic, model.MessageVoice:
		media = l.T("history.file")
		if m.Media != nil {
			switch {
			case m.Media.Title != "" && m.Media.Performer != "":
				media = m.Media.Performer + " – " + m.Media.Title
			case m.Media.Title != "":
				media = m.Media.Title
			case m.Media.FileName != "":
				media = m.Media.FileName
			}
		}
	}
	if m.StoryOf != nil {
		// A story's photo or video, as Telegram Desktop words it.
		media = l.T("history.story")
		if m.StoryOf.Expired {
			media = l.T("preview.story_expired")
		}
	}
	switch {
	case media == "":
	case text == "":
		text = media
	default:
		text = media + ", " + text
	}
	if m.SenderName != "" {
		text = m.SenderName + ": " + text
	}
	return text
}

// layoutFound draws a found message as a row of the chat list: the chat's
// avatar and title, the message's date, and its text.
func (l *chatList) layoutFound(gtx layout.Context, p *searchPanel, f model.FoundMessage, selected bool, now time.Time, catalog localization.Catalog) layout.Dimensions {
	c := f.Chat
	c.Last, c.LastTime, c.Unread = model.Preview{Text: foundText(f.Message, catalog)}, f.Message.Date, 0
	return l.layoutRowWith(gtx, p.foundRow(f.Message.Key), nil, c, selected, false, now, catalog)
}

// failure is why the search failed, to be told in the list's toast, when
// it failed since it was last asked; "" otherwise. Being offline is not a
// failure: the status line says so.
func (p *searchPanel) failure(l localization.Catalog) string {
	err := p.results.Err
	if errors.Is(err, model.ErrSearchOffline) {
		err = nil
	}
	if err == p.told {
		return ""
	}
	p.told = err
	if err == nil {
		return ""
	}
	return l.T("search.failed") + ": " + mediaErrorText(err)
}
