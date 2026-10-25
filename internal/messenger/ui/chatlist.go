// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"strings"
	"time"

	"komarugram/internal/diagnostics"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"
	"gio-mw/widget/search"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	chatRowHeight  = unit.Dp(64)
	chatAvatarSize = unit.Dp(48)
	listTopGap     = unit.Dp(6)
	searchHeader   = unit.Dp(72)
	chatRowPadding = unit.Dp(10)
	chatRowMargin  = unit.Dp(6)
	// chatAvatarInset is where avatars start in wide and narrow lists alike,
	// so that they stay in place when the list collapses.
	chatAvatarInset  = chatRowMargin + chatRowPadding
	chatSelectRadius = unit.Dp(10)
)

// chatList is the list of chats of a section.
type chatList struct {
	// toast is what to tell of what happened in the list: the list, too
	// narrow for a long error, does not draw it, but the App, in the chat
	// open or over the window (relayListToast).
	toast  toast
	avatar avatarLayout
	// badges draw the marks around a chat's name: see App.badges.
	badges badgesLayout
	// presence tells when the users of private chats were online, for the
	// dot on their avatars; nil for none.
	presence model.PresenceSource
	list     scroll.List
	rows     map[int64]*chatRow
	// shown are the chats laid out in the last frame; only they can have
	// been clicked.
	shown  []int64
	search *search.Search
	// panel searches messages too, when the store can; nil filters the
	// chats by title only.
	panel *searchPanel
	// items are the search results laid out in the last frame.
	items []searchItem
	// menu is the menu of a chat right-clicked.
	menu chatRowMenu
	// reorder drags the pinned chats into another order.
	reorder pinReorder
	// overlays says how the menus are drawn; nil draws them opaque. bd is
	// the recording of the rows behind them in the frame drawn last, nil
	// when nothing blurs it.
	overlays func() overlayPrefs
	bd       *blurBackdrop
	// previews draw the thumbnails and custom emoji of the last messages;
	// nil draws neither. animate tells whether the emoji may play.
	previews *previewMedia
	animate  func() bool
	// activity tells what others do in the chats, drawn in place of their
	// last messages; nil for nothing.
	activity model.ActivitySource
	// stories are those with stories now, drawn as the first row of all
	// chats, nil for none; openStories opens a peer's. storiesShown tells
	// whether the last frame had the row.
	stories      model.StorySource
	storiesRow   storiesRow
	openStories  func(model.StoryPeer)
	storiesShown bool
	// storiesHeight is how tall the row of stories was last laid out.
	storiesHeight int
	// invalidate draws the list again, as its row of stories scrolls.
	invalidate func()
}

// overlayPrefs is how the list's overlays are drawn.
func (l *chatList) overlayPrefs() overlayPrefs {
	if l.overlays == nil {
		return overlayPrefs{opacity: float32(composerBlurOpacity)}
	}
	return l.overlays()
}

// menuBackdrop is what the list's menus blur, nil for opaque ones.
func (l *chatList) menuBackdrop() *blurBackdrop {
	if l.overlayPrefs().menus {
		return l.bd
	}
	return nil
}

// chatPick is a chat picked in the list: a chat of the list or found by a
// search, or a message found in one.
type chatPick struct {
	ID int64
	// Chat is the chat picked from search results, which may not be in the
	// account's chat list.
	Chat *model.Chat
	// Message is the message to open the chat at, 0 for none.
	Message model.MessageID
}

type chatRow struct {
	surface
	line previewLine
}

func newChatList() *chatList {
	bar := search.Bar()
	bar.LeadingIcon.Icon = iconSearch
	return &chatList{
		list:   scroll.List{List: layout.List{Axis: layout.Vertical}},
		rows:   make(map[int64]*chatRow),
		search: bar,
	}
}

func (l *chatList) row(id int64) *chatRow {
	r, ok := l.rows[id]
	if !ok {
		r = new(chatRow)
		l.rows[id] = r
	}
	return r
}

