// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"net"
	"strconv"
	"sync"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/proxy"
)

const (
	proxyRowHeight = unit.Dp(60)
	proxyRowGap    = unit.Dp(2)
	proxyRadio     = unit.Dp(20)
)

// pingFunc measures the time of a connection to Telegram through p, directly
// for nil: proxy.Ping, or what a demo makes up.
type pingFunc func(ctx context.Context, p *proxy.Proxy) (time.Duration, error)

// proxyPing is what is known of the time of one connection.
type proxyPing struct {
	busy bool
	took time.Duration
	err  error
}

// proxyRow is the input of a row of the list.
type proxyRow struct {
	pick, remove surface
}

// proxyView is the list of proxies, with how long each takes to answer, and
// the field that adds another by its link. The settings show it in a card, and
// the sign-in in a dialog.
type proxyView struct {
	store      *proxy.Store
	ping       pingFunc
	invalidate func()

	rows   map[string]*proxyRow
	link   *textField
	add    surface
	check  surface
	opened bool

	// text is the link as it was last read, and what was read of it.
	text   string
	parsed proxy.Proxy
	err    error
	// problem is why the link was not added, shown until it is edited.
	problem error

	mu    sync.Mutex
	pings map[string]proxyPing
	// adding is set while an added link is read, which may ask the network
	// which side of it is the address; outcome is what it came to.
	adding  bool
	outcome *addOutcome
}

type addOutcome struct {
	proxy proxy.Proxy
	err   error
}

func newProxyView(store *proxy.Store, ping pingFunc, invalidate func()) *proxyView {
	if store == nil {
		store = proxy.NewStore("", nil)
	}
	if ping == nil {
		ping = proxy.Ping
	}
	if invalidate == nil {
		invalidate = func() {}
	}
	return &proxyView{
		store: store, ping: ping, invalidate: invalidate,
		rows: map[string]*proxyRow{}, link: newTextField(0, ""), pings: map[string]proxyPing{},
	}
}

// Open has the view measure every proxy again, as it comes up.
func (v *proxyView) Open() { v.opened = false }

// Summary is what the proxy in use is called, for where there is one line of
// room: the settings' list of sections.
func (v *proxyView) Summary(l localization.Catalog) string {
	p, ok := v.store.Active()
	if !ok {
		return l.T("settings.proxy_none")
	}
	return proxyTitle(p, l)
}

// proxyTitle is how a proxy is called in the list.
func proxyTitle(p proxy.Proxy, l localization.Catalog) string {
	if p == proxy.Telegram() {
		return l.T("proxy.websocket")
	}
	return p.String()
}

// proxyKind is the name of the protocol of a proxy.
func proxyKind(p proxy.Proxy, l localization.Catalog) string {
	return l.T("proxy.kind_" + string(p.Kind))
}

// lookup tells whether a name resolves: for links that cannot be read by their
// look, which names are an address and which a user.
func lookup(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err == nil
}

// startPing measures the proxy p, which is the direct connection for nil.
func (v *proxyView) startPing(id string, p *proxy.Proxy) {
	v.mu.Lock()
	if v.pings[id].busy {
		v.mu.Unlock()
		return
	}
	v.pings[id] = proxyPing{busy: true}
	v.mu.Unlock()
	var own *proxy.Proxy
	if p != nil {
		c := *p
		own = &c
	}
	go func() {
		defer crash.Recover("ping proxy", func(e *crash.Panic) {
			v.mu.Lock()
			v.pings[id] = proxyPing{err: e}
			v.mu.Unlock()
			v.invalidate()
		})
		took, err := v.ping(context.Background(), own)
		v.mu.Lock()
		v.pings[id] = proxyPing{took: took, err: err}
		v.mu.Unlock()
		v.invalidate()
	}()
}

// startAll measures every proxy of the list, and the two that are always
// there.
func (v *proxyView) startAll() {
	v.startPing(proxy.DirectID, nil)
	tg := proxy.Telegram()
	v.startPing(tg.ID(), &tg)
	for _, p := range v.store.List() {
		v.startPing(p.ID(), &p)
	}
}

