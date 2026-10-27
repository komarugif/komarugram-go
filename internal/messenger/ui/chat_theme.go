// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"math"
	"reflect"
	"time"
	"unsafe"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// themeResult is a chat's look, worked out off the frame.
type themeResult struct {
	generation uint64
	appearance model.ChatAppearance
	style      *model.ChatThemeStyle
	// background is nil when the wallpaper is the one drawn already, key.
	background *chattheme.Background
	key        string
	image      *image.RGBA
	size       image.Point
	err        error
}

// renderResult is the wallpaper rendered at the size the history took.
type renderResult struct {
	generation uint64
	image      *image.RGBA
	size       image.Point
}

type themeChoicesResult struct {
	themes []model.ChatTheme
	err    error
}

// chatThemeController is how the chats of a window look: the chat's own
// theme, else the look the settings give every chat, and the wallpaper
// behind the history, rendered at its size off the frame.
type chatThemeController struct {
	source     model.ChatThemeSource
	media      model.ConversationStore
	images     *imageOps
	invalidate func()
	// look is the look of every chat in a theme of the application, and
	// loadWallpaper reads a picture it names; without them the chats have
	// the application's colors.
	look          func(dark bool) preferences.ChatMode
	loadWallpaper func(name string) ([]byte, error)

	chat     int64
	dark     bool
	revision uint64
	mode     preferences.ChatMode
	// appearance is the chat's own theme and wallpaper; style, what its
	// messages are drawn in, nil for the application's colors.
	appearance model.ChatAppearance
	style      *model.ChatThemeStyle
	// background is the wallpaper, key what it was made of, and image it
	// rendered at size.
	background *chattheme.Background
	key        string
	image      *image.RGBA
	size       image.Point
	// want is the size the history took last.
	want image.Point
	// generation tells the results of a load from the ones of loads
	// before it.
	generation uint64
	result     chan themeResult
	cancel     context.CancelFunc
	loading    bool
	rendering  chan renderResult
	problem    error
	// told is the problem handed to be told last: see newProblem.
	told error
	// selected is the theme previewed on the chat's page; saving, while it
	// is applied.
	selected *model.ChatTheme
	saving   bool
	// local keeps the themes set only here for a store that cannot.
	local map[int64]model.ChatTheme
	// bubble is the gradient of outgoing bubbles at phase.
	bubble *image.RGBA
	phase  int64
	// contexts are the themed values of the contexts of the frame drawn
	// at contextsAt, made once for each map of values rather than for each
	// message; the first used of them, filled again in the next frame rather
	// than made anew. A map of another frame may have the address of one
	// gone.
	contexts   []*themedValues
	used       int
	contextsAt time.Time
	page       chatThemePage
}

// themedValues are the values of a context with its theme's scheme
// changed, and the theme they hold.
type themedValues struct {
	key    contextKey
	values map[string]any
	theme  token.Theme
	scheme token.Scheme
}

type contextKey struct {
	values unsafe.Pointer
	kind   int
}

const (
	historyValues = iota
	incomingValues
	outgoingValues
)

func newChatThemeController(source model.ConversationStore, images *imageOps, invalidate func()) *chatThemeController {
	t := &chatThemeController{media: source, images: images, invalidate: invalidate, local: map[int64]model.ChatTheme{}}
	t.source, _ = source.(model.ChatThemeSource)
	t.page.init(t)
	return t
}

func (t *chatThemeController) Close() {
	if t.cancel != nil {
		t.cancel()
	}
	t.page.close()
}

// Release drops the wallpaper of a hidden window; it is made again when
// the window shows.
func (t *chatThemeController) Release() {
	t.image, t.background, t.key = nil, nil, ""
	t.bubble = nil
	t.contexts, t.used = nil, 0
	t.reload()
	t.page.release()
}

// modeFor is the settings' look of the chats in the application's theme.
func (t *chatThemeController) modeFor(dark bool) preferences.ChatMode {
	if t.look == nil {
		return preferences.ChatMode{}
	}
	return t.look(dark)
}

// Update takes the looks worked out and starts working one out when the
// chat, the theme or the settings changed.
func (t *chatThemeController) Update(chat int64, dark bool) {
	revision := uint64(0)
	if source, ok := t.media.(interface{ ChatThemeRevision(int64) uint64 }); ok {
		revision = source.ChatThemeRevision(chat)
	}
	mode := t.modeFor(dark)
	if chat != t.chat || dark != t.dark {
		t.chat, t.dark = chat, dark
		t.selected = nil
		t.revision, t.mode = revision, mode
		t.reload()
	} else if t.selected == nil && (revision != t.revision || !sameChatMode(mode, t.mode)) {
		t.revision, t.mode = revision, mode
		t.reload()
	}
	t.drain()
}