// visible returns the chats the section lists.
func (l *chatList) visible(sec section, folders []model.Folder, chats []model.Chat, catalog localization.Catalog) []model.Chat {
	var match func(model.Chat) bool
	switch sec.kind {
	case sectionFolder:
		for _, f := range folders {
			if f.ID == sec.folder {
				match = f.Contains
			}
		}
	case sectionSearch:
		query := strings.ToLower(strings.TrimSpace(l.search.GetText()))
		if query != "" {
			match = func(c model.Chat) bool { return strings.Contains(strings.ToLower(catalog.ChatTitle(c)), query) }
		}
	}
	if match == nil {
		return chats
	}
	var out []model.Chat
	for _, c := range chats {
		if match(c) {
			out = append(out, c)
		}
	}
	return out
}

// searching reports whether the search panel shows its results.
func (l *chatList) searching(sec section, narrow bool) bool {
	return l.panel != nil && sec.kind == sectionSearch && !narrow
}

// Update returns what the user picked, if anything.
func (l *chatList) Update(gtx layout.Context, sec section, catalog localization.Catalog) (chatPick, bool) {
	l.search.SupportingText = catalog.T("chat.search")
	if l.search.TrailingIcon.Clickable.Clicked(gtx) {
		l.search.ClearText()
		l.search.Focus(gtx)
	}
	// The clear button shows only while there is something to clear.
	if l.search.GetText() != "" {
		l.search.TrailingIcon.Icon, l.search.TrailingIcon.Label = iconClear, catalog.T("chat.clear")
	} else {
		l.search.TrailingIcon.Icon, l.search.TrailingIcon.Label = nil, ""
	}
	if l.panel != nil && sec.kind == sectionSearch {
		l.panel.Update(gtx, l.search.GetText())
		// A chat picked from what was found goes first in the search
		// history, as does one picked from the history itself.
		typed := strings.TrimSpace(l.search.GetText()) != ""
		remember := func(c model.Chat) {
			if store := l.panel.recent.store; store != nil {
				store.BumpRecentChat(c)
			}
		}
		for _, it := range l.items {
			switch {
			case it.chat != nil && l.row(it.chat.ID).click.Clicked(gtx):
				c := *it.chat
				if typed || it.recent {
					remember(c)
				}
				return chatPick{ID: c.ID, Chat: &c}, true
			case it.found != nil && l.panel.foundRow(it.found.Message.Key).click.Clicked(gtx):
				c := it.found.Chat
				if typed {
					remember(c)
				}
				return chatPick{ID: c.ID, Chat: &c, Message: it.found.Message.Key.MessageID}, true
			}
		}
	}
	for _, id := range l.shown {
		if l.row(id).click.Clicked(gtx) {
			return chatPick{ID: id}, true
		}
	}
	if p, ok := l.storiesRow.clicked(gtx); ok && l.openStories != nil {
		l.openStories(p)
	}
	return chatPick{}, false
}

// storyPeers are the peers of the row of stories the section shows: all
// chats have it, as Telegram Desktop's main list does, unless the list is
// too narrow for names.
func (l *chatList) storyPeers(sec section, narrow bool) []model.StoryPeer {
	if l.stories == nil || sec.kind != sectionAll || narrow {
		return nil
	}
	return l.stories.StoryPeers()
}

