// SPDX-License-Identifier: Unlicense OR MIT

// Package appearance watches the system color scheme, so that an application
// can follow the system between light and dark themes, and the system's
// accent color where the system has one.
//
// Support by platform:
//   - Linux: the XDG desktop portal color-scheme setting, then the GNOME
//     color-scheme, then a theme name containing "dark" from XFCE (xfconf)
//     or GNOME (gtk-theme), all over D-Bus with change notifications.
//   - Windows: the "AppsUseLightTheme" personalization setting, polled.
//   - macOS: the "AppleInterfaceStyle" user default, polled.
//   - Other platforms: the scheme stays unknown.
//
// The accent color, by platform:
//   - Linux: on KDE Plasma, the selection color of kdeglobals, which
//     Plasma makes from the accent (or its AccentColor), followed by
//     KConfig's and the portal's change notifications; elsewhere the XDG
//     desktop portal accent-color setting (GNOME 47 and later).
//   - Windows: the DWM "AccentColor" personalization setting, polled.
//   - Other platforms: none.
package appearance

import (
	"image/color"
	"strings"
	"sync"
)

// Scheme is a system color scheme.
type Scheme int

const (
	Unknown Scheme = iota
	Light
	Dark
)

// Monitor tracks the system color scheme. Use Start to create one.
type Monitor struct {
	mu       sync.Mutex
	scheme   Scheme
	accent   color.NRGBA
	onChange func(Scheme)
	stop     func()
}

// Start begins watching the system. onChange, when not nil, is called from
// another goroutine whenever the scheme or the accent color changes.
func Start(onChange func(Scheme)) *Monitor {
	m := &Monitor{onChange: onChange}
	m.stop = startPlatform(m)
	return m
}

// Fixed is a monitor that watches nothing and reports s: the system of a
// window no system shows, as one a test or a render draws.
func Fixed(s Scheme) *Monitor {
	return &Monitor{scheme: s}
}

// Scheme returns the current system color scheme.
func (m *Monitor) Scheme() Scheme {
	if m == nil {
		return Unknown
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scheme
}

// Accent returns the system's accent color, opaque, and whether the system
// has one.
func (m *Monitor) Accent() (color.NRGBA, bool) {
	if m == nil {
		return color.NRGBA{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accent, m.accent.A != 0
}

// WithAccent sets the accent color a Fixed monitor reports; a transparent
// one is none.
func (m *Monitor) WithAccent(c color.NRGBA) *Monitor {
	m.mu.Lock()
	m.accent = c
	m.mu.Unlock()
	return m
}

// Close stops watching the system.
func (m *Monitor) Close() {
	if m != nil && m.stop != nil {
		m.stop()
	}
}

func (m *Monitor) set(s Scheme) {
	m.mu.Lock()
	changed := m.scheme != s
	m.scheme = s
	m.mu.Unlock()
	if changed && m.onChange != nil {
		m.onChange(s)
	}
}

// setAccent sets the accent color; ok false, or a transparent c, is none.
func (m *Monitor) setAccent(c color.NRGBA, ok bool) {
	if ok {
		c.A = 255
	} else {
		c = color.NRGBA{}
	}
	m.mu.Lock()
	changed := m.accent != c
	m.accent = c
	s := m.scheme
	m.mu.Unlock()
	if changed && m.onChange != nil {
		m.onChange(s)
	}
}

// schemeOfTheme guesses the scheme from a theme name, as desktops without a
// color-scheme setting only have dark variants of their themes.
func schemeOfTheme(name string) Scheme {
	if name == "" {
		return Unknown
	}
	if strings.Contains(strings.ToLower(name), "dark") {
		return Dark
	}
	return Light
}
