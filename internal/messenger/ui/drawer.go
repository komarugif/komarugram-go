// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/overlay"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	drawerWidth     = unit.Dp(300)
	drawerRowHeight = unit.Dp(48)
)

// drawer holds the sidebar's items in a panel over the window, opened by the
// menu button when the sidebar is hidden.
type drawer struct {
	item    *overlay.Item
	list    scroll.List
	rows    map[drawerKey]*drawerRow
	blocker widget.Clickable
	// badges draw the marks around the name: see App.badges.
	badges badgesLayout
}

// drawerKey identifies a row: a section, or the theme switch.
type drawerKey struct {
	section section
	theme   bool
}

type drawerRow struct {
	surface
}

// drawerEntry is a row as shown.
type drawerEntry struct {
	key    drawerKey
	icon   wdk.IconWidget
	title  string
	unread int
}

func newDrawer() *drawer {
	return &drawer{
		list: scroll.List{List: layout.List{Axis: layout.Vertical}},
		rows: make(map[drawerKey]*drawerRow),
	}
}

func (d *drawer) row(k drawerKey) *drawerRow {
	r, ok := d.rows[k]
	if !ok {
		r = new(drawerRow)
		d.rows[k] = r
	}
	return r
}

func (d *drawer) isOpen() bool {
	return d.item != nil && !d.item.Closed()
}

// open shows the drawer; content draws it.
func (d *drawer) open(o *overlay.Overlay, content layout.Widget) {
	if d.isOpen() {
		return
	}
	d.list.Position = layout.Position{}
	d.item = overlay.NewItem(content, block.GravityMiddleStart).CloseOnScrim()
	o.Show(d.item)
}

func (d *drawer) close() {
	if d.item != nil {
		d.item.Close()
	}
}

func drawerEntries(folders []model.Folder, chats []model.Chat, dark bool, l localization.Catalog) []drawerEntry {
	entries := []drawerEntry{{key: drawerKey{section: section{kind: sectionAll}}, icon: iconAllChats, title: l.T("nav.all"),
		unread: unreadChats(chats, func(model.Chat) bool { return true })}}
	for _, f := range folders {
		entries = append(entries, drawerEntry{key: drawerKey{section: section{kind: sectionFolder, folder: f.ID}},
			icon: folderIcon(f), title: f.Title, unread: unreadChats(chats, f.Contains)})
	}
	entries = append(entries,
		drawerEntry{key: drawerKey{section: section{kind: sectionSearch}}, icon: iconSearch, title: l.T("nav.search")},
		drawerEntry{key: drawerKey{section: section{kind: sectionProfile}}, icon: iconProfile, title: l.T("nav.profile")},
		drawerEntry{key: drawerKey{section: section{kind: sectionSaved}}, icon: iconSaved, title: l.T("nav.saved")},
		drawerEntry{key: drawerKey{section: section{kind: sectionDownloads}}, icon: iconDownloads, title: l.T("nav.downloads")},
		drawerEntry{key: drawerKey{section: section{kind: sectionSettings}}, icon: iconSettings, title: l.T("nav.settings")},
	)
	theme := drawerEntry{key: drawerKey{theme: true}, icon: iconDark, title: l.T("nav.theme_dark_full")}
	if dark {
		theme.icon, theme.title = iconLight, l.T("nav.theme_light_full")
	}
	return append(entries, theme)
}

// Update returns what the user picked; the caller closes the drawer.
func (d *drawer) Update(gtx layout.Context, entries []drawerEntry) (drawerKey, bool) {
	for _, e := range entries {
		if d.row(e.key).click.Clicked(gtx) {
			return e.key, true
		}
	}
	return drawerKey{}, false
}

