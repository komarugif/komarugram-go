// SPDX-License-Identifier: Unlicense OR MIT

package appicon

import (
	"testing"
	"time"
)

func TestAutoNewYearSeason(t *testing.T) {
	cases := []struct {
		date time.Time
		want string
	}{
		{time.Date(2026, time.November, 30, 23, 59, 0, 0, time.UTC), Default},
		{time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC), NewYear},
		{time.Date(2026, time.December, 31, 23, 59, 0, 0, time.UTC), NewYear},
		{time.Date(2027, time.January, 1, 12, 0, 0, 0, time.UTC), NewYear},
		{time.Date(2027, time.January, 2, 0, 0, 0, 0, time.UTC), Default},
	}
	for _, tc := range cases {
		if got := Resolve(Auto, tc.date); got != tc.want {
			t.Errorf("Resolve(Auto, %s) = %q, want %q", tc.date.Format("2006-01-02"), got, tc.want)
		}
		if got := Resolve(Default, tc.date); got != Default {
			t.Errorf("Resolve(Default, %s) = %q, want %q", tc.date.Format("2006-01-02"), got, Default)
		}
		if got := Resolve(NewYear, tc.date); got != NewYear {
			t.Errorf("Resolve(NewYear, %s) = %q, want %q", tc.date.Format("2006-01-02"), got, NewYear)
		}
	}
}

func TestImages(t *testing.T) {
	sizes := []int{16, 32, 256}
	for _, name := range All {
		for i, im := range ImagesFor(name, sizes...) {
			size := sizes[i]
			if b := im.Bounds(); b.Dx() != size || b.Dy() != size {
				t.Fatalf("%q %dpx: bounds %v", name, size, b)
			}
			// The logo is a disc: its corners are transparent, its middle opaque.
			if a := im.NRGBAAt(0, 0).A; a != 0 {
				t.Errorf("%q %dpx: corner alpha %d, want transparent", name, size, a)
			}
			if a := im.NRGBAAt(size/2, size/2).A; a != 255 {
				t.Errorf("%q %dpx: middle alpha %d, want opaque", name, size, a)
			}
		}
	}
	for i, im := range TrayImages(sizes...) {
		size := sizes[i]
		if b := im.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("tray %dpx: bounds %v", size, b)
		}
		if a := im.NRGBAAt(0, 0).A; a != 0 {
			t.Errorf("tray %dpx: corner alpha %d, want transparent", size, a)
		}
	}
}
