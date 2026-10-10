package localization

func init() {
	ru := map[string]string{
		"browser":            "Браузер для Mini Apps",
		"found":              "Найден",
		"not_found":          "Не найден на этом компьютере.",
		"custom":             "Указан вручную",
		"choose":             "Указать файл…",
		"reset":              "Искать автоматически",
		"rejected":           "Файл не подошёл",
		"not_executable":     "это не исполняемый файл",
		"wrong":              "программа не отвечает как {program}",
		"not_browser":        "это не браузер на основе Chromium и не Firefox",
		"old_firefox":        "нужен Firefox 140 или новее",
		"unsupported_system": "на этой системе он пока не поддерживается",
		"old_version":        "версия {version} не поддерживается, нужна {want}",
		"failed":             "не удалось проверить",
		"snap":               "плееры из snap пока не поддерживаются",
	}
	en := map[string]string{
		"browser":            "Browser for Mini Apps",
		"found":              "Found",
		"not_found":          "Not found on this computer.",
		"custom":             "Set by you",
		"choose":             "Choose file…",
		"reset":              "Find automatically",
		"rejected":           "The file was not accepted",
		"not_executable":     "it is not an executable file",
		"wrong":              "the program does not answer as {program}",
		"not_browser":        "it is neither a Chromium-based browser nor Firefox",
		"old_firefox":        "Firefox 140 or later is needed",
		"unsupported_system": "it is not supported on this system yet",
		"old_version":        "version {version} is not supported, {want} is needed",
		"failed":             "could not check it",
		"snap":               "players installed as snaps are not supported yet",
	}
	for k, v := range ru {
		russian["program."+k] = v
	}
	for k, v := range en {
		english["program."+k] = v
	}
}
