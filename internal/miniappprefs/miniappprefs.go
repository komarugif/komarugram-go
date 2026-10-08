// SPDX-License-Identifier: Unlicense OR MIT

// Package miniappprefs holds the Mini App privacy settings — what a Mini App
// may keep between launches — and the settings UI for them.
package miniappprefs

import (
	"path/filepath"
	"sync"

	"gio-mw/exp"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/radio"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/pkg/miniapp"
)

// Settings holds the Mini App privacy settings. It is safe for concurrent
// use.
type Settings struct {
	mu      sync.Mutex
	storage miniapp.Storage
	changed func(miniapp.Storage)
}

// New returns settings starting with the given storage mode.
func New(storage miniapp.Storage) *Settings {
	return &Settings{storage: storage}
}

// Storage is what the next Mini App launch may keep.
func (s *Settings) Storage() miniapp.Storage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storage
}

func (s *Settings) SetStorage(storage miniapp.Storage) {
	s.mu.Lock()
	if s.storage == storage {
		s.mu.Unlock()
		return
	}
	s.storage = storage
	changed := s.changed
	s.mu.Unlock()
	if changed != nil {
		changed(storage)
	}
}

// SetPreferenceChanged installs the callback used to persist user changes.
func (s *Settings) SetPreferenceChanged(changed func(miniapp.Storage)) {
	s.mu.Lock()
	s.changed = changed
	s.mu.Unlock()
}

// Strings are the texts of View.
type Strings struct {
	Title    string
	Storage  map[miniapp.Storage]string
	Hints    map[miniapp.Storage]string
	Browser  string // Followed by the browser that will run Mini Apps.
	NoneSeen string
}

var English = Strings{
	Title: "Mini Apps",
	Storage: map[miniapp.Storage]string{
		miniapp.Ephemeral: "Keep nothing",
		miniapp.PerApp:    "Separate storage for each app",
		miniapp.Shared:    "One storage for all apps of the account",
	},
	Hints: map[miniapp.Storage]string{
		miniapp.Ephemeral: "Cookies and site data are deleted when the Mini App window closes.",
		miniapp.PerApp:    "Every Mini App finds its own data again, and no app can read another's.",
		miniapp.Shared:    "All Mini Apps of the account share one profile, as Telegram Desktop does.",
	},
	Browser:  "Mini Apps open in: ",
	NoneSeen: "no Chromium-based browser or Firefox found",
}

var Russian = Strings{
	Title: "Mini Apps",
	Storage: map[miniapp.Storage]string{
		miniapp.Ephemeral: "Ничего не хранить",
		miniapp.PerApp:    "Отдельное хранилище для каждого приложения",
		miniapp.Shared:    "Общее хранилище для всех приложений аккаунта",
	},
	Hints: map[miniapp.Storage]string{
		miniapp.Ephemeral: "Cookies и данные сайтов удаляются, когда окно Mini App закрывается.",
		miniapp.PerApp:    "Каждое приложение находит свои данные снова, и ни одно не видит чужие.",
		miniapp.Shared:    "Все Mini Apps аккаунта используют один профиль, как в Telegram Desktop.",
	},
	Browser:  "Mini Apps открываются в: ",
	NoneSeen: "браузер на основе Chromium не найден",
}

// View is the settings UI for Settings.
type View struct {
	// TitleStyle is the style of the title, TypestyleTitleLarge by default.
	TitleStyle token.Typestyle
	settings   *Settings
	strings    Strings
	storage    *radio.Radios[miniapp.Storage]
}

func NewView(settings *Settings, strings Strings) *View {
	modes := []miniapp.Storage{miniapp.Ephemeral, miniapp.PerApp, miniapp.Shared}
	return &View{
		settings: settings,
		strings:  strings,
		storage:  radio.NewRadios(modes, settings.Storage(), settings.SetStorage),
	}
}

// SetStrings switches the labels without replacing widget state.
func (v *View) SetStrings(strings Strings) { v.strings = strings }

func (v *View) Update(gtx layout.Context) {
	v.storage.SetValue(v.settings.Storage())
	v.storage.Update(gtx)
}

func (v *View) Layout(gtx layout.Context) layout.Dimensions {
	onSurface := exp.GetSurfaceTheme(gtx).OnColor
	text := func(txt string, style token.Typestyle) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return wdk.LayoutLabel(gtx, wdk.LabelStyle{Typestyle: style, Color: onSurface}, txt)
		}
	}
	titleStyle := v.TitleStyle
	if titleStyle == token.TypestyleDefault {
		titleStyle = token.TypestyleTitleLarge
	}
	browser := v.strings.NoneSeen
	if found := miniapp.Browser(); found != "" {
		browser = filepath.Base(found)
	}
	return block.Line{
		Axis:     block.AxisVertical,
		Overflow: block.OverflowClip,
	}.Layout(gtx,
		block.NewSegment(text(v.strings.Title, titleStyle)),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return v.storage.Layout(gtx, radio.LeadingKind, v.strings.Storage)
		}),
		block.NewVerticalSpacer(unit.Dp(4)),
		block.NewSegment(text(v.strings.Hints[v.settings.Storage()], token.TypestyleBodyMedium)),
		block.NewVerticalSpacer(unit.Dp(8)),
		block.NewSegment(text(v.strings.Browser+browser, token.TypestyleBodyMedium)),
	)
}
