// SPDX-License-Identifier: Unlicense OR MIT

package main

// haikuEnv is what to set in the environment on Haiku, from get, for the
// messenger to find its settings and caches however it was started. Haiku
// keeps a user's in config/settings and config/cache of the home, which
// the Desktop names in XDG_CONFIG_HOME and XDG_CACHE_HOME. A shell over
// SSH sets neither, and Go looked in ~/.config; the notification server,
// which starts the messenger for a click, sets neither, nor HOME, and the
// messenger it started found no settings and no running messenger to hand
// the click to. The home of Haiku's one user is /boot/home.
func haikuEnv(get func(string) string) map[string]string {
	set := map[string]string{}
	home := get("HOME")
	if home == "" {
		home = "/boot/home"
		set["HOME"] = home
	}
	if get("XDG_CONFIG_HOME") == "" {
		set["XDG_CONFIG_HOME"] = home + "/config/settings"
	}
	if get("XDG_CACHE_HOME") == "" {
		set["XDG_CACHE_HOME"] = home + "/config/cache"
	}
	return set
}