// Layout draws the panel at the left of the window.
func (d *drawer) Layout(gtx layout.Context, entries []drawerEntry, current section, me model.Profile, drawAvatar avatarLayout, private bool) layout.Dimensions {
	sc := scheme(gtx)
	width := min(gtx.Dp(drawerWidth), gtx.Constraints.Max.X-gtx.Dp(56))
	size := image.Pt(max(width, 0), gtx.Constraints.Max.Y)
	radius := gtx.Dp(16)
	panel := clip.RRect{Rect: image.Rectangle{Max: size}, NE: radius, SE: radius}
	stack := panel.Push(gtx.Ops)
	fillRect(gtx, sc.SurfaceContainerLow, size)
	// Take the clicks on the panel itself, or they reach the scrim, which
	// closes the drawer.
	d.blocker.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: size}
	})

	// Header: the account.
	pad := gtx.Dp(16)
	offset(gtx, image.Pt(pad, pad), func(gtx layout.Context) layout.Dimensions {
		return drawAvatar(gtx, me.ID, model.KindUser, me.Name(), unit.Dp(56))
	})
	textGtx := gtx
	textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-2*pad, 0), size.Y)}
	offset(textGtx, image.Pt(pad, pad+gtx.Dp(64)), func(gtx layout.Context) layout.Dimensions {
		var before, after layout.Widget
		if d.badges != nil {
			before, after = d.badges(me.Badges, me.Name(), 18, false)
		}
		return withBadges(gtx, before, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, me.Name(), token.TypestyleTitleMediumEmphasized, sc.Surface.OnColor, 1)
		}, after)
	})
	offset(textGtx, image.Pt(pad, pad+gtx.Dp(88)), func(gtx layout.Context) layout.Dimensions {
		if private {
			return layout.Dimensions{}
		}
		return label(gtx, me.Phone, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
	})
	headerHeight := pad + gtx.Dp(116)
	offset(gtx, image.Pt(0, headerHeight-gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		return layout.Dimensions{}
	})

	listGtx := gtx
	listGtx.Constraints = layout.Exact(image.Pt(size.X, max(size.Y-headerHeight, 0)))
	offset(listGtx, image.Pt(0, headerHeight), func(gtx layout.Context) layout.Dimensions {
		return d.list.Layout(gtx, len(entries), func(gtx layout.Context, i int) layout.Dimensions {
			e := entries[i]
			active := !e.key.theme && e.key.section == current
			return layout.Inset{Left: 8, Right: 8, Top: 2, Bottom: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return d.row(e.key).Layout(gtx, e, active)
			})
		})
	})
	stack.Pop()
	return layout.Dimensions{Size: size}
}

func (r *drawerRow) Layout(gtx layout.Context, e drawerEntry, active bool) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(drawerRowHeight))
	content := sc.SurfaceVariant.OnColor
	background := sc.SecondaryContainer.Color.SetOpacity(0)
	if active {
		content = sc.SecondaryContainer.OnColor
		background = sc.SecondaryContainer.Color
	}
	style := surfaceStyle{radius: size.Y / 2, background: background, content: content}
	return r.surface.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		pad := gtx.Dp(16)
		iconPx := gtx.Dp(24)
		offset(gtx, image.Pt(pad, (size.Y-iconPx)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(iconPx, iconPx), func(gtx layout.Context) layout.Dimensions {
				return e.icon(gtx, content)
			})
		})
		right := size.X - pad
		if e.unread > 0 {
			right -= drawBadgeRight(gtx, image.Pt(right, (size.Y-gtx.Dp(18))/2), e.unread, sc.Primary.Color, sc.Primary.OnColor) + gtx.Dp(8)
		}
		textX := pad + iconPx + gtx.Dp(12)
		textGtx := gtx
		textGtx.Constraints = layout.Constraints{Max: image.Pt(max(right-textX, 0), size.Y)}
		offset(textGtx, image.Pt(textX, (size.Y-gtx.Dp(20))/2), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, e.title, token.TypestyleLabelLargeEmphasized, content, 1)
		})
		return layout.Dimensions{Size: size}
	})
}