func sameChatMode(a, b preferences.ChatMode) bool {
	return reflect.DeepEqual(a, b)
}

func (t *chatThemeController) drain() {
	if t.result != nil {
		select {
		case r := <-t.result:
			t.result = nil
			t.loading = false
			if r.generation != t.generation {
				break
			}
			if r.err == nil && t.saving {
				t.selected = nil
			}
			t.saving = false
			t.problem = r.err
			t.appearance, t.style = r.appearance, r.style
			t.bubble = nil
			t.contexts, t.used = nil, 0
			if r.key != t.key {
				t.key, t.background = r.key, r.background
				t.image, t.size = r.image, r.size
			}
		default:
		}
	}
	if t.rendering != nil {
		select {
		case r := <-t.rendering:
			t.rendering = nil
			if r.generation == t.generation {
				t.image, t.size = r.image, r.size
			}
		default:
		}
	}
}

// reload works the chat's look out again.
func (t *chatThemeController) reload() {
	chat := t.chat
	if a, ok := t.local[chat]; ok && t.selected == nil {
		t.job(func(context.Context) (model.ChatAppearance, error) {
			return model.ChatAppearance{Theme: a, Local: true}, nil
		})
		return
	}
	if t.selected != nil {
		theme := *t.selected
		t.job(func(context.Context) (model.ChatAppearance, error) { return model.ChatAppearance{Theme: theme}, nil })
		return
	}
	if t.source == nil {
		t.job(func(context.Context) (model.ChatAppearance, error) { return model.ChatAppearance{}, nil })
		return
	}
	t.job(func(ctx context.Context) (model.ChatAppearance, error) { return t.source.ChatAppearance(ctx, chat) })
}

// job works out a look from the appearance fn gives: the chat's theme or
// the settings', and the wallpaper, rendered at the size last drawn.
func (t *chatThemeController) job(fn func(context.Context) (model.ChatAppearance, error)) {
	if t.cancel != nil {
		t.cancel()
	}
	t.generation++
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.cancel = cancel
	ch := make(chan themeResult, 1)
	t.result = ch
	t.loading = true
	t.problem = nil
	generation, dark, mode, key, size := t.generation, t.dark, t.mode, t.key, t.want
	if size == (image.Point{}) {
		size = image.Pt(720, 900)
	}
	load := t.loadWallpaper
	go func() {
		defer cancel()
		defer crash.Recover("chat theme", func(e *crash.Panic) {
			select {
			case ch <- themeResult{generation: generation, err: e}:
				t.invalidate()
			default:
			}
		})
		// A chat whose theme cannot be had is drawn as the settings say.
		a, err := fn(ctx)
		if err != nil {
			a = model.ChatAppearance{}
		}
		r := themeResult{generation: generation, appearance: a, err: err}
		var w *model.ChatWallpaper
		r.style, w = resolveLook(a, dark, mode)
		var file string
		if w == nil && mode.Wallpaper != nil {
			w, file = settingsWallpaper(mode.Wallpaper, dark)
		}
		r.key = wallpaperKey(w, file)
		if w != nil && r.key != key {
			var data []byte
			data, err = wallpaperData(ctx, t.media, w, file, load, false)
			var perr error
			r.background, perr = chattheme.Prepare(w, data)
			// Keep the colors if the picture fails, but tell why.
			r.err = errors.Join(r.err, err, perr)
			r.image, r.size = r.background.Render(size), size
		}
		ch <- r
		t.invalidate()
	}()
}

// resolveLook is what a chat is drawn in: its theme's variant for dark,
// else the settings' preset, and the wallpaper the chat or its theme has;
// nil for the settings' wallpaper.
func resolveLook(a model.ChatAppearance, dark bool, mode preferences.ChatMode) (*model.ChatThemeStyle, *model.ChatWallpaper) {
	style := a.Theme.Light
	if dark || style == nil {
		if a.Theme.Dark != nil {
			style = a.Theme.Dark
		}
	}
	if style == nil && a.Theme.Light != nil {
		style = a.Theme.Light
	}
	own := style != nil
	if !own {
		style = chattheme.Style(chattheme.Preset(mode.Theme), mode.Accent)
	}
	w := a.Wallpaper
	if w == nil && own && style.Wallpaper != nil {
		w = style.Wallpaper
	}
	if w == nil && !own && mode.Wallpaper == nil && style != nil {
		w = style.Wallpaper
	}
	return style, w
}

