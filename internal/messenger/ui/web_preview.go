// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"net/url"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// A message's link preview is a card under its text, as Telegram Desktop
// draws a web page (HistoryView::WebPage): a bar and a tint in the color
// of the sender, the site, the title and the description, the page's
// photo small beside them. A press on it opens the link, through the
// usual confirmation; a page Telegram has an Instant View of has a button
// under it that opens the view in an article window (Iv::Instance::show).

// webPreviewDescriptionLines is how many lines of the description show.
const webPreviewDescriptionLines = 3

// webPreview draws the link preview of m.
func (p *chatPage) webPreview(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	w := m.WebPage
	if r.preview.Clicked(gtx) {
		p.askLink(w.URL)
	}
	if r.instantView.Clicked(gtx) {
		p.openInstantView(m)
	}
	sc := scheme(gtx)
	col := sc.Primary.Color
	if !m.Outgoing && m.SenderID != 0 {
		col = senderColor(gtx, m.SenderID)
	}
	gtx.Constraints.Min = image.Point{}
	thumb := 0
	if w.Photo != nil && w.Video == nil && p.media != nil && p.images != nil {
		thumb = gtx.Dp(56)
	}
	site := w.Site
	if site == "" {
		if u, err := url.Parse(w.URL); err == nil {
			site = u.Hostname()
		}
	}
	pad := image.Pt(gtx.Dp(11), gtx.Dp(6))
	text := gtx
	text.Constraints.Max.X = max(1, gtx.Constraints.Max.X-2*pad.X-thumb-min(thumb, gtx.Dp(8)))
	macro := op.Record(gtx.Ops)
	dims := offset(text, pad, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, site, token.TypestyleLabelLargeEmphasized, col, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if w.Title == "" {
					return layout.Dimensions{}
				}
				return label(gtx, w.Title, token.TypestyleTitleSmall, sc.Surface.OnColor, 2)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if w.Description == "" {
					return layout.Dimensions{}
				}
				return label(gtx, w.Description, token.TypestyleBodyMedium, sc.Surface.OnColor, webPreviewDescriptionLines)
			}),
		)
	})
	call := macro.Stop()
	size := image.Pt(gtx.Constraints.Max.X, max(dims.Size.Y, thumb)+2*pad.Y)
	// The page's video, under the text, as Telegram Desktop attaches it
	// (CreateAttach): it plays as a video message's does.
	var video op.CallOp
	videoAt := image.Pt(pad.X, dims.Size.Y+pad.Y+gtx.Dp(6))
	if w.Video != nil && p.media != nil {
		if r.previewVideo == nil {
			r.previewVideo = &messageRow{alone: true}
		}
		inner := gtx
		inner.Constraints.Max.X = max(1, gtx.Constraints.Max.X-2*pad.X)
		vm := model.Message{Key: m.Key, Kind: w.VideoKind, Media: w.Video, Date: m.Date, SenderID: m.SenderID, NoForwards: m.NoForwards}
		macro := op.Record(gtx.Ops)
		vd := offset(inner, videoAt, func(gtx layout.Context) layout.Dimensions { return p.mediaLayout(gtx, r.previewVideo, vm, l, animate) })
		video = macro.Stop()
		size.Y = videoAt.Y + vd.Size.Y + pad.Y
		size.X = min(size.X, max(dims.Size.X, vd.Size.X)+2*pad.X)
	} else if thumb == 0 {
		size.X = min(size.X, dims.Size.X+2*pad.X)
	}
	radius := gtx.Dp(6)
	style := surfaceStyle{radius: radius, background: col.SetOpacity(.12), content: col, button: site}
	card := r.preview.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		fillRect(gtx, col, image.Pt(gtx.Dp(3), size.Y))
		call.Add(gtx.Ops)
		if thumb > 0 {
			offset(gtx, image.Pt(size.X-pad.X-thumb, pad.Y), func(gtx layout.Context) layout.Dimensions {
				defer clip.UniformRRect(image.Rectangle{Max: image.Pt(thumb, thumb)}, gtx.Dp(4)).Push(gtx.Ops).Pop()
				photo := model.Message{Key: m.Key, Kind: model.MessagePhoto, Media: w.Photo}
				if frame, _ := p.media.Frame(photo, animate); frame != nil {
					gtx.Constraints = layout.Exact(image.Pt(thumb, thumb))
					return widget.Image{Src: p.images.Op(frame), Fit: widget.Cover}.Layout(gtx)
				}
				fillRect(gtx, col.SetOpacity(.2), image.Pt(thumb, thumb))
				return layout.Dimensions{Size: image.Pt(thumb, thumb)}
			})
		}
		return layout.Dimensions{Size: size}
	})
	// Over the card, so that a press on the video plays it.
	video.Add(gtx.Ops)
	if !w.InstantView {
		return card
	}
	if _, ok := p.source.(model.InstantViewStore); !ok {
		return card
	}
	// The Instant View button, under the card and as wide, as Telegram
	// Desktop's view button.
	gap := gtx.Dp(6)
	button := offset(gtx, image.Pt(0, card.Size.Y+gap), func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("rich.instant_view"), token.TypestyleLabelLargeEmphasized, col, 1)
		})
		call := macro.Stop()
		size := image.Pt(card.Size.X, dims.Size.Y)
		return r.instantView.Layout(gtx, size, surfaceStyle{radius: radius, background: col.SetOpacity(.12), content: col, button: l.T("rich.instant_view")}, func(gtx layout.Context) layout.Dimensions {
			offset(gtx, image.Pt((size.X-dims.Size.X)/2, 0), func(gtx layout.Context) layout.Dimensions {
				call.Add(gtx.Ops)
				return dims
			})
			return layout.Dimensions{Size: size}
		})
	})
	return layout.Dimensions{Size: image.Pt(card.Size.X, card.Size.Y+gap+button.Size.Y)}
}

// errNoInstantView is a page whose Instant View is gone.
var errNoInstantView = errors.New("no instant view")

// openInstantView loads the Instant View of m's link preview off the frame,
// and shows it in an article window titled with the site.
func (p *chatPage) openInstantView(m model.Message) {
	store, ok := p.source.(model.InstantViewStore)
	if !ok || m.WebPage == nil {
		return
	}
	w := *m.WebPage
	p.openSource(articleSource{url: w.URL}, func(ctx context.Context) (model.Message, string, error) {
		page, err := store.InstantView(ctx, w.URL)
		if err != nil {
			return model.Message{}, "", err
		}
		if len(page.Blocks) == 0 {
			return model.Message{}, "", errNoInstantView
		}
		title := w.Site
		if title == "" {
			if u, err := url.Parse(w.URL); err == nil {
				title = u.Hostname()
			}
		}
		page.Part = false
		return articleMessage(m, page), title, nil
	})
}
