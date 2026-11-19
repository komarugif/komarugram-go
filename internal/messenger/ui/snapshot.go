// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"maps"
	"math"
	"time"

	"gio-mw/defaults"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/radio"
	"gio-mw/widget/toggle"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/resample"
)

// shotTheme is the theme a snapshot is drawn in.
type shotTheme int

const (
	shotCurrent shotTheme = iota
	shotLight
	shotDark
)

// shotOptions are what AyuGram's message shot box lets choose.
type shotOptions struct {
	theme                                 shotTheme
	background, date, reactions, spoilers bool
}

// shotDialog makes a snapshot of the selected messages, as AyuGram's
// message shot box does: a preview, the theme, what the snapshot shows,
// and buttons that save it as a PNG or copy it.
type shotDialog struct {
	modal    modal
	msgs     []model.Message
	opts     shotOptions
	themes   *radio.Radios[shotTheme]
	switches *toggle.Toggle[string]
	// stale is set when the preview is not of the options; rendering,
	// while one is on its way.
	stale, rendering bool
	// opened is when the dialog opened; retryAt, when the messages are laid
	// out again while their pictures load (see shotMediaWait).
	opened, retryAt time.Time
	results         chan shotRender
	image           *image.RGBA
	encoded         []byte
	err             error
	save, copy      surface
	close           surface
	loader          loadingIndicator
	light, dark     *token.Theme
	// themeFonts is the version of the fonts the themes were made with.
	themeFonts uint64
}

// shotMediaWait is how long a snapshot waits for the pictures of its
// messages to load, laid out again until none is loading: the selected
// messages of a chat's shared files are not drawn in the size the
// history has them in, and their pictures in it load only now. After it,
// what has not loaded is drawn as it is.
const (
	shotMediaWait  = 20 * time.Second
	shotMediaRetry = 100 * time.Millisecond
	shotMargin     = 8
	shotMessageGap = 24
	shotMaxWidth   = 1000 // PNG pixels, including the margins.
)

type shotRender struct {
	image *image.RGBA
	err   error
}

var shotSwitches = []string{"background", "date", "reactions", "spoilers"}

// selectedMessages are the messages selected, in their order.
func (p *chatPage) selectedMessages() []model.Message {
	var msgs []model.Message
	for _, m := range p.messages {
		if p.selection.selected[m.Key.MessageID] {
			if picked := p.selection.parts[m.Key.MessageID]; picked != nil {
				msgs = append(msgs, model.GroupAlbums(appendSelectedParts(nil, m, picked))...)
			} else {
				msgs = append(msgs, m)
			}
		}
	}
	return msgs
}

// open shows the dialog for msgs, drawn as the chat shows them.
func (d *shotDialog) open(p *chatPage, msgs []model.Message) {
	if len(msgs) == 0 {
		return
	}
	d.stop()
	d.msgs = msgs
	d.opts = shotOptions{background: true, date: true, reactions: true}
	d.themes = radio.NewRadios([]shotTheme{shotCurrent, shotLight, shotDark}, shotCurrent, func(t shotTheme) {
		d.opts.theme, d.stale = t, true
	})
	d.switches = toggle.NewToggle(shotSwitches, []string{"background", "date", "reactions"}, func(values []string) {
		on := func(v string) bool {
			for _, one := range values {
				if one == v {
					return true
				}
			}
			return false
		}
		d.opts.background, d.opts.date, d.opts.reactions, d.opts.spoilers = on("background"), on("date"), on("reactions"), on("spoilers")
		d.stale = true
	})
	d.stale = true
	d.opened = time.Now()
	d.modal.Open()
}

func (d *shotDialog) stop() {
	*d = shotDialog{light: d.light, dark: d.dark, themeFonts: d.themeFonts}
}

