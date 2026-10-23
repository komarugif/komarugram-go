// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chattheme"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// chatThemePage is the page of a chat's theme in its information dialog,
// as Telegram Desktop's choice of a chat's theme: the chat as it will look,
// the themes offered as cards, and the choice to set it for all in the
// chat or only here.
type chatThemePage struct {
	t             *chatThemeController
	choicesResult chan themeChoicesResult
	choicesCancel context.CancelFunc
	choices       []model.ChatTheme
	// none is the card of no theme, cards the ones of choices.
	none                                     surface
	cards                                    []surface
	list                                     scroll.List
	apply, local, reset, importButton, retry surface
	thumbs                                   *wallpaperThumbs
	// importing is the theme file being read.
	importing chan themeImport
	problem   error
	loader    loadingIndicator
}

type themeImport struct {
	theme model.ChatTheme
	err   error
	// chosen is false when the chooser was closed without a file.
	chosen bool
}

// themeFiles are what the importer takes: Telegram Desktop's themes and
// palettes.
var themeFiles = fileFilter{"Telegram Desktop", []string{".tdesktop-theme", ".tdesktop-palette"}}

func (p *chatThemePage) init(t *chatThemeController) {
	p.t = t
	p.list.Axis = layout.Vertical
	p.thumbs = newWallpaperThumbs(t.media, t.invalidate)
}

func (p *chatThemePage) close() {
	if p.choicesCancel != nil {
		p.choicesCancel()
	}
	p.thumbs.Close()
}

func (p *chatThemePage) release() { p.thumbs.Release() }

func (p *chatThemePage) loadChoices() {
	t := p.t
	if p.choicesResult != nil || t.source == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p.choicesCancel = cancel
	ch := make(chan themeChoicesResult, 1)
	p.choicesResult = ch
	go func() {
		defer cancel()
		defer crash.Recover("chat themes", func(e *crash.Panic) { ch <- themeChoicesResult{err: e}; t.invalidate() })
		themes, err := t.source.ChatThemes(ctx)
		ch <- themeChoicesResult{themes, err}
		t.invalidate()
	}()
}

func (p *chatThemePage) drain() {
	if p.choicesResult != nil {
		select {
		case r := <-p.choicesResult:
			p.choicesResult = nil
			p.problem = r.err
			if r.err == nil {
				p.choices = r.themes
				p.cards = make([]surface, len(p.choices))
			}
		default:
		}
	}
	if p.importing != nil {
		select {
		case r := <-p.importing:
			p.importing = nil
			if r.err != nil {
				p.problem = r.err
			} else if r.chosen {
				p.t.choose(r.theme)
			}
		default:
		}
	}
}

// variant is the variant of a theme for dark, or its other one.
func variant(theme model.ChatTheme, dark bool) *model.ChatThemeStyle {
	if dark && theme.Dark != nil || theme.Light == nil {
		return theme.Dark
	}
	return theme.Light
}

// shownID is the theme of the card lit: the one previewed, else the
// chat's; "" is no theme, and "-" a theme without a card.
func (p *chatThemePage) shownID() string {
	t := p.t
	theme := t.appearance.Theme
	if t.selected != nil {
		theme = *t.selected
	}
	if theme.ID == "" && (theme.Light != nil || theme.Dark != nil) {
		return "-"
	}
	return theme.ID
}

// LayoutChoices draws the page of the chat's theme.
func (t *chatThemeController) LayoutChoices(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	return t.page.Layout(gtx, l)
}

func (p *chatThemePage) update(gtx layout.Context) {
	t := p.t
	t.Update(t.chat, t.dark)
	p.drain()
	if t.saving {
		return
	}
	if p.none.Clicked(gtx) {
		t.choose(model.ChatTheme{})
	}
	for i := range p.cards {
		if p.cards[i].Clicked(gtx) {
			t.choose(p.choices[i])
		}
	}
	if p.retry.Clicked(gtx) {
		t.reload()
		p.loadChoices()
	}
	if p.importButton.Clicked(gtx) && p.importing == nil {
		ch := make(chan themeImport, 1)
		p.importing = ch
		go func() {
			defer crash.Recover("theme import", func(e *crash.Panic) { ch <- themeImport{err: e}; t.invalidate() })
			ch <- importTheme(context.Background())
			t.invalidate()
		}()
	}
	if p.reset.Clicked(gtx) && t.selected == nil && t.appearance.Local {
		p.save(nil, false)
	}
	if t.selected != nil && !t.loading {
		if p.local.Clicked(gtx) {
			theme := *t.selected
			p.save(&theme, false)
		}
		if p.apply.Clicked(gtx) && p.canApply() {
			theme := *t.selected
			p.save(&theme, true)
		}
	}
}

