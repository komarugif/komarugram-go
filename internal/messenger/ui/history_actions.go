// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"gio-mw/token"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/userdirs"
)

// selectionBar holds the buttons over selected messages, as Telegram
// Desktop's top bar has them: forward, delete and snapshot, each with the
// number of messages, and cancel at the end. A button shows only when its
// action is allowed for every selected message.
type selectionBar struct {
	forward, remove, snapshot, cancel surface
}

// selectedParts are the selected messages in order, with the parts of an
// album as messages of their own, as Telegram counts them.
func (p *chatPage) selectedParts() []model.Message {
	var out []model.Message
	for _, m := range p.messages {
		if p.selection.selected[m.Key.MessageID] {
			out = append(out, messageParts(m)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.MessageID < out[j].Key.MessageID })
	return out
}

// messageParts are the messages m shows: the parts of an album, or m.
func messageParts(m model.Message) []model.Message {
	if len(m.Attachments) > 0 {
		return m.Attachments
	}
	return []model.Message{m}
}

// selectionRights tells what may be done with the selected messages.
func (p *chatPage) selectionRights() model.MessageRights {
	return p.rightsFor(p.selectedParts())
}

// rightsFor tells what may be done with parts, messages of the open chat.
func (p *chatPage) rightsFor(parts []model.Message) model.MessageRights {
	var r model.MessageRights
	if source, ok := p.source.(model.RightsSource); ok {
		r = source.MessageRights(p.chat, parts)
	} else {
		// Without a store that knows the rights, nothing is protected, and
		// channels delete for everyone.
		r = model.MessageRights{Forward: true, Save: true, Delete: true, Revoke: true, Everyone: p.chat <= -1000000000000}
		for _, m := range parts {
			if m.Kind == model.MessageService || m.NoForwards {
				r.Forward = false
			}
			if m.NoForwards {
				r.Save = false
			}
		}
	}
	for _, m := range parts {
		// Telegram deleted it: only this computer has it.
		if m.Deleted {
			r.Forward = false
		}
	}
	if _, ok := p.source.(model.MessageForwarder); !ok {
		r.Forward = false
	}
	if _, ok := p.source.(model.MessageDeleter); !ok {
		r.Delete = false
	}
	if p.frozen.Frozen() {
		// A frozen account can only read.
		r.Forward, r.Delete = false, false
	}
	return r
}

// selectionHeader draws the selection's buttons over the chat's header.
func (p *chatPage) selectionHeader(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	b := &p.actions
	rights := p.selectionRights()
	if b.cancel.Clicked(gtx) {
		p.clearSelection()
		gtx.Execute(op.InvalidateCmd{})
	}
	if b.forward.Clicked(gtx) && rights.Forward {
		p.openForward(gtx)
	}
	if b.remove.Clicked(gtx) && rights.Delete {
		p.openDelete(gtx, rights)
	}
	if b.snapshot.Clicked(gtx) && rights.Save {
		p.shot.open(p, p.selectedMessages())
		gtx.Execute(op.InvalidateCmd{})
	}
	count := p.selectionCount()
	size := gtx.Constraints.Max
	gap := layout.Rigid(layout.Spacer{Width: 8}.Layout)
	var buttons []layout.FlexChild
	add := func(s *surface, key string) {
		if len(buttons) > 0 {
			buttons = append(buttons, gap)
		}
		buttons = append(buttons, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return tonalButton(gtx, s, l.T(key), count)
		}))
	}
	if rights.Forward {
		add(&b.forward, "history.select_forward")
	}
	if rights.Delete {
		add(&b.remove, "history.select_delete")
	}
	if rights.Save {
		add(&b.snapshot, "history.select_snapshot")
	}
	// Cancel sits at the end, the actions at the start, both in the middle
	// of the header's height.
	measure := func(w layout.Widget) (layout.Dimensions, op.CallOp) {
		macro := op.Record(gtx.Ops)
		dims := w(gtx)
		return dims, macro.Stop()
	}
	gtx.Constraints.Min = image.Point{}
	cancel, cancelCall := measure(func(gtx layout.Context) layout.Dimensions {
		return textButton(gtx, &b.cancel, l.T("history.cancel"))
	})
	end := size.X - gtx.Dp(8) - cancel.Size.X
	offset(gtx, image.Pt(end, (size.Y-cancel.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
		cancelCall.Add(gtx.Ops)
		return cancel
	})
	start := gtx.Dp(12)
	area := gtx
	area.Constraints = layout.Constraints{Max: image.Pt(max(end-start-gtx.Dp(8), 0), size.Y)}
	actions, actionsCall := measure(func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = area.Constraints
		if len(buttons) == 0 {
			// Nothing may be done with this selection: it only counts.
			return label(gtx, l.Count("history.selection_count", count, nil), token.TypestyleTitleSmall, scheme(gtx).Surface.OnColor, 1)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, buttons...)
	})
	func() {
		defer clip.Rect{Max: image.Pt(end-gtx.Dp(8), size.Y)}.Push(gtx.Ops).Pop()
		offset(gtx, image.Pt(start, (size.Y-actions.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			actionsCall.Add(gtx.Ops)
			return actions
		})
	}()
	return layout.Dimensions{Size: size}
}

