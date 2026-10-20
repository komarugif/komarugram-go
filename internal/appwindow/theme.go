// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

// Theme is the color theme the user picked in the application, for the
// windows the host draws itself, such as the one of a panic.
type Theme int32

const (
	// ThemeAuto follows the system's color scheme.
	ThemeAuto Theme = iota
	ThemeLight
	ThemeDark
)

// SetTheme tells the host the theme the user picked. The host's own
// windows redraw in it.
func (h *Host) SetTheme(t Theme) {
	if Theme(h.theme.Swap(int32(t))) == t {
		return
	}
	h.mu.Lock()
	windows := make([]*Window, 0, len(h.windows))
	for w := range h.windows {
		windows = append(windows, w)
	}
	h.mu.Unlock()
	for _, w := range windows {
		w.Invalidate()
	}
}

// SetTheme is Host.SetTheme for the host of w.
func (w *Window) SetTheme(t Theme) {
	if w.host != nil {
		w.host.SetTheme(t)
	}
}

// Theme is the theme the user picked, ThemeAuto before they say.
func (h *Host) Theme() Theme {
	if h == nil {
		return ThemeAuto
	}
	return Theme(h.theme.Load())
}
