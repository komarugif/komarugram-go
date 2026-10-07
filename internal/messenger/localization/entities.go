// SPDX-License-Identifier: Unlicense OR MIT

package localization

import "strconv"

// Texts of the entities of messages a click acts on, and of the dates they
// write, by Telegram Desktop's keys where it has them.
func init() {
	keys := map[string]string{
		"date.month_day": "lng_month_day", "date.month_day_year": "lng_month_day_year",
		"date.now":           "lng_date_relative_now",
		"date.seconds_ago":   "lng_date_relative_seconds_ago",
		"date.minutes_ago":   "lng_date_relative_minutes_ago",
		"date.hours_ago":     "lng_date_relative_hours_ago",
		"date.days_ago":      "lng_date_relative_days_ago",
		"date.months_ago":    "lng_date_relative_months_ago",
		"date.years_ago":     "lng_date_relative_years_ago",
		"date.in_seconds":    "lng_date_relative_in_seconds",
		"date.in_minutes":    "lng_date_relative_in_minutes",
		"date.in_hours":      "lng_date_relative_in_hours",
		"date.in_days":       "lng_date_relative_in_days",
		"date.in_months":     "lng_date_relative_in_months",
		"date.in_years":      "lng_date_relative_in_years",
		"date.copy":          "lng_context_copy_date",
		"date.copied":        "lng_date_copied",
		"date.calendar":      "lng_context_add_to_calendar",
		"phone.copy":         "lng_profile_copy_phone",
		"phone.loading":      "lng_contacts_loading",
		"phone.profile":      "lng_context_view_profile",
		"phone.not_telegram": "lng_menu_not_contact",
		"card.copy":          "lng_context_bank_card_copy",
		"card.copied":        "lng_context_bank_card_copied",
	}
	// Months are 1 to 12, weekdays 0 (Sunday) to 6, as Go counts them.
	for m := 1; m <= 12; m++ {
		n := strconv.Itoa(m)
		keys["date.month_short."+n] = "lng_month" + n + "_small"
		keys["date.month_of."+n] = "lng_month_day" + n
	}
	for d, name := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		keys["date.weekday_full."+strconv.Itoa(d)] = "lng_hours_" + name
	}
	for key, telegram := range keys {
		TelegramKeys[key] = telegram
	}
	ru := map[string]string{
		"date.month_day": "{day} {month}", "date.month_day_year": "{day} {month} {year}",
		"date.now":              "сейчас",
		"date.seconds_ago#one":  "{count} секунду назад",
		"date.seconds_ago#few":  "{count} секунды назад",
		"date.seconds_ago#many": "{count} секунд назад",
		"date.minutes_ago#one":  "{count} минуту назад",
		"date.minutes_ago#few":  "{count} минуты назад",
		"date.minutes_ago#many": "{count} минут назад",
		"date.hours_ago#one":    "{count} час назад",
		"date.hours_ago#few":    "{count} часа назад",
		"date.hours_ago#many":   "{count} часов назад",
		"date.days_ago#one":     "{count} день назад",
		"date.days_ago#few":     "{count} дня назад",
		"date.days_ago#many":    "{count} дней назад",
		"date.months_ago#one":   "{count} месяц назад",
		"date.months_ago#few":   "{count} месяца назад",
		"date.months_ago#many":  "{count} месяцев назад",
		"date.years_ago#one":    "{count} год назад",
		"date.years_ago#few":    "{count} года назад",
		"date.years_ago#many":   "{count} лет назад",
		"date.in_seconds#one":   "через {count} секунду",
		"date.in_seconds#few":   "через {count} секунды",
		"date.in_seconds#many":  "через {count} секунд",
		"date.in_minutes#one":   "через {count} минуту",
		"date.in_minutes#few":   "через {count} минуты",
		"date.in_minutes#many":  "через {count} минут",
		"date.in_hours#one":     "через {count} час",
		"date.in_hours#few":     "через {count} часа",
		"date.in_hours#many":    "через {count} часов",
		"date.in_days#one":      "через {count} день",
		"date.in_days#few":      "через {count} дня",
		"date.in_days#many":     "через {count} дней",
		"date.in_months#one":    "через {count} месяц",
		"date.in_months#few":    "через {count} месяца",
		"date.in_months#many":   "через {count} месяцев",
		"date.in_years#one":     "через {count} год",
		"date.in_years#few":     "через {count} года",
		"date.in_years#many":    "через {count} лет",
		"date.copy":             "Копировать дату",
		"date.copied":           "Дата скопирована.",
		"date.calendar":         "Добавить в календарь",
		"date.calendar_failed":  "Не удалось открыть событие в календаре",
		"phone.copy":            "Копировать номер",
		"phone.loading":         "Загрузка…",
		"phone.profile":         "Открыть профиль",
		"phone.not_telegram":    "Этого номера нет в Telegram",
		"card.copy":             "Копировать номер карты",
		"card.copied":           "Номер карты скопирован.",
		"date.weekday_full.0":   "воскресенье",
		"date.weekday_full.1":   "понедельник",
		"date.weekday_full.2":   "вторник",
		"date.weekday_full.3":   "среда",
		"date.weekday_full.4":   "четверг",
		"date.weekday_full.5":   "пятница",
		"date.weekday_full.6":   "суббота",
	}
	en := map[string]string{
		"date.month_day": "{month} {day}", "date.month_day_year": "{month} {day}, {year}",
		"date.now":               "now",
		"date.seconds_ago#one":   "{count} second ago",
		"date.seconds_ago#other": "{count} seconds ago",
		"date.minutes_ago#one":   "{count} minute ago",
		"date.minutes_ago#other": "{count} minutes ago",
		"date.hours_ago#one":     "{count} hour ago",
		"date.hours_ago#other":   "{count} hours ago",
		"date.days_ago#one":      "{count} day ago",
		"date.days_ago#other":    "{count} days ago",
		"date.months_ago#one":    "{count} month ago",
		"date.months_ago#other":  "{count} months ago",
		"date.years_ago#one":     "{count} year ago",
		"date.years_ago#other":   "{count} years ago",
		"date.in_seconds#one":    "in {count} second",
		"date.in_seconds#other":  "in {count} seconds",
		"date.in_minutes#one":    "in {count} minute",
		"date.in_minutes#other":  "in {count} minutes",
		"date.in_hours#one":      "in {count} hour",
		"date.in_hours#other":    "in {count} hours",
		"date.in_days#one":       "in {count} day",
		"date.in_days#other":     "in {count} days",
		"date.in_months#one":     "in {count} month",
		"date.in_months#other":   "in {count} months",
		"date.in_years#one":      "in {count} year",
		"date.in_years#other":    "in {count} years",
		"date.copy":              "Copy Date",
		"date.copied":            "Date copied to clipboard.",
		"date.calendar":          "Add to Calendar",
		"date.calendar_failed":   "Could not open the event in a calendar",
		"phone.copy":             "Copy Phone Number",
		"phone.loading":          "Loading...",
		"phone.profile":          "View profile",
		"phone.not_telegram":     "This number is not on Telegram",
		"card.copy":              "Copy Card Number",
		"card.copied":            "Card number copied to clipboard.",
		"date.weekday_full.0":    "Sunday",
		"date.weekday_full.1":    "Monday",
		"date.weekday_full.2":    "Tuesday",
		"date.weekday_full.3":    "Wednesday",
		"date.weekday_full.4":    "Thursday",
		"date.weekday_full.5":    "Friday",
		"date.weekday_full.6":    "Saturday",
	}
	for i, m := range [][2][12]string{{
		{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"},
		{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"},
	}, {
		{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
		{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	}} {
		target := ru
		if i == 1 {
			target = en
		}
		for j := range 12 {
			n := strconv.Itoa(j + 1)
			target["date.month_short."+n], target["date.month_of."+n] = m[0][j], m[1][j]
		}
	}
	for key, value := range ru {
		russian[key] = value
	}
	for key, value := range en {
		english[key] = value
	}
}
