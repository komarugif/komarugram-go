// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/indicator"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// storagePage is what the dialog of the storage channel asks.
type storagePage int

const (
	storageAsk storagePage = iota
	storageChoose
	storageSend
	storageProgress
)

// storageResult is what came of asking the store, on its goroutine.
type storageResult struct {
	found  model.StorageFound
	err    error
	failed string
}

// storageOffer offers, when Local Premium is switched on and no channel of its
// name is found, to keep the lists in a private channel: an existing one or a
// new one. A channel without GIFs can then take the whole list, with a warning
// first and over a dialog that cannot be closed: the list's order is only right
// once all have gone. It shows also when a pass goes on after a restart, or was
// started in another window.
type storageOffer struct {
	source model.StorageChannels
	modal  modal
	page   storagePage
	// busy: the store was asked, and the dialog waits.
	busy bool
	// unbound: the dialog shows, and no channel is bound yet; declined tells
	// when it closes so, or when asking failed.
	unbound  bool
	declined func()
	done chan storageResult

	candidates []model.StorageChannel
	rows       []surface
	list       layout.List
	sendAll    int
	pass       model.StoragePass

	bind, later *button.Button
	create      *button.Button
	back        *button.Button
	send, skip  *button.Button
	bar         *indicator.Indicator
}

func newStorageOffer(source model.StorageChannels) *storageOffer {
	return &storageOffer{source: source, done: make(chan storageResult, 1),
		bind: button.Filled(), later: button.Text(), create: button.Filled(), back: button.Text(),
		send: button.Filled(), skip: button.Text(), bar: indicator.Linear(),
		list: layout.List{Axis: layout.Vertical}}
}

// Activated tells that the user switched Local Premium on.
func (o *storageOffer) Activated(invalidate func()) {
	o.ask(invalidate, func(ctx context.Context) (model.StorageFound, error) { return o.source.FindStorageChannel(ctx) })
}

func (o *storageOffer) ask(invalidate func(), call func(context.Context) (model.StorageFound, error)) {
	o.busy = true
	go func() {
		var result storageResult
		defer func() {
			o.done <- result
			invalidate()
		}()
		defer crash.Recover("storage channel", func(p *crash.Panic) { result.err = p })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		result.found, result.err = call(ctx)
	}()
}

// Update answers the dialog, and follows the pass; toast tells what came of it.
func (o *storageOffer) Update(gtx layout.Context, toast func(string), l localization.Catalog, invalidate func()) {
	select {
	case r := <-o.done:
		o.busy = false
		o.result(r, toast, l)
	default:
	}
	pass := o.source.StoragePassState()
	o.pass = pass
	switch {
	case pass.Active && (!o.modal.Shown() || o.page != storageProgress):
		o.page = storageProgress
		o.busy = false
		if !o.modal.Shown() {
			o.modal.Open()
		}
	case !pass.Active && o.modal.Shown() && !o.modal.closing && o.page == storageProgress && !o.busy:
		o.modal.Close()
		if pass.Failed {
			toast(l.T("storage.pass_failed"))
		} else {
			toast(l.T("storage.pass_done"))
		}
	case pass.Failed:
		toast(l.T("storage.pass_failed"))
	}
	if !o.modal.Shown() || o.busy {
		return
	}
	switch o.page {
	case storageAsk:
		if o.bind.Clicked(gtx) {
			o.page = storageChoose
		}
		if o.later.Clicked(gtx) {
			o.modal.Close()
		}
	case storageChoose:
		if o.back.Clicked(gtx) {
			o.page = storageAsk
		}
		if o.create.Clicked(gtx) {
			o.bindChannel(0, invalidate)
		}
		for i := range o.rows {
			if o.rows[i].Clicked(gtx) {
				o.bindChannel(o.candidates[i].ID, invalidate)
			}
		}
	case storageSend:
		if o.skip.Clicked(gtx) {
			o.modal.Close()
		}
		if o.send.Clicked(gtx) {
			o.ask(invalidate, func(ctx context.Context) (model.StorageFound, error) {
				return model.StorageFound{Bound: true}, o.source.SendAllGIFs(ctx)
			})
		}
	}
}

func (o *storageOffer) bindChannel(id int64, invalidate func()) {
	o.ask(invalidate, func(ctx context.Context) (model.StorageFound, error) { return o.source.BindStorageChannel(ctx, id) })
}

