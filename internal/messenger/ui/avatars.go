// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/model"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
)

type avatarLayout func(layout.Context, int64, model.ChatKind, string, unit.Dp) layout.Dimensions
type avatarSource interface {
	Avatar(int64) (model.Message, bool)
}
type avatarImages struct {
	source avatarSource
	media  *chatmedia.Manager
}

func newAvatarImages(source model.ConversationStore, changed func()) *avatarImages {
	s, ok := source.(avatarSource)
	if !ok {
		return nil
	}
	return &avatarImages{s, chatmedia.NewAvatars(source, changed)}
}

// avatarPhoto is the picture of id's avatar as it shows now, a frame of
// its video when it plays; nil without one, or until it has loaded.
func (a *App) avatarPhoto(id int64) image.Image {
	if !a.window.Motion.AnimationsEnabled() {
		a.avatars.media.PauseAnimations()
	}
	msg, ok := a.avatars.source.Avatar(id)
	if !ok {
		return nil
	}
	status := a.avatars.media.Status(msg, false)
	im := status.Frame
	if im == nil {
		im = status.Preview
	}
	if msg.Media.Thumbnail != nil && a.window.Motion.AnimationsEnabled() {
		video := model.Message{Kind: model.MessageGIF, Media: msg.Media.Thumbnail}
		if frame, _ := a.avatars.media.Frame(video, true); frame != nil {
			im = frame
		}
	}
	return im
}

func (a *App) layoutAvatar(gtx layout.Context, id int64, kind model.ChatKind, title string, size unit.Dp) layout.Dimensions {
	dims := layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
	if a.avatars == nil || kind == model.KindSaved {
		return avatar(gtx, id, kind, title, size)
	}
	im := a.avatarPhoto(id)
	if im == nil {
		return avatar(gtx, id, kind, title, size)
	}
	bounds := image.Rectangle{Max: dims.Size}
	defer avatarShape(gtx, bounds.Max).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(dims.Size)
	widget.Image{Src: a.images.Op(im), Fit: widget.Cover}.Layout(gtx)
	return dims
}
func senderAvatar(kind model.ChatKind, m model.Message) int64 {
	if m.Kind == model.MessageService {
		return 0
	}
	switch kind {
	case model.KindGroup:
		if m.SenderID != 0 {
			return m.SenderID
		}
		return m.Key.ChatID
	case model.KindChannel:
		// A channel's posts are its own: no avatar, as Telegram shows them.
		return 0
	case model.KindSaved:
		if m.ForwardFromID <= -1000000000000 {
			return m.ForwardFromID
		}
	}
	return 0
}

// senderAvatar is senderAvatar for the open chat. A snapshot shows the
// avatar of the other side of a private chat too, as Telegram Desktop's
// does, although the history leaves it out.
func (p *chatPage) senderAvatar(m model.Message) int64 {
	if id := senderAvatar(p.kind, m); id != 0 {
		return id
	}
	if p.snapshotting && !m.Outgoing && m.Kind != model.MessageService && (p.kind == model.KindUser || p.kind == model.KindBot) {
		return m.Key.ChatID
	}
	return 0
}

// senderOpens tells whether a click on the avatar of m's sender opens
// their chat: a member's in a group or a channel's that posted there, not
// the account's own or an anonymous admin's, which is the group itself.
func (p *chatPage) senderOpens(m model.Message) bool {
	sender := m.SenderID > 0 || model.IsChannelID(m.SenderID) && m.SenderID != m.Key.ChatID
	return p.openSender != nil && !p.snapshotting && p.kind == model.KindGroup &&
		!m.Outgoing && sender && m.Kind != model.MessageService
}

// avatarName is the name whose initials stand for the sender of m.
func (p *chatPage) avatarName(m model.Message) string {
	if m.SenderName != "" {
		return m.SenderName
	}
	return p.title
}

func (p *chatPage) withSenderAvatar(gtx layout.Context, m model.Message, bubble layout.Widget) layout.Dimensions {
	id := p.senderAvatar(m)
	if id == 0 || p.avatar == nil {
		return bubble(gtx)
	}
	avatar := layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(34), gtx.Dp(34))}
	})
	gap := layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(gtx.Dp(8), 0)} })
	body := layout.Rigid(bubble)
	dims := layout.Flex{Alignment: layout.End}.Layout(gtx, avatar, gap, body)
	p.rows[m.Key.MessageID].bodySize = dims.Size
	return dims
}

// stickyAvatarY stays inside its own message while following the viewport's
// bottom edge. Once the message exits, its avatar exits with it.
func stickyAvatarY(bodyTop, bodyBottom, viewport, height, margin int) int {
	return max(bodyTop, min(bodyBottom-height, viewport-height-margin))
}

// stickyAvatars draws the senders' avatars, one for each group of a
// sender's messages in a row, which sticks to the bottom of the history
// above cover, the height a floating composer hides, while its group is in
// view.
func (p *chatPage) stickyAvatars(gtx layout.Context, cover int) {
	if p.avatar == nil || p.heights == nil {
		return
	}
	first := p.list.Position.First
	if first < 0 || first >= len(p.messages) {
		return
	}
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	visible := max(0, gtx.Constraints.Max.Y-cover)
	size := gtx.Dp(34)
	top := func(i int) int {
		return int(p.heights.Prefix(i)-p.heights.Prefix(first)) - p.list.Position.Offset
	}
	joined := func(i int, flag bubbleJoin) bool { return i < len(p.joins) && p.joins[i]&flag != 0 }
	for i := first; i < len(p.messages) && top(i) < gtx.Constraints.Max.Y; i++ {
		msg := p.messages[i]
		id := p.senderAvatar(msg)
		if id == 0 {
			continue
		}
		// The group's last message draws its avatar; the last one in
		// view does when the group goes on below.
		last := !joined(i, joinBelow)
		inView := i+1 < len(p.messages) && top(i+1) < gtx.Constraints.Max.Y
		if !last && inView {
			continue
		}
		r := p.rows[msg.Key.MessageID]
		if r == nil {
			continue
		}
		start := i
		for start > 0 && joined(start, joinAbove) {
			start--
		}
		groupTop := top(start) + r.bodyTop
		if s := p.rows[p.messages[start].Key.MessageID]; s != nil {
			groupTop = top(start) + s.bodyTop
		}
		groupBottom := top(i) + r.bodyTop + r.bodySize.Y
		if !last {
			groupBottom = gtx.Constraints.Max.Y + size
		}
		y := stickyAvatarY(groupTop, groupBottom, visible, size, gtx.Dp(4))
		if y+size > 0 && y < gtx.Constraints.Max.Y {
			offset(gtx, image.Pt(r.avatarPoint.X, y), func(gtx layout.Context) layout.Dimensions {
				dims := p.avatar(gtx, id, model.KindUser, p.avatarName(msg), 34)
				if p.senderOpens(msg) && p.selectionCount() == 0 {
					gtx.Constraints = layout.Exact(image.Pt(size, size))
					r.avatar.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return layout.Dimensions{Size: gtx.Constraints.Max}
					})
				}
				return dims
			})
		}
	}
}
