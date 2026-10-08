// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"maps"
	"testing"
)

// TestHaikuEnv checks that a messenger started without the Desktop's
// environment, as the notification server starts it, finds the Desktop's
// directories, and that what the environment names is kept.
func TestHaikuEnv(t *testing.T) {
	for _, c := range []struct {
		env, want map[string]string
	}{
		{map[string]string{}, map[string]string{"HOME": "/boot/home", "XDG_CONFIG_HOME": "/boot/home/config/settings", "XDG_CACHE_HOME": "/boot/home/config/cache"}},
		{map[string]string{"HOME": "/boot/home"}, map[string]string{"XDG_CONFIG_HOME": "/boot/home/config/settings", "XDG_CACHE_HOME": "/boot/home/config/cache"}},
		{map[string]string{"HOME": "/boot/home", "XDG_CONFIG_HOME": "/x", "XDG_CACHE_HOME": "/y"}, map[string]string{}},
	} {
		if got := haikuEnv(func(k string) string { return c.env[k] }); !maps.Equal(got, c.want) {
			t.Errorf("env %v: set %v, want %v", c.env, got, c.want)
		}
	}
}