func (d *shotDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	select {
	case r := <-d.results:
		d.rendering, d.results = false, nil
		d.image, d.err, d.encoded = r.image, r.err, nil
		if r.err != nil {
			d.modal.Toast(l.T("history.snapshot_failed") + ": " + mediaErrorText(r.err))
		}
	default:
	}
	if !d.modal.closing {
		if d.close.Clicked(gtx) {
			d.modal.Close()
		}
		if d.save.Clicked(gtx) && d.image != nil {
			if b, err := d.png(); err != nil {
				d.modal.Toast(l.T("history.snapshot_failed") + ": " + mediaErrorText(err))
			} else if path, err := saveSnapshot(b, fmt.Sprintf("komarugram-go-%s.png", time.Now().Format("2006-01-02-150405"))); err != nil {
				d.modal.Toast(l.T("history.snapshot_failed") + ": " + mediaErrorText(err))
			} else {
				d.modal.Toast(l.Format("history.snapshot_saved", map[string]string{"path": path}))
			}
		}
		if d.copy.Clicked(gtx) && d.image != nil {
			if b, err := d.png(); err == nil {
				gtx.Execute(clipboard.WriteCmd{Type: "image/png", Data: io.NopCloser(bytes.NewReader(b))})
				d.modal.Toast(l.T("shot.copied"))
			}
		}
		d.themes.Update(gtx)
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(520))
		height := min(gtx.Constraints.Max.Y, gtx.Dp(720))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions { return d.layoutContent(gtx, p, l) }, defaultCardPadding)
	})
	if !shown {
		d.stop()
		return
	}
	// The switches take input during layout. Render their new options even
	// when the switches are not animated and no further frame is due.
	if d.stale && !d.rendering && !gtx.Now.Before(d.retryAt) {
		d.render(gtx, p, l)
	}
}

// png is the snapshot as a PNG, encoded once.
func (d *shotDialog) png() ([]byte, error) {
	if d.encoded != nil {
		return d.encoded, nil
	}
	var b bytes.Buffer
	if err := png.Encode(&b, d.image); err != nil {
		return nil, err
	}
	d.encoded = b.Bytes()
	return d.encoded, nil
}

func (d *shotDialog) layoutContent(gtx layout.Context, p *chatPage, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("shot.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
		}),
		vspace(12),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Max
			fillRounded(gtx, sc.SurfaceContainerLow, size, gtx.Dp(12))
			switch {
			case d.image != nil:
				return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = gtx.Constraints.Max
					return widget.Image{Src: p.images.Op(d.image), Fit: widget.ScaleDown, Position: layout.N, Scale: 1 / gtx.Metric.PxPerDp}.Layout(gtx)
				})
			case d.err != nil:
				// The toast told why; the preview stays empty.
				return layout.Dimensions{Size: size}
			}
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.loader.sized(gtx, l, 32) })
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("shot.theme"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.themes.Layout(gtx, radio.LeadingKind, map[shotTheme]string{
				shotCurrent: l.T("shot.theme_current"), shotLight: l.T("settings.theme_light"), shotDark: l.T("settings.theme_dark"),
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.switches.Layout(gtx, map[string]string{
				"background": l.T("shot.background"), "date": l.T("shot.date"), "reactions": l.T("shot.reactions"), "spoilers": l.T("shot.spoilers"),
			})
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return textButton(gtx, &d.close, l.T("stickers.close"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.copy, l.T("shot.copy")) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.save, l.T("shot.save")) }),
			)
		}),
	)
}

// render lays the messages out as the options ask, and renders them off
// the frame.
func (d *shotDialog) render(gtx layout.Context, p *chatPage, l localization.Catalog) {
	d.stale = false
	sgtx := gtx
	sgtx.Values = maps.Clone(gtx.Values)
	if v := defaults.FontsVersion(); v != d.themeFonts {
		d.themeFonts, d.light, d.dark = v, nil, nil
	}
	switch d.opts.theme {
	case shotLight:
		if d.light == nil {
			d.light = defaults.NewTheme(gtx, appColors{}.scheme(false))
		}
		wdk.InitMaterialThemeInContext(sgtx, d.light)
	case shotDark:
		if d.dark == nil {
			d.dark = defaults.NewTheme(gtx, appColors{}.scheme(true))
		}
		wdk.InitMaterialThemeInContext(sgtx, d.dark)
	}
	p.media.LoadingSeen()
	ops, size, ok := p.buildSnapshot(sgtx, l, d.msgs, d.opts)
	if ok && p.media.LoadingSeen() > 0 && time.Since(d.opened) < shotMediaWait {
		// Laid out again once the pictures came, or in a moment.
		d.stale, d.retryAt = true, gtx.Now.Add(shotMediaRetry)
		gtx.Execute(op.InvalidateCmd{At: d.retryAt})
		return
	}
	if !ok {
		d.image, d.err = nil, fmt.Errorf("%s", l.T("history.snapshot_too_tall"))
		d.modal.Toast(l.T("history.snapshot_too_tall"))
		return
	}
	pad := max(1, gtx.Dp(shotMargin))
	fill := scheme(sgtx).SurfaceContainerLow.AsNRGBA()
	var wallpaper *chattheme.Background
	if d.opts.theme == shotCurrent && d.opts.background && p.appearance != nil {
		wallpaper = p.appearance.background
	}
	d.rendering = true
	d.results = make(chan shotRender, 1)
	results := d.results
	go func() {
		defer crash.Recover("snapshot", func(e *crash.Panic) { results <- shotRender{err: e}; p.invalidate() })
		img, err := renderSnapshot(ops, size)
		if err == nil {
			img = finishMessageShot(img, pad, fill, wallpaper)
		}
		results <- shotRender{img, err}
		p.invalidate()
	}()
}