func (p *chatPage) openForward(gtx layout.Context) {
	p.openForwardParts(gtx, p.selectedParts(), true)
}

// openForwardParts opens the picker to forward parts, messages of the open
// chat; selection tells that they are the selected ones.
func (p *chatPage) openForwardParts(gtx layout.Context, parts []model.Message, selection bool) {
	f := &p.forwarding
	if f.busy {
		return
	}
	f.ids = f.ids[:0]
	for _, m := range parts {
		f.ids = append(f.ids, m.Key.MessageID)
	}
	f.selection = selection
	if len(f.ids) == 0 {
		return
	}
	f.from = p.chat
	f.open(gtx)
}

// forwardTargets are the chats messages may be forwarded to, matching the
// search, Saved Messages first.
func (p *chatPage) forwardTargets(l localization.Catalog) []model.Chat {
	if p.chats == nil {
		return nil
	}
	rights, _ := p.source.(model.RightsSource)
	return p.forwarding.targets(p.chats(), rights, l)
}

// updateForward takes the result of a forward.
func (p *chatPage) updateForward(l localization.Catalog) {
	f := &p.forwarding
	if err, done := f.result(l); done && err == nil {
		if f.from == p.chat && f.selection {
			p.clearSelection()
		}
		p.toast.Show(l.Format("history.forwarded", map[string]string{"chat": l.ChatTitle(f.target)}))
	}
}

func (p *chatPage) forward(target model.Chat) {
	f := &p.forwarding
	source, ok := p.source.(model.MessageForwarder)
	if !ok {
		return
	}
	from, ids := f.from, append([]model.MessageID(nil), f.ids...)
	ctx := context.Background()
	if p.composer != nil {
		ctx = p.composer.ctx
	}
	f.send(ctx, target, func(ctx context.Context, to int64) error {
		return source.ForwardMessages(ctx, from, ids, to)
	}, p.invalidate)
}

// forwardDialog draws the picker when it is open.
func (p *chatPage) forwardDialog(gtx layout.Context, l localization.Catalog) {
	if !p.forwarding.modal.Shown() {
		return
	}
	if c, ok := p.forwarding.layout(gtx, l, p.forwardTargets(l), p.avatar); ok {
		p.forward(c)
	}
}

// snapshotMaxHeight bounds a snapshot to what a GPU texture holds.
const snapshotMaxHeight = 16384

// renderSnapshot draws ops of size in a headless GPU context.
func renderSnapshot(ops *op.Ops, size image.Point) (*image.RGBA, error) {
	// The context is current on this thread only.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		return nil, err
	}
	defer win.Release()
	if err := win.Frame(ops); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		return nil, err
	}
	return img, nil
}

// saveSnapshot saves a snapshot's PNG as name in the user's pictures.
func saveSnapshot(png []byte, name string) (string, error) {
	dir := picturesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, png, 0o644)
}

// picturesDir and downloadsDir are the user's pictures and downloads
// directories, which may not exist yet. Tests save elsewhere: on Windows the
// system names them, whatever the environment says.
var (
	picturesDir  = userdirs.Pictures
	downloadsDir = userdirs.Downloads
)

// canRepeat reports whether m may be sent again, as AyuGram's Repeat
// Message does: a message that may be forwarded, in a private chat or a
// group, where the account may write.
func (p *chatPage) canRepeat(m model.Message) bool {
	if _, ok := p.source.(model.MessageForwarder); !ok || p.threadRoot != 0 || p.kind == model.KindChannel || !p.canReply(m) {
		return false
	}
	return m.Kind != model.MessageService && p.rightsFor(messageParts(m)).Forward
}

// repeat forwards m to its own chat, "+1".
func (p *chatPage) repeat(m model.Message) {
	forwarder, ok := p.source.(model.MessageForwarder)
	if !ok {
		return
	}
	var ids []model.MessageID
	for _, part := range messageParts(m) {
		ids = append(ids, part.Key.MessageID)
	}
	chat := p.chat
	go func() {
		defer crash.Recover("forward to the chat", func(e *crash.Panic) { p.reportMedia(e) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := forwarder.ForwardMessages(ctx, chat, ids, chat); err != nil {
			p.reportMedia(err)
		}
	}()
}
