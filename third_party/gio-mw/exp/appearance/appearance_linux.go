// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux && !android

package appearance

import (
	"bufio"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalDest     = "org.freedesktop.portal.Desktop"
	portalPath     = "/org/freedesktop/portal/desktop"
	portalSettings = "org.freedesktop.portal.Settings"

	xfconfDest  = "org.xfce.Xfconf"
	xfconfPath  = "/org/xfce/Xfconf"
	xfconfIface = "org.xfce.Xfconf"

	// KConfig tells of a change to a file it writes with this signal, on
	// the path of the file's name.
	kconfigPath   = "/kdeglobals"
	kconfigIface  = "org.kde.kconfig.notify"
	kconfigSignal = kconfigIface + ".ConfigChanged"

	// kdeSettle is how long kdeglobals is left after its last change
	// before it is read again.
	kdeSettle = 300 * time.Millisecond
)

// The sources, in order of preference.
var (
	portalScheme = [2]string{"org.freedesktop.appearance", "color-scheme"}
	gnomeScheme  = [2]string{"org.gnome.desktop.interface", "color-scheme"}
	gnomeTheme   = [2]string{"org.gnome.desktop.interface", "gtk-theme"}
	xfceTheme    = [2]string{"xsettings", "/Net/ThemeName"}

	portalAccent = [2]string{"org.freedesktop.appearance", "accent-color"}
)

// sources holds the last value of every source.
type sources struct {
	mu     sync.Mutex
	values map[[2]string]dbus.Variant
}

func (s *sources) put(key [2]string, v dbus.Variant) {
	s.mu.Lock()
	s.values[key] = v
	s.mu.Unlock()
}

// scheme resolves the sources into a scheme.
func (s *sources) scheme() Scheme {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.values[portalScheme].Value().(uint32); ok {
		switch v {
		case 1:
			return Dark
		case 2:
			return Light
		}
	}
	if v, ok := s.values[gnomeScheme].Value().(string); ok {
		switch v {
		case "prefer-dark":
			return Dark
		case "prefer-light":
			return Light
		}
	}
	// XFCE keeps its theme in xfconf; the GNOME key may be stale there.
	for _, key := range [][2]string{xfceTheme, gnomeTheme} {
		if v, ok := s.values[key].Value().(string); ok && v != "" {
			return schemeOfTheme(v)
		}
	}
	return Unknown
}

// accent resolves the sources into an accent color: on KDE, kdeglobals',
// else the portal's. Plasma's portal tells of a new accent with the one
// before it (Plasma 6.8: on a change to blue it sent the green that was
// set before, and ReadOne returned it too until the next change).
func (s *sources) accent(kde func() (color.NRGBA, bool)) (color.NRGBA, bool) {
	if kde != nil {
		if c, ok := kde(); ok {
			return c, true
		}
	}
	s.mu.Lock()
	v := s.values[portalAccent]
	s.mu.Unlock()
	return portalColor(v)
}

// portalColor reads the portal's accent color: red, green and blue from 0
// to 1; values out of that range mean there is none.
func portalColor(v dbus.Variant) (color.NRGBA, bool) {
	fields, ok := v.Value().([]any)
	if !ok || len(fields) != 3 {
		return color.NRGBA{}, false
	}
	var rgb [3]uint8
	for i, f := range fields {
		x, ok := f.(float64)
		if !ok || math.IsNaN(x) || x < 0 || x > 1 {
			return color.NRGBA{}, false
		}
		rgb[i] = uint8(math.Round(x * 255))
	}
	return color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}, true
}

// onKDE reports whether the session is KDE Plasma's.
func onKDE() bool {
	for _, d := range strings.Split(os.Getenv("XDG_CURRENT_DESKTOP"), ":") {
		if strings.EqualFold(d, "KDE") {
			return true
		}
	}
	return false
}

// kdeAccent reads the accent color from kdeglobals: the selection color
// of the colors applied, which Plasma makes from the accent (the one
// chosen, the wallpaper's when the accent follows the wallpaper, or the
// color scheme's own) and which its portal reports as the accent; else
// AccentColor of [General].
func kdeAccent() (color.NRGBA, bool) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return color.NRGBA{}, false
		}
		dir = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(dir, "kdeglobals"))
	if err != nil {
		return color.NRGBA{}, false
	}
	defer f.Close()
	return parseKDEAccent(bufio.NewScanner(f))
}

func parseKDEAccent(sc *bufio.Scanner) (color.NRGBA, bool) {
	var group string
	var accent, selection string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			group = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case group == "[General]" && key == "AccentColor":
			accent = value
		case group == "[Colors:Selection]" && key == "BackgroundNormal":
			selection = value
		}
	}
	for _, v := range []string{selection, accent} {
		if c, ok := parseKDEColor(v); ok {
			return c, true
		}
	}
	return color.NRGBA{}, false
}

