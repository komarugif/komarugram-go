// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"

	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
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
	sectionArchive
)

// section is what the sidebar has selected.
type section struct {
	kind   sectionKind
	folder int64 // Custom folder, or the folder filter within the archive.
}

func (s section) destination() section {
	if s.kind == sectionArchive {
		return folderSection(s.folder, false)
	}
	return s
}

func folderSection(id int64, archive bool) section {
	if archive {
		return section{kind: sectionArchive, folder: id}
	}
	if id == 0 {
		return section{kind: sectionAll}
	}
	return section{kind: sectionFolder, folder: id}
}

// showsChats reports whether the chat list is shown next to the page.
func (s section) showsChats() bool {
	return s.kind != sectionProfile && s.kind != sectionSettings && s.kind != sectionDownloads
}

// sidebar is the always visible column of section buttons.
type sidebar struct {
	folderMenu *folderContextMenu
	menus      menuPreferences
	buttons    map[drawerKey]*navButton
	shown      []drawerEntry
	list       scroll.List
}

func newSidebar() *sidebar {
	return &sidebar{
		buttons: make(map[drawerKey]*navButton),
		list:    scroll.List{List: layout.List{Axis: layout.Vertical}},
	}
}

// sidebarEvents are what the user asked for through the sidebar.
type sidebarEvents struct {
	section     *section
	toggleTheme bool
}

func (s *sidebar) Update(gtx layout.Context, folders []model.Folder) sidebarEvents {
	for _, e := range s.shown {
		if !s.buttons[e.key].Clicked(gtx) {
			continue
		}
		if slices.Contains(s.menus.layout(menuSidebar).Hidden, navigationID(e)) {
			continue
		}
		if e.key.theme {
			return sidebarEvents{toggleTheme: true}
		}
		sec := e.key.section
		if sec.kind == sectionFolder && !slices.ContainsFunc(folders, func(f model.Folder) bool { return f.ID == sec.folder }) {
			continue
		}
		return sidebarEvents{section: &sec}
	}
	return sidebarEvents{}
}

// sidebarNeededHeight is the height the sidebar needs to show all its
// buttons without scrolling.
func sidebarNeededHeight(rows int) unit.Dp {
	return 2*8 + unit.Dp(rows)*(navButtonHeight+4)
}

// Layout draws the sidebar in the full height of the constraints.
func (s *sidebar) Layout(gtx layout.Context, current section, entries []drawerEntry, dark bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Dp(sidebarWidth), gtx.Constraints.Max.Y)
	fillWindowSurface(gtx, sc.SurfaceContainer, size)

	s.shown = entries
	// Only the utility rows following the folder block are anchored at the bottom.
	split := 0
	for i, e := range entries {
		if navigationID(e) == preferences.FolderMenuItem {
			split = i + 1
		}
		if s.buttons[e.key] == nil {
			s.buttons[e.key] = new(navButton)
		}
	}
	top, bottom := entries[:split], entries[split:]
	draw := func(gtx layout.Context, e drawerEntry) layout.Dimensions {
		title := e.title
		if e.key.theme {
			title = l.T("nav.theme_dark")
			if dark {
				title = l.T("nav.theme_light")
			}
		}
		dims := s.buttons[e.key].Layout(gtx, e.icon, title, !e.key.theme && e.key.section == current.destination(), e.unread)
		if !e.key.theme && (e.key.section.kind == sectionAll || e.key.section.kind == sectionFolder) {
			s.folderMenu.hit(gtx, e.key.section.folder, dims.Size)
		}
		return dims
	}
	pad := gtx.Dp(8)
	step := gtx.Dp(navButtonHeight) + gtx.Dp(4)
	x := (size.X - gtx.Dp(navButtonWidth)) / 2
	bottomHeight := len(bottom)*step + pad
	bottomTop := size.Y - bottomHeight
	for i, e := range bottom {
		offset(gtx, image.Pt(x, bottomTop+i*step), func(gtx layout.Context) layout.Dimensions { return draw(gtx, e) })
	}
	topGtx := gtx
	topGtx.Constraints = layout.Exact(image.Pt(size.X, max(bottomTop-pad, 0)))
	offset(topGtx, image.Pt(0, pad), func(gtx layout.Context) layout.Dimensions {
		return s.list.Layout(gtx, len(top), func(gtx layout.Context, i int) layout.Dimensions {
			offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions { return draw(gtx, top[i]) })
			return layout.Dimensions{Size: image.Pt(size.X, step)}
		})
	})
	return layout.Dimensions{Size: size}
}
