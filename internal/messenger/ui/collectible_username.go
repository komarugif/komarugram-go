// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"io"
	"strconv"
	"strings"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"

	"gioui.org/io/clipboard"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/widget"
)

// collectibleDialog tells how a collectible username of the chat whose
// info is open was bought, as Telegram Desktop's CollectibleInfoBox: on
// Fragment, when and for how much, with its page there and its link.
type collectibleDialog struct {
	name   string
	owner  model.Chat
	modal  modal
	header widget.Clickable
	close  surface
	more   surface
	copy   surface
	result chan collectibleResult
	cancel context.CancelFunc
	info   *model.CollectibleUsername
	err    error
}

type collectibleResult struct {
	info model.CollectibleUsername
	err  error
}

// openCollectible opens the dialog of the username name of the open chat.
func (p *chatInfo) openCollectible(name string) {
	if p.profiles == nil {
		return
	}
	d := &collectibleDialog{name: name, owner: p.chat, result: make(chan collectibleResult, 1)}
	d.modal.Open()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	d.cancel = cancel
	source, invalidate := p.profiles, p.invalidate
	go func() {
		defer cancel()
		defer crash.Recover("collectible username", func(e *crash.Panic) { d.result <- collectibleResult{err: e} })
		info, err := source.CollectibleUsername(ctx, name)
		d.result <- collectibleResult{info: info, err: err}
		invalidate()
	}()
	p.collectible = d
}

// layoutCollectible draws the dialog over the info, when it is open.
func (p *chatInfo) layoutCollectible(gtx layout.Context, l localization.Catalog) {
	d := p.collectible
	if d == nil {
		return
	}
	select {
	case r := <-d.result:
		d.err = r.err
		if r.err == nil {
			d.info = &r.info
		}
	default:
	}
	mention, link := "@"+d.name, "https://t.me/"+d.name
	if d.close.Clicked(gtx) {
		d.modal.Close()
	}
	if d.header.Clicked(gtx) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(mention))})
		d.modal.Toast(l.T("info.username_copied"))
	}
	if d.copy.Clicked(gtx) {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(link))})
		d.modal.Toast(l.T("info.link_copied"))
	}
	if d.more.Clicked(gtx) && d.info != nil && d.info.URL != "" {
		p.renderer.askLink(d.info.URL)
	}
	covered := p.renderer.link != ""
	shown := d.modal.Layout(gtx, covered, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(380))
		gtx.Constraints = layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, gtx.Constraints.Max.Y)}
		macro := op.Record(gtx.Ops)
		dims := p.collectibleContent(gtx, d, l, mention)
		content := macro.Stop()
		defer clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(20)).Push(gtx.Ops).Pop()
		fillRect(gtx, scheme(gtx).Surface.Color, dims.Size)
		content.Add(gtx.Ops)
		offset(gtx, image.Pt(dims.Size.X-gtx.Dp(52), gtx.Dp(4)), func(gtx layout.Context) layout.Dimensions {
			return navigationButton(gtx, &d.close, iconClear, l.T("viewer.close"))
		})
		return dims
	})
	if !shown && p.collectible == d {
		d.cancel()
		p.collectible = nil
	}
}

