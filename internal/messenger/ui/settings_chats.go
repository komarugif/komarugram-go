// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"io"
	"os"
	"strconv"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"
	"gio-mw/widget/checkbox"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconChats     = wdk.RequireIconWidget(icons.CommunicationChat)
	iconWallpaper = wdk.RequireIconWidget(icons.DeviceWallpaper)
	iconDone      = wdk.RequireIconWidget(icons.ActionDone)
)

// imageFiles are the pictures a wallpaper can be made of.
var imageFiles = fileFilter{"Images", []string{".jpg", ".jpeg", ".png", ".webp"}}

// chatsSettings is the theme and the wallpaper of every chat, as the top of
// Telegram Desktop's Chat Settings: Classic, Day, Tinted and Night, with an
// accent, beside the application's own colors, and a wallpaper from
// Telegram's gallery or a file. There is a look for the light theme and
// one for the dark, as Telegram Desktop keeps a theme for the day and one
// for the night.
type chatsSettings struct {
	themesHeight, wallpaperHeight heightTransition
	chats                         func() preferences.ChatLook
	setChats                      func(preferences.ChatLook) error
	// dark reports whether the application is dark; setDark makes it so,
	// for a theme of the other kind.
	dark    func() bool
	setDark func(bool)
	store   *preferences.Store
	// wallpapers are Telegram's; media fetches their pictures.
	wallpapers model.WallpaperSource
	media      model.ConversationStore
	images     *imageOps
	thumbs     *wallpaperThumbs
	invalidate func()
	// toast tells what went wrong on the page.
	toast *toast

	presets                             [5]surface
	accents                             []surface
	fromGallery, fromFile, useThemeWall surface
	gallery                             wallpaperGallery
}

func newChatsSettings(invalidate func()) *chatsSettings {
	s := &chatsSettings{invalidate: invalidate, accents: make([]surface, 8)}
	s.gallery.init(s)
	return s
}

// available reports whether the section can be shown.
func (s *chatsSettings) available() bool { return s != nil && s.chats != nil && s.setChats != nil }

// mode is the look of the chats in the application's theme now.
func (s *chatsSettings) mode() preferences.ChatMode {
	if s.dark() {
		return s.chats().Night
	}
	return s.chats().Day
}

// change changes the look of the chats in the theme of dark.
func (s *chatsSettings) change(dark bool, edit func(*preferences.ChatMode)) {
	look := s.chats()
	m := &look.Day
	if dark {
		m = &look.Night
	}
	edit(m)
	if err := s.setChats(look); err != nil {
		s.tell(err)
	}
}

func (s *chatsSettings) tell(err error) {
	if s.toast != nil {
		s.toast.Show(mediaErrorText(err))
	}
}

func (s *chatsSettings) Update(gtx layout.Context) {
	if !s.available() {
		return
	}
	s.gallery.drain()
	dark := s.dark()
	for i, p := range chattheme.Presets {
		if !s.presets[i].Clicked(gtx) {
			continue
		}
		// A theme of the other kind is the one of the other theme of the
		// application, which it switches to, as Telegram Desktop's Night.
		target := dark
		if p != chattheme.PresetApp {
			target = p.Dark()
		}
		s.change(target, func(m *preferences.ChatMode) {
			if m.Theme != string(p) {
				m.Theme, m.Accent = string(p), 0
			}
		})
		if target != dark && s.setDark != nil {
			s.setDark(target)
		}
	}
	preset := chattheme.Preset(s.mode().Theme)
	for i, c := range chattheme.Accents(preset) {
		if i < len(s.accents) && s.accents[i].Clicked(gtx) {
			s.change(dark, func(m *preferences.ChatMode) {
				m.Accent = 0
				if i > 0 {
					m.Accent = 0xff000000 | c
				}
			})
		}
	}
	if s.fromGallery.Clicked(gtx) {
		s.gallery.open()
	}
	if s.fromFile.Clicked(gtx) {
		s.gallery.chooseFile()
	}
	if s.useThemeWall.Clicked(gtx) {
		s.change(dark, func(m *preferences.ChatMode) { m.Wallpaper = nil })
	}
}

