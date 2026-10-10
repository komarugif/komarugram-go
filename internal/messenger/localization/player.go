package localization

func init() {
	ru := map[string]string{
		"title":               "Внешний плеер",
		"subtitle":            "Плеер: {player}",
		"not_chosen":          "не выбран",
		"none":                "Не найдены ни mpv, ни VLC, ни браузер на Chromium или Firefox: установите что-то из них, чтобы смотреть видео и слушать аудио.",
		"not_installed":       "не установлен",
		"hint":                "Видео и аудио открываются в отдельном окне плеера.",
		"only":                "Найден только {player}. Установите второй плеер, чтобы выбирать.",
		"undecided":           "Приложение спросит при первом открытии видео и запомнит выбор.",
		"ask_option":          "Спросить при открытии видео",
		"ask":                 "Чем открывать видео?",
		"ask_body":            "Установлены {first} и {second}. Выбор можно изменить в Настройках, в разделе «Внешние интеграции».",
		"fallback":            "mpv и VLC не найдены, поэтому видео и аудио откроются в окне браузера, в котором работают Mini Apps. Некоторые форматы он может не проиграть.",
		"browser":             "Видео и аудио откроются в окне браузера, в котором работают Mini Apps. Некоторые форматы он может не проиграть.",
		"browser_cannot_play": "Браузер не может проиграть этот файл: вероятно, в нём нет нужного кодека. Установите mpv или VLC.",
	}
	en := map[string]string{
		"title":               "External player",
		"subtitle":            "Player: {player}",
		"not_chosen":          "not chosen",
		"none":                "Neither mpv, VLC nor a Chromium-based browser or Firefox was found: install one of them to watch videos and listen to audio.",
		"not_installed":       "not installed",
		"hint":                "Videos and audio open in a separate player window.",
		"only":                "Only {player} was found. Install the other player to choose.",
		"undecided":           "The app will ask when you first open a video and remember the choice.",
		"ask_option":          "Ask when a video opens",
		"ask":                 "Open videos in?",
		"ask_body":            "Both {first} and {second} are installed. You can change this later in Settings, under External integrations.",
		"fallback":            "Neither mpv nor VLC was found, so videos and audio open in a window of the browser Mini Apps run in. It may not play some formats.",
		"browser":             "Videos and audio open in a window of the browser Mini Apps run in. It may not play some formats.",
		"browser_cannot_play": "The browser cannot play this file: it probably lacks the codec. Install mpv or VLC.",
	}
	for k, v := range ru {
		russian["player."+k] = v
	}
	for k, v := range en {
		english["player."+k] = v
	}
}