func (v *proxyView) row(id string) *proxyRow {
	r := v.rows[id]
	if r == nil {
		r = new(proxyRow)
		v.rows[id] = r
	}
	return r
}

// Update handles what was clicked and typed.
func (v *proxyView) Update(gtx layout.Context) {
	if !v.opened {
		v.opened = true
		v.startAll()
	}
	forced := v.store.Forced()
	ids := []string{proxy.DirectID, proxy.Telegram().ID()}
	for _, p := range v.store.List() {
		ids = append(ids, p.ID())
	}
	for _, id := range ids {
		r := v.row(id)
		if r.pick.Clicked(gtx) && !forced {
			if err := v.store.Select(id); err == nil {
				v.invalidate()
			}
		}
		if r.remove.Clicked(gtx) && id != proxy.DirectID && id != proxy.Telegram().ID() {
			v.store.Remove(id)
			delete(v.rows, id)
		}
	}
	if v.check.Clicked(gtx) {
		v.startAll()
	}
	if text := v.link.Text(); text != v.text {
		v.text, v.problem = text, nil
		v.parsed, v.err = proxy.Parse(text)
	}
	submitted := v.link.Submitted(gtx)
	if v.add.Clicked(gtx) || submitted {
		v.beginAdd()
	}
	v.mu.Lock()
	outcome := v.outcome
	v.outcome = nil
	v.mu.Unlock()
	if outcome != nil {
		v.adding = false
		switch {
		case outcome.err != nil:
			v.problem = outcome.err
		case v.store.Add(outcome.proxy) != nil:
			v.problem = errors.New("proxy: not saved")
		default:
			v.link.Clear()
			v.text, v.parsed, v.err = "", proxy.Proxy{}, nil
			p := outcome.proxy
			v.startPing(p.ID(), &p)
		}
	}
}

// beginAdd starts reading the link that was typed to add it: on a goroutine,
// as it may ask the network which side of it is the address.
func (v *proxyView) beginAdd() {
	if v.text == "" || v.adding {
		return
	}
	v.adding = true
	text := v.text
	go func() {
		defer crash.Recover("add proxy", func(e *crash.Panic) {
			v.mu.Lock()
			v.outcome = &addOutcome{err: e}
			v.mu.Unlock()
			v.invalidate()
		})
		p, err := proxy.ParseLookup(text, lookup)
		v.mu.Lock()
		v.outcome = &addOutcome{p, err}
		v.mu.Unlock()
		v.invalidate()
	}()
}

// proxyErrorText says in words what is wrong with a link.
func proxyErrorText(err error, l localization.Catalog) string {
	switch {
	case errors.Is(err, proxy.ErrUnknown), errors.Is(err, proxy.ErrEmpty):
		return l.T("proxy.err_unknown")
	case errors.Is(err, proxy.ErrBadPort):
		return l.T("proxy.err_port")
	case errors.Is(err, proxy.ErrNoSecret):
		return l.T("proxy.err_secret_missing")
	case errors.Is(err, proxy.ErrBadSecret):
		return l.T("proxy.err_secret")
	case errors.Is(err, proxy.ErrNoPassword):
		return l.T("proxy.err_password")
	case errors.Is(err, proxy.ErrNoAddress), errors.Is(err, proxy.ErrBadHost):
		return l.T("proxy.err_address")
	}
	return l.T("proxy.err_unknown")
}

// detected is what a link that was read says it is, as it is typed.
func detected(l localization.Catalog, p proxy.Proxy) string {
	return fmt.Sprintf(l.T("proxy.detected"), proxyKind(p, l)+" · "+proxyTitle(p, l))
}