// presetTitle is what a preset is called.
func presetTitle(p chattheme.Preset, l localization.Catalog) string {
	if p == chattheme.PresetApp {
		return l.T("chats.theme_app")
	}
	return l.T("chats.theme_" + string(p))
}

// chatsSubtitle is the subtitle of the section: the theme of the chats now.
func (s *chatsSettings) subtitle(l localization.Catalog) string {
	if !s.available() {
		return ""
	}
	return presetTitle(chattheme.Preset(s.mode().Theme), l)
}

// Layout draws the cards of the themes and of the wallpaper.
func (s *chatsSettings) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	s.thumbs.Frame()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.themesHeight.Card(gtx, func(gtx layout.Context) layout.Dimensions { return s.layoutThemes(gtx, l) }, defaultCardPadding)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.wallpaperHeight.Card(gtx, func(gtx layout.Context) layout.Dimensions { return s.layoutWallpaper(gtx, l) }, defaultCardPadding)
		}),
	)
}

const (
	presetCardWidth  = unit.Dp(96)
	presetCardHeight = unit.Dp(68)
	accentSize       = unit.Dp(30)
)

// styleOf is the style a preset gives with the accent of mode, when mode
// is its own.
func styleOf(p chattheme.Preset, mode preferences.ChatMode) *model.ChatThemeStyle {
	accent := uint32(0)
	if chattheme.Preset(mode.Theme) == p {
		accent = mode.Accent
	}
	return chattheme.Style(p, accent)
}

func (s *chatsSettings) layoutThemes(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	mode := s.mode()
	chosen := chattheme.Preset(mode.Theme)
	// The cards share the width of a row, as Telegram Desktop's do.
	gap := gtx.Dp(10)
	n := len(chattheme.Presets)
	w := max(gtx.Dp(64), min(gtx.Dp(presetCardWidth), (gtx.Constraints.Max.X-(n-1)*gap)/n))
	h := w * gtx.Dp(presetCardHeight) / gtx.Dp(presetCardWidth)
	titleH := gtx.Dp(38)
	grid := func(gtx layout.Context) layout.Dimensions {
		cols := max(1, (gtx.Constraints.Max.X+gap)/(w+gap))
		for i, p := range chattheme.Presets {
			at := image.Pt(i%cols*(w+gap), i/cols*(h+titleH+gap))
			offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(w, h)
				style := styleOf(p, mode)
				var im *image.RGBA
				if style != nil && style.Wallpaper != nil {
					im, _ = s.thumbs.Get("preset/"+string(p), style.Wallpaper, size, nil)
				}
				radius := gtx.Dp(12)
				themePreview(gtx, s.images, im, style, size, radius)
				outline(gtx, size, radius, p == chosen)
				s.presets[i].Layout(gtx, image.Pt(w, h+titleH), surfaceStyle{radius: radius, content: sc.Surface.OnColor}, nil)
				return offset(gtx, image.Pt(0, h+gtx.Dp(6)), func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(w, titleH-gtx.Dp(6)))
					col := sc.SurfaceVariant.OnColor
					if p == chosen {
						col = sc.Primary.Color
					}
					return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return centeredLabel(gtx, presetTitle(p, l), token.TypestyleLabelMedium, col, 2)
					})
				})
			})
		}
		rows := (n + cols - 1) / cols
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rows*(h+titleH+gap)-gap)}
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("chats.themes"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(12),
		layout.Rigid(grid),
	}
	if accents := chattheme.Accents(chosen); len(accents) > 0 {
		children = append(children, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.layoutAccents(gtx, accents, mode, l)
		}))
	}
	children = append(children, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("chats.themes_hint"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutAccents draws the accents of the theme as colored circles, the
// one chosen ringed.
func (s *chatsSettings) layoutAccents(gtx layout.Context, accents []uint32, mode preferences.ChatMode, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	d, gap := gtx.Dp(accentSize), gtx.Dp(12)
	cols := max(1, (gtx.Constraints.Max.X+gap)/(d+gap))
	chosen := 0
	for i, c := range accents {
		if mode.Accent != 0 && mode.Accent&0xffffff == c {
			chosen = i
		}
	}
	for i, c := range accents {
		if i >= len(s.accents) {
			break
		}
		at := image.Pt(i%cols*(d+gap), i/cols*(d+gap))
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(d, d)
			inset := 0
			if i == chosen {
				ring := clip.Ellipse{Max: size}.Path(gtx.Ops)
				paint.FillShape(gtx.Ops, chattheme.RGB(c), clip.Stroke{Path: ring, Width: float32(gtx.Dp(2))}.Op())
				inset = gtx.Dp(5)
			}
			paint.FillShape(gtx.Ops, chattheme.RGB(c), clip.Ellipse{Min: image.Pt(inset, inset), Max: size.Sub(image.Pt(inset, inset))}.Op(gtx.Ops))
			return s.accents[i].Layout(gtx, size, surfaceStyle{radius: d / 2, content: sc.Surface.OnColor, button: l.T("chats.accent")}, nil)
		})
	}
	rows := (len(accents) + cols - 1) / cols
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rows*(d+gap)-gap)}
}

