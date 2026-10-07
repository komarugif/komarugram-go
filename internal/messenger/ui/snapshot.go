// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"maps"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/radio"
	"gio-mw/widget/toggle"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
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
	results          chan shotRender
	image            *image.RGBA
	encoded          []byte
	err              error
	save, copy       surface
	close            surface
	loader           loadingIndicator
	light, dark      *token.Theme
	// themeFonts is the version of the fonts the themes were made with.
	themeFonts uint64
}

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
			msgs = append(msgs, m)
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
	if d.stale && !d.rendering {
		d.render(gtx, p, l)
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
					return widget.Image{Src: p.images.Op(d.image), Fit: widget.Contain, Position: layout.N}.Layout(gtx)
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
			d.light = defaults.NewTheme(gtx, schemes.SchemeBaselineLight())
		}
		wdk.InitMaterialThemeInContext(sgtx, d.light)
	case shotDark:
		if d.dark == nil {
			d.dark = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
		}
		wdk.InitMaterialThemeInContext(sgtx, d.dark)
	}
	ops, size, ok := p.buildSnapshot(sgtx, l, d.msgs, d.opts)
	if !ok {
		d.image, d.err = nil, fmt.Errorf("%s", l.T("history.snapshot_too_tall"))
		d.modal.Toast(l.T("history.snapshot_too_tall"))
		return
	}
	d.rendering = true
	d.results = make(chan shotRender, 1)
	results := d.results
	go func() {
		img, err := renderSnapshot(ops, size)
		results <- shotRender{img, err}
		p.invalidate()
	}()
}

// buildSnapshot lays msgs out as the history shows them, as opts asks, on
// the chat's background: without the selection's highlight, and with the
// avatars of private chats, which the history leaves out. ok is false
// when they are too tall for one picture.
func (p *chatPage) buildSnapshot(gtx layout.Context, l localization.Catalog, msgs []model.Message, opts shotOptions) (ops *op.Ops, size image.Point, ok bool) {
	width := p.historyWidth
	if width <= 0 {
		width = gtx.Dp(600)
	}
	ops = new(op.Ops)
	sgtx := gtx.Disabled()
	sgtx.Ops = ops
	sgtx.Constraints = layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, snapshotMaxHeight)}
	pad := gtx.Dp(8)
	type row struct {
		call   op.CallOp
		height int
	}
	var rows []row
	height := 2 * pad
	// A chat's theme is of the theme it is shown in.
	appearance := p.appearance
	if opts.theme != shotCurrent {
		p.appearance = nil
	}
	revealed := map[model.MessageID]bool{}
	p.snapshotting = true
	defer func() {
		p.snapshotting, p.appearance = false, appearance
		for id, was := range revealed {
			if r := p.rows[id]; r != nil {
				r.revealed = was
			}
		}
	}()
	joins := messageJoins(msgs)
	for i, m := range msgs {
		date := opts.date && (i == 0 || !sameDay(m.Date, msgs[i-1].Date))
		if !opts.reactions {
			m.Reactions = nil
		}
		if opts.spoilers {
			if r := p.rows[m.Key.MessageID]; r != nil {
				revealed[m.Key.MessageID] = r.revealed
				r.revealed = true
			}
		}
		macro := op.Record(ops)
		dims := p.row(sgtx, m, date, joins[i], l, false)
		if r := p.rows[m.Key.MessageID]; r != nil && p.avatar != nil && joins[i]&joinBelow == 0 {
			if id := p.senderAvatar(m); id != 0 {
				offset(sgtx, r.avatarPoint, func(gtx layout.Context) layout.Dimensions {
					return p.avatar(gtx, id, model.KindUser, p.avatarName(m), 34)
				})
			}
		}
		rows = append(rows, row{macro.Stop(), dims.Size.Y})
		height += dims.Size.Y
	}
	if height > snapshotMaxHeight {
		return nil, image.Point{}, false
	}
	size = image.Pt(width, height)
	sgtx.Constraints = layout.Exact(size)
	fillRect(sgtx, scheme(sgtx).SurfaceContainerLow, size)
	if p.appearance != nil && opts.background {
		p.appearance.Background(sgtx)
	}
	y := pad
	for _, r := range rows {
		stack := op.Offset(image.Pt(0, y)).Push(ops)
		r.call.Add(ops)
		stack.Pop()
		y += r.height
	}
	return ops, size, true
}
