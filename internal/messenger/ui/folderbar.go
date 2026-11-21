// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	folderChipHeight  = unit.Dp(32)
	folderChipPadding = unit.Dp(14)
	folderChipGap     = unit.Dp(4)
)

// folderBar is the horizontal, scrollable row of folders shown above the
// chat list when the sidebar is hidden.
type folderBar struct {
	folderMenu *folderContextMenu
	list       scroll.List
	chips      map[int64]*folderChip // By folder id; 0 is all chats.
}

type folderChip struct {
	surface
}

func newFolderBar() *folderBar {
	return &folderBar{
		list:  scroll.List{List: layout.List{Axis: layout.Horizontal}},
		chips: make(map[int64]*folderChip),
	}
}

func (b *folderBar) chip(id int64) *folderChip {
	c, ok := b.chips[id]
	if !ok {
		c = new(folderChip)
		b.chips[id] = c
	}
	return c
}

// barFolder is a folder as the bar shows it; all chats are id 0.
type barFolder struct {
	id     int64
	title  string
	unread int
}

func barFolders(folders []model.Folder, chats []model.Chat, l localization.Catalog) []barFolder {
	var out []barFolder
	for _, f := range folders {
		title := f.Title
		if f.ID == 0 {
			title = l.T("nav.all")
		}
		out = append(out, barFolder{id: f.ID, title: title, unread: unreadChats(chats, f.Contains)})
	}
	return out
}

// unreadChats counts the unmuted chats with unread messages that match.
func unreadChats(chats []model.Chat, match func(model.Chat) bool) int {
	n := 0
	for _, c := range chats {
		if c.Unread > 0 && !c.Muted && match(c) {
			n++
		}
	}
	return n
}

// Update returns the section the user picked, if any.
func (b *folderBar) Update(gtx layout.Context, folders []model.Folder) (section, bool) {
	if b.chip(0).click.Clicked(gtx) {
		return section{kind: sectionAll}, true
	}
	for _, f := range folders {
		if f.ID != 0 && b.chip(f.ID).click.Clicked(gtx) {
			return section{kind: sectionFolder, folder: f.ID}, true
		}
	}
	return section{}, false
}

// Layout draws the bar in the exact size of the constraints.
func (b *folderBar) Layout(gtx layout.Context, current section, folders []model.Folder, chats []model.Chat, l localization.Catalog) layout.Dimensions {
	items := barFolders(folders, chats, l)
	size := gtx.Constraints.Max
	return b.list.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
		f := items[i]
		active := (f.id == 0 && current.kind == sectionAll) || (current.kind == sectionFolder && current.folder == f.id)
		gap := gtx.Dp(folderChipGap)
		dims := offset(gtx, image.Pt(gap, (size.Y-gtx.Dp(folderChipHeight))/2), func(gtx layout.Context) layout.Dimensions {
			dims := b.chip(f.id).Layout(gtx, f.title, active, f.unread)
			b.folderMenu.hit(gtx, f.id, dims.Size)
			return dims
		})
		return layout.Dimensions{Size: image.Pt(dims.Size.X+gap, size.Y)}
	})
}

func (c *folderChip) Layout(gtx layout.Context, title string, active bool, unread int) layout.Dimensions {
	sc := scheme(gtx)
	content := sc.SurfaceVariant.OnColor
	background := sc.SecondaryContainer.Color.SetOpacity(0)
	if active {
		content = sc.SecondaryContainer.OnColor
		background = sc.SecondaryContainer.Color
	}

	// Measure the title and the counter to size the chip.
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	titleDims := label(gtx, title, token.TypestyleLabelLarge, content, 1)
	titleCall := macro.Stop()
	pad := gtx.Dp(folderChipPadding)
	height := gtx.Dp(folderChipHeight)
	width := pad + titleDims.Size.X + pad
	var badgeCall op.CallOp
	badgeWidth := 0
	if unread > 0 {
		macro = op.Record(gtx.Ops)
		badgeWidth = drawBadge(gtx, image.Point{}, unread, sc.Primary.Color, sc.Primary.OnColor).X
		badgeCall = macro.Stop()
		width += gtx.Dp(6) + badgeWidth
	}
	size := image.Pt(width, height)
	style := surfaceStyle{radius: height / 2, background: background, content: content}
	return c.surface.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		offset(gtx, image.Pt(pad, (height-titleDims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			titleCall.Add(gtx.Ops)
			return titleDims
		})
		if unread > 0 {
			offset(gtx, image.Pt(pad+titleDims.Size.X+gtx.Dp(6), (height-gtx.Dp(18))/2), func(gtx layout.Context) layout.Dimensions {
				badgeCall.Add(gtx.Ops)
				return layout.Dimensions{}
			})
		}
		return layout.Dimensions{Size: size}
	})
}
