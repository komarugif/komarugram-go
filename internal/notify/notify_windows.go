// SPDX-License-Identifier: Unlicense OR MIT

package notify

// New returns the notifier of this system; tray may be nil, and then no
// notification shows.
func New(app string, tray Balloon) Notifier { return newWinToast(app, tray) }