// canApply reports whether the theme previewed can be set for all in the
// chat: one of Telegram's, or none.
func (p *chatThemePage) canApply() bool {
	s := p.t.selected
	return p.t.source != nil && s != nil && (s.ID != "" || s.Light == nil && s.Dark == nil)
}

// save sets theme for the chat: for all in it, or only here; a nil theme
// takes the one set only here away.
func (p *chatThemePage) save(theme *model.ChatTheme, all bool) {
	t := p.t
	chat := t.chat
	t.saving = true
	if _, persistent := t.media.(model.LocalChatThemeSource); !persistent && !all {
		if theme == nil {
			delete(t.local, chat)
		} else {
			t.local[chat] = *theme
		}
	}
	t.job(func(ctx context.Context) (model.ChatAppearance, error) {
		if all {
			if err := t.source.SetChatTheme(ctx, chat, theme.ID); err != nil {
				return model.ChatAppearance{}, err
			}
		} else if source, ok := t.media.(model.LocalChatThemeSource); ok {
			if err := source.SetLocalChatTheme(ctx, chat, theme); err != nil {
				return model.ChatAppearance{}, err
			}
		} else if theme != nil {
			return model.ChatAppearance{Theme: *theme, Local: true}, nil
		}
		if t.source == nil {
			return model.ChatAppearance{}, nil
		}
		return t.source.ChatAppearance(ctx, chat)
	})
}

// importTheme asks for a Telegram Desktop theme in the system's chooser and
// reads the chat's part of it.
func importTheme(ctx context.Context) themeImport {
	choice := chooseFile(ctx, &themeFiles)
	if choice.err != nil || choice.path == "" {
		return themeImport{err: choice.err}
	}
	f, err := os.Open(choice.path)
	if err != nil {
		return themeImport{err: err}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, chattheme.MaxBytes+1))
	if err != nil {
		return themeImport{err: err}
	}
	theme, err := chattheme.ImportDesktop(data)
	theme.Title = strings.TrimSuffix(filepath.Base(choice.path), filepath.Ext(choice.path))
	return themeImport{theme: theme, err: err, chosen: true}
}

const (
	themeCardWidth  = unit.Dp(84)
	themeCardHeight = unit.Dp(108)
	themeCardGap    = unit.Dp(8)
)

func (p *chatThemePage) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	p.update(gtx)
	t := p.t
	p.thumbs.Frame()
	sc := scheme(gtx)
	if p.choicesResult != nil && len(p.choices) == 0 {
		return p.loader.page(gtx, l)
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(236))
			var average uint32
			if t.background != nil {
				average = t.background.Average()
			}
			dark := t.dark
			if t.style != nil {
				dark = t.style.Dark
			}
			var im image.Image
			if t.image != nil {
				im = t.image
			}
			themeScene(gtx, t.images, im, t.style, t.background != nil, average, dark, size, l)
			return layout.Dimensions{Size: size}
		},
		func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("chat_theme.choose"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
		},
		func(gtx layout.Context) layout.Dimensions { return p.layoutCards(gtx, l) },
		func(gtx layout.Context) layout.Dimensions { return p.layoutActions(gtx, l) },
	}
	return p.list.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		return layout.Inset{Left: 16, Right: 16, Top: 8, Bottom: 8}.Layout(gtx, rows[i])
	})
}

// layoutCards draws the card of no theme and the ones of the themes, in
// rows that fill the width.
func (p *chatThemePage) layoutCards(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	t := p.t
	w, h, gap := gtx.Dp(themeCardWidth), gtx.Dp(themeCardHeight), gtx.Dp(themeCardGap)
	cols := max(1, (gtx.Constraints.Max.X+gap)/(w+gap))
	n := len(p.choices) + 1
	shown := p.shownID()
	for i := 0; i < n; i++ {
		at := image.Pt(i%cols*(w+gap), i/cols*(h+gap))
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(w, h)
			if i == 0 {
				return p.noThemeCard(gtx, size, shown == "", l.T("chat_theme.none"))
			}
			theme := p.choices[i-1]
			return p.themeCard(gtx, &p.cards[i-1], theme, variant(theme, t.dark), size, shown == theme.ID)
		})
	}
	rows := (n + cols - 1) / cols
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rows*(h+gap)-gap)}
}