// Layout draws the list in the exact size of the constraints.
func (l *chatList) Layout(gtx layout.Context, sec section, folders []model.Folder, chats []model.Chat, selected int64, narrow bool, catalog localization.Catalog) layout.Dimensions {
	end := diagnostics.From(gtx.Values).Begin("chat-list")
	defer end()
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillWindowSurface(gtx, sc.Surface.Color, size)

	// Only search has a header; otherwise the chats take the full height
	// under a small gap.
	headerHeight := gtx.Dp(listTopGap)
	if sec.kind == sectionSearch && !narrow {
		headerHeight = gtx.Dp(searchHeader)
		offset(gtx, image.Pt(gtx.Dp(12), gtx.Dp(8)), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(size.X-gtx.Dp(24), headerHeight-gtx.Dp(8)))
			gtx.Constraints.Min.Y = 0
			return l.search.Layout(gtx)
		})
	}

	if l.searching(sec, narrow) {
		panelTop := headerHeight
		headerHeight += l.panel.headerHeight(gtx)
		offset(gtx, image.Pt(0, panelTop), func(gtx layout.Context) layout.Dimensions {
			l.panel.layoutHeader(gtx, catalog)
			return layout.Dimensions{}
		})
		// The results of the other tab slide in, as a tab's content does.
		area := image.Rect(0, headerHeight, size.X, max(size.Y, headerHeight))
		l.panel.tabs.Slide(gtx, area, func(gtx layout.Context) layout.Dimensions {
			return l.layoutResults(gtx, selected, catalog)
		})
		if text := l.panel.failure(catalog); text != "" {
			l.toast.Show(text)
		}
		return layout.Dimensions{Size: size}
	}
	l.items = l.items[:0]

	chats = l.reorder.update(gtx, sec, chats, l.shown, &l.toast, catalog)
	visible := l.visible(sec, folders, chats, catalog)
	l.menu.update(gtx, sec, chats, l.shown, &l.toast, catalog)
	listGtx := gtx
	listGtx.Constraints = layout.Exact(image.Pt(size.X, max(size.Y-headerHeight, 0)))
	// The rows are recorded to be drawn again, blurred, behind whatever
	// overlay blurs them.
	overlays := l.overlayPrefs()
	recording := overlays.menus
	l.bd = nil
	rows := func(gtx layout.Context) layout.Dimensions {
		if len(visible) == 0 {
			return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return centeredLabel(gtx, catalog.T("chat.not_found"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 2)
				})
			})
		}
		now := time.Now()
		l.shown = l.shown[:0]
		// The row of stories is the list's first: it scrolls away with the
		// chats. The chats in view stay where they are as it comes or goes.
		peers := l.storyPeers(sec, narrow)
		row := len(peers) > 0
		if row != l.storiesShown {
			if l.list.Position.First > 0 || l.list.Position.Offset > 0 {
				if row {
					l.list.Position.First++
				} else {
					l.list.Position.First = max(l.list.Position.First-1, 0)
				}
			}
			l.storiesShown = row
		}
		first := 0
		if row {
			first = 1
		} else {
			l.storiesRow.shown = l.storiesRow.shown[:0]
		}
		dims := l.list.Layout(gtx, len(visible)+first, func(gtx layout.Context, i int) layout.Dimensions {
			if i < first {
				invalidate := l.invalidate
				if invalidate == nil {
					invalidate = func() {}
				}
				dims := l.storiesRow.layout(gtx, peers, l.avatar, catalog, invalidate)
				l.storiesHeight = dims.Size.Y
				return dims
			}
			c := visible[i-first]
			if c.Pinned && c.ID == l.reorder.lifted {
				// The lifted row is drawn over the others, after them.
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(chatRowHeight))}
			}
			l.shown = append(l.shown, c.ID)
			if slide := l.reorder.offset(c.ID, gtx.Now); slide != 0 {
				defer op.Offset(image.Pt(0, int(math.Round(float64(slide))))).Push(gtx.Ops).Pop()
			}
			dims := l.layoutRow(gtx, c, c.ID == selected, narrow, now, catalog)
			l.menu.rowOp(gtx, c.ID, dims.Size)
			if c.Pinned && sec.kind == sectionAll {
				l.reorder.rowOp(gtx, c.ID, dims.Size)
			}
			return dims
		})
		l.reorder.drawn = 0
		if i := chatIndex(visible, l.reorder.lifted); l.reorder.lifted != 0 && i >= 0 {
			c := visible[i]
			l.reorder.layoutLifted(gtx, l.slotTop(gtx, i+first, first), func(gtx layout.Context) layout.Dimensions {
				return l.layoutRow(gtx, c, c.ID == selected, narrow, now, catalog)
			})
		}
		l.reorder.animate(gtx)
		return dims
	}
	offset(listGtx, image.Pt(0, headerHeight), func(gtx layout.Context) layout.Dimensions {
		if !recording {
			return rows(gtx)
		}
		record := op.Record(gtx.Ops)
		dims := rows(gtx)
		call := record.Stop()
		call.Add(gtx.Ops)
		l.bd = newBackdrop(call, overlays.opacity)
		return dims
	})
	// Over the rows, which would keep the press from anything under them.
	menuGtx := listGtx
	offset(menuGtx, image.Pt(0, headerHeight), func(gtx layout.Context) layout.Dimensions {
		l.reorder.areaOp(gtx)
		l.menu.areaOp(gtx)
		l.menu.layout(gtx, l.menuBackdrop(), catalog)
		return layout.Dimensions{}
	})
	return layout.Dimensions{Size: size}
}