// parseKDEColor reads a color as KConfig writes it: "r,g,b", or "#rrggbb".
func parseKDEColor(v string) (color.NRGBA, bool) {
	if hex, ok := strings.CutPrefix(v, "#"); ok && len(hex) == 6 {
		n, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return color.NRGBA{}, false
		}
		return color.NRGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, true
	}
	parts := strings.Split(v, ",")
	if len(parts) < 3 {
		return color.NRGBA{}, false
	}
	var rgb [3]uint8
	for i := range rgb {
		n, err := strconv.ParseUint(strings.TrimSpace(parts[i]), 10, 8)
		if err != nil {
			return color.NRGBA{}, false
		}
		rgb[i] = uint8(n)
	}
	return color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}, true
}

func startPlatform(m *Monitor) func() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil
	}
	src := &sources{values: make(map[[2]string]dbus.Variant)}
	portal := conn.Object(portalDest, portalPath)
	for _, key := range [][2]string{portalScheme, gnomeScheme, gnomeTheme, portalAccent} {
		if v, ok := readPortalSetting(portal, key); ok {
			src.put(key, v)
		}
	}
	var theme dbus.Variant
	if err := conn.Object(xfconfDest, xfconfPath).Call(xfconfIface+".GetProperty", 0, xfceTheme[0], xfceTheme[1]).Store(&theme); err == nil {
		src.put(xfceTheme, theme)
	}
	var kde func() (color.NRGBA, bool)
	if onKDE() {
		kde = kdeAccent
		_ = conn.AddMatchSignal(
			dbus.WithMatchObjectPath(kconfigPath),
			dbus.WithMatchInterface(kconfigIface),
			dbus.WithMatchMember("ConfigChanged"),
		)
	}
	m.set(src.scheme())
	m.setAccent(src.accent(kde))

	_ = conn.AddMatchSignal(
		dbus.WithMatchObjectPath(portalPath),
		dbus.WithMatchInterface(portalSettings),
		dbus.WithMatchMember("SettingChanged"),
	)
	_ = conn.AddMatchSignal(
		dbus.WithMatchObjectPath(xfconfPath),
		dbus.WithMatchInterface(xfconfIface),
		dbus.WithMatchMember("PropertyChanged"),
	)
	// The portal's signals of a change come before kdeglobals is replaced
	// (Plasma 6.8: they came 12 ms before the new colors could be read,
	// ConfigChanged about when they could), which is why Plasma's portal
	// tells of a new accent with the one before it. kdeglobals is read
	// again once the signals have stopped for kdeSettle.
	var (
		settleMu sync.Mutex
		settle   *time.Timer
		closed   bool
	)
	accentChanged := func() {
		m.setAccent(src.accent(kde))
		if kde == nil {
			return
		}
		settleMu.Lock()
		defer settleMu.Unlock()
		if closed {
			return
		}
		if settle != nil {
			settle.Stop()
		}
		settle = time.AfterFunc(kdeSettle, func() { m.setAccent(src.accent(kde)) })
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go func() {
		// The channel is closed when the connection is closed.
		for sig := range signals {
			if sig.Name == kconfigSignal {
				accentChanged()
				continue
			}
			if len(sig.Body) != 3 {
				continue
			}
			ns, _ := sig.Body[0].(string)
			key, _ := sig.Body[1].(string)
			value, ok := sig.Body[2].(dbus.Variant)
			if !ok {
				continue
			}
			k := [2]string{ns, key}
			switch {
			case sig.Name == portalSettings+".SettingChanged" && (k == portalScheme || k == gnomeScheme || k == gnomeTheme),
				sig.Name == xfconfIface+".PropertyChanged" && k == xfceTheme:
				src.put(k, value)
				m.set(src.scheme())
			case sig.Name == portalSettings+".SettingChanged" && k == portalAccent:
				src.put(k, value)
				accentChanged()
			}
		}
	}()
	return func() {
		conn.Close()
		settleMu.Lock()
		closed = true
		if settle != nil {
			settle.Stop()
		}
		settleMu.Unlock()
	}
}

// readPortalSetting reads a setting with ReadOne, falling back to the
// deprecated Read that wraps the value in an extra variant.
func readPortalSetting(obj dbus.BusObject, key [2]string) (dbus.Variant, bool) {
	var v dbus.Variant
	if err := obj.Call(portalSettings+".ReadOne", 0, key[0], key[1]).Store(&v); err == nil {
		return v, true
	}
	if err := obj.Call(portalSettings+".Read", 0, key[0], key[1]).Store(&v); err != nil {
		return dbus.Variant{}, false
	}
	if inner, ok := v.Value().(dbus.Variant); ok {
		return inner, true
	}
	return v, true
}