// layoutRow draws one proxy of the list: whether it is the one in use, what
// it is called, how long it takes, and, where the user added it, a button
// that removes it.
func (v *proxyView) layoutRow(gtx layout.Context, l localization.Catalog, id, title, hint string, selected, removable bool) layout.Dimensions {
	r := v.row(id)
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(proxyRowHeight))
	background, content := sc.Surface.OnColor.SetOpacity(0), sc.Surface.OnColor
	if selected {
		background, content = sc.SecondaryContainer.Color, sc.SecondaryContainer.OnColor
	}
	style := surfaceStyle{radius: gtx.Dp(12), background: background, content: content}
	pad := gtx.Dp(14)
	remove := gtx.Dp(40)
	right := pad
	if removable {
		right = remove + gtx.Dp(8)
	}

	ping := v.pingText(l, id)
	hintColor := content.SetOpacity(0.7)
	if ping.state == 3 {
		// Why it does not answer is under its name, where there is room for
		// the words; the time at the end is for a time.
		hintColor = sc.Error.Color
		if removable {
			hint += " · " + ping.text
		} else {
			hint = ping.text
		}
		ping = pingShown{}
	}
	r.pick.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		// The marker of the one in use.
		d := gtx.Dp(proxyRadio)
		radio := image.Rectangle{Min: image.Pt(pad, (size.Y-d)/2)}
		radio.Max = radio.Min.Add(image.Pt(d, d))
		ring := sc.SurfaceVariant.OnColor
		if selected {
			ring = sc.Primary.Color
		}
		line := gtx.Dp(2)
		paint.FillShape(gtx.Ops, ring.AsNRGBA(), clip.Stroke{
			Path:  clip.Ellipse{Min: radio.Min.Add(image.Pt(line/2, line/2)), Max: radio.Max.Sub(image.Pt(line/2, line/2))}.Path(gtx.Ops),
			Width: float32(line),
		}.Op())
		if selected {
			dot := d / 2
			at := radio.Min.Add(image.Pt((d-dot)/2, (d-dot)/2))
			paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse(image.Rectangle{Min: at, Max: at.Add(image.Pt(dot, dot))}).Op(gtx.Ops))
		}

		// How long it takes, at the end, keeping its width.
		macro := op.Record(gtx.Ops)
		pg := gtx
		pg.Constraints = layout.Constraints{Max: image.Pt(size.X/3, size.Y)}
		pingDims := label(pg, ping.text, token.TypestyleLabelLarge, ping.color(sc), 1)
		pingCall := macro.Stop()
		offset(gtx, image.Pt(size.X-right-pingDims.Size.X, (size.Y-pingDims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			pingCall.Add(gtx.Ops)
			return pingDims
		})

		// The name and what it is, in the room that is left.
		left := pad + d + gtx.Dp(14)
		textGtx := gtx
		textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-left-right-pingDims.Size.X-gtx.Dp(8), 0), size.Y)}
		macro = op.Record(gtx.Ops)
		dims := layout.Flex{Axis: layout.Vertical}.Layout(textGtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleBodyLarge, content, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, hint, token.TypestyleBodySmall, hintColor, 1)
			}),
		)
		call := macro.Stop()
		offset(gtx, image.Pt(left, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return layout.Dimensions{Size: size}
	})
	if removable {
		// Over the row, whose click would take it.
		offset(gtx, image.Pt(size.X-remove-gtx.Dp(8), (size.Y-remove)/2), func(gtx layout.Context) layout.Dimensions {
			box := image.Pt(remove, remove)
			gtx.Constraints = layout.Exact(box)
			rs := surfaceStyle{radius: remove / 2, background: content.SetOpacity(0), content: content, button: l.T("proxy.remove")}
			return r.remove.Layout(gtx, box, rs, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(20), gtx.Dp(20)))
					return iconDelete(gtx, content.SetOpacity(0.8))
				})
			})
		})
	}
	return layout.Dimensions{Size: size}
}

// pingShown is how a measured time is drawn.
type pingShown struct {
	text string
	// state is 0 for what is not known, 1 for a time that is good, 2 for one
	// that is slow, 3 for a failure.
	state int
}

