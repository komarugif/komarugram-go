// SPDX-License-Identifier: Unlicense OR MIT

package localization

// Texts of the power saving settings that are this client's own.
func init() {
	for key, texts := range map[string][2]string{
		"settings.pause_inactive":      {"Останавливать анимации в неактивном окне", "Pause animations in an inactive window"},
		"settings.pause_inactive_hint": {"Стикеры, GIF, анимированные эмодзи и видеоаватарки замирают, пока окно не в фокусе, как в Telegram Desktop.", "Stickers, GIFs, animated emoji and video avatars stand still while the window is out of focus, as in Telegram Desktop."},
	} {
		russian[key], english[key] = texts[0], texts[1]
	}
}
