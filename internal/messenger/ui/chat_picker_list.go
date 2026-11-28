// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"

	"gio-mw/token"
	"gio-mw/widget/scroll"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// chatPickerList supplies the searchable recipients list for forwarding and folder editing.
type chatPickerList struct {
	filters   []pickerFilter
	filterID  int64
	chips     *folderBar
	presence  model.PresenceSource
	usernames model.UsernameSource
	search    widget.Editor
	list      scroll.List
	rows      map[int64]*surface
	selection map[int64]bool // Non-nil for a multiple selection.
}

func (f *chatPickerList) reset() {
	f.search.SingleLine = true
	f.search.SetText("")
	f.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	if f.rows == nil {
		f.rows = map[int64]*surface{}
	}
	f.selection = nil
	f.filterID = 0
	f.chips = newFolderBar()
	f.filters = nil
}

type pickerFilter struct {
	id    int64
	title string
	match func(model.Chat) bool
}

// The selected chip and the query are independent. Neither changes checked peers.
func (f *chatPickerList) filterChats(gtx layout.Context) func(model.Chat) bool {
	var all func(model.Chat) bool
	for _, filter := range f.filters {
		if f.chips.chip(filter.id).Clicked(gtx) && filter.id != f.filterID {
			f.filterID = filter.id
			f.list.Position = layout.Position{}
		}
	}
	for _, filter := range f.filters {
		if filter.id == 0 {
			all = filter.match
		}
		if filter.id == f.filterID {
			return filter.match
		}
	}
	if f.filterID != 0 {
		f.filterID = 0
		f.list.Position = layout.Position{}
	}
	return all
}

func (f *chatPickerList) layoutFilters(gtx layout.Context) layout.Dimensions {
	if len(f.filters) < 2 {
		return layout.Dimensions{}
	}
	return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = gtx.Dp(folderChipHeight)
		gtx.Constraints.Max.Y = gtx.Constraints.Min.Y
		return f.chips.list.Layout(gtx, len(f.filters), func(gtx layout.Context, i int) layout.Dimensions {
			filter := f.filters[i]
			return layout.Inset{Right: folderChipGap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.chips.chip(filter.id).Layout(gtx, filter.title, filter.id == f.filterID, 0)
			})
		})
	})
}

func (f *chatPickerList) clicked(gtx layout.Context, targets []model.Chat) (model.Chat, bool) {
	for _, c := range targets {
		if r := f.rows[c.ID]; r != nil && r.Clicked(gtx) {
			return c, true
		}
	}
	return model.Chat{}, false
}

func (f *chatPickerList) searchField(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(40))
	fillRounded(gtx, sc.SurfaceContainerHigh, size, gtx.Dp(20))
	return layout.Inset{Top: 10, Bottom: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return flatEditor(gtx, &f.search, l.T("chat.search"))
	})
}

func (f *chatPickerList) layoutRows(gtx layout.Context, l localization.Catalog, targets []model.Chat, avatar avatarLayout) layout.Dimensions {
	sc := scheme(gtx)
	if len(targets) == 0 {
		return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("chat.not_found"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
			})
		})
	}
	return f.list.Layout(gtx, len(targets), func(gtx layout.Context, i int) layout.Dimensions {
		return f.row(gtx, targets[i], l, avatar)
	})
}

func (f *chatPickerList) targets(gtx layout.Context, chats []model.Chat, rights model.RightsSource, l localization.Catalog) []model.Chat {
	for {
		ev, ok := f.search.Update(gtx)
		if !ok {
			break
		}
		if _, changed := ev.(widget.ChangeEvent); changed {
			f.list.Position = layout.Position{}
		}
	}
	query := strings.ToLower(strings.TrimSpace(f.search.Text()))
	handle := strings.TrimPrefix(query, "@")
	match := f.filterChats(gtx)
	var saved, out []model.Chat
	for _, c := range chats {
		if (match != nil && !match(c)) || (rights != nil && !rights.CanSend(c.ID)) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(l.ChatTitle(c)), query) {
			if f.usernames == nil || handle == "" || !strings.Contains(strings.ToLower(f.usernames.Username(c.ID)), handle) {
				continue
			}
		}
		if c.Kind == model.KindSaved {
			saved = append(saved, c)
		} else {
			out = append(out, c)
		}
	}
	return append(saved, out...)
}

// row draws a chat of the picker: its avatar and title.
func (f *chatPickerList) row(gtx layout.Context, c model.Chat, l localization.Catalog, avatarOf avatarLayout) layout.Dimensions {
	r := f.rows[c.ID]
	if r == nil {
		r = new(surface)
		f.rows[c.ID] = r
	}
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(52))
	style := surfaceStyle{radius: gtx.Dp(10), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return r.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		avatarPx := gtx.Dp(40)
		avatarAt := image.Pt(gtx.Dp(6), (size.Y-avatarPx)/2)
		offset(gtx, avatarAt, func(gtx layout.Context) layout.Dimensions {
			if avatarOf != nil {
				return avatarOf(gtx, c.ID, c.Kind, l.ChatTitle(c), 40)
			}
			return avatar(gtx, c.ID, c.Kind, l.ChatTitle(c), 40)
		})
		if chatOnline(gtx, c, f.presence) {
			drawOnlineDot(gtx, avatarAt, avatarPx, dialogSurface(gtx))
		}
		textX := gtx.Dp(6) + avatarPx + gtx.Dp(12)
		right := gtx.Dp(6)
		if f.selection != nil {
			right = gtx.Dp(40)
			mark := iconChatUnchecked
			if f.selection[c.ID] {
				mark = iconChatChecked
			}
			at := image.Pt(size.X-right, (size.Y-gtx.Dp(22))/2)
			offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(gtx.Dp(22), gtx.Dp(22)), func(gtx layout.Context) layout.Dimensions { return mark(gtx, sc.Primary.Color) })
			})
		}
		text := gtx
		text.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-right, 0), size.Y)}
		macro := op.Record(gtx.Ops)
		dims := layout.Flex{Axis: layout.Vertical}.Layout(text, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.ChatTitle(c), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if f.usernames == nil || strings.TrimSpace(f.search.Text()) == "" {
				return layout.Dimensions{}
			}
			if name := f.usernames.Username(c.ID); name != "" {
				return label(gtx, "@"+name, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
			}
			return layout.Dimensions{}
		}))
		call := macro.Stop()
		offset(gtx, image.Pt(textX, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return layout.Dimensions{Size: size}
	})
}
