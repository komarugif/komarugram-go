// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"slices"
	"strings"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"
	"gio-mw/widget/toggle"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// A community shown as one chat has one row in the list of all chats in
// place of its chats, as Telegram Desktop's (Data::CommunityInfo): the
// number of them with something unread, the time of the newest, and their
// names for its line. It opens, as a forum does, as a page in place of a
// history, with the switch that shows its chats as one chat or apart, and
// its chats as Telegram for Android lists them (CommunityUtils): those the
// account is in, those it can read, those it can ask to join, and the
// hidden ones.

var (
	// iconKindCommunity marks a community before its title.
	iconKindCommunity = wdk.RequireIconWidget(icons.ActionGroupWork)
	// iconUngroup is the item that shows a community's chats apart.
	iconUngroup = wdk.RequireIconWidget(icons.NavigationUnfoldMore)
)

// communityNames is how many chats the line of a community names, as
// Telegram Desktop's (kShowChatNamesCount).
const communityNames = 20

// communityRows are chats as the list of all chats shows them: the chats of
// each community shown as one chat give way to its row, which goes where
// the newest of them would, unless pinned; a community shown apart has no
// row.
func communityRows(chats []model.Chat, l localization.Catalog) []model.Chat {
	collapsed := map[int64]bool{}
	for _, c := range chats {
		if c.Kind == model.KindCommunity && c.Collapsed {
			collapsed[c.ID] = true
		}
	}
	if len(collapsed) == 0 && !slices.ContainsFunc(chats, func(c model.Chat) bool { return c.Kind == model.KindCommunity }) {
		return chats
	}
	out := make([]model.Chat, 0, len(chats))
	members := map[int64][]model.Chat{}
	var rows []model.Chat
	for _, c := range chats {
		switch {
		case c.Kind == model.KindCommunity:
			if c.Collapsed {
				rows = append(rows, c)
			}
		case collapsed[c.Community]:
			members[c.Community] = append(members[c.Community], c)
		default:
			out = append(out, c)
		}
	}
	for _, r := range rows {
		r = communityRow(r, members[r.ID], l)
		at := len(out)
		for i, c := range out {
			if r.Pinned && (!c.Pinned || c.PinRank > r.PinRank) || !r.Pinned && !c.Pinned && c.SortTime().Before(r.SortTime()) {
				at = i
				break
			}
		}
		out = slices.Insert(out, at, r)
	}
	return out
}

// communityRow is the row of community, shown as one chat of members: as
// Telegram Desktop counts them, the chats with something unread, muted
// when all of those are.
func communityRow(community model.Chat, members []model.Chat, l localization.Catalog) model.Chat {
	slices.SortStableFunc(members, func(a, b model.Chat) int { return b.SortTime().Compare(a.SortTime()) })
	community.Unread = 0
	unmuted := false
	var names []string
	for _, c := range members {
		if c.Unread > 0 {
			community.Unread++
			unmuted = unmuted || !c.Muted
		}
		if c.SortTime().After(community.LastTime) {
			community.LastTime = c.SortTime()
		}
		if len(names) < communityNames {
			names = append(names, l.ChatTitle(c))
		}
	}
	if community.Unread > 0 {
		community.Muted = !unmuted
	}
	community.Last = model.Preview{Text: strings.Join(names, ", ")}
	return community
}

// communityPage draws the community that is open.
type communityPage struct {
	list scroll.List
	rows map[int64]*chatRow
	// collapse is the switch that shows the community as one chat.
	collapse *toggle.Toggle[string]
	// community is the one the page was last drawn for.
	community int64
	// asked is what the switch asked for in the last frame, nil for
	// nothing; pending is what it asked the store for, which the switch
	// shows until the store has it, or refuses it.
	asked   *bool
	pending *bool
	// spinner is the indicator of the community on its way, retry reads it
	// again after it could not be; told is the failure the toast told.
	spinner loadingIndicator
	retry   surface
	told    error

	mu sync.Mutex
	// failed is what the switch came to, until it is told.
	failed error
}

func newCommunityPage() *communityPage {
	p := &communityPage{rows: map[int64]*chatRow{}}
	p.list.Axis = layout.Vertical
	p.collapse = toggle.NewToggle([]string{"one"}, nil, func(values []string) {
		v := len(values) == 1
		p.asked = &v
	})
	return p
}

func (p *communityPage) row(id int64) *chatRow {
	r := p.rows[id]
	if r == nil {
		r = new(chatRow)
		p.rows[id] = r
	}
	return r
}

// communityItem is an element of the page: the switch, a heading or a chat.
type communityItem struct {
	toggle  bool
	heading string
	peer    *model.CommunityPeer
}