// wallpaperNow is the wallpaper of the chats now, with the key of its
// thumbnail, and how to read the picture of one of the settings.
func (s *chatsSettings) wallpaperNow() (*model.ChatWallpaper, string, func(context.Context) ([]byte, error)) {
	mode := s.mode()
	if w := mode.Wallpaper; w != nil {
		cw, file := settingsWallpaper(w, s.dark())
		key := wallpaperKey(cw, file)
		if file == "" {
			return cw, key, nil
		}
		store := s.store
		return cw, key, func(context.Context) ([]byte, error) { return store.LoadWallpaper(file) }
	}
	if style := chattheme.Style(chattheme.Preset(mode.Theme), mode.Accent); style != nil && style.Wallpaper != nil {
		return style.Wallpaper, "preset/" + mode.Theme, nil
	}
	return nil, "", nil
}

func (s *chatsSettings) layoutWallpaper(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	custom := s.mode().Wallpaper != nil
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("chats.wallpaper"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := image.Pt(gtx.Dp(76), gtx.Dp(76))
					radius := gtx.Dp(12)
					defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
					fillRect(gtx, sc.SurfaceContainerLow, size)
					if w, key, load := s.wallpaperNow(); w != nil {
						if im, _ := s.thumbs.Get(key, w, size, load); im != nil {
							drawCover(gtx, s.images, im, size)
						}
					}
					return layout.Dimensions{Size: size}
				}),
				layout.Rigid(layout.Spacer{Width: 16}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					rows := []layout.FlexChild{
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &s.fromGallery, l.T("chats.from_gallery"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &s.fromFile, l.T("chats.from_file"))
						}),
					}
					if custom {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &s.useThemeWall, l.T("chats.wallpaper_reset"))
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				}),
			)
		}),
	)
}

// layoutDialog draws the gallery over the window.
func (s *chatsSettings) layoutDialog(gtx layout.Context, l localization.Catalog) {
	if s == nil || !s.available() {
		return
	}
	s.gallery.Layout(gtx, l)
}

// wallpaperGallery is Telegram Desktop's Choose a Wallpaper: the gallery
// of Telegram's wallpapers and a file, then the wallpaper chosen over a
// chat, to apply it.
type wallpaperGallery struct {
	s       *chatsSettings
	dialog  modal
	list    scroll.List
	loader  loadingIndicator
	result  chan galleryResult
	papers  []model.ChatWallpaper
	tiles   []surface
	file    surface
	close   surface
	back    surface
	problem error
	// previewing shows chosen over a chat; data is its picture when it
	// came from a file, key its thumbnails' key.
	previewing bool
	chosen     model.ChatWallpaper
	data       []byte
	key        string
	blur       *checkbox.Checkboxes[string]
	cancel     *button.Button
	apply      *button.Button
	applying   chan error
	files      chan fileWallpaper
}

type galleryResult struct {
	papers []model.ChatWallpaper
	err    error
}

type fileWallpaper struct {
	data []byte
	err  error
}

