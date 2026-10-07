// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"sort"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
)

type messageDeletion struct {
	modal             modal
	busy              bool
	chat              int64
	ids               []model.MessageID
	results           chan error
	rights            model.MessageRights
	cancel, self, all surface
	loader            loadingIndicator
}

func (p *chatPage) openDelete(gtx layout.Context, rights model.MessageRights) {
	p.openDeleteParts(gtx, p.selectedParts(), rights)
}

// openDeleteParts asks to delete parts, messages of the open chat, which
// rights allows.
func (p *chatPage) openDeleteParts(gtx layout.Context, parts []model.Message, rights model.MessageRights) {
	d := &p.deletion
	if d.busy {
		return
	}
	d.rights = rights
	seen := map[model.MessageID]bool{}
	for _, m := range parts {
		seen[m.Key.MessageID] = true
	}
	ids := make([]model.MessageID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return
	}
	d.ids = ids
	d.chat = p.chat
	d.modal.Open()
	gtx.Execute(op.InvalidateCmd{})
}
func (p *chatPage) updateDelete(chat int64, l localization.Catalog) {
	d := &p.deletion
	if d.modal.Shown() && d.chat != chat {
		d.modal.Hide()
	}
	if d.results == nil {
		return
	}
	select {
	case err := <-d.results:
		d.busy = false
		d.results = nil
		if err != nil {
			d.modal.Toast(l.T("delete.failed") + ": " + mediaErrorText(err))
		}
		if err == nil {
			d.modal.Close()
			if d.chat == chat {
				for _, id := range d.ids {
					delete(p.selection.selected, id)
				}
			}
		}
	default:
	}
}
func (p *chatPage) deleteSelected(revoke bool) {
	d := &p.deletion
	if d.busy {
		return
	}
	source, ok := p.source.(model.MessageDeleter)
	if !ok {
		return
	}
	d.busy = true
	d.results = make(chan error, 1)
	results := d.results
	chat := d.chat
	ids := append([]model.MessageID(nil), d.ids...)
	ctx := context.Background()
	if p.composer != nil {
		ctx = p.composer.ctx
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		err := source.DeleteMessages(ctx, chat, ids, revoke)
		results <- err
		p.invalidate()
	}()
}
func (p *chatPage) deleteDialog(gtx layout.Context, l localization.Catalog) {
	d := &p.deletion
	if !d.modal.Shown() {
		return
	}
	// Channels and supergroups delete for everyone only; elsewhere for
	// everyone when every message allows it, and for oneself always.
	self := !d.rights.Everyone
	all := d.rights.Everyone || d.rights.Revoke
	if !d.busy {
		if d.cancel.Clicked(gtx) {
			d.modal.Close()
		}
		if self && d.self.Clicked(gtx) {
			p.deleteSelected(false)
		}
		if all && d.all.Clicked(gtx) {
			p.deleteSelected(true)
		}
	}
	sc := scheme(gtx)
	d.modal.Layout(gtx, d.busy, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			hint := ""
			switch {
			case d.rights.Everyone:
				hint = l.T("delete.channel")
			case !all:
				hint = l.Count("delete.self_hint", len(d.ids), nil)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.Count("delete.sure", len(d.ids), nil), token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if hint == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, hint, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 3)
					})
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if d.busy {
						return p.deletion.loader.centered(gtx, l, 24)
					}
					var buttons []layout.FlexChild
					add := func(s *surface, text string) {
						buttons = append(buttons, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, s, text) }))
					}
					add(&d.cancel, l.T("history.cancel"))
					if self {
						add(&d.self, l.T("delete.self"))
					}
					if all {
						text := l.T("delete.all")
						if d.rights.Everyone {
							text = l.T("delete.confirm")
						}
						add(&d.all, text)
					}
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx, buttons...)
				}),
			)
		}, defaultCardPadding)
	})
}
