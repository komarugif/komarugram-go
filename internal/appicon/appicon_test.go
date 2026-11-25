// SPDX-License-Identifier: Unlicense OR MIT

package appicon

import "testing"

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