// buildSnapshot lays msgs out within the PNG's width limit, then moves
// their columns together. Private chats include sender avatars. The
// result is cropped after rendering. ok is false when it is too tall.
func (p *chatPage) buildSnapshot(gtx layout.Context, l localization.Catalog, msgs []model.Message, opts shotOptions) (ops *op.Ops, size image.Point, ok bool) {
	pad := max(1, gtx.Dp(shotMargin))
	width := shotMaxWidth - 2*pad
	insetLeft := gtx.Dp(rowInsetLeft)
	insets := insetLeft + gtx.Dp(rowInsetRight)
	room := image.Pt(width-insets, 0)
	ops = new(op.Ops)
	// The headless renderer needs its own frozen dust images, which the
	// chat cannot change while the snapshot is drawn.
	sgtx := gtx.Disabled()
	sgtx.Values = maps.Clone(gtx.Values)
	withEffects(sgtx, new(effects), true)
	sgtx.Ops = ops
	sgtx.Constraints = layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, snapshotMaxHeight)}
	type dateLabel struct {
		call     op.CallOp
		y, width int
	}
	type rowDraw struct {
		call     op.CallOp
		align    layout.Direction
		y, width int
	}
	var dates []dateLabel
	var rows []rowDraw
	var height, widestIncoming, widestOutgoing, widestCentered int
	// A chat's theme is of the theme it is shown in.
	appearance := p.appearance
	if opts.theme != shotCurrent {
		p.appearance = nil
	}
	p.snapshotting, p.snapshotRevealSpoilers = true, opts.spoilers
	defer func() {
		p.snapshotting, p.snapshotRevealSpoilers, p.appearance = false, false, appearance
	}()
	joins := messageJoins(msgs, p.kind == model.KindSaved)
	for i, m := range msgs {
		if opts.date && (i == 0 || !sameDay(m.Date, msgs[i-1].Date)) {
			dateGtx := sgtx
			dateGtx.Constraints.Min.X = 0
			macro := op.Record(ops)
			dims := layout.Inset{Top: 10, Bottom: 10}.Layout(dateGtx, func(gtx layout.Context) layout.Dimensions {
				return datePill(gtx, m.Date.Local().Format("02.01.2006"))
			})
			dates = append(dates, dateLabel{call: macro.Stop(), y: height, width: dims.Size.X})
			widestCentered = max(widestCentered, dims.Size.X)
			height += dims.Size.Y
		}
		if !opts.reactions {
			m.Reactions = nil
		}
		row := rowDraw{align: layout.W, y: height}
		macro := op.Record(ops)
		dims := p.row(sgtx, m, false, joins[i], l, false)
		if r := p.rows[m.Key.MessageID]; r != nil && r.bodySize.X > 0 {
			row.width = r.bodySize.X
			switch {
			case m.ServicePill():
				row.align = layout.Center
				widestCentered = max(widestCentered, row.width)
			case m.Outgoing:
				row.align = layout.E
				widestOutgoing = max(widestOutgoing, row.width)
			default:
				widestIncoming = max(widestIncoming, row.width)
			}
			if p.avatar != nil && joins[i]&joinBelow == 0 {
				if id := p.senderAvatar(m); id != 0 {
					offset(sgtx, r.avatarPoint, func(gtx layout.Context) layout.Dimensions {
						return p.avatar(gtx, id, model.KindUser, p.avatarName(m), 34)
					})
				}
			}
		}
		row.call = macro.Stop()
		rows = append(rows, row)
		height += dims.Size.Y
	}
	if height+2*pad > snapshotMaxHeight {
		return nil, image.Point{}, false
	}
	columns := widestIncoming + widestOutgoing
	if widestIncoming > 0 && widestOutgoing > 0 {
		columns += gtx.Dp(shotMessageGap)
	}
	packedWidth := min(width, insets+max(columns, widestCentered))
	packedRoom := image.Pt(packedWidth-insets, 0)
	left, right := packedWidth, 0
	for _, row := range rows {
		body := image.Pt(row.width, 0)
		x := row.align.Position(body, packedRoom).X
		shift := x - row.align.Position(body, room).X
		stack := op.Offset(image.Pt(shift, row.y)).Push(ops)
		row.call.Add(ops)
		stack.Pop()
		if row.width > 0 {
			left, right = min(left, insetLeft+x), max(right, insetLeft+x+row.width)
		}
	}
	// Center the dates over the selected messages so a short bubble on
	// either side does not have to share the width of the chat with them.
	for _, date := range dates {
		x := min(max(0, (left+right-date.width)/2), packedWidth-date.width)
		stack := op.Offset(image.Pt(x, date.y)).Push(ops)
		date.call.Add(ops)
		stack.Pop()
	}
	return ops, image.Pt(packedWidth, max(1, height)), true
}