func (g *wallpaperGallery) init(s *chatsSettings) {
	g.s = s
	g.list.Axis = layout.Vertical
	g.cancel = button.Text()
	g.apply = button.Filled()
	g.blur = checkbox.NewCheckboxes([]string{"blur"}, nil, func(values []string) {
		g.chosen.Blur = len(values) == 1
	})
	g.dialog.back = func() bool {
		if g.previewing && len(g.data) == 0 {
			g.previewing = false
			return true
		}
		return false
	}
}

// open shows the gallery, asking Telegram for it.
func (g *wallpaperGallery) open() {
	g.previewing = false
	g.data = nil
	g.dialog.Open()
	g.list.Position = layout.Position{}
	if g.result != nil || g.s.wallpapers == nil {
		return
	}
	ch := make(chan galleryResult, 1)
	g.result = ch
	source, invalidate := g.s.wallpapers, g.s.invalidate
	go func() {
		defer crash.Recover("wallpapers", func(e *crash.Panic) { ch <- galleryResult{err: e}; invalidate() })
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		papers, err := source.ChatWallpapers(ctx)
		ch <- galleryResult{papers, err}
		invalidate()
	}()
}

// chooseFile asks for a picture in the system's chooser, to preview it.
func (g *wallpaperGallery) chooseFile() {
	if g.files != nil {
		return
	}
	ch := make(chan fileWallpaper, 1)
	g.files = ch
	invalidate := g.s.invalidate
	go func() {
		defer crash.Recover("wallpaper file", func(e *crash.Panic) { ch <- fileWallpaper{err: e}; invalidate() })
		ch <- readWallpaperFile(context.Background())
		invalidate()
	}()
}

func readWallpaperFile(ctx context.Context) fileWallpaper {
	choice := chooseFile(ctx, &imageFiles)
	if choice.err != nil || choice.path == "" {
		return fileWallpaper{err: choice.err}
	}
	f, err := os.Open(choice.path)
	if err != nil {
		return fileWallpaper{err: err}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, chattheme.MaxBytes+1))
	if err != nil {
		return fileWallpaper{err: err}
	}
	if _, err := chattheme.Decode(data); err != nil {
		return fileWallpaper{err: err}
	}
	return fileWallpaper{data: data}
}

func (g *wallpaperGallery) drain() {
	if g.result != nil {
		select {
		case r := <-g.result:
			g.result = nil
			g.problem = r.err
			if r.err == nil {
				g.papers = r.papers
				g.tiles = make([]surface, len(g.papers))
			} else if g.dialog.Shown() {
				g.dialog.Toast(mediaErrorText(r.err))
			} else {
				g.s.tell(r.err)
			}
		default:
		}
	}
	if g.files != nil {
		select {
		case r := <-g.files:
			g.files = nil
			switch {
			case r.err != nil && g.dialog.Shown():
				g.dialog.Toast(mediaErrorText(r.err))
			case r.err != nil:
				g.s.tell(r.err)
			case len(r.data) > 0:
				g.preview(model.ChatWallpaper{}, r.data)
				if !g.dialog.Shown() {
					g.dialog.Open()
				}
			}
		default:
		}
	}
	if g.applying != nil {
		select {
		case err := <-g.applying:
			g.applying = nil
			if err != nil {
				g.dialog.Toast(mediaErrorText(err))
			} else {
				g.dialog.Close()
			}
		default:
		}
	}
}

// preview shows w over a chat; data is the picture of a file.
func (g *wallpaperGallery) preview(w model.ChatWallpaper, data []byte) {
	g.previewing = true
	g.chosen = w
	g.data = data
	g.key = wallpaperKey(&w, "")
	if len(data) > 0 {
		g.key = "file/" + wallpaperKey(&model.ChatWallpaper{Image: data}, "")
	}
	g.blur.SetValues(nil)
	if w.Blur {
		g.blur.SetValues([]string{"blur"})
	}
}

// photo reports whether the wallpaper chosen is a picture, which can be
// blurred.
func (g *wallpaperGallery) photo() bool {
	return len(g.data) > 0 || g.chosen.Media != nil && !g.chosen.Pattern
}