// chatIndex is where the chat of id is in chats, -1 for nowhere.
func chatIndex(chats []model.Chat, id int64) int {
	for i, c := range chats {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// slotTop is where the list's element k is, by the position the list was
// laid out at; the first of them are the row of stories.
func (l *chatList) slotTop(gtx layout.Context, k, first int) int {
	height := func(j int) int {
		if j < first {
			return l.storiesHeight
		}
		return gtx.Dp(chatRowHeight)
	}
	pos := l.list.Position
	y := -pos.Offset
	for j := pos.First; j < k; j++ {
		y += height(j)
	}
	for j := k; j < pos.First; j++ {
		y -= height(j)
	}
	return y
}

// withMembersLine gives c, a chat found by a search or remembered from one,
// how big it is as its line, when it has no message to show.
func withMembersLine(c model.Chat, catalog localization.Catalog) model.Chat {
	if c.Last.IsZero() && c.Members > 0 {
		key := "status.members"
		if c.Kind == model.KindChannel {
			key = "status.subscribers"
		}
		c.Last.Text = catalog.Count(key, c.Members, nil)
	}
	return c
}

// layoutResults draws the results of the search panel.
func (l *chatList) layoutResults(gtx layout.Context, selected int64, catalog localization.Catalog) layout.Dimensions {
	l.items = l.panel.items(catalog)
	l.shown = l.shown[:0]
	now := time.Now()
	recent := &l.panel.recent
	overlays := l.overlayPrefs()
	l.bd = nil
	var record op.MacroOp
	if overlays.menus {
		record = op.Record(gtx.Ops)
	}
	dims := l.list.Layout(gtx, len(l.items), func(gtx layout.Context, i int) layout.Dimensions {
		it := l.items[i]
		switch {
		case it.recentHeading:
			return recent.layoutHeading(gtx, catalog)
		case it.recent:
			dims := l.layoutRow(gtx, withMembersLine(*it.chat, catalog), it.chat.ID == selected, false, now, catalog)
			recent.rowOp(gtx, it.chat.ID, dims.Size)
			return dims
		case it.chat != nil:
			return l.layoutRow(gtx, withMembersLine(*it.chat, catalog), it.chat.ID == selected, false, now, catalog)
		case it.found != nil:
			return l.layoutFound(gtx, l.panel, *it.found, false, now, catalog)
		case it.heading != "":
			return layoutHeading(gtx, it.heading)
		}
		return l.panel.layoutStatus(gtx, catalog)
	})
	l.panel.moreNear(l.list.Position, len(l.items))
	if overlays.menus {
		call := record.Stop()
		call.Add(gtx.Ops)
		l.bd = newBackdrop(call, overlays.opacity)
	}
	// Over the rows, which would keep the press from anything under them.
	recent.areaOp(gtx)
	recent.layoutMenu(gtx, l.menuBackdrop(), catalog)
	return dims
}

func (l *chatList) layoutRow(gtx layout.Context, c model.Chat, selected, narrow bool, now time.Time, catalog localization.Catalog) layout.Dimensions {
	row := l.row(c.ID)
	return l.layoutRowWith(gtx, &row.surface, &row.line, c, selected, narrow, now, catalog)
}

// layoutRowWith draws the row of c, which takes its clicks with r, its
// last message with line; nil draws it as plain text.
func (l *chatList) layoutRowWith(gtx layout.Context, r *surface, line *previewLine, c model.Chat, selected, narrow bool, now time.Time, catalog localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	margin := gtx.Dp(chatRowMargin)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(chatRowHeight))

	titleColor := sc.Surface.OnColor
	textColor := sc.SurfaceVariant.OnColor
	accent := sc.Primary.Color
	background := sc.Primary.Color.SetOpacity(0)
	if selected {
		background = sc.Primary.Color
		titleColor, textColor, accent = sc.Primary.OnColor, sc.Primary.OnColor, sc.Primary.OnColor
	}
	style := surfaceStyle{
		area:       image.Rectangle{Min: image.Pt(margin, 0), Max: image.Pt(size.X-margin, size.Y)},
		radius:     gtx.Dp(chatSelectRadius),
		background: background,
		content:    titleColor,
	}
	return r.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		pad := gtx.Dp(chatRowPadding)
		avatarPx := gtx.Dp(chatAvatarSize)
		avatarY := (size.Y - avatarPx) / 2
		avatarX := gtx.Dp(chatAvatarInset)
		offset(gtx, image.Pt(avatarX, avatarY), func(gtx layout.Context) layout.Dimensions {
			if l.avatar != nil {
				return l.avatar(gtx, c.ID, c.Kind, catalog.ChatTitle(c), chatAvatarSize)
			}
			return avatar(gtx, c.ID, c.Kind, catalog.ChatTitle(c), chatAvatarSize)
		})
		if l.online(gtx, c) {
			ring := sc.Surface.Color
			if selected {
				ring = sc.Primary.Color
			}
			drawOnlineDot(gtx, image.Pt(avatarX, avatarY), avatarPx, ring)
		}

		badgeBackground, badgeText := sc.Primary.Color, sc.Primary.OnColor
		if c.Muted {
			badgeBackground, badgeText = sc.Outline, sc.Surface.Color
		}
		if selected {
			badgeBackground, badgeText = sc.Primary.OnColor, sc.Primary.Color
		}
		if narrow {
			if c.Unread > 0 {
				drawBadgeRight(gtx, image.Pt(avatarX+avatarPx+gtx.Dp(4), avatarY+avatarPx-gtx.Dp(16)), c.Unread, badgeBackground, badgeText)
			}
		} else {
			textX := avatarX + avatarPx + pad
			textWidth := size.X - margin - pad - textX
			drafted := sc.Error.Color
			if selected {
				drafted = sc.Primary.OnColor
			}
			l.layoutRowText(gtx, c, line, image.Rect(textX, gtx.Dp(11), textX+textWidth, size.Y-gtx.Dp(11)),
				titleColor, textColor, accent, drafted, badgeBackground, badgeText, now, catalog)
		}
		return layout.Dimensions{Size: size}
	})
}