// result goes on from what the store told.
func (o *storageOffer) result(r storageResult, toast func(string), l localization.Catalog) {
	switch {
	case r.err != nil:
		text := l.T("storage.failed") + ": " + mediaErrorText(r.err)
		if o.modal.Shown() {
			o.modal.Toast(text)
		} else {
			toast(text)
			o.decline()
		}
	case !r.found.Bound:
		o.unbound = true
		o.candidates = r.found.Candidates
		o.rows = make([]surface, len(o.candidates))
		o.page = storageAsk
		o.modal.Open()
	case r.found.SendAll > 0:
		o.unbound = false
		o.sendAll = r.found.SendAll
		o.page = storageSend
		if !o.modal.Shown() {
			o.modal.Open()
		}
	case r.found.Title != "":
		// Bound, with nothing more to ask.
		o.unbound = false
		o.modal.Close()
		toast(l.Format("storage.bound", map[string]string{"title": r.found.Title}))
	}
}

func (o *storageOffer) decline() {
	o.unbound = false
	if o.declined != nil {
		o.declined()
	}
}

func (o *storageOffer) Layout(gtx layout.Context, l localization.Catalog) {
	if !o.modal.Shown() {
		return
	}
	locked := o.busy || o.page == storageProgress
	shown := o.modal.Layout(gtx, locked, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
		if o.busy {
			gtx = gtx.Disabled()
		}
		return o.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			switch o.page {
			case storageChoose:
				return o.layoutChoose(gtx, l)
			case storageSend:
				return o.layoutSend(gtx, l)
			case storageProgress:
				return o.layoutProgress(gtx, l)
			}
			return o.layoutAsk(gtx, l)
		}, defaultCardPadding)
	})
	if !shown && o.unbound {
		o.decline()
	}
}

// storageDialog is a dialog's title, its text, and what comes under them.
func storageDialog(gtx layout.Context, title string, body []string, warning string, rest ...layout.FlexChild) layout.Dimensions {
	sc := scheme(gtx)
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title, token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
		}),
	}
	for _, text := range body {
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
		}))
	}
	if warning != "" {
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, warning, token.TypestyleBodyMedium, sc.Error.Color, 0)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, append(rows, rest...)...)
}

// storageButtons are a dialog's buttons, at its right.
func storageButtons(buttons ...layout.Widget) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, 2*len(buttons))
		for i, b := range buttons {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout))
			}
			children = append(children, layout.Rigid(b))
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

func (o *storageOffer) layoutAsk(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	return storageDialog(gtx, l.T("storage.title"), []string{l.T("storage.body")}, "",
		vspace(16),
		storageButtons(
			func(gtx layout.Context) layout.Dimensions { return o.later.Layout(gtx, l.T("storage.later")) },
			func(gtx layout.Context) layout.Dimensions { return o.bind.Layout(gtx, l.T("storage.bind")) },
		))
}

func (o *storageOffer) layoutChoose(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	rest := []layout.FlexChild{vspace(8)}
	if len(o.candidates) > 0 {
		rest = append(rest, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(288))
			gtx.Constraints.Min.Y = 0
			return o.list.Layout(gtx, len(o.candidates), func(gtx layout.Context, i int) layout.Dimensions {
				return actionRow(gtx, &o.rows[i], iconChannels, o.candidates[i].Title)
			})
		}))
	} else {
		rest = append(rest, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("storage.no_channels"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	rest = append(rest, vspace(16), storageButtons(
		func(gtx layout.Context) layout.Dimensions { return o.back.Layout(gtx, l.T("storage.back")) },
		func(gtx layout.Context) layout.Dimensions { return o.create.Layout(gtx, l.T("storage.create")) },
	))
	body := []string{l.Format("storage.choose_body", map[string]string{"title": model.StorageChannelTitle})}
	return storageDialog(gtx, l.T("storage.choose"), body, "", rest...)
}

func (o *storageOffer) layoutSend(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	body := []string{l.Format("storage.send_body", map[string]string{"count": strconv.Itoa(o.sendAll)})}
	return storageDialog(gtx, l.T("storage.send_title"), body, l.T("storage.send_warning"),
		vspace(16),
		storageButtons(
			func(gtx layout.Context) layout.Dimensions { return o.skip.Layout(gtx, l.T("storage.later")) },
			func(gtx layout.Context) layout.Dimensions { return o.send.Layout(gtx, l.T("storage.send")) },
		))
}

func (o *storageOffer) layoutProgress(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	pass := o.pass
	share := float32(0)
	if pass.Total > 0 {
		share = float32(pass.Done) / float32(pass.Total)
	}
	status := l.Format("storage.progress", map[string]string{"done": strconv.Itoa(pass.Done), "total": strconv.Itoa(pass.Total)})
	rows := append([]layout.FlexChild{vspace(16)}, progressRows(o.bar, status, share)...)
	return storageDialog(gtx, l.T("storage.send_title"), []string{l.T("storage.progress_body")}, "", rows...)
}
