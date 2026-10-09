// SPDX-License-Identifier: Unlicense OR MIT

package localization

// Texts of the installation the sign-in offers, and of the removal. Keys
// ending in _linux are the texts of Linux, where the system's list of
// programs is the applications menu.
func init() {
	for key, texts := range map[string][2]string{
		"install.title":           {"Установить KomaruGram?", "Install KomaruGram?"},
		"install.body":            {"Программа скопирует себя в выбранную папку и появится в списке программ Windows, откуда её можно будет удалить. Можно и не устанавливать: этот файл работает как есть.", "The program copies itself into the folder chosen and appears in Windows' list of programs, where it can be uninstalled from. It need not be installed: this file works as it is."},
		"install.body_linux":      {"Программа скопирует себя в выбранную папку и появится в меню приложений, откуда её можно будет удалить. Можно и не устанавливать: этот файл работает как есть.", "The program copies itself into the folder chosen and appears in the applications menu, where it can be uninstalled from. It need not be installed: this file works as it is."},
		"install.folder":          {"Папка установки", "Installation folder"},
		"install.browse":          {"Обзор…", "Browse…"},
		"install.desktop":         {"Ярлык на рабочем столе", "Desktop shortcut"},
		"install.menu":            {"Пункт в меню «Пуск»", "Start menu entry"},
		"install.menu_linux":      {"Пункт в меню приложений", "Applications menu entry"},
		"install.admin":           {"Для этой папки Windows запросит права администратора.", "Windows will ask for the administrator's rights for this folder."},
		"install.admin_linux":     {"Нет прав на запись в эту папку. Выберите папку в своём домашнем каталоге.", "This folder cannot be written to. Choose a folder in your home directory."},
		"install.replace":         {"Установленная копия в этой папке будет заменена.", "The copy installed in this folder will be replaced."},
		"install.install":         {"Установить", "Install"},
		"install.installing":      {"Устанавливаем…", "Installing…"},
		"install.skip":            {"Не устанавливать", "Don't install"},
		"install.failed":          {"Не удалось установить: %s", "Could not install: %s"},
		"install.cancelled":       {"Установка отменена: права администратора не получены.", "Installation cancelled: the administrator's rights were not given."},
		"install.no_chooser":      {"Окно выбора папки недоступно: введите путь вручную.", "The folder chooser is unavailable: enter the path."},
		"uninstall.settings":      {"Удалить KomaruGram", "Uninstall KomaruGram"},
		"uninstall.settings_hint": {"Закрыть программу и открыть окно удаления", "Quit the program and open the uninstaller"},
		"uninstall.title":         {"Удаление KomaruGram", "Uninstall KomaruGram"},
		"uninstall.body":          {"Будут удалены программа из папки %s, её ярлыки и запись в списке программ.", "The program in %s, its shortcuts and its entry in the list of programs will be removed."},
		"uninstall.body_linux":    {"Будут удалены программа из папки %s, её ярлык и пункт в меню приложений.", "The program in %s, its launcher and its applications menu entry will be removed."},
		"uninstall.data":          {"Удалить также мои данные", "Also remove my data"},
		"uninstall.data_hint":     {"Аккаунты, сеансы, история, кэш и настройки на этом компьютере. Сеансы на серверах Telegram останутся активными: завершить их можно с другого устройства.", "Accounts, sessions, history, cache and settings on this computer. The sessions stay active on Telegram's servers: they can be ended from another device."},
		"uninstall.uninstall":     {"Удалить", "Uninstall"},
		"uninstall.uninstalling":  {"Удаляем…", "Uninstalling…"},
		"uninstall.cancel":        {"Отмена", "Cancel"},
		"uninstall.done":          {"KomaruGram удалён.", "KomaruGram has been uninstalled."},
		"uninstall.close":         {"Закрыть", "Close"},
		"uninstall.missing":       {"KomaruGram не установлен: в системе нет его записи.", "KomaruGram is not installed: the system has no entry of it."},
		"uninstall.failed":        {"Не удалось удалить: %s", "Could not uninstall: %s"},
		"uninstall.running":       {"Не удалось удалить: возможно, KomaruGram ещё запущен. Закройте его из трея и повторите.", "Could not uninstall: KomaruGram may still be running. Quit it from the tray and try again."},
		"uninstall.cancelled":     {"Удаление отменено: права администратора не получены.", "Uninstall cancelled: the administrator's rights were not given."},
	} {
		russian[key], english[key] = texts[0], texts[1]
	}
}
