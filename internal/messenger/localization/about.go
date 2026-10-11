// SPDX-License-Identifier: Unlicense OR MIT

package localization

// Texts of the settings' About section.
func init() {
	for key, texts := range map[string][2]string{
		"settings.about":           {"О приложении", "About"},
		"about.platform":           {"Платформа", "Platform"},
		"about.repository":         {"Исходный код на GitHub", "Source code on GitHub"},
		"about.maintainer_linux":   {"Сопровождающий версии для Linux", "Maintainer of the Linux version"},
		"about.maintainer_haiku":   {"Сопровождающий версии для Haiku", "Maintainer of the Haiku version"},
		"about.maintainer_windows": {"Сопровождающий версии для Windows", "Maintainer of the Windows version"},
		"about.maintainer_darwin":  {"Сопровождающий версии для macOS", "Maintainer of the macOS version"},
		"about.community":          {"Сообщество KomaruGram", "KomaruGram community"},
		"about.claude":             {"Claude помогает разрабатывать KomaruGram", "Claude helps build KomaruGram"},
		"about.community_failed":   {"Не удалось открыть: %s", "Could not open: %s"},
	} {
		russian[key], english[key] = texts[0], texts[1]
	}
}
