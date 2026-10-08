// SPDX-License-Identifier: Unlicense OR MIT

// Package notify shows desktop notifications: through the freedesktop
// Notifications service on Linux and FreeBSD, the notify command on Haiku,
// and as the tray icon's balloon on Windows, which shows them as toasts.
package notify

import "unicode/utf8"

// Notification is one notification.
type Notification struct {
	Title, Body string
	Sound       bool
	// Tag names what the notification is about; a new one with the same tag
	// takes the place of the old.
	Tag string
	// Open runs on a goroutine of its own when the notification is clicked,
	// with the activation token the desktop gave, if any.
	Open func(token string)
}

// Balloon is the tray icon, which shows notifications on Windows.
type Balloon interface {
	Notify(title, text string, sound bool) error
}

// Notifier shows notifications. Show never blocks for long and fails
// quietly: a desktop without notifications is not an error to the user.
type Notifier interface {
	Show(Notification)
	// Clicked is for the tray to call when its balloon is clicked.
	Clicked(token string)
	Close()
}

// clip shortens s to n runes, with an ellipsis.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
