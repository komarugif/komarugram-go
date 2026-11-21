// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"strconv"

	"gio-mw/token"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// Unloaded peers remain selectable under All until the account knows their kind.
const unknownPickerKind model.ChatKind = -1

type folderPeerPicker struct {
	chatPickerList
	modal         modal
	include       bool
	chats         []model.Chat
	order         []int64
	apply, cancel surface
}

func (v *folderSettings) openPeers(include bool, l localization.Catalog) {
	p := &v.peers
	p.reset()
	p.include = include
	p.filters = []pickerFilter{
		{title: l.T("reacted.all")},
		{id: 1, title: l.T("folders.private"), match: func(c model.Chat) bool { return c.Kind == model.KindUser || c.Kind == model.KindSaved }},
		{id: 2, title: l.T("folders.groups"), match: func(c model.Chat) bool { return c.Kind == model.KindGroup }},
		{id: 3, title: l.T("folders.channels"), match: func(c model.Chat) bool { return c.Kind == model.KindChannel }},
		{id: 4, title: l.T("folders.bots"), match: func(c model.Chat) bool { return c.Kind == model.KindBot }},
	}
	p.order = slices.Clone(v.working.Excluded)
	if include {
		p.order = slices.Concat(v.working.Pinned, v.working.Included)
	}
	p.selection = make(map[int64]bool, len(p.order))
	for _, id := range p.order {
		p.selection[id] = true
	}
	known := map[int64]bool{}
	p.chats = nil
	for _, c := range v.dialogs() {
		if c.Kind != model.KindCommunity && !known[c.ID] {
			p.chats = append(p.chats, c)
			known[c.ID] = true
		}
	}
	for _, ids := range [][]int64{v.working.Pinned, v.working.Included, v.working.Excluded} {
		for _, id := range ids {
			if !known[id] {
				p.chats = append(p.chats, model.Chat{ID: id, Kind: unknownPickerKind, Title: l.Format("folders.unknown", map[string]string{"id": strconv.FormatInt(id, 10)})})
				known[id] = true
			}
		}
	}
	p.modal.field = &p.search
	p.modal.Open()
}

func (p *folderPeerPicker) applyTo(f *model.Folder) {
	withoutSelected := func(ids []int64) []int64 {
		return slices.DeleteFunc(slices.Clone(ids), func(id int64) bool { return p.selection[id] })
	}
	if p.include {
		f.Excluded = withoutSelected(f.Excluded)
		f.Pinned = slices.DeleteFunc(slices.Clone(f.Pinned), func(id int64) bool { return !p.selection[id] })
		f.Included = slices.DeleteFunc(slices.Clone(p.order), func(id int64) bool { return slices.Contains(f.Pinned, id) })
	} else {
		f.Pinned, f.Included = withoutSelected(f.Pinned), withoutSelected(f.Included)
		f.Excluded = slices.Clone(p.order)
	}
}

func (v *folderSettings) layoutPeers(gtx layout.Context, l localization.Catalog) {
	p := &v.peers
	targets := p.targets(gtx, p.chats, nil, l)
	if !p.modal.closing {
		if c, ok := p.clicked(gtx, targets); ok {
			if p.selection[c.ID] {
				delete(p.selection, c.ID)
				p.order = slices.DeleteFunc(p.order, func(id int64) bool { return id == c.ID })
			} else {
				p.selection[c.ID] = true
				p.order = append(p.order, c.ID)
			}
		}
		if p.apply.Clicked(gtx) {
			p.applyTo(&v.working)
			p.modal.Close()
		} else if p.cancel.Clicked(gtx) {
			p.modal.Close()
		}
	}
	shown := p.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(min(gtx.Constraints.Max.X, gtx.Dp(420)), max(0, min(gtx.Constraints.Max.Y-gtx.Dp(32), gtx.Dp(560)))))
		return p.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			key := "folders.select_exclude"
			if p.include {
				key = "folders.select_include"
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T(key), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 1)
				}), vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.searchField(gtx, l) }), vspace(8),
				layout.Rigid(p.layoutFilters), layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return p.layoutRows(gtx, l, targets, v.avatar) }), vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return folderDialogActions(gtx, &p.cancel, l.T("account.cancel"), &p.apply, l.T("editor.done")+" ("+strconv.Itoa(len(p.selection))+")")
				}))
		}, defaultCardPadding)
	})
	if !shown {
		p.release()
	}
}

func (p *folderPeerPicker) release() {
	p.chats = nil
	p.rows = nil
	p.order = nil
	p.selection = nil
}
