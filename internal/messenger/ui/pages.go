// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"

	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/scroll"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	pageMaxWidth       = unit.Dp(560)
	chatHeaderSize     = unit.Dp(56)
	pageAvatarSize     = unit.Dp(96)
	chatHeaderImage    = unit.Dp(40)
	defaultCardPadding = unit.Dp(20)
)

// layoutEmptyPage is shown when no chat is open.
func layoutEmptyPage(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.SurfaceContainerLow, size)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return pill(gtx, l.T("page.choose_chat"))
	})
	return layout.Dimensions{Size: size}
}

// layoutChatPage composes the chat header, history and message composer.
func layoutChatPage(gtx layout.Context, c model.Chat, l localization.Catalog, drawAvatar avatarLayout, badges badgesLayout, body layout.Widget, selection *chatPage) layout.Dimensions {
	return layoutChatPageHead(gtx, c, l, drawAvatar, badges, nil, body, selection)
}

// chatHead is the header of a page that is not a chat itself, such as the
// comments to a post: a way back, a title and a line under it.
type chatHead struct {
	back            *button.Button
	title, subtitle string
}

// layoutChatPageHead is layoutChatPage with head, when set, in place of
// the chat's avatar, name and status.
func layoutChatPageHead(gtx layout.Context, c model.Chat, l localization.Catalog, drawAvatar avatarLayout, badges badgesLayout, head *chatHead, body layout.Widget, selection *chatPage) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	header := image.Pt(size.X, gtx.Dp(chatHeaderSize))
	// Record the ordinary header, but replay it only when search or selection
	// does not replace it. Stacking translucent headers would hide the desktop.
	headerRecording := op.Record(gtx.Ops)
	fillWindowSurface(gtx, sc.Surface.Color, header)
	pad := gtx.Dp(16)
	imagePx := gtx.Dp(chatHeaderImage)
	title, status := c.Title, chatStatus(c, l)
	if selection != nil {
		status = selection.chatStatusOnline(c, gtx.Now, l)
	}
	infoPx := 0
	if head == nil {
		offset(gtx, image.Pt(pad, (header.Y-imagePx)/2), func(gtx layout.Context) layout.Dimensions {
			return drawAvatar(gtx, c.ID, c.Kind, c.Title, chatHeaderImage)
		})
		if selection != nil {
			// The buttons at the end: search and the chat's menu.
			infoPx = selection.headActionsWidth(gtx, false) - pad + gtx.Dp(8)
		} else {
			// The info mark at the end tells that the header opens the chat's info.
			infoPx = gtx.Dp(24)
			offset(gtx, image.Pt(size.X-pad-infoPx, (header.Y-infoPx)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(infoPx, infoPx), func(gtx layout.Context) layout.Dimensions {
					return iconInfo(gtx, sc.SurfaceVariant.OnColor)
				})
			})
		}
	} else {
		title, status = head.title, head.subtitle
		// The back button takes the avatar's place; it is laid out last,
		// over the header's own click.
		pad = gtx.Dp(8)
		if selection != nil {
			infoPx = max(0, selection.headActionsWidth(gtx, true)-pad)
		}
	}
	textX := pad + imagePx + gtx.Dp(12)
	textGtx := gtx
	textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-2*pad-infoPx, 0), header.Y)}
	offset(textGtx, image.Pt(textX, gtx.Dp(8)), func(gtx layout.Context) layout.Dimensions {
		var before, after layout.Widget
		if badges != nil && head == nil {
			before, after = badges(c.Badges, 18, false)
		}
		return withBadges(gtx, before, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title, token.TypestyleTitleMediumEmphasized, sc.Surface.OnColor, 1)
		}, after)
	})
	offset(textGtx, image.Pt(textX, gtx.Dp(30)), func(gtx layout.Context) layout.Dimensions {
		return label(gtx, status, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
	})
	offset(gtx, image.Pt(0, header.Y), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		return layout.Dimensions{}
	})

	if selection != nil && head == nil {
		hgtx := gtx
		hgtx.Constraints = layout.Exact(header)
		selection.header.Layout(hgtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Dimensions{Size: header}
		})
		if c.Kind != model.KindSaved {
			layoutAvatarTarget(gtx, &selection.headAvatar)
		}
		selection.layoutHeadActions(gtx, header, size.X-gtx.Dp(8), false, l)
	}
	if selection != nil && head != nil {
		selection.layoutHeadActions(gtx, header, size.X-gtx.Dp(8), true, l)
	}
	if head != nil {
		// The button's target is 48 dp, around where the avatar would be.
		target := gtx.Dp(48)
		offset(gtx, image.Pt(pad+(imagePx-target)/2, (header.Y-target)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(target, target))
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return head.back.LayoutIconOnly(gtx, l.T("settings.back"), iconBack)
			})
		})
	}
	headerCall := headerRecording.Stop()
	bodyGtx := gtx
	bodyGtx.Constraints = layout.Exact(image.Pt(size.X, max(size.Y-header.Y, 0)))
	offset(bodyGtx, image.Pt(0, header.Y), func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, sc.SurfaceContainerLow, gtx.Constraints.Max)
		return body(gtx)
	})
	headerGtx := gtx
	headerGtx.Constraints = layout.Exact(header)
	active := selection != nil && selection.chat == c.ID
	if active && selection.chatSearch.open {
		selection.chatSearchUpdate(headerGtx)
	}
	switch {
	case active && selection.selectionCount() > 0:
		fillWindowSurface(headerGtx, sc.Surface.Color, header)
		selection.selectionHeader(headerGtx, l)
	case active && selection.chatSearch.open:
		selection.layoutChatSearch(headerGtx, header, l)
	default:
		headerCall.Add(gtx.Ops)
	}
	if selection != nil && head == nil && selection.chat == c.ID {
		selection.layoutChatMenu(gtx, l)
	}
	if selection != nil && selection.chat == c.ID {
		selection.layoutDialogs(gtx, l)
	}
	return layout.Dimensions{Size: size}
}

// scrollPage lays out content in a centered, scrollable column, with a
// toast at its bottom for what the page has to tell.
type scrollPage struct {
	list  scroll.List
	toast toast
}

func (p *scrollPage) layout(gtx layout.Context, content layout.Widget) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.SurfaceContainerLow, size)
	p.list.Axis = layout.Vertical
	gtx.Constraints = layout.Exact(size)
	p.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(pageMaxWidth))
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(unit.Dp(24)).Layout(gtx, content)
		})
	})
	p.toast.Layout(gtx, image.Rectangle{Max: size})
	return layout.Dimensions{Size: size}
}

// layoutAvatarTarget lays out, over the avatar of a chat's header, the click
// that opens its photo; laid out after the header's own, it takes the
// clicks on the avatar from it.
func layoutAvatarTarget(gtx layout.Context, click *widget.Clickable) {
	side := gtx.Dp(chatHeaderImage)
	offset(gtx, image.Pt(gtx.Dp(16), (gtx.Dp(chatHeaderSize)-side)/2), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(side, side))
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	})
}
