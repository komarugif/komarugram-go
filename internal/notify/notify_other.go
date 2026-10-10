// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows && !haiku && !((linux && !android) || freebsd)

package notify

// New returns a notifier that shows nothing: notifications are not
// implemented on this system.
func New(app string, tray Balloon) Notifier { return newBalloon(nil) }
