// SPDX-License-Identifier: Unlicense OR MIT

package sendfiles

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers PNG for image.Decode
	"os"

	// GIF's first frame is a preview and a photo; the others are what people
	// have in their folders.
	_ "image/gif"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	"komarugram/pkg/program"
)

// jpegQuality is Telegram Desktop's, for the photos it recompresses.
const jpegQuality = 94

// maxPhotoBytes is what Telegram takes as a photo; a larger file is
// compressed again.
const maxPhotoBytes = 10 << 20

// Photo is a picture ready to be uploaded as a photo.
type Photo struct {
	JPEG          []byte
	Width, Height int
}

// PreparePhoto makes the image at path a photo: turned as its Exif asks,
// scaled down to fit a square of 1280 pixels a side, or 2560 for high
// quality, with alpha flattened on white and saved as JPEG. A JPEG that
// needs none of that is sent as it is.
func PreparePhoto(path string, highQuality bool) (Photo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Photo{}, err
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Photo{}, err
	}
	turn := 1
	if format == "jpeg" {
		turn = orientationOf(data)
	}
	side := StandardSide
	if highQuality {
		side = HighQualitySide
	}
	// Scaled first, then turned: the smaller picture is the cheaper to turn.
	b := img.Bounds()
	scaled := b.Dx() > side || b.Dy() > side
	img = orient(fit(img, side), turn)
	b = img.Bounds()
	if !scaled && format == "jpeg" && turn == 1 && len(data) <= maxPhotoBytes {
		return Photo{JPEG: data, Width: b.Dx(), Height: b.Dy()}, nil
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flatten(img), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return Photo{}, err
	}
	return Photo{JPEG: out.Bytes(), Width: b.Dx(), Height: b.Dy()}, nil
}

// Thumbnail decodes the image at path, turned as it is shown, and scales it
// down to fit a square of side pixels.
func Thumbnail(path string, side int) (*image.RGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	turn := 1
	if format == "jpeg" {
		turn = orientationOf(data)
	}
	return toRGBA(orient(fit(img, side), turn)), nil
}

// thumbSide and thumbQuality are Telegram Desktop's, for the thumbnail a
// document is sent with.
const (
	thumbSide    = 320
	thumbQuality = 87
)

// CoverThumbnail decodes the picture of music, data, and scales it down to
// fit a square of side pixels.
func CoverThumbnail(data []byte, side int) (*image.RGBA, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if b := img.Bounds(); !ValidDimensions(b.Dx(), b.Dy()) {
		return nil, errors.New("the picture has no shape a thumbnail takes")
	}
	return toRGBA(fit(img, side)), nil
}

// DocumentThumbnail makes the picture of music, data, the thumbnail it is
// sent with: at most 320 pixels a side, as JPEG.
func DocumentThumbnail(data []byte) ([]byte, error) {
	img, err := CoverThumbnail(data, thumbSide)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flatten(img), &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// VideoThumbnail asks ffmpeg for the first picture of the video at path,
// scaled to fit a square of side pixels.
func VideoThumbnail(ctx context.Context, ffmpeg, path string, side int) (*image.RGBA, error) {
	if ffmpeg == "" {
		return nil, errors.New("no ffmpeg")
	}
	cmd := program.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path, "-frames:v", "1",
		"-vf", "scale='min("+itoa(side)+",iw)':-2", "-f", "image2pipe", "-c:v", "png", "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, err
	}
	return toRGBA(img), nil
}

func itoa(n int) string {
	var digits [20]byte
	i := len(digits)
	for {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			return string(digits[i:])
		}
	}
}

// fit scales img down to fit a square of side pixels, keeping its shape; an
// image that fits is left alone.
func fit(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	if w >= h {
		h = max(1, h*side/w)
		w = side
	} else {
		w = max(1, w*side/h)
		h = side
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// flatten lays img on white, as JPEG has no alpha.
func flatten(img image.Image) image.Image {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return img
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok && rgba.Rect.Min == (image.Point{}) {
		return rgba
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// orient turns img as Exif's orientation o asks: 2 mirrors it, 3 turns it
// upside down, 4 flips it, 5 to 8 turn it a quarter.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
