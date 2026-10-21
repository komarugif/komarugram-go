// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const sidebarWidth = unit.Dp(84)

type sectionKind int

const (
	sectionAll sectionKind = iota
	sectionFolder
	sectionSearch
	sectionProfile
	sectionSaved
	sectionSettings
	sectionDownloads
)

// section is what the sidebar has selected.
type section struct {
	kind   sectionKind
	folder int64 // For sectionFolder.
}

// showsChats reports whether the chat list is shown next to the page.
func (s section) showsChats() bool {
	return s.kind != sectionProfile && s.kind != sectionSettings && s.kind != sectionDownloads
}

// sidebar is the always visible column of section buttons.
type sidebar struct {
	menus     menuPreferences
	account   func() string
	all       navButton
	folders   map[int64]*navButton
	search    navButton
	profile   navButton
	saved     navButton
	downloads navButton
	settings  navButton
	theme     navButton
	list      scroll.List
}

func newSidebar() *sidebar {
	return &sidebar{
		folders: make(map[int64]*navButton),
		list:    scroll.List{List: layout.List{Axis: layout.Vertical}},
	}
}

// sidebarEvents are what the user asked for through the sidebar.
type sidebarEvents struct {
	section     *section
	toggleTheme bool
}

func (s *sidebar) Update(gtx layout.Context, folders []model.Folder) sidebarEvents {
	var ev sidebarEvents
	pick := func(sec section) { ev.section = &sec }
	if s.visible("all") && s.all.Clicked(gtx) {
		pick(section{kind: sectionAll})
	}
	for _, f := range folders {
		if s.visible(navigationID(drawerKey{section: section{kind: sectionFolder, folder: f.ID}}, s.accountID())) && s.folderButton(f.ID).Clicked(gtx) {
			pick(section{kind: sectionFolder, folder: f.ID})
		}
	}
	if s.visible("search") && s.search.Clicked(gtx) {
		pick(section{kind: sectionSearch})
	}
	if s.visible("profile") && s.profile.Clicked(gtx) {
		pick(section{kind: sectionProfile})
	}
	if s.visible("saved") && s.saved.Clicked(gtx) {
		pick(section{kind: sectionSaved})
	}
	if s.visible("downloads") && s.downloads.Clicked(gtx) {
		pick(section{kind: sectionDownloads})
	}
	if s.visible("settings") && s.settings.Clicked(gtx) {
		pick(section{kind: sectionSettings})
	}
	if s.visible("theme") && s.theme.Clicked(gtx) {
		ev.toggleTheme = true
	}
	return ev
}

func (s *sidebar) folderButton(id int64) *navButton {
	b, ok := s.folders[id]
	if !ok {
		b = new(navButton)
		s.folders[id] = b
	}
	return b
}

// sidebarNeededHeight is the height the sidebar needs to show all its
// buttons without scrolling.
func sidebarNeededHeight(folders int) unit.Dp {
	const bottomButtons = 5
	return 2*8 + unit.Dp(1+folders+bottomButtons)*(navButtonHeight+4)
}

// Layout draws the sidebar in the full height of the constraints.
func (s *sidebar) Layout(gtx layout.Context, current section, folders []model.Folder, chats []model.Chat, dark bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Dp(sidebarWidth), gtx.Constraints.Max.Y)
	fillWindowSurface(gtx, sc.SurfaceContainer, size)

	unread := func(match func(model.Chat) bool) int { return unreadChats(chats, match) }
	button := func(b *navButton, icon wdk.IconWidget, txt string, active bool, badge int) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return b.Layout(gtx, icon, txt, active, badge)
		}
	}

	// Chat sections scroll when there are many folders; the rest stays at
	// the bottom.
	top := []layout.Widget{
		button(&s.all, iconAllChats, l.T("nav.all"), current.kind == sectionAll,
			unread(func(model.Chat) bool { return true })),
	}
	for _, f := range folders {
		top = append(top, button(s.folderButton(f.ID), folderIcon(f), f.Title,
			current.kind == sectionFolder && current.folder == f.ID, unread(f.Contains)))
	}
	themeIcon, themeText := iconDark, l.T("nav.theme_dark")
	if dark {
		themeIcon, themeText = iconLight, l.T("nav.theme_light")
	}
	bottom := []layout.Widget{
		button(&s.search, iconSearch, l.T("nav.search"), current.kind == sectionSearch, 0),
		button(&s.profile, iconProfile, l.T("nav.profile"), current.kind == sectionProfile, 0),
		button(&s.saved, iconSaved, l.T("nav.saved"), current.kind == sectionSaved, 0),
		button(&s.downloads, iconDownloads, l.T("nav.downloads"), current.kind == sectionDownloads, 0),
		button(&s.settings, iconSettings, l.T("nav.settings"), current.kind == sectionSettings, 0),
		button(&s.theme, themeIcon, themeText, false, 0),
	}

	ids := []string{"all"}
	for _, f := range folders {
		ids = append(ids, navigationID(drawerKey{section: section{kind: sectionFolder, folder: f.ID}}, s.accountID()))
	}
	ids = append(ids, "search", "profile", "saved", "downloads", "settings", "theme")
	widgets := append(top, bottom...)
	byID := map[string]layout.Widget{}
	for i, id := range ids {
		byID[id] = widgets[i]
	}
	ids = s.menus.layout(menuSidebar).Arrange(ids, false)
	split := len(ids)
	for split > 0 {
		id := ids[split-1]
		if id != "search" && id != "profile" && id != "saved" && id != "downloads" && id != "settings" && id != "theme" {
			break
		}
		split--
	}
	top = nil
	bottom = nil
	for i, id := range ids {
		if i < split {
			top = append(top, byID[id])
		} else {
			bottom = append(bottom, byID[id])
		}
	}
	pad := gtx.Dp(8)
	step := gtx.Dp(navButtonHeight) + gtx.Dp(4)
	x := (size.X - gtx.Dp(navButtonWidth)) / 2
	bottomHeight := len(bottom)*step + pad
	bottomTop := size.Y - bottomHeight
	for i, w := range bottom {
		offset(gtx, image.Pt(x, bottomTop+i*step), w)
	}
	topGtx := gtx
	topGtx.Constraints = layout.Exact(image.Pt(size.X, max(bottomTop-pad, 0)))
	offset(topGtx, image.Pt(0, pad), func(gtx layout.Context) layout.Dimensions {
		return s.list.Layout(gtx, len(top), func(gtx layout.Context, i int) layout.Dimensions {
			offset(gtx, image.Pt(x, 0), top[i])
			return layout.Dimensions{Size: image.Pt(size.X, step)}
		})
	})
	return layout.Dimensions{Size: size}
}

func (s *sidebar) accountID() string {
	if s.account == nil {
		return ""
	}
	return s.account()
}
func (s *sidebar) visible(id string) bool {
	return len(s.menus.layout(menuSidebar).Arrange([]string{id}, false)) > 0
}