// settingsWallpaper is the wallpaper of the settings, with the name of its
// picture.
func settingsWallpaper(p *preferences.Wallpaper, dark bool) (*model.ChatWallpaper, string) {
	return &model.ChatWallpaper{ID: p.ID, Colors: p.Colors, Rotation: p.Rotation, Intensity: p.Intensity, Blur: p.Blur, Pattern: p.Pattern, Tile: p.Tile, Dark: p.Dark || dark}, p.File
}

// wallpaperKey tells wallpapers apart, so that one drawn already is kept.
func wallpaperKey(w *model.ChatWallpaper, file string) string {
	if w == nil {
		return ""
	}
	c := *w
	sum := crc32.ChecksumIEEE(c.Image)
	c.Image = nil
	b, _ := json.Marshal(struct {
		W     model.ChatWallpaper
		Image uint32
		N     int
		File  string
	}{c, sum, len(w.Image), file})
	return string(b)
}

// LoadChoices asks for the themes offered, for the chat's page.
func (t *chatThemeController) LoadChoices() { t.page.loadChoices() }

func (t *chatThemeController) choose(theme model.ChatTheme) {
	t.selected = &theme
	t.reload()
}

// Background draws the wallpaper over the history, rendering it again off
// the frame when the history's size changed.
func (t *chatThemeController) Background(gtx layout.Context) {
	if t.background == nil {
		return
	}
	size := gtx.Constraints.Max
	t.want = size
	if size.X > 0 && size.Y > 0 && size != t.size && t.rendering == nil && !t.loading {
		t.render(size)
	}
	if t.image != nil {
		widget.Image{Src: t.images.Op(t.image), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
	}
}

func (t *chatThemeController) render(size image.Point) {
	ch := make(chan renderResult, 1)
	t.rendering = ch
	b, generation := t.background, t.generation
	go func() {
		defer crash.Recover("wallpaper", func(*crash.Panic) { ch <- renderResult{generation: generation, size: size}; t.invalidate() })
		ch <- renderResult{generation: generation, image: b.Render(size), size: size}
		t.invalidate()
	}()
}

// Bubble fills a bubble in the theme's colors, the outgoing ones in their
// gradient, which the theme may animate.
func (t *chatThemeController) Bubble(gtx layout.Context, outgoing, animate bool, shape bubbleShape) {
	if t.style == nil {
		return
	}
	size := gtx.Constraints.Min
	if !outgoing {
		paint.FillShape(gtx.Ops, token.NewMatColorFromHexRGBA(t.style.Incoming).AsNRGBA(), shape.rrect(size).Op(gtx.Ops))
		return
	}
	colors := t.style.Outgoing
	if len(colors) == 0 {
		return
	}
	if len(colors) == 1 {
		paint.FillShape(gtx.Ops, chattheme.RGB(colors[0]), shape.rrect(size).Op(gtx.Ops))
		return
	}
	phase := int64(0)
	if t.style.Animated && animate && len(colors) > 2 {
		phase = gtx.Now.UnixMilli() / 100
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(100 * time.Millisecond)})
	}
	if t.bubble == nil || t.phase != phase {
		t.phase = phase
		t.bubble = chattheme.Gradient(colors, 48, 64, 0, float64(phase)/20)
	}
	defer shape.rrect(size).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(size)
	widget.Image{Src: t.images.Op(t.bubble), Fit: widget.Fill}.Layout(gtx)
}

// themed returns gtx with its theme's scheme changed by edit, making the
// values once for each map of values of the frame.
func (t *chatThemeController) themed(gtx layout.Context, kind int, edit func(*token.Scheme)) layout.Context {
	if !gtx.Now.Equal(t.contextsAt) {
		// The values of the last frame are no longer read.
		t.used, t.contextsAt = 0, gtx.Now
	}
	key := contextKey{reflect.ValueOf(gtx.Values).UnsafePointer(), kind}
	for _, c := range t.contexts[:t.used] {
		if c.key == key {
			gtx.Values = c.values
			return gtx
		}
	}
	if t.used == len(t.contexts) {
		t.contexts = append(t.contexts, &themedValues{values: make(map[string]any, len(gtx.Values))})
	}
	c := t.contexts[t.used]
	t.used++
	c.key = key
	c.theme = *wdk.GetMaterialTheme(gtx)
	c.scheme = *c.theme.Scheme
	c.theme.Scheme = &c.scheme
	edit(&c.scheme)
	clear(c.values)
	for k, v := range gtx.Values {
		c.values[k] = v
	}
	c.values[wdk.ThemeNamespace] = &c.theme
	gtx.Values = c.values
	return gtx
}