func (p *chatThemePage) themeCard(gtx layout.Context, s *surface, theme model.ChatTheme, style *model.ChatThemeStyle, size image.Point, lit bool) layout.Dimensions {
	sc := scheme(gtx)
	radius := gtx.Dp(12)
	var im *image.RGBA
	if style != nil && style.Wallpaper != nil {
		im, _ = p.thumbs.Get("", style.Wallpaper, size, nil)
	}
	themePreview(gtx, p.t.images, im, style, size, radius)
	emoji := theme.ID
	if emoji != "" {
		offset(gtx, image.Pt(0, size.Y-gtx.Dp(34)), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(size.X, gtx.Dp(30)))
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, emoji, token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
			})
		})
	}
	outline(gtx, size, radius, lit)
	return s.Layout(gtx, size, surfaceStyle{radius: radius, content: sc.Surface.OnColor, button: theme.Title}, nil)
}

func (p *chatThemePage) noThemeCard(gtx layout.Context, size image.Point, lit bool, title string) layout.Dimensions {
	sc := scheme(gtx)
	radius := gtx.Dp(12)
	fillRounded(gtx, sc.SurfaceContainerHigh, size, radius)
	gtx.Constraints = layout.Exact(size)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return centeredLabel(gtx, title, token.TypestyleLabelLarge, sc.SurfaceVariant.OnColor, 2)
			}),
			vspace(6),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, "❌", token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
			}),
		)
	})
	outline(gtx, size, radius, lit)
	return p.none.Layout(gtx, size, surfaceStyle{radius: radius, content: sc.Surface.OnColor, button: title}, nil)
}

// outline rings a card of size in the primary color when lit.
func outline(gtx layout.Context, size image.Point, radius int, lit bool) {
	if !lit {
		return
	}
	width := float32(gtx.Dp(2))
	rect := image.Rectangle{Max: size}
	path := clip.UniformRRect(rect, radius).Path(gtx.Ops)
	paint.FillShape(gtx.Ops, scheme(gtx).Primary.Color.AsNRGBA(), clip.Stroke{Path: path, Width: width * 2}.Op())
}

func (p *chatThemePage) layoutActions(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	t := p.t
	var buttons []layout.FlexChild
	add := func(s *surface, text string) {
		buttons = append(buttons, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, s, text) }))
	}
	if p.problem != nil || t.problem != nil && t.selected == nil {
		add(&p.retry, l.T("history.retry"))
	}
	if t.selected != nil {
		if p.canApply() {
			add(&p.apply, l.T("chat_theme.apply"))
		}
		add(&p.local, l.T("chat_theme.local"))
	} else if t.appearance.Local {
		add(&p.reset, l.T("chat_theme.reset"))
	}
	add(&p.importButton, l.T("chat_theme.import"))
	if t.loading || t.saving {
		gtx = gtx.Disabled()
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, buttons...)
}