func (p *chatInfo) collectibleContent(gtx layout.Context, d *collectibleDialog, l localization.Catalog, mention string) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Inset{Top: 32, Bottom: 16, Left: 24, Right: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				n := gtx.Dp(72)
				size := image.Pt(n, n)
				fillRounded(gtx, sc.Primary.Color, size, n/2)
				return exact(gtx, size, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, "@", token.TypestyleDisplaySmall, sc.Primary.OnColor, 1)
					})
				})
			}), vspace(16),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return d.header.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					title := l.Format("info.collectible_title", map[string]string{"username": mention})
					return centeredLabel(gtx, title, token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
				})
			}), vspace(12),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.collectibleOwner(gtx, d.owner, l) }), vspace(16),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				switch {
				case d.info != nil:
					bought := d.info.Bought.Local()
					args := map[string]string{
						"date":  l.Format("audio.date", map[string]string{"date": bought.Format("02.01.2006"), "time": bought.Format("15:04")}),
						"price": "**" + collectiblePrice(*d.info, l) + "**",
					}
					text := richLabel{runs: markedRuns(l.Format("info.collectible_info", args)), style: token.TypestyleBodyMedium, color: sc.Surface.OnColor, align: text.Middle}
					return text.Layout(gtx)
				case d.err != nil:
					return centeredLabel(gtx, mediaErrorText(d.err), token.TypestyleBodyMedium, sc.Error.Color, 0)
				}
				return exact(gtx, image.Pt(gtx.Dp(32), gtx.Dp(32)), func(gtx layout.Context) layout.Dimensions {
					ring(gtx, gtx.Dp(32), 0, true)
					return layout.Dimensions{Size: gtx.Constraints.Max}
				})
			}), vspace(20),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if d.info == nil || d.info.URL == "" {
					return layout.Dimensions{}
				}
				return filledButton(gtx, &d.more, l.T("info.collectible_more"))
			}), vspace(4),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return textButton(gtx, &d.copy, l.T("info.collectible_copy"))
				})
			}),
		)
	})
}

// collectibleOwner is the capsule of the owner: the avatar and the name.
func (p *chatInfo) collectibleOwner(gtx layout.Context, owner model.Chat, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	height := gtx.Dp(28)
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	nameGtx := gtx
	nameGtx.Constraints.Max.X = max(0, gtx.Constraints.Max.X-height-gtx.Dp(20))
	nameDims := label(nameGtx, l.ChatTitle(owner), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
	name := macro.Stop()
	size := image.Pt(height+gtx.Dp(8)+nameDims.Size.X+gtx.Dp(12), height)
	fillRounded(gtx, sc.SurfaceContainerHigh, size, height/2)
	if p.drawAvatar != nil {
		p.drawAvatar(withSize(gtx, image.Pt(height, height)), owner.ID, owner.Kind, owner.Title, 28)
	}
	offset(gtx, image.Pt(height+gtx.Dp(8), (height-nameDims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
		name.Add(gtx.Ops)
		return nameDims
	})
	return layout.Dimensions{Size: size}
}

// filledButton draws a button of txt across the width, in the primary
// color.
func filledButton(gtx layout.Context, s *surface, txt string) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(40))
	style := surfaceStyle{radius: size.Y / 2, background: sc.Primary.Color, content: sc.Primary.OnColor, button: txt}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, txt, token.TypestyleLabelLarge, sc.Primary.OnColor, 1)
		})
	})
}

// collectiblePrice words what the username cost, as Telegram Desktop:
// the amount in TON, then in the currency.
func collectiblePrice(info model.CollectibleUsername, l localization.Catalog) string {
	decimal := l.T("number.decimal")
	crypto := fmt.Sprintf("%s %s", info.CryptoCurrency, strconv.FormatInt(info.CryptoAmount, 10))
	if info.CryptoCurrency == "TON" {
		crypto = decimalAmount(info.CryptoAmount, 9, decimal) + " TON"
	}
	if info.Currency == "" {
		return crypto
	}
	fiat := decimalAmount(info.Amount, 2, decimal)
	if info.Currency == "USD" {
		fiat = "$" + fiat
	} else {
		fiat += " " + info.Currency
	}
	return crypto + " (" + fiat + ")"
}

// decimalAmount is amount of units with exp digits after the point, with
// at least two of them and no zeros at the end beyond.
func decimalAmount(amount int64, exp int, point string) string {
	sign := ""
	if amount < 0 {
		sign, amount = "-", -amount
	}
	unit := int64(1)
	for range exp {
		unit *= 10
	}
	minor := strings.TrimRight(fmt.Sprintf("%0*d", exp, amount%unit), "0")
	for len(minor) < min(2, exp) {
		minor += "0"
	}
	if minor == "" {
		return sign + strconv.FormatInt(amount/unit, 10)
	}
	return sign + strconv.FormatInt(amount/unit, 10) + point + minor
}
