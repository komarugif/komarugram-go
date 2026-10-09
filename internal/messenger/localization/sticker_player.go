// SPDX-License-Identifier: Unlicense OR MIT

package localization

func init() {
	for k, v := range map[string]string{
		"title":       "Внутренний плеер WebM-стикеров",
		"wasm":        "WASM-песочница",
		"mp4_title":   "Внутренний плеер MP4-анимаций",
		"mp4_hint":    "GIF и анимированные аватары. Декодер для песочницы загружается с GitHub при первом использовании.",
		"audio_title": "Плеер голосовых сообщений и музыки",
		"audio_hint":  "WASM-песочница умеет играть Opus, MP3, FLAC, WAV и M4A без внешних программ; остальное открывается во внешнем плеере.",
		"external":    "Внешний плеер (mpv, VLC или браузер)",
		// Where mpv does not run, as on Windows 7.
		"external_no_mpv": "Внешний плеер (VLC или браузер)",
	} {
		russian["sticker_player."+k] = v
	}
	for k, v := range map[string]string{
		"title":           "Internal WebM sticker player",
		"wasm":            "WASM sandbox",
		"mp4_title":       "Internal MP4 animation player",
		"mp4_hint":        "GIFs and animated avatars. The sandbox's decoder is downloaded from GitHub when first used.",
		"audio_title":     "Player of voice messages and music",
		"audio_hint":      "The WASM sandbox can play Opus, MP3, FLAC, WAV and M4A without other programs; anything else opens in the external player.",
		"external":        "External player (mpv, VLC or the browser)",
		"external_no_mpv": "External player (VLC or the browser)",
	} {
		english["sticker_player."+k] = v
	}
}