// themeScene draws a chat in a theme as it will look: its wallpaper, im,
// with a date and a message each way over it; style nil for the
// application's colors, and service set when there is a wallpaper, whose
// color average takes the plates.
func themeScene(gtx layout.Context, images *imageOps, im image.Image, style *model.ChatThemeStyle, service bool, average uint32, dark bool, size image.Point, l localization.Catalog) {
	defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(16)).Push(gtx.Ops).Pop()
	fillRect(gtx, scheme(gtx).SurfaceContainerLow, size)
	if im != nil {
		drawCover(gtx, images, im, size)
	}
	gtx.Constraints = layout.Exact(size)
	history := gtx
	if service {
		plate := serviceColor(average, dark)
		history = editedScheme(gtx, func(sc *token.Scheme) {
			sc.SecondaryContainer.Color = plate
			sc.SecondaryContainer.OnColor = token.NewMatColorFromHexRGB(0xffffff)
		})
	}
	bubble := func(gtx layout.Context, text string, outgoing bool) layout.Dimensions {
		if style != nil {
			gtx = editedScheme(gtx, func(sc *token.Scheme) {
				fg, accent, date := style.Text, style.Accent, style.InDate
				if outgoing {
					fg, accent, date = style.OutText, style.OutAccent, style.OutDate
				}
				sc.Surface.OnColor = token.NewMatColorFromHexRGBA(fg)
				sc.SurfaceVariant.OnColor = sc.Surface.OnColor.SetOpacity(.65)
				if date != 0 {
					sc.SurfaceVariant.OnColor = token.NewMatColorFromHexRGBA(date)
				}
				sc.Primary.Color = token.NewMatColorFromHexRGB(accent & 0xffffff)
			})
		}
		sc := scheme(gtx)
		gtx.Constraints.Min = image.Point{}
		gtx.Constraints.Max.X = gtx.Constraints.Max.X * 3 / 4
		m := model.Message{Text: text, Date: previewTime, Outgoing: outgoing, Views: 0}
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Min
				shape := bubbleShape{gtx.Dp(16), gtx.Dp(16), gtx.Dp(16), gtx.Dp(16)}
				col := sc.Surface.Color
				if outgoing {
					col = sc.PrimaryContainer.Color
				}
				if style != nil {
					col = token.NewMatColorFromHexRGBA(style.Incoming)
					if outgoing && len(style.Outgoing) > 0 {
						col = token.NewMatColorFromHexRGB(style.Outgoing[0])
					}
				}
				paint.FillShape(gtx.Ops, col.AsNRGBA(), shape.rrect(size).Op(gtx.Ops))
				if style != nil && outgoing && len(style.Outgoing) > 1 {
					defer shape.rrect(size).Push(gtx.Ops).Pop()
					drawCover(gtx, images, sceneGradient(style.Outgoing), size)
				}
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 8, Bottom: 7, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return label(gtx, text, token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
						}),
						vspace(2),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return messageFooter(gtx, m, l, false, previewSendState(gtx, m))
						}),
					)
				})
			}),
		)
	}
	// The messages lie at the bottom, as at the end of a chat.
	layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.S.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = 0
			return sceneMessages(gtx, history, bubble, l)
		})
	})
}

// sceneMessages are the date and the messages of a scene.
func sceneMessages(gtx, history layout.Context, bubble func(layout.Context, string, bool) layout.Dimensions, l localization.Catalog) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Values = history.Values
			width := gtx.Constraints.Max.X
			gtx.Constraints.Min = image.Point{}
			macro := op.Record(gtx.Ops)
			dims := datePill(gtx, l.T("chat_theme.today"))
			call := macro.Stop()
			defer op.Offset(image.Pt((width-dims.Size.X)/2, 0)).Push(gtx.Ops).Pop()
			call.Add(gtx.Ops)
			return layout.Dimensions{Size: image.Pt(width, dims.Size.Y)}
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return bubble(gtx, l.T("chat_theme.text_in"), false)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return bubble(gtx, l.T("chat_theme.text_out"), true)
			})
		}),
	)
}

// sceneGradients keeps the gradients of the scenes drawn, so that a frame
// does not make them again; windows draw on goroutines of their own.
var sceneGradients = struct {
	sync.Mutex
	m map[string]*image.RGBA
}{m: map[string]*image.RGBA{}}

func sceneGradient(colors []uint32) *image.RGBA {
	sceneGradients.Lock()
	defer sceneGradients.Unlock()
	key := (wallpaperKey(&model.ChatWallpaper{Colors: colors}, ""))
	if im, ok := sceneGradients.m[key]; ok {
		return im
	}
	if len(sceneGradients.m) > 32 {
		clear(sceneGradients.m)
	}
	im := chattheme.Gradient(colors, 32, 32, 0, 0)
	sceneGradients.m[key] = im
	return im
}

// editedScheme is gtx with a copy of its theme's scheme changed by edit.
func editedScheme(gtx layout.Context, edit func(*token.Scheme)) layout.Context {
	theme := *wdk.GetMaterialTheme(gtx)
	sc := *theme.Scheme
	theme.Scheme = &sc
	edit(&sc)
	values := make(map[string]any, len(gtx.Values))
	for k, v := range gtx.Values {
		values[k] = v
	}
	values[wdk.ThemeNamespace] = &theme
	gtx.Values = values
	return gtx
}
