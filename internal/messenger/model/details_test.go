// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"testing"
	"time"
)

func TestRegisteredAround(t *testing.T) {
	first, last := registrations[0], registrations[len(registrations)-1]
	for _, c := range []struct {
		user     int64
		from, to string
		how      Registration
	}{
		{first.id - 1, "2013-09", "2013-09", RegisteredBefore},
		{last.id + 1, "2026-08", "2026-08", RegisteredAfter},
		// An account whose day is known: 2021-10-12.
		{1974255900, "2021-09", "2021-11", RegisteredAbout},
	} {
		at, how, _ := RegisteredAround(c.user)
		if got := at.UTC().Format("2006-01"); got < c.from || got > c.to || how != c.how {
			t.Errorf("user %d: %s %d, want %s to %s, %d", c.user, got, how, c.from, c.to, c.how)
		}
	}
	// Halfway between two points of the table is halfway between their days.
	a, b := registrations[len(registrations)/2], registrations[len(registrations)/2+1]
	mid, _, _ := RegisteredAround((a.id + b.id) / 2)
	if d := mid.Sub(time.Unix((a.date+b.date)/2, 0)); d > time.Hour || d < -time.Hour {
		t.Errorf("not halfway: %v", d)
	}
}

// TestRegistrationsGoForward walks the table: a larger id is never older.
func TestRegistrationsGoForward(t *testing.T) {
	var previous time.Time
	for user := registrations[0].id; user < registrations[len(registrations)-1].id; user += 1e6 {
		at, _, _ := RegisteredAround(user)
		if at.Before(previous) {
			t.Fatalf("user %d is older than one before it: %v, %v", user, at, previous)
		}
		previous = at
	}
}

// TestRegistrationSpread tells far from the points known: between the
// accounts of 2017-07 and of 2018-06 nothing is known, the year at best,
// unlike in 2022, which the known accounts fill.
func TestRegistrationSpread(t *testing.T) {
	if _, _, spread := RegisteredAround(500000000); spread < 92*24*time.Hour {
		t.Errorf("between points a year apart: %v", spread)
	}
	if _, _, spread := RegisteredAround(5500000000); spread > 92*24*time.Hour || spread <= 0 {
		t.Errorf("in 2022: %v", spread)
	}
}