// applyChosen keeps the wallpaper chosen for the chats of the theme now.
func (g *wallpaperGallery) applyChosen() {
	s := g.s
	w, data := g.chosen, g.data
	dark := s.dark()
	ch := make(chan error, 1)
	g.applying = ch
	media, invalidate := s.media, s.invalidate
	go func() {
		defer crash.Recover("wallpaper", func(e *crash.Panic) { ch <- e; invalidate() })
		defer invalidate()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if len(data) == 0 && (len(w.Image) > 0 || w.Media != nil) {
			var err error
			if data, err = wallpaperData(ctx, media, &w, "", nil, false); err != nil {
				ch <- err
				return
			}
		}
		saved := &preferences.Wallpaper{ID: w.ID, Colors: w.Colors, Rotation: w.Rotation, Intensity: w.Intensity, Blur: w.Blur, Pattern: w.Pattern, Tile: w.Tile, Dark: w.Dark}
		if len(data) > 0 {
			name, err := s.store.SaveWallpaper(data)
			if err != nil {
				ch <- err
				return
			}
			saved.File = name
		}
		look := s.chats()
		if dark {
			look.Night.Wallpaper = saved
		} else {
			look.Day.Wallpaper = saved
		}
		ch <- s.setChats(look)
	}()
}

func (g *wallpaperGallery) update(gtx layout.Context) {
	if g.close.Clicked(gtx) {
		g.dialog.Close()
	}
	if g.back.Clicked(gtx) {
		g.previewing = false
	}
	if g.file.Clicked(gtx) {
		g.chooseFile()
	}
	for i := range g.tiles {
		if g.tiles[i].Clicked(gtx) {
			g.preview(g.papers[i], nil)
		}
	}
	if g.previewing {
		g.blur.Update(gtx)
		if g.cancel.Clicked(gtx) {
			if len(g.data) > 0 {
				g.dialog.Close()
			}
			g.previewing = false
		}
		if g.apply.Clicked(gtx) && g.applying == nil {
			g.applyChosen()
		}
	}
}

func (g *wallpaperGallery) Layout(gtx layout.Context, l localization.Catalog) {
	if !g.dialog.Shown() {
		return
	}
	g.drain()
	g.update(gtx)
	g.dialog.Layout(gtx, g.applying != nil, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(min(gtx.Constraints.Max.X, gtx.Dp(460)), min(gtx.Constraints.Max.Y, gtx.Dp(680)))
		gtx.Constraints = layout.Exact(size)
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(16)).Push(gtx.Ops).Pop()
		fillRect(gtx, scheme(gtx).Surface.Color, size)
		if g.previewing {
			return g.layoutPreview(gtx, l)
		}
		return g.layoutGrid(gtx, l)
	})
}

// header draws a dialog's title with a button at its start and the close
// button at its end.
func (g *wallpaperGallery) header(gtx layout.Context, title string, back bool, l localization.Catalog) layout.Dimensions {
	return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !back {
					return layout.Dimensions{}
				}
				return navigationButton(gtx, &g.back, iconBack, l.T("settings.back"))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleTitleLarge, scheme(gtx).Surface.OnColor, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return navigationButton(gtx, &g.close, iconClear, l.T("viewer.close"))
			}),
		)
	})
}