// finishMessageShot finds the painted pixels before adding the margin and
// background. This keeps pixels beyond the bubble, such as tails and avatars.
func finishMessageShot(img *image.RGBA, pad int, fill color.NRGBA, wallpaper *chattheme.Background) *image.RGBA {
	size := img.Bounds().Size()
	left, top, right, bottom := size.X, size.Y, 0, 0
	for y := range size.Y {
		line := img.Pix[y*img.Stride:][:size.X*4]
		for x := range size.X {
			if line[x*4+3] != 0 {
				left, top = min(left, x), min(top, y)
				right, bottom = max(right, x+1), max(bottom, y+1)
			}
		}
	}
	content := image.Rectangle{Min: image.Pt(left, top), Max: image.Pt(right, bottom)}
	if content.Empty() {
		content = image.Rectangle{}
	}
	size = content.Size().Add(image.Pt(2*pad, 2*pad))
	var out *image.RGBA
	if wallpaper != nil {
		out = wallpaper.Render(size)
		if out.Bounds().Size() != size {
			out = resample.Resize(out, size.X, size.Y)
		}
	} else {
		out = image.NewRGBA(image.Rectangle{Max: size})
		draw.Draw(out, out.Bounds(), image.NewUniform(fill), image.Point{}, draw.Src)
	}
	// Gio's headless target encodes linear-premultiplied RGB in sRGB.
	// Blend the antialiased edges over the opaque background in linear light.
	for y := range content.Dy() {
		src := img.Pix[(content.Min.Y+y)*img.Stride+content.Min.X*4:][:content.Dx()*4]
		dst := out.Pix[(pad+y)*out.Stride+pad*4:][:len(src)]
		for x := 0; x < len(src); x += 4 {
			switch alpha := src[x+3]; alpha {
			case 0:
			case 255:
				copy(dst[x:x+4], src[x:x+4])
			default:
				remaining := float64(255-alpha) / 255
				for c := x; c < x+3; c++ {
					dst[c] = shotEncodeSRGB(shotLinearSRGB[src[c]] + shotLinearSRGB[dst[c]]*remaining)
				}
			}
		}
	}
	return out
}

var shotLinearSRGB = func() (linear [256]float64) {
	for i := range linear {
		v := float64(i) / 255
		if v <= 0.04045 {
			linear[i] = v / 12.92
		} else {
			linear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return linear
}()

func shotEncodeSRGB(v float64) uint8 {
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(min(255, v*255+0.5))
}
