// SPDX-License-Identifier: Unlicense OR MIT

package localization

// Texts of the settings of the overlays (the floating composer, the context
// menus and the toasts), which are this client's own.
func init() {
	for key, texts := range map[string][2]string{
		"settings.overlays":                   {"Прозрачность и размытие", "Transparency and blur"},
		"settings.overlays_transparency":      {"Прозрачность оверлеев", "Overlay transparency"},
		"settings.overlays_transparency_hint": {"Панели, под которыми включено размытие, пропускают столько размытого фона; остальные непрозрачны.", "Overlays that blur what is behind them let this much of it show through; the others are opaque."},
		"settings.window_blur":                {"Размытие под основным окном", "Blur behind the main window"},
		"settings.window_transparency":        {"Прозрачность основного окна", "Main window transparency"},
		"settings.window_transparency_hint":   {"Фон бокового меню, списка чатов и заголовка чата. Текст и аватарки остаются непрозрачными. Работает на Wayland, Windows и macOS. Размытие на Wayland зависит от композитора, а на Windows 10 и 11 окно с ним рисует свою рамку вместо системной.", "The backgrounds of the sidebar, chat list and chat header. Text and avatars stay opaque. Works on Wayland, Windows and macOS. On Wayland blur depends on the compositor; on Windows 10 and 11 a window with it draws its own frame in place of the system one."},
		"settings.menus_blur":                 {"Размытие под контекстными меню", "Blur behind the context menus"},
		"settings.toasts_blur":                {"Размытие под уведомлениями", "Blur behind the toasts"},
		"settings.overlays_classic":           {"У классической панели ввода нет фона под собой: её размытие включается вместе с плавающей.", "The classic composer has no content behind it: its blur applies once it floats."},
	} {
		russian[key], english[key] = texts[0], texts[1]
	}
}
