// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"
	"fmt"
	"image"
	"komarugram/internal/messenger/shortcuts"
	"math"
	"runtime"
	"slices"

	"gio-mw/token"
	"gio-mw/wdk"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

var (
	iconMenuVisible = wdk.RequireIconWidget(icons.ContentRemoveCircleOutline)
	iconMenuHidden  = wdk.RequireIconWidget(icons.ContentRemoveCircle)
	iconMenuDrag    = wdk.RequireIconWidget(icons.EditorDragHandle)
)

const menuSettingsRowHeight = 48

type menuSettings struct {
	strings localization.Catalog
	get     menuPreferences
	set     func(preferences.Menus) error
	catalog func(localization.Catalog) []menuCatalog
	cards   map[string]*menuSettingsCard
	toast   *toast
}
type menuSettingsCard struct {
	reset        surface
	rows         map[string]*surface
	drag         byte
	dragging     string
	pointer      pointer.ID
	order        []string
	startY       int
	grabY, dragY float32
	positions    map[string]*menuRowMotion
}

func newMenuSettings(get menuPreferences, set func(preferences.Menus) error, catalog func(localization.Catalog) []menuCatalog, t *toast) *menuSettings {
	return &menuSettings{get: get, set: set, catalog: catalog, cards: map[string]*menuSettingsCard{}, toast: t}
}
func (s *menuSettings) state(id string) *menuSettingsCard {
	c := s.cards[id]
	if c == nil {
		c = &menuSettingsCard{rows: map[string]*surface{}, positions: map[string]*menuRowMotion{}}
		s.cards[id] = c
	}
	return c
}
func (s *menuSettings) save(id string, value preferences.MenuLayout) {
	m := s.get().Clone()
	if m == nil {
		m = preferences.Menus{}
	}
	m[id] = value
	if err := s.set(m); err != nil && s.toast != nil {
		if errors.Is(err, preferences.ErrSettingsAccess) {
			s.toast.Show(s.strings.T("keys.keep_settings"))
		} else {
			s.toast.Show(err.Error())
		}
	}
}
func catalogIDs(c menuCatalog) []string {
	ids := make([]string, len(c.items))
	for i, v := range c.items {
		ids[i] = v.id
	}
	return ids
}
func (s *menuSettings) Update(gtx layout.Context, l localization.Catalog) {
	s.strings = l
	for _, catalog := range s.catalog(l) {
		c := s.state(catalog.id)
		value := s.get.layout(catalog.id)
		ids := value.Arrange(catalogIDs(catalog), true)
		if c.dragging != "" && !slices.Contains(ids, c.dragging) {
			c.dragging = ""
			c.order = nil
		}
		if c.reset.Clicked(gtx) {
			s.save(catalog.id, preferences.MenuLayout{})
			c.dragging = ""
			c.order = nil
			continue
		}
		for _, id := range ids {
			if row := c.rows[id]; row != nil && row.Clicked(gtx) {
				hidden := slices.Clone(value.Hidden)
				if i := slices.Index(hidden, id); i >= 0 {
					hidden = slices.Delete(hidden, i, i+1)
				} else {
					hidden = append(hidden, id)
				}
				value.Hidden = hidden
				s.save(catalog.id, value)
			}
		}
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &c.drag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
			if !ok {
				break
			}
			e, ok := ev.(pointer.Event)
			if !ok {
				continue
			}
			height := float32(gtx.Dp(menuSettingsRowHeight))
			localY := e.Position.Y - float32(c.startY)
			index := int(localY / height)
			switch e.Kind {
			case pointer.Press:
				if (e.Source != pointer.Mouse || e.Buttons == pointer.ButtonPrimary) && index >= 0 && index < len(ids) {
					// Rows may still be settling after the previous drop. Hit the
					// visible row rather than interpreting its old slot as another item.
					distance := float32(math.MaxFloat32)
					for i, id := range ids {
						if row := c.positions[id]; row != nil {
							d := float32(math.Abs(float64(localY - row.value - height/2)))
							if d < distance {
								index = i
								distance = d
							}
						}
					}
					c.dragging = ids[index]
					c.pointer = e.PointerID
					c.order = slices.Clone(ids)
					c.dragY = float32(index) * height
					if row := c.positions[c.dragging]; row != nil {
						c.dragY = row.value
					}
					c.grabY = localY - c.dragY
					gtx.Execute(pointer.GrabCmd{Tag: &c.drag, ID: e.PointerID})
				}
			case pointer.Drag:
				if c.dragging != "" && e.PointerID == c.pointer {
					c.dragY = max(0, min(localY-c.grabY, float32(len(c.order)-1)*height))
					c.order = moveMenuItem(c.order, c.dragging, int((c.dragY+height/2)/height))
					gtx.Execute(op.InvalidateCmd{})
				}
			case pointer.Release:
				if c.dragging != "" && e.PointerID == c.pointer {
					s.save(catalog.id, value.WithOrder(c.order))
					c.dragging = ""
					c.order = nil
					gtx.Execute(op.InvalidateCmd{})
				}
			case pointer.Cancel:
				c.dragging = ""
				c.order = nil
			}
		}
	}
}
func moveMenuItem(ids []string, id string, to int) []string {
	from := slices.Index(ids, id)
	if from < 0 || len(ids) == 0 {
		return ids
	}
	to = max(0, min(to, len(ids)-1))
	if from == to {
		return ids
	}
	out := slices.Clone(ids)
	out = slices.Delete(out, from, from+1)
	out = slices.Insert(out, to, id)
	return out
}
func (s *menuSettings) Layout(gtx layout.Context, p *settingsPage, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	shortcut := "Ctrl+,"
	if runtime.GOOS == "darwin" {
		shortcut = "⌘,"
	}
	hint := fmt.Sprintf(l.T("menus.shortcut"), shortcut)
	if p.shortcuts != nil {
		c, _ := shortcuts.Find("app.settings")
		bindings := p.shortcuts.get().For(c)
		if len(bindings) > 0 {
			hint = fmt.Sprintf(l.T("menus.shortcut"), shortcutText(bindings, l))
		} else {
			hint = ""
		}
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("menus.hint"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}), vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, hint, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		}),
	}
	for _, catalog := range s.catalog(l) {
		children = append(children, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("menu-"+catalog.id).Card(gtx, func(gtx layout.Context) layout.Dimensions { return s.layoutCard(gtx, catalog, l) }, defaultCardPadding)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}
func (s *menuSettings) layoutCard(gtx layout.Context, catalog menuCatalog, l localization.Catalog) layout.Dimensions {
	c := s.state(catalog.id)
	sc := scheme(gtx)
	value := s.get.layout(catalog.id)
	ids := value.Arrange(catalogIDs(catalog), true)
	if c.dragging != "" {
		ids = c.order
	}
	byID := map[string]menuChoice{}
	for _, v := range catalog.items {
		byID[v.id] = v
	}
	header := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, catalog.title, token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &c.reset, l.T("menus.reset")) }),
	)
	c.startY = header.Size.Y + gtx.Dp(8)
	width := gtx.Constraints.Max.X
	height := gtx.Dp(menuSettingsRowHeight)
	drawIDs := slices.Clone(ids)
	if c.dragging != "" {
		drawIDs = slices.DeleteFunc(drawIDs, func(id string) bool { return id == c.dragging })
		drawIDs = append(drawIDs, c.dragging)
	}
	for _, id := range drawIDs {
		i := slices.Index(ids, id)
		choice, ok := byID[id]
		if !ok {
			continue
		}
		motion := c.positions[id]
		if motion == nil {
			motion = new(menuRowMotion)
			c.positions[id] = motion
		}
		target := float32(i * height)
		if c.dragging == id {
			target = c.dragY
		}
		y := c.startY + int(math.Round(float64(motion.Position(gtx, target, c.dragging == id))))
		hidden := slices.Contains(value.Hidden, id)
		if c.dragging == id {
			offset(gtx, image.Pt(0, y), func(gtx layout.Context) layout.Dimensions {
				fillRounded(gtx, sc.SecondaryContainer.Color, image.Pt(width, height), gtx.Dp(8))
				return layout.Dimensions{}
			})
		}
		handle := image.Rect(0, y, gtx.Dp(40), y+height)
		area := clip.Rect(handle).Push(gtx.Ops)
		pointer.CursorGrab.Add(gtx.Ops)
		event.Op(gtx.Ops, &c.drag)
		area.Pop()
		inRect(gtx, handle, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(gtx.Dp(20), gtx.Dp(20)), func(gtx layout.Context) layout.Dimensions { return iconMenuDrag(gtx, sc.SurfaceVariant.OnColor) })
			})
		})
		inRect(gtx, image.Rect(gtx.Dp(44), y, width-gtx.Dp(48), y+height), func(gtx layout.Context) layout.Dimensions {
			color := sc.Surface.OnColor
			if hidden {
				color = sc.SurfaceVariant.OnColor
			}
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return exact(gtx, image.Pt(gtx.Dp(20), gtx.Dp(20)), func(gtx layout.Context) layout.Dimensions { return choice.icon(gtx, color) })
					}),
					layout.Rigid(layout.Spacer{Width: 12}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, choice.title, token.TypestyleBodyMedium, color, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !hidden {
							return layout.Dimensions{}
						}
						return label(gtx, l.T("menus.hidden"), token.TypestyleLabelSmall, color, 1)
					}),
				)
			})
		})
		row := c.rows[id]
		if row == nil {
			row = new(surface)
			c.rows[id] = row
		}
		inRect(gtx, image.Rect(width-gtx.Dp(48), y, width, y+height), func(gtx layout.Context) layout.Dimensions {
			icon, caption := iconMenuVisible, l.T("menus.hide")
			if hidden {
				icon, caption = iconMenuHidden, l.T("menus.show")
			}
			return row.Layout(gtx, gtx.Constraints.Max, surfaceStyle{content: sc.Surface.OnColor, button: caption + ": " + choice.title, radius: gtx.Dp(24)}, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(gtx.Dp(24), gtx.Dp(24)), func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.Primary.Color) })
				})
			})
		})
	}
	return layout.Dimensions{Size: image.Pt(width, c.startY+len(ids)*height)}
}

// CancelDrag discards a preview when navigating away from the section.
func (s *menuSettings) CancelDrag() {
	for _, c := range s.cards {
		c.dragging = ""
		c.order = nil
	}
}

// The grabbed row tracks the pointer directly. Its neighbors slide into their
// new slots; after release/cancel the grabbed row settles from its last position.
// Motion settings and geometry changes snap the same paint and input bounds.
type menuRowMotion struct {
	tween         wdk.FloatTween
	value         float32
	width, height int
}

func (m *menuRowMotion) Position(gtx layout.Context, target float32, tracking bool) float32 {
	height := gtx.Dp(menuSettingsRowHeight)
	if tracking || m.width != gtx.Constraints.Max.X || m.height != height {
		m.tween = wdk.FloatTween{}
	}
	m.width, m.height = gtx.Constraints.Max.X, height
	m.tween.Duration, m.tween.Easing = token.DurationShort4, &token.EasingStandard
	m.value = m.tween.Animate(gtx, target)
	return m.value
}
