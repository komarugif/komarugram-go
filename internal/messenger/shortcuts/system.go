// SPDX-License-Identifier: Unlicense OR MIT
package shortcuts

// SystemReport is advisory: a desktop can intercept input before Gio receives it.
// In particular, a negative KDE query is not an all-desktop/physical-key proof.
type SystemReport struct {
	KDE           bool
	PortalVersion uint32
	Checked       map[Chord]bool
	Occupied      map[Chord]bool
}