func (p pingShown) color(sc *token.Scheme) token.MatColor {
	switch p.state {
	case 1:
		return sc.Primary.Color
	case 3:
		return sc.Error.Color
	}
	return sc.SurfaceVariant.OnColor
}

// Times past these are told as slow.
const proxySlow = 700 * time.Millisecond

// pingText says how long the proxy took, or why it did not answer.
func (v *proxyView) pingText(l localization.Catalog, id string) pingShown {
	v.mu.Lock()
	p, ok := v.pings[id]
	v.mu.Unlock()
	switch {
	case !ok:
		return pingShown{}
	case p.busy:
		return pingShown{text: l.T("proxy.checking")}
	case p.err != nil:
		key := "proxy.fail_other"
		switch proxy.Why(p.err) {
		case proxy.FailTimeout:
			key = "proxy.fail_timeout"
		case proxy.FailAuth:
			key = "proxy.fail_auth"
		case proxy.FailRefused:
			key = "proxy.fail_refused"
		}
		return pingShown{text: l.T(key), state: 3}
	}
	state := 1
	if p.took > proxySlow {
		state = 2
	}
	return pingShown{text: strconv.Itoa(int(p.took.Round(time.Millisecond)/time.Millisecond)) + " " + l.T("proxy.ms"), state: state}
}

// Layout draws all of it, for a page that scrolls as a whole: the field that
// adds a proxy, the list, and the button that measures them.
func (v *proxyView) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.LayoutForm(gtx, l) }),
		vspace(16),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.LayoutList(gtx, l) }),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.LayoutCheck(gtx, l) }),
	)
}

// LayoutForm draws the field that adds a proxy by its link, with what was
// made of the link below it, and the button that adds it.
func (v *proxyView) LayoutForm(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.link.Layout(gtx, l.T("proxy.link"), v.problem != nil)
		}),
		vspace(6),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text, color := l.T("proxy.link_hint"), sc.SurfaceVariant.OnColor
			switch {
			case v.problem != nil:
				text, color = proxyErrorText(v.problem, l), sc.Error.Color
			case v.text != "" && v.err == nil:
				text = detected(l, v.parsed)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 4, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, text, token.TypestyleBodySmall, color, 2)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return tonalButton(gtx, &v.add, l.T("proxy.add"), 0)
				}),
			)
		}),
	)
}

// LayoutList draws the proxies, the ones that are always there first, and
// what is to be known of changing them below.
func (v *proxyView) LayoutList(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	active := v.store.ActiveID()
	forced := v.store.Forced()
	type entry struct {
		id, title, hint string
		removable       bool
	}
	tg := proxy.Telegram()
	entries := []entry{
		{id: proxy.DirectID, title: l.T("proxy.direct"), hint: l.T("proxy.direct_hint")},
		{id: tg.ID(), title: l.T("proxy.websocket"), hint: l.T("proxy.websocket_hint")},
	}
	for _, p := range v.store.List() {
		entries = append(entries, entry{id: p.ID(), title: proxyTitle(p, l), hint: proxyKind(p, l), removable: true})
	}
	var rows []layout.FlexChild
	if forced {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("proxy.forced"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			})
		}))
	}
	for _, e := range entries {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: proxyRowGap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return v.layoutRow(gtx, l, e.id, e.title, e.hint, e.id == active, e.removable)
			})
		}))
	}
	rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("proxy.note"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// LayoutCheck draws the button that measures every proxy again.
func (v *proxyView) LayoutCheck(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	return textButton(gtx, &v.check, l.T("proxy.check"))
}

// usesTelegramWebSocket reports whether Telegram is reached over the WebSocket
// of its own web clients now. A nil view, which a window without proxies has,
// is not.
func (v *proxyView) usesTelegramWebSocket() bool {
	if v == nil {
		return false
	}
	p, ok := v.store.Active()
	return ok && p == proxy.Telegram()
}
