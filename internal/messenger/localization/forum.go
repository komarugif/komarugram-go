// SPDX-License-Identifier: Unlicense OR MIT

package localization

// Texts of forums, whose chats are lists of topics.
func init() {
	for key, telegram := range map[string]string{
		"forum.messages": "lng_forum_messages", "forum.messages_none": "lng_forum_no_messages",
		"forum.loading": "lng_contacts_loading",
	} {
		TelegramKeys[key] = telegram
	}
	for key, value := range map[string]string{
		"forum.no_topics":     "В этой группе пока нет тем",
		"forum.no_messages":   "Здесь пока нет сообщений…",
		"forum.messages#one":  "{count} сообщение",
		"forum.messages#few":  "{count} сообщения",
		"forum.messages#many": "{count} сообщений",
		"forum.messages_none": "Нет сообщений",
		"forum.loading":       "Загрузка…",
	} {
		russian[key] = value
	}
	for key, value := range map[string]string{
		"forum.no_topics":      "There are no topics in this group yet",
		"forum.no_messages":    "No messages here yet...",
		"forum.messages#one":   "{count} message",
		"forum.messages#other": "{count} messages",
		"forum.messages_none":  "No messages",
		"forum.loading":        "Loading...",
	} {
		english[key] = value
	}
}
