// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"

	"gio-mw/token"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// likedRed is the colour of the heart put, as Telegram Desktop's.
var likedRed = token.NewMatColorFromHexRGB(0xff4d5e)

// viewerShare is what the viewer does with what it shows beyond keeping it:
// forwards it, a message or a story, to a chat the picker picks, and puts
// the account's heart on a story, as Telegram Desktop's media viewer and
// its stories have them.
type viewerShare struct {
	forwarder model.MessageForwarder
	rights    model.RightsSource
	// chats are those to forward to, nil for no forwarding; avatar draws
	// theirs, nil for plain ones.
	chats  func() []model.Chat
	avatar avatarLayout

	forward, like widget.Clickable
	picker        forwardPicker
	// item is what the picker forwards; picking is set while it is open.
	item    model.Message
	picking bool
	// failed brings what a heart failed with.
	failed chan error
	// loader turns while a peer's stories are asked for.
	loader loadingIndicator
}

// isStory reports whether m is a story: one of those a peer has now, or
// one kept on a profile, whose ids are below zero.
func (v *mediaViewer) isStory(m model.Message) bool {
	return v.gallery == galleryStories || m.Key.MessageID < 0 && !model.IsProfilePhoto(m.Key.MessageID) && !m.Local()
}

// canForward reports whether m may be forwarded from the viewer: a message
// of Telegram's, or a story, that does not protect its content.
func (v *mediaViewer) canForward(m model.Message) bool {
	if v.share.chats == nil || v.alone || m.NoForwards {
		return false
	}
	if v.isStory(m) {
		return v.stories != nil
	}
	return v.share.forwarder != nil && m.Key.MessageID > 0 && !m.Local()
}

// storyNow is m, a story, as the store knows it now: its heart changes
// while it is shown.
func (v *mediaViewer) storyNow(m model.Message) model.Message {
	if v.stories != nil && v.isStory(m) {
		own := v.realStory(m)
		if now, ok := v.stories.StoryNow(own.Key.ChatID, own.Key.MessageID); ok {
			// Kept as the viewer keys it.
			now.Key = m.Key
			return now
		}
	}
	return m
}

// updateShare takes the clicks on the buttons of m, the item shown, and
// what came of them, before anything is drawn.
func (v *mediaViewer) updateShare(gtx layout.Context, m model.Message, l localization.Catalog) {
	s := &v.share
	if s.forward.Clicked(gtx) && v.canForward(m) {
		s.item, s.picking = m, true
		s.picker.open(gtx)
	}
	if s.like.Clicked(gtx) {
		v.toggleLike(m)
	}
	select {
	case err := <-s.failed:
		v.toast.Show(mediaErrorText(err))
	default:
	}
	if err, done := s.picker.result(l); done && err == nil {
		v.toast.Show(l.Format("history.forwarded", map[string]string{"chat": l.ChatTitle(s.picker.target)}))
	}
	if s.picking && !s.picker.modal.Shown() {
		// Closed: the keys are the viewer's again.
		s.picking, v.focus = false, true
	}
}

// toggleLike puts the account's heart on m, a story, or takes it away.
func (v *mediaViewer) toggleLike(m model.Message) {
	now := v.storyNow(m)
	if v.stories == nil || now.Story == nil {
		return
	}
	if v.share.failed == nil {
		v.share.failed = make(chan error, 1)
	}
	failed, stories, like := v.share.failed, v.stories, !now.Story.Liked
	m = v.realStory(m)
	go func() {
		defer crash.Recover("story heart", func(e *crash.Panic) { failed <- e; v.invalidate() })
		if err := stories.LikeStory(context.Background(), m.Key.ChatID, m.Key.MessageID, like); err != nil {
			select {
			case failed <- err:
			default:
			}
		}
		v.invalidate()
	}()
}

// layoutShare draws the picker over the viewer, when it is open, and
// forwards to the chat picked.
func (v *mediaViewer) layoutShare(gtx layout.Context, l localization.Catalog) {
	s := &v.share
	if !s.picker.modal.Shown() || s.chats == nil {
		return
	}
	targets := s.picker.targets(s.chats(), s.rights, l)
	c, ok := s.picker.layout(gtx, l, targets, s.avatar)
	if !ok {
		return
	}
	item, story, stories, forwarder := v.realStory(s.item), v.isStory(s.item), v.stories, s.forwarder
	s.picker.send(context.Background(), c, func(ctx context.Context, to int64) error {
		if story {
			return stories.ShareStory(ctx, item.Key.ChatID, item.Key.MessageID, to)
		}
		return forwarder.ForwardMessages(ctx, item.Key.ChatID, []model.MessageID{item.Key.MessageID}, to)
	}, v.invalidate)
}

// layoutWaiting draws the viewer while the stories it opened on are asked
// for: the backdrop, the ring, and the button that closes it.
func (v *mediaViewer) layoutWaiting(gtx layout.Context, l localization.Catalog) {
	if v.backdrop.Clicked(gtx) && !v.standalone || v.close.Clicked(gtx) {
		v.Close()
		return
	}
	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	v.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.Fill(gtx.Ops, v.backdropColor)
		return layout.Dimensions{Size: size}
	})
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return v.share.loader.sized(gtx, l, 48)
	})
	d := gtx.Dp(40)
	offset(gtx, image.Pt(size.X-gtx.Dp(12)-d, (gtx.Dp(viewerBar)-d)/2), func(gtx layout.Context) layout.Dimensions {
		return viewerButton(gtx, &v.close, iconClear, d)
	})
}

// layoutLike draws the heart of a story, red once the account put it, and
// how many put theirs.
func layoutLike(gtx layout.Context, c *widget.Clickable, story model.Story, d int) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		white := token.NewMatColorFromHexRGB(0xffffff)
		icon, col := iconReacted, white
		if story.Liked {
			icon, col = iconLiked, likedRed
		}
		var count layout.Dimensions
		var call op.CallOp
		if story.Likes > 0 {
			macro := op.Record(gtx.Ops)
			cgtx := gtx
			cgtx.Constraints.Min = image.Point{}
			count = label(cgtx, fmt.Sprint(story.Likes), token.TypestyleLabelLarge, white, 1)
			call = macro.Stop()
		}
		inner := d * 3 / 5
		width := d
		if count.Size.X > 0 {
			width = (d-inner)/2 + inner + gtx.Dp(6) + count.Size.X + (d-inner)/2
		}
		size := image.Pt(width, d)
		alpha := uint8(110)
		if c.Hovered() {
			alpha = 170
		}
		paint.FillShape(gtx.Ops, color.NRGBA{R: 40, G: 44, B: 52, A: alpha}, clip.UniformRRect(image.Rectangle{Max: size}, d/2).Op(gtx.Ops))
		offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(inner, inner))
			return icon(gtx, col)
		})
		if count.Size.X > 0 {
			offset(gtx, image.Pt((d-inner)/2+inner+gtx.Dp(6), (d-count.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
				call.Add(gtx.Ops)
				return count
			})
		}
		return layout.Dimensions{Size: size}
	})
}
