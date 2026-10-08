// SPDX-License-Identifier: Unlicense OR MIT

// Package tray shows the application's icon in the system tray: a
// StatusNotifierItem on Linux and FreeBSD desktops, a notification area icon
// on Windows, an item of the Deskbar on Haiku. The icon opens the
// application on a click and offers a menu.
package tray

import "errors"

// ErrUnsupported is returned by Start where no tray is implemented.
var ErrUnsupported = errors.New("tray: not supported on this platform")

// Item is an entry of the icon's menu.
type Item struct {
	Label string
	// Action runs on a goroutine of its own when the item is chosen, with
	// the activation token the desktop provided, if any.
	Action func(token string)
	// Separator draws a line instead of an item; Label and Action are unused.
	Separator bool
}

type Options struct {
	// ID names the application to the desktop; it should not change.
	ID    string
	Title string
	// Activate runs on a goroutine of its own when the icon is clicked. On
	// Wayland, token is the XDG activation token that lets the application
	// raise a window; it is empty where there is none.
	Activate func(token string)
	// Notified runs on a goroutine of its own when the notification Notify
	// showed last is clicked.
	Notified func(token string)
	Items    []Item
}
