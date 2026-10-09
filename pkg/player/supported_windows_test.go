// SPDX-License-Identifier: Unlicense OR MIT

package player

import "testing"

func TestAtLeast81(t *testing.T) {
	for _, c := range []struct {
		major, minor uint32
		want         bool
	}{{6, 1, false}, {6, 2, false}, {6, 3, true}, {10, 0, true}} {
		if got := atLeast81(c.major, c.minor); got != c.want {
			t.Errorf("%d.%d: %v", c.major, c.minor, got)
		}
	}
}
