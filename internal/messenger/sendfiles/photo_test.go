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

// segment is a JPEG segment of marker with payload.
func segment(marker byte, payload string) []byte {
	out := []byte{0xff, marker, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	return append(out, payload...)
}

// A JPEG sent as it is goes without what tells where, when and with what it was
// taken, and without the pictures after it; its colors' profile stays, and
// its picture is the same.
func TestPreparePhotoStripsMetadata(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	body := plain.Bytes()[2:]
	tagged := []byte{0xff, 0xd8}
	for _, s := range [][]byte{
		segment(0xe0, "JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"),
		segment(0xe1, "Exif\x00\x00MM\x00\x2a GPSLatitude 55.7558 Canon EOS R5"),
		segment(0xe1, "http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta CreateDate=2026>"),
		segment(0xe2, "ICC_PROFILE\x00\x01\x01display p3"),
		segment(0xe2, "MPF\x00II*\x00 second picture"),
		segment(0xed, "Photoshop 3.0\x008BIM city=Moscow"),
		segment(0xfe, "taken at home"),
	} {
		tagged = append(tagged, s...)
	}
	tagged = append(tagged, body...)
	// A phone's second picture, with its own Exif, after the end of the first.
	tagged = append(tagged, 0xff, 0xd8)
	tagged = append(tagged, segment(0xe1, "Exif\x00\x00 GPSLongitude 37.6173")...)
	tagged = append(tagged, body...)
	path := filepath.Join(t.TempDir(), "camera.jpg")
	if err := os.WriteFile(path, tagged, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := PreparePhoto(path, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"Exif", "GPS", "Canon", "xmpmeta", "MPF", "second picture", "Photoshop", "Moscow", "taken at home"} {
		if bytes.Contains(p.JPEG, []byte(gone)) {
			t.Errorf("%q is still there", gone)
		}
	}
	for _, kept := range []string{"JFIF", "ICC_PROFILE"} {
		if !bytes.Contains(p.JPEG, []byte(kept)) {
			t.Errorf("%q is gone", kept)
		}
	}
	if !bytes.HasSuffix(p.JPEG, body[len(body)-64:]) {
		t.Error("the picture's data is not what it was")
	}
	before, _ := jpeg.Decode(bytes.NewReader(plain.Bytes()))
	after, err := jpeg.Decode(bytes.NewReader(p.JPEG))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range []image.Point{{0, 0}, {31, 20}, {63, 47}} {
		if before.At(pt.X, pt.Y) != after.At(pt.X, pt.Y) {
			t.Fatalf("the picture differs at %v", pt)
		}
	}
}
