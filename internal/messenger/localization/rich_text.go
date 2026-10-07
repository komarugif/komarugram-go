// SPDX-License-Identifier: Unlicense OR MIT

package localization

func init() {
	TelegramKeys["text.copy_code"] = "lng_code_block_header_copy"
	TelegramKeys["text.copied"] = "lng_text_copied"
	TelegramKeys["rich.photo"] = "lng_in_dlg_photo"
	TelegramKeys["rich.video"] = "lng_in_dlg_video"
	TelegramKeys["rich.album"] = "lng_in_dlg_album"
	TelegramKeys["rich.audio"] = "lng_in_dlg_audio_file"
	TelegramKeys["rich.file"] = "lng_in_dlg_file"
	TelegramKeys["rich.map"] = "lng_maps_point"
	TelegramKeys["rich.table"] = "lng_in_dlg_table"
	TelegramKeys["rich.click_to_view"] = "lng_iv_click_to_view"
	TelegramKeys["rich.unsupported_title"] = "lng_unsupported_block_title"
	TelegramKeys["rich.unsupported_text"] = "lng_unsupported_block_text"
	TelegramKeys["rich.anchor_missing"] = "lng_iv_not_found_in_message"
	TelegramKeys["rich.show_more"] = "lng_view_button_full_article"
	TelegramKeys["rich.stop_draft"] = "lng_stop_button"
	TelegramKeys["rich.search"] = "lng_dlg_filter"
	TelegramKeys["rich.share"] = "lng_iv_share"
	TelegramKeys["rich.open_file"] = "lng_markdown_preview_open_file"
	TelegramKeys["rich.markdown_cant"] = "lng_markdown_preview_cant"
	TelegramKeys["rich.open_in_browser"] = "lng_iv_open_in_browser"
	TelegramKeys["rich.instant_view"] = "lng_view_button_iv"
	TelegramKeys["rich.save_html"] = "lng_context_save_html"
	TelegramKeys["rich.html_saved"] = "lng_export_html_saved_to"
	TelegramKeys["rich.html_failed"] = "lng_export_html_failed"
	TelegramKeys["rich.html_media_missing"] = "lng_export_html_media_missing"
	TelegramKeys["rich.html_embed"] = "lng_export_html_embed"
	for key, texts := range map[string][2]string{
		"text.copy_code": {"Копировать", "Copy"},
		"text.copied":    {"Текст скопирован", "Text copied"},
		// Telegram uses icons for these actions; these are accessibility labels
		// and visible text for our shared text buttons.
		"text.expand_quote":   {"Развернуть цитату", "Expand quote"},
		"text.collapse_quote": {"Свернуть цитату", "Collapse quote"},
		// What a rich message without text shows (model.RichPage.Fallback).
		"rich.photo": {"Фото", "Photo"},
		"rich.video": {"Видео", "Video"},
		"rich.album": {"Альбом", "Album"},
		"rich.audio": {"Аудиофайл", "Audio file"},
		"rich.file":  {"Файл", "File"},
		"rich.map":   {"Геопозиция", "Location"},
		"rich.table": {"Таблица", "Table"},
		// What an article names rather than shows.
		"rich.click_to_view":     {"Нажмите, чтобы посмотреть", "Click to View"},
		"rich.unsupported_title": {"Неподдерживаемый блок", "Unsupported Block"},
		"rich.unsupported_text":  {"Обновите Telegram, чтобы увидеть эту часть сообщения", "Update Telegram to view this part of the message"},
		// A link to an anchor the article does not have; the button under
		// an article Telegram sent cut short.
		"rich.anchor_missing": {"Похоже, эта ссылка недействительна.", "This link appears to be invalid."},
		"rich.show_more":      {"Показать ещё", "Show more"},
		// The button that stops a draft a bot streams.
		"rich.stop_draft": {"Остановить", "Stop"},
		// The article window's steps through the anchors it went to.
		"rich.back":    {"Назад", "Back"},
		"rich.forward": {"Вперёд", "Forward"},
		// Its search, sharing and zoom.
		"rich.search": {"Поиск", "Search"},
		"rich.share":  {"Поделиться", "Share"},
		// The viewer of Markdown files.
		"rich.open_file":     {"Открыть файл", "Open file"},
		"rich.markdown_cant": {"Не удалось показать этот файл Markdown", "Can't preview this Markdown file"},
		// Instant View.
		"rich.open_in_browser": {"Открыть в браузере", "Open in Browser"},
		"rich.instant_view":    {"Мгновенный просмотр", "Instant View"},
		"rich.zoom_in":         {"Увеличить", "Zoom in"},
		"rich.zoom_out":        {"Уменьшить", "Zoom out"},
		// Saving a rich message as HTML. Telegram Desktop's toast links
		// its downloads folder; this one says where the page is.
		"rich.save_html":          {"Сохранить как HTML", "Save as HTML"},
		"rich.html_saved":         {"Сообщение сохранено: {path}", "Message was saved to {path}"},
		"rich.html_failed":        {"Не удалось сохранить сообщение как HTML.", "Could not save the message as HTML."},
		"rich.html_media_missing": {"Медиа недоступно", "Media unavailable"},
		"rich.html_embed":         {"Встроенное содержимое", "Embedded content"},
	} {
		russian[key], english[key] = texts[0], texts[1]
	}
}