// historyContext is gtx for what lies on the wallpaper: dates, service
// messages and the times of stickers, on plates of the wallpaper's hue
// that read on it.
func (t *chatThemeController) historyContext(gtx layout.Context) layout.Context {
	if t.background == nil {
		return gtx
	}
	dark := t.dark
	if t.style != nil {
		dark = t.style.Dark
	}
	plate := serviceColor(t.background.Average(), dark)
	return t.themed(gtx, historyValues, func(sc *token.Scheme) {
		sc.SecondaryContainer.Color = plate
		sc.SecondaryContainer.OnColor = token.NewMatColorFromHexRGB(0xffffff)
		sc.InverseSurface.Color = plate.SetOpacity(1)
		sc.InverseSurface.OnColor = token.NewMatColorFromHexRGB(0xffffff)
	})
}

// serviceColor is the plate of what lies on a wallpaper of average color:
// its hue, dark enough for white text, as Telegram's service messages.
func serviceColor(average uint32, dark bool) token.MatColor {
	c := chattheme.RGB(average)
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (hi + lo) / 2
	s := 0.
	if hi != lo {
		s = (hi - lo) / (1 - math.Abs(2*l-1))
	}
	h := 0.
	switch {
	case hi == lo:
	case hi == r:
		h = math.Mod((g-b)/(hi-lo)+6, 6)
	case hi == g:
		h = (b-r)/(hi-lo) + 2
	default:
		h = (r-g)/(hi-lo) + 4
	}
	light, alpha := .3, .55
	if dark {
		light, alpha = .18, .8
	}
	s = math.Min(s, .6)
	ch := (1 - math.Abs(2*light-1)) * s
	x := ch * (1 - math.Abs(math.Mod(h, 2)-1))
	m := light - ch/2
	var rr, gg, bb float64
	switch int(h) {
	case 0:
		rr, gg = ch, x
	case 1:
		rr, gg = x, ch
	case 2:
		gg, bb = ch, x
	case 3:
		gg, bb = x, ch
	case 4:
		rr, bb = x, ch
	default:
		rr, bb = ch, x
	}
	v := uint32((rr+m)*255+.5)<<24 | uint32((gg+m)*255+.5)<<16 | uint32((bb+m)*255+.5)<<8 | uint32(alpha*255+.5)
	return token.NewMatColorFromHexRGBA(v)
}

// incoming is the color of the theme's incoming bubbles; false for the
// application's colors.
func (t *chatThemeController) incoming() (token.MatColor, bool) {
	if t.style == nil {
		return token.MatColor{}, false
	}
	return token.NewMatColorFromHexRGBA(t.style.Incoming), true
}

// messageContext is gtx for a message's bubble: its text, time and links
// in the theme's colors.
func (t *chatThemeController) messageContext(gtx layout.Context, outgoing bool) layout.Context {
	s := t.style
	if s == nil {
		return gtx
	}
	kind := incomingValues
	if outgoing {
		kind = outgoingValues
	}
	return t.themed(gtx, kind, func(sc *token.Scheme) {
		fg, accent, date := s.Text, s.Accent, s.InDate
		if outgoing {
			fg, accent, date = s.OutText, s.OutAccent, s.OutDate
		}
		sc.Surface.OnColor = token.NewMatColorFromHexRGBA(fg)
		sc.SurfaceVariant.OnColor = sc.Surface.OnColor.SetOpacity(.65)
		if date != 0 {
			sc.SurfaceVariant.OnColor = token.NewMatColorFromHexRGBA(date)
		}
		sc.Primary.Color = token.NewMatColorFromHexRGB(accent & 0xffffff)
	})
}

// DiscardPreview goes back from a theme previewed to the chat's own.
func (t *chatThemeController) DiscardPreview() {
	if t.selected != nil && !t.saving {
		t.selected = nil
		t.reload()
	}
}

// newProblem returns the problem that came since it was last asked, for
// the page that shows the themes to tell in its toast; nil if none.
func (t *chatThemeController) newProblem() error {
	if p := t.page.problem; p != nil && p != t.told {
		t.told = p
		return p
	}
	if t.problem == t.told {
		return nil
	}
	t.told = t.problem
	return t.problem
}

// Backdrop draws the wallpaper as the history has it, for a view of the
// chat of another size, which renders nothing: a dialog over the history
// would make the two sizes render in turn.
func (t *chatThemeController) Backdrop(gtx layout.Context) {
	if t.image != nil {
		widget.Image{Src: t.images.Op(t.image), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
	}
}
