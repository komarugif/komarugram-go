// SPDX-License-Identifier: Unlicense OR MIT

package model

import "testing"

// Sync has nothing to share without GIFs or pins, and goes with the last of them.
func TestLocalPremiumSyncNeedsSomethingToShare(t *testing.T) {
	if (LocalPremium{Star: true, Media: true, Sync: true}).Consistent().Sync {
		t.Fatal("kept with nothing to share")
	}
	for _, l := range []LocalPremium{{GIFs: true, Sync: true}, {Pins: true, Sync: true}} {
		if !l.Consistent().Sync {
			t.Fatalf("%+v lost it", l)
		}
	}
}

// Ticking the GIFs with the channel in use offers to send them there, as ticking
// Sync does; nothing else asks.
func TestLocalPremiumAsksForChannel(t *testing.T) {
	for _, c := range []struct {
		name    string
		was, is LocalPremium
		asks    bool
	}{
		{"Sync ticked", LocalPremium{Pins: true}, LocalPremium{Pins: true, Sync: true}, true},
		{"GIFs ticked with Sync on", LocalPremium{Pins: true, Sync: true}, LocalPremium{Pins: true, GIFs: true, Sync: true}, true},
		{"GIFs ticked without it", LocalPremium{Pins: true}, LocalPremium{Pins: true, GIFs: true}, false},
		{"pins ticked with Sync on", LocalPremium{GIFs: true, Sync: true}, LocalPremium{GIFs: true, Pins: true, Sync: true}, false},
		{"another part", LocalPremium{GIFs: true, Sync: true}, LocalPremium{GIFs: true, Sync: true, Star: true}, false},
		{"Sync unticked", LocalPremium{GIFs: true, Sync: true}, LocalPremium{GIFs: true}, false},
	} {
		if got := c.is.AsksForChannel(c.was); got != c.asks {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
