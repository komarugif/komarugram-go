// SPDX-License-Identifier: Unlicense OR MIT

package sendfiles

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// A profile's photo is the square in the middle of the picture, scaled down.
func TestPrepareProfilePhotoIsSquare(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3000, 1000))
	for y := range 1000 {
		for x := range 3000 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 1000 && x < 2000 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	path := filepath.Join(t.TempDir(), "wide.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	p, err := PrepareProfilePhoto(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1000 || p.Height != 1000 {
		t.Fatalf("%dx%d", p.Width, p.Height)
	}
	out, err := jpeg.Decode(bytes.NewReader(p.JPEG))
	if err != nil {
		t.Fatal(err)
	}
	// The middle is blue, edges included: the red sides are cut off.
	for _, x := range []int{2, 500, 997} {
		if r, _, b, _ := out.At(x, 500).RGBA(); r > 0x4000 || b < 0xc000 {
			t.Fatalf("at %d: r=%x b=%x", x, r, b)
		}
	}
}
