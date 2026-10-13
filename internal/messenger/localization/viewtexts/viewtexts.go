// SPDX-License-Identifier: Unlicense OR MIT

// Package viewtexts words the views of other packages, which take their
// texts as Strings, from a catalog.
package viewtexts

import (
	"gio-mw/exp/powersave"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// Motion are the texts of the animations settings (motion.View).
func Motion(l localization.Catalog) motion.Strings {
	return motion.Strings{
		Title: l.T("animations.title"),
		Modes: map[powersave.Mode]string{
			powersave.ModeAuto: l.T("animations.mode_auto"),
			powersave.ModeOn:   l.T("animations.mode_on"),
			powersave.ModeOff:  l.T("animations.mode_off"),
		},
		LowBattery: l.T("animations.low_battery"),
		Never:      l.T("animations.never"),
		Status:     l.T("animations.status"),
		On:         l.T("animations.on"),
		Off:        l.T("animations.off"),
		NoBattery:  l.T("animations.no_battery"),
		Battery:    l.T("animations.battery"),
		Plugged:    l.T("animations.plugged"),
		Unplugged:  l.T("animations.unplugged"),
	}
}

// MiniApps are the texts of the Mini Apps storage settings
// (miniappprefs.View).
func MiniApps(l localization.Catalog) miniappprefs.Strings {
	return miniappprefs.Strings{
		Title: l.T("miniapp.title"),
		Storage: map[miniapp.Storage]string{
			miniapp.Ephemeral: l.T("miniapp.storage_ephemeral"),
			miniapp.PerApp:    l.T("miniapp.storage_per_app"),
			miniapp.Shared:    l.T("miniapp.storage_shared"),
		},
		Hints: map[miniapp.Storage]string{
			miniapp.Ephemeral: l.T("miniapp.hint_ephemeral"),
			miniapp.PerApp:    l.T("miniapp.hint_per_app"),
			miniapp.Shared:    l.T("miniapp.hint_shared"),
		},
		Browser:  l.T("miniapp.browser"),
		NoneSeen: l.T("miniapp.none_seen"),
	}
}