// communityItems are the elements of the page of c.
func communityItems(c model.Community, l localization.Catalog) []communityItem {
	items := []communityItem{{toggle: true}}
	sections := c.Sections()
	add := func(heading string, peers []model.CommunityPeer) {
		if len(peers) == 0 {
			return
		}
		items = append(items, communityItem{heading: heading})
		for i := range peers {
			items = append(items, communityItem{peer: &peers[i]})
		}
	}
	add(l.T("community.joined"), sections[model.CommunityJoined])
	add(l.T("community.viewable"), sections[model.CommunityViewable])
	add(l.T("community.requestable"), sections[model.CommunityRequestable])
	add(l.T("community.hidden_chats"), sections[model.CommunityHidden])
	return items
}

// layoutCommunity draws the community c: its header over its chats.
func (a *App) layoutCommunity(gtx layout.Context, c model.Chat, l localization.Catalog) layout.Dimensions {
	p := a.community
	source, _ := a.store.(model.CommunitySource)
	if source == nil || p == nil {
		return layoutEmptyPage(gtx, l)
	}
	if p.community != c.ID {
		p.community = c.ID
		p.list.Position = layout.Position{}
		p.rows = map[int64]*chatRow{}
		p.asked, p.pending = nil, nil
		source.OpenCommunity(c.ID)
	}
	if p.tell(a, l) {
		p.pending = nil
	}
	if p.asked != nil {
		a.setCommunityCollapsed(c.ID, *p.asked)
		p.pending, p.asked = p.asked, nil
	}
	info := source.Community(c.ID)
	// The store's, not the chat's: a community shown apart has no row, and
	// the page is of the chat as it was opened.
	collapsed := info.Collapsed
	if p.pending != nil {
		if *p.pending != collapsed {
			collapsed = *p.pending
		} else {
			p.pending = nil
		}
	}
	if collapsed != (len(p.collapse.GetValues()) == 1) {
		if collapsed {
			p.collapse.SetValues([]string{"one"})
		} else {
			p.collapse.SetValues(nil)
		}
	}
	if info.Err != nil && info.Err != p.told {
		a.toast.Show(l.T("community.failed"))
	}
	p.told = info.Err
	if p.retry.Clicked(gtx) {
		source.OpenCommunity(c.ID)
	}
	head := c
	head.Members = len(info.Peers)
	if head.Title == "" {
		head.Title = info.Title
	}
	return layoutChatPage(gtx, head, l, a.layoutAvatar, a.badges, a.returnButton(), func(gtx layout.Context) layout.Dimensions {
		return p.layout(gtx, info, a.chats, l, func(peer model.CommunityPeer) { a.openCommunityPeer(c, peer, l) })
	}, nil)
}

// layout draws the chats of info as rows draws a list's, and opens one
// clicked with open.
func (p *communityPage) layout(gtx layout.Context, info model.Community, rows *chatList, l localization.Catalog, open func(model.CommunityPeer)) layout.Dimensions {
	sc := scheme(gtx)
	fillRect(gtx, sc.Surface.Color, gtx.Constraints.Max)
	switch {
	case len(info.Peers) == 0 && info.Loading:
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			px := gtx.Dp(32)
			gtx.Constraints = layout.Exact(image.Pt(px, px))
			return p.spinner.Layout(gtx, l)
		})
	case len(info.Peers) == 0 && info.Err != nil:
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return textButton(gtx, &p.retry, l.T("history.retry"))
		})
	}
	items := communityItems(info, l)
	now := time.Now()
	return p.list.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
		it := items[i]
		switch {
		case it.toggle:
			return p.layoutToggle(gtx, info.About, l)
		case it.heading != "":
			return layoutHeading(gtx, it.heading)
		}
		peer := *it.peer
		row := p.row(peer.Chat.ID)
		if row.Clicked(gtx) {
			open(peer)
		}
		shown := peer.Chat
		if !peer.Joined {
			shown = withMembersLine(shown, l)
			if peer.Hidden {
				shown.Last = model.Preview{Text: l.T("community.hidden")}
			}
		}
		return rows.layoutRowWith(gtx, &row.surface, &row.line, shown, false, false, now, l)
	})
}

