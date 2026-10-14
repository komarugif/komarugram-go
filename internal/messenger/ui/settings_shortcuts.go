// SPDX-License-Identifier: Unlicense OR MIT
package ui

import (
	"context"
	"errors"
	"fmt"
	"gio-mw/token"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"image"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/shortcuts"
	"slices"
	"strings"
	"time"
)

type shortcutRow struct{ edit, clear, reset surface }
type shortcutProbe struct {
	generation uint64
	report     shortcuts.SystemReport
}
type shortcutSettings struct {
	strings               localization.Catalog
	check                 func(context.Context, []shortcuts.Chord) shortcuts.SystemReport
	manual                *textField
	manualText            string
	get                   func() shortcuts.Bindings
	set                   func(shortcuts.Bindings) error
	catalog               func() []shortcuts.Command
	invalidate            func()
	toast                 *toast
	rows                  map[string]*shortcutRow
	refresh, save, cancel surface
	dialog                modal
	editing               string
	candidate             *shortcuts.Chord
	problem               string
	focus                 bool
	keys                  byte
	signature             string
	report                shortcuts.SystemReport
	generation            uint64
	stop                  context.CancelFunc
	answers               chan shortcutProbe
	busy                  bool
}

func newShortcutSettings(get func() shortcuts.Bindings, set func(shortcuts.Bindings) error, catalog func() []shortcuts.Command, invalidate func(), t *toast) *shortcutSettings {
	return &shortcutSettings{check: shortcuts.CheckSystem, manual: newTextField(0, ""), get: get, set: set, catalog: catalog, invalidate: invalidate, toast: t, rows: map[string]*shortcutRow{}, answers: make(chan shortcutProbe, 2)}
}
func (s *shortcutSettings) Capturing() bool { return s != nil && s.dialog.Shown() && !s.dialog.closing }
func shortcutTitle(c shortcuts.Command, l localization.Catalog) string {
	return l.T(c.Label)
}
func (s *shortcutSettings) find(id string) (shortcuts.Command, bool) {
	for _, c := range s.catalog() {
		if c.ID == id {
			return c, true
		}
	}
	return shortcuts.Command{}, false
}
func (s *shortcutSettings) write(b shortcuts.Bindings) {
	if err := s.set(b); err != nil && s.toast != nil {
		if errors.Is(err, preferences.ErrSettingsAccess) {
			s.toast.Show(s.strings.T("keys.keep_settings"))
		} else {
			s.toast.Show(err.Error())
		}
	}
}
func (s *shortcutSettings) change(id string, chords []shortcuts.Chord) {
	b := s.get().Clone()
	if b == nil {
		b = shortcuts.Bindings{}
	}
	b[id] = slices.Clone(chords)
	s.write(b)
}
func (s *shortcutSettings) Close() {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	s.generation++
	s.busy = false
	s.candidate = nil
	s.editing = ""
	s.dialog.Hide()
}
func (s *shortcutSettings) UpdateCapture(gtx layout.Context) {
	if !s.Capturing() {
		return
	}
	for {
		e, ok := gtx.Event(key.FocusFilter{Target: &s.keys}, key.Filter{Focus: &s.keys, Optional: shortcuts.AllModifiers}, key.Filter{Focus: &s.keys, Name: key.NameEscape, Optional: shortcuts.AllModifiers}, key.Filter{Focus: &s.keys, Name: key.NameTab, Optional: shortcuts.AllModifiers}, key.Filter{Focus: &s.keys, Name: key.NameReturn, Optional: shortcuts.AllModifiers})
		if !ok {
			break
		}
		ev, ok := e.(key.Event)
		if !ok || ev.State != key.Press {
			continue
		}
		if ev.Name == key.NameEscape {
			s.dialog.Close()
			continue
		}
		position := ev.Physical
		if position == "" {
			position = ev.Name
		}
		if !shortcuts.ValidKey(position) {
			continue
		}
		chord := shortcuts.Chord{Key: position, Modifiers: ev.Modifiers}
		if s.candidate == nil || *s.candidate != chord {
			s.candidate = &chord
			s.problem = ""
			s.signature = ""
		}
	}
}
func (s *shortcutSettings) probe(chords []shortcuts.Chord) {
	if s.stop != nil {
		s.stop()
	}
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.busy = true
	go func() {
		defer cancel()
		job, done := context.WithTimeout(ctx, 4*time.Second)
		answer := shortcutProbe{generation, s.check(job, chords)}
		done()
		select {
		case s.answers <- answer:
			s.invalidate()
		case <-ctx.Done():
		}
	}()
}
func (s *shortcutSettings) Update(gtx layout.Context, l localization.Catalog) {
	s.strings = l
	s.UpdateCapture(gtx)
	if s.Capturing() {
		s.manual.Submitted(gtx)
		if text := s.manual.Text(); text != s.manualText {
			s.manualText = text
			chord, err := shortcuts.Parse(text)
			if err == nil {
				s.candidate = &chord
				s.problem = ""
				s.signature = ""
			} else {
				s.candidate = nil
				s.problem = l.T("keys.invalid")
			}
		}
	}
	for {
		select {
		case a := <-s.answers:
			if a.generation == s.generation {
				s.report = a.report
				s.busy = false
			}
		default:
			goto ready
		}
	}
ready:
	var chords []shortcuts.Chord
	for _, c := range s.catalog() {
		if c.ReadOnly {
			continue
		}
		for _, b := range s.get().For(c) {
			if !slices.Contains(chords, b) {
				chords = append(chords, b)
			}
		}
		r := s.rows[c.ID]
		if r == nil {
			continue
		}
		if r.edit.Clicked(gtx) {
			s.editing = c.ID
			s.manual.Clear()
			s.manualText = ""
			s.candidate = nil
			s.problem = ""
			s.focus = true
			s.dialog.Open()
			s.UpdateCapture(gtx)
		}
		if r.clear.Clicked(gtx) {
			s.change(c.ID, nil)
		}
		if r.reset.Clicked(gtx) {
			b := s.get().Clone()
			delete(b, c.ID)
			s.write(b)
		}
	}
	if s.candidate != nil && !slices.Contains(chords, *s.candidate) {
		chords = append(chords, *s.candidate)
	}
	signature := fmt.Sprint(chords)
	if s.refresh.Clicked(gtx) {
		s.signature = ""
	}
	if s.signature != signature {
		s.signature = signature
		s.probe(chords)
	}
	if s.cancel.Clicked(gtx) {
		s.dialog.Close()
	}
	if s.Capturing() {
		if _, ok := s.find(s.editing); !ok {
			s.dialog.Close()
		}
	}
	if s.save.Clicked(gtx) && s.Capturing() && s.candidate != nil && !s.busy {
		c, ok := s.find(s.editing)
		if !ok || c.ReadOnly {
			return
		}
		if err := shortcuts.Validate(c, *s.candidate); err != nil {
			s.problem = l.T("keys.invalid")
			return
		}
		conflicts := shortcuts.Conflicts(c, *s.candidate, s.catalog(), s.get())
		if len(conflicts) > 0 {
			s.problem = fmt.Sprintf(l.T("keys.conflict"), shortcutTitle(conflicts[0], l))
			return
		}
		s.change(c.ID, []shortcuts.Chord{*s.candidate})
		s.dialog.Close()
	}
}
func shortcutText(chords []shortcuts.Chord, l localization.Catalog) string {
	if len(chords) == 0 {
		return l.T("keys.unassigned")
	}
	var values []string
	for _, c := range chords {
		values = append(values, c.String())
	}
	return strings.Join(values, "  ·  ")
}
func (s *shortcutSettings) Layout(gtx layout.Context, p *settingsPage, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	header := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("keys.hint"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
	}), vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("keys.system_hint"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
	}), vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return textButton(gtx, &s.refresh, l.T("keys.check_system"))
	})}
	status := "keys.system_unknown"
	if s.busy {
		status = "keys.checking"
	} else if s.report.KDE {
		status = "keys.kde_hint"
	}
	header = append(header, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T(status), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
	}))
	if s.report.PortalVersion > 0 {
		header = append(header, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("keys.portal"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	cards := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, header...)
	})}
	catalog := s.catalog()
	for _, scope := range []string{"app", "history", "composer", "viewer", "article", "fixed"} {
		var commands []shortcuts.Command
		for _, c := range catalog {
			group := c.Scope
			if c.ReadOnly {
				group = "fixed"
			}
			if group == scope {
				commands = append(commands, c)
			}
		}
		if len(commands) == 0 {
			continue
		}
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.cardHeight("shortcuts-"+scope).Card(gtx, func(gtx layout.Context) layout.Dimensions {
				rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("keys.scope_"+scope), token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
				}), vspace(8)}
				for _, c := range commands {
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.layoutRow(gtx, c, l) }))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, defaultCardPadding)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, cards...)
}
func (s *shortcutSettings) layoutRow(gtx layout.Context, c shortcuts.Command, l localization.Catalog) layout.Dimensions {
	r := s.rows[c.ID]
	if r == nil {
		r = new(shortcutRow)
		s.rows[c.ID] = r
	}
	sc := scheme(gtx)
	binding := s.get().For(c)
	return layout.Inset{Top: 8, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		body := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, shortcutTitle(c, l), token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
		}), vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			widgets := []layout.FlexChild{layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, shortcutText(binding, l), token.TypestyleLabelLarge, sc.Primary.Color, 0)
			})}
			if !c.ReadOnly {
				widgets = append(widgets, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &r.edit, l.T("keys.assign")) }), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return navigationButton(gtx, &r.clear, iconClear, l.T("keys.remove"))
				}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return navigationButton(gtx, &r.reset, iconReset, l.T("menus.reset"))
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, widgets...)
		})}
		for _, b := range binding {
			if s.report.Occupied[b] {
				body = append(body, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, fmt.Sprintf(l.T("keys.system_conflict"), b.String()), token.TypestyleBodySmall, sc.Error.Color, 0)
				}))
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, body...)
	})
}
func (s *shortcutSettings) Overlay(gtx layout.Context, l localization.Catalog) {
	if !s.dialog.Shown() {
		return
	}
	s.dialog.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		return s.dialog.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(520))
			sc := scheme(gtx)
			// A zero-area tag receives keyboard focus without covering dialog buttons.
			area := clip.Rect(image.Rectangle{}).Push(gtx.Ops)
			event.Op(gtx.Ops, &s.keys)
			area.Pop()
			if s.focus {
				gtx.Execute(key.FocusCmd{Tag: &s.keys})
				s.focus = false
			}
			c, _ := s.find(s.editing)
			text := l.T("keys.press")
			if s.candidate != nil {
				text = s.candidate.String()
			}
			body := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, shortcutTitle(c, l), token.TypestyleTitleLarge, sc.Surface.OnColor, 0)
			}), vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleTitleMedium, sc.Primary.Color, 0)
			}), vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("keys.record_hint"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			})}
			body = append(body, vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.manual.Layout(gtx, l.T("keys.manual"), false) }))
			if s.candidate != nil && s.report.Occupied[*s.candidate] {
				body = append(body, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, fmt.Sprintf(l.T("keys.system_conflict"), s.candidate.String()), token.TypestyleBodyMedium, sc.Error.Color, 0)
				}))
			}
			if s.problem != "" {
				body = append(body, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, s.problem, token.TypestyleBodyMedium, sc.Error.Color, 0)
				}))
			}
			body = append(body, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return buttonRow(gtx, func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &s.cancel, l.T("page.cancel")) }, func(gtx layout.Context) layout.Dimensions {
					if s.candidate == nil || s.busy {
						gtx = gtx.Disabled()
					}
					return textButton(gtx, &s.save, l.T("keys.save_binding"))
				})
			}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, body...)
		}, defaultCardPadding)
	})
}