// layoutRowText draws the title, time, last message and unread counter in
// area.
func (l *chatList) layoutRowText(gtx layout.Context, c model.Chat, line *previewLine, area image.Rectangle, titleColor, textColor, accent, drafted, badgeBackground, badgeText token.MatColor, now time.Time, catalog localization.Catalog) {
	measure := func(w layout.Widget) (layout.Dimensions, op.CallOp) {
		macro := op.Record(gtx.Ops)
		dims := w(gtx)
		return dims, macro.Stop()
	}
	width := area.Dx()
	gtx.Constraints = layout.Constraints{Max: image.Pt(width, area.Dy())}

	// Top line: title, and the time with the mark of whether the last
	// message, the account's, was read.
	timeDims, timeCall := measure(func(gtx layout.Context) layout.Dimensions {
		if c.LastTime.IsZero() {
			return layout.Dimensions{} // A chat found by a search.
		}
		state := c.LastSendState()
		if calmOf(gtx)&calmRead != 0 && state != model.SendSending {
			state = model.SendNone
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if state == model.SendNone {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return sendStateMark(gtx, state, sendStateMarkSize, accent)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, chatTime(c.LastTime, now, catalog), token.TypestyleLabelSmall, textColor, 1)
			}),
		)
	})
	titleGtx := gtx
	titleGtx.Constraints.Max.X = max(width-timeDims.Size.X-gtx.Dp(8), 0)
	offset(titleGtx, area.Min, func(gtx layout.Context) layout.Dimensions {
		var before, after layout.Widget
		if l.badges != nil {
			before, after = l.badges(c.Badges, catalog.ChatTitle(c), 16, false)
		}
		return withBadges(gtx, before, func(gtx layout.Context) layout.Dimensions {
			kind := chatKindIcon(c.Kind)
			if kind == nil {
				return label(gtx, catalog.ChatTitle(c), token.TypestyleTitleSmallEmphasized, titleColor, 1)
			}
			// As materialgram marks them: groups, channels and bots have
			// an icon of their kind before the title.
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(gtx.Dp(16), gtx.Dp(16)), func(gtx layout.Context) layout.Dimensions {
						return kind(gtx, titleColor)
					})
				}),
				layout.Rigid(layout.Spacer{Width: 4}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, catalog.ChatTitle(c), token.TypestyleTitleSmallEmphasized, titleColor, 1)
				}),
			)
		}, after)
	})
	offset(gtx, image.Pt(area.Max.X-timeDims.Size.X, area.Min.Y+gtx.Dp(2)), func(gtx layout.Context) layout.Dimensions {
		timeCall.Add(gtx.Ops)
		return timeDims
	})

	// Bottom line: sender, message and the unread counter.
	lineY := area.Min.Y + gtx.Dp(22)
	messageWidth := width
	switch {
	case c.Unread > 0:
		badgeWidth := drawBadgeRight(gtx, image.Pt(area.Max.X, lineY+gtx.Dp(1)), c.Unread, badgeBackground, badgeText)
		messageWidth -= badgeWidth + gtx.Dp(8)
	case c.Pinned:
		// A pinned chat with nothing unread shows the pin where the counter
		// would be, as Telegram Desktop's list does.
		px := gtx.Dp(16)
		offset(gtx, image.Pt(area.Max.X-px, lineY+gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
			return drawPin(gtx, px, textColor)
		})
		messageWidth -= px + gtx.Dp(8)
	}
	x := area.Min.X
	if line != nil {
		lineGtx := gtx
		lineGtx.Constraints.Max.X = max(messageWidth, 0)
		offset(lineGtx, image.Pt(x, lineY), func(gtx layout.Context) layout.Dimensions {
			return l.layoutLine(gtx, line, c, textColor, accent, drafted, catalog)
		})
		return
	}
	sender, message := catalog.Preview(c.Last, catalog.ChatTitle(c), c.Kind == model.KindChannel)
	if sender != "" {
		senderGtx := gtx
		senderGtx.Constraints.Max.X = max(messageWidth/2, 0)
		dims := offset(senderGtx, image.Pt(x, lineY), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, sender+": ", token.TypestyleBodyMedium, accent, 1)
		})
		x += dims.Size.X
		messageWidth -= dims.Size.X
	}
	messageGtx := gtx
	messageGtx.Constraints.Max.X = max(messageWidth, 0)
	offset(messageGtx, image.Pt(x, lineY), func(gtx layout.Context) layout.Dimensions {
		return label(gtx, message, token.TypestyleBodyMedium, textColor, 1)
	})
}