// layoutToggle draws the community's description, and the switch that shows
// it as one chat with what it does.
func (p *communityPage) layoutToggle(gtx layout.Context, about string, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	inset := layout.Inset{Top: 12, Bottom: 8, Left: chatAvatarInset, Right: chatAvatarInset}
	return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		var children []layout.FlexChild
		if about != "" {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, about, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout))
		}
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.collapse.Layout(gtx, map[string]string{"one": l.T("community.show_as_one")})
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("community.show_as_one_about"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// openCommunityPeer opens a chat of community c, as Telegram for Android
// does (CommunitySheet): a hidden group or channel the account can neither
// join nor read only tells that it cannot; the others open, users and bots
// hidden or not, with the way back to the community, and the pages of
// those it is not in offer to join them.
func (a *App) openCommunityPeer(c model.Chat, peer model.CommunityPeer, l localization.Catalog) {
	if peer.Type() == model.CommunityHidden {
		key := "community.hidden_unavailable"
		if peer.Chat.Kind == model.KindChannel {
			key = "community.hidden_unavailable_channel"
		}
		a.toast.Show(l.T(key))
		return
	}
	a.openReturning(peer.Chat, c, nil)
}

// openCommunity opens the page of community: its chat in the list, or one
// made of what the store knows of it, as a community shown apart has none.
func (a *App) openCommunity(community int64) {
	if c, ok := chatByID(a.store.Chats(), community); ok && c.Kind == model.KindCommunity {
		a.open(chatPick{ID: community})
		return
	}
	chat := model.Chat{ID: community, Kind: model.KindCommunity}
	if source, ok := a.store.(model.CommunitySource); ok {
		chat.Title = source.Community(community).Title
	}
	a.open(chatPick{ID: community, Chat: &chat})
}

// setCommunityCollapsed asks the store to show community as one chat, or
// apart, in the background.
func (a *App) setCommunityCollapsed(community int64, collapsed bool) {
	source, _ := a.store.(model.CommunitySource)
	p := a.community
	if source == nil || p == nil {
		return
	}
	invalidate := a.window.Invalidate
	go func() {
		defer crash.Recover("community collapse", func(e *crash.Panic) {
			p.mu.Lock()
			p.failed = e
			p.mu.Unlock()
			invalidate()
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := source.SetCommunityCollapsed(ctx, community, collapsed)
		p.mu.Lock()
		p.failed = err
		p.mu.Unlock()
		invalidate()
	}()
}

// tell shows in the toast what the switch came to, if it failed, and
// reports whether it did.
func (p *communityPage) tell(a *App, l localization.Catalog) bool {
	p.mu.Lock()
	err := p.failed
	p.failed = nil
	p.mu.Unlock()
	if err != nil {
		a.toast.Show(l.T("community.collapse_failed"))
	}
	return err != nil
}

// communityBadge is the mark on the avatar of a chat of a community shown
// apart, where a user's online dot would be, which opens the community, as
// Telegram Desktop draws it (dialogsCommunityBadge) and Telegram for
// Android opens it from there.
type communityBadge struct {
	click     widget.Clickable
	community int64
}

// communityBadgeSize is the badge's diameter, its ring around it.
const (
	communityBadgeSize = unit.Dp(16)
	communityBadgeRing = unit.Dp(2)
)

// badge is the community badge of the row of chat.
func (l *chatList) badge(chat int64) *communityBadge {
	if l.communityBadges == nil {
		l.communityBadges = map[int64]*communityBadge{}
	}
	b := l.communityBadges[chat]
	if b == nil {
		b = new(communityBadge)
		l.communityBadges[chat] = b
	}
	return b
}

// layoutCommunityBadge draws the badge of c over its avatar at avatar, of
// px, in a ring of the row's ring color, and takes its clicks.
func (l *chatList) layoutCommunityBadge(gtx layout.Context, c model.Chat, avatar image.Point, px int, ring token.MatColor) {
	b := l.badge(c.ID)
	b.community = c.Community
	drawCommunityBadge(gtx, &b.click, avatar, px, ring)
}

// drawCommunityBadge draws the community badge over the corner of an
// avatar at avatar, of px, in a ring of ring, and takes its clicks with
// click.
func drawCommunityBadge(gtx layout.Context, click *widget.Clickable, avatar image.Point, px int, ring token.MatColor) {
	sc := scheme(gtx)
	border := gtx.Dp(communityBadgeRing)
	inner := gtx.Dp(communityBadgeSize)
	outer := inner + 2*border
	// Where Telegram Desktop puts it: 3 px out of the avatar's corner.
	corner := avatar.Add(image.Pt(px-inner+gtx.Dp(3)-border, px-inner+gtx.Dp(3)-border))
	offset(gtx, corner, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(outer, outer))
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			fillRounded(gtx, ring, image.Pt(outer, outer), outer/2)
			offset(gtx, image.Pt(border, border), func(gtx layout.Context) layout.Dimensions {
				fillRounded(gtx, sc.Surface.OnColor, image.Pt(inner, inner), inner/2)
				// A chevron down, a quarter of the badge each way.
				center := float32(inner) / 2
				arm := float32(inner) / 4
				var path clip.Path
				path.Begin(gtx.Ops)
				path.MoveTo(f32.Pt(center-arm, center-arm/2))
				path.LineTo(f32.Pt(center, center+arm/2))
				path.LineTo(f32.Pt(center+arm, center-arm/2))
				paint.FillShape(gtx.Ops, sc.Surface.Color.AsNRGBA(), clip.Stroke{Path: path.End(), Width: float32(max(gtx.Dp(1.5), 1))}.Op())
				return layout.Dimensions{}
			})
			return layout.Dimensions{Size: image.Pt(outer, outer)}
		})
	})
}