func (g *wallpaperGallery) layoutGrid(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	s := g.s
	s.thumbs.Frame()
	sc := scheme(gtx)
	current := s.mode().Wallpaper
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.header(gtx, l.T("chats.gallery"), false, l)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return actionRow(gtx, &g.file, iconAttachPhoto, l.T("chats.from_file"))
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if g.result != nil && len(g.papers) == 0 {
				return g.loader.page(gtx, l)
			}
			gap := gtx.Dp(6)
			inner := gtx.Constraints.Max.X - gtx.Dp(32)
			w := (inner - 2*gap) / 3
			h := w * 3 / 2
			rows := (len(g.papers) + 2) / 3
			return g.list.Layout(gtx, rows, func(gtx layout.Context, row int) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 16, Bottom: unit.Dp(float32(gap) / gtx.Metric.PxPerDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					for c := 0; c < 3; c++ {
						i := row*3 + c
						if i >= len(g.papers) {
							break
						}
						paper := &g.papers[i]
						offset(gtx, image.Pt(c*(w+gap), 0), func(gtx layout.Context) layout.Dimensions {
							size := image.Pt(w, h)
							defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Push(gtx.Ops).Pop()
							fillRect(gtx, sc.SurfaceContainerHigh, size)
							if im, _ := s.thumbs.Get("", paper, size, nil); im != nil {
								drawCover(gtx, s.images, im, size)
							}
							if current != nil && current.ID != 0 && current.ID == paper.ID {
								check(gtx, size)
							}
							return g.tiles[i].Layout(gtx, size, surfaceStyle{radius: gtx.Dp(8), content: sc.Surface.OnColor, button: l.T("chats.wallpaper")}, nil)
						})
					}
					return layout.Dimensions{Size: image.Pt(inner, h)}
				})
			})
		}),
	)
}

// check marks the tile of size chosen, at its corner.
func check(gtx layout.Context, size image.Point) {
	sc := scheme(gtx)
	d := gtx.Dp(24)
	at := size.Sub(image.Pt(d+gtx.Dp(8), d+gtx.Dp(8)))
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, sc.Surface.Color.AsNRGBA(), clip.Ellipse{Min: image.Pt(-2, -2), Max: image.Pt(d+2, d+2)}.Op(gtx.Ops))
		paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
		return exact(gtx, image.Pt(d, d), func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(3).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return iconDone(gtx, sc.Primary.OnColor)
			})
		})
	})
}

func (g *wallpaperGallery) layoutPreview(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	s := g.s
	s.thumbs.Frame()
	mode := s.mode()
	style := chattheme.Style(chattheme.Preset(mode.Theme), mode.Accent)
	dark := s.dark()
	if style != nil {
		dark = style.Dark
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return g.header(gtx, l.T("chats.preview"), len(g.data) == 0, l)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Max
				w := g.chosen
				var load func(context.Context) ([]byte, error)
				data, media := g.data, s.media
				if len(data) > 0 {
					load = func(context.Context) ([]byte, error) { return data, nil }
				} else if w.Media != nil {
					load = func(ctx context.Context) ([]byte, error) { return wallpaperData(ctx, media, &w, "", nil, false) }
				}
				im, average := s.thumbs.Get(g.key+"/blur="+strconv.FormatBool(w.Blur), &w, size, load)
				var picture image.Image
				if im != nil {
					picture = im
				}
				themeScene(gtx, s.images, picture, style, im != nil, average, dark, size, l)
				return layout.Dimensions{Size: size}
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						if !g.photo() {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
						}
						return g.blur.Layout(gtx, map[string]string{"blur": l.T("chats.blur")})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return g.cancel.Layout(gtx, l.T("history.cancel")) }),
					layout.Rigid(layout.Spacer{Width: 8}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return g.apply.Layout(gtx, l.T("chats.apply")) }),
				)
			})
		}),
	)
}

// actionRow is a row of a dialog that does something: an icon and its
// words in the primary color, as Telegram Desktop's "Choose from file".
func actionRow(gtx layout.Context, s *surface, icon wdk.IconWidget, text string) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(48))
	return s.Layout(gtx, size, surfaceStyle{content: sc.Primary.Color}, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		return layout.Inset{Left: 24, Right: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return actionRowContent(gtx, icon, text)
			})
		})
	})
}

// actionRowContent is the icon and the words of an actionRow.
func actionRowContent(gtx layout.Context, icon wdk.IconWidget, text string) layout.Dimensions {
	sc := scheme(gtx)
	// The row centers it: West keeps the height as the least.
	gtx.Constraints.Min.Y = 0
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(gtx.Dp(24), gtx.Dp(24)), func(gtx layout.Context) layout.Dimensions {
				return icon(gtx, sc.Primary.Color)
			})
		}),
		layout.Rigid(layout.Spacer{Width: 16}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, token.TypestyleBodyLarge, sc.Primary.Color, 1)
		}),
	)
}