// layoutLine draws line, the last message of c or its draft, in the width
// of the constraints; what someone does in the chat now takes its place.
func (l *chatList) layoutLine(gtx layout.Context, line *previewLine, c model.Chat, fg, accent, drafted token.MatColor, catalog localization.Catalog) layout.Dimensions {
	animate := l.animate != nil && l.animate()
	if l.activity != nil && !c.Forum && line.act.update(gtx, l.activity.Activities(c.ID), 0, privateChat(c), catalog) {
		return line.act.layout(gtx, token.TypestyleBodyMedium, accent, animate)
	}
	line.update(c, catalog.ChatTitle(c), catalog, previewEmojiPx(gtx))
	return line.layout(gtx, l.previews, fg, accent, drafted, animate)
}

// privateChat reports whether c is a chat with one person, where what they
// do is told without their name.
func privateChat(c model.Chat) bool {
	return c.Kind == model.KindUser || c.Kind == model.KindBot || c.Kind == model.KindSaved
}

// online reports whether the row of c marks its user online, and has the
// window drawn again when that changes.
func (l *chatList) online(gtx layout.Context, c model.Chat) bool {
	if l.presence == nil || c.Kind != model.KindUser || calmOf(gtx)&calmOnline != 0 {
		return false
	}
	p, ok := l.presence.Presence(c.ID)
	if !ok || !p.Online(gtx.Now) {
		return false
	}
	gtx.Execute(op.InvalidateCmd{At: p.At})
	return true
}

// chatKindIcon is the mark of a chat's kind, nil for private chats.
func chatKindIcon(kind model.ChatKind) wdk.IconWidget {
	switch kind {
	case model.KindGroup:
		return iconKindGroup
	case model.KindChannel:
		return iconKindChannel
	case model.KindBot:
		return iconKindBot
	}
	return nil
}
