// SPDX-License-Identifier: Unlicense OR MIT

package sendfiles

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"os"

	"komarugram/internal/messenger/wasmmodule"
	"komarugram/pkg/h264"
	"komarugram/pkg/mp4"
	"komarugram/pkg/video"
	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// decodedThumbnail is the first picture of the video at path, scaled to fit a
// square of side pixels, by the client's decoders in their sandboxes: H.264 and
// MPEG-4 Part 2 in MP4, the first fetched if it is not there once the file is known
// to be one it decodes; VP9 in WebM, built in. Anything else, HEVC or VP8 say, has
// no picture.
func decodedThumbnail(ctx context.Context, path string, side int) (*image.RGBA, error) {
	c, err := video.ReadContainer(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// The decoders give the picture as it lies, of the shape it shows at: the turn
	// comes after.
	w, h := fitted(c.Width, c.Height, side)
	if c.Turn%180 != 0 {
		w, h = h, w
	}
	size := image.Pt(w, h)
	index, err := webm.ReadFirstPicture(f, info.Size())
	if err == nil {
		return vp9Thumbnail(ctx, index, size)
	}
	if !errors.Is(err, webm.ErrNotWebM) {
		return nil, err
	}
	file, err := mp4.DemuxAt(f, info.Size())
	if err != nil {
		return nil, err
	}
	module, err := wasmmodule.AVCDec.Load(ctx)
	if err != nil {
		return nil, err
	}
	rt, err := h264.NewRuntime(ctx, module)
	if err != nil {
		return nil, err
	}
	defer rt.Close(context.Background())
	v, err := rt.OpenFile(ctx, file)
	if err != nil {
		return nil, err
	}
	defer v.Close(context.Background())
	if _, err := v.Next(ctx); err != nil {
		return nil, err
	}
	return toRGBA(orient(v.Picture(size), exifTurn(c.Turn))), nil
}

// vp9Thumbnail decodes the first picture of index at size.
func vp9Thumbnail(ctx context.Context, index *webm.Index, size image.Point) (*image.RGBA, error) {
	rt, err := vp9.NewRuntimeWithLimits(ctx, vp9.PictureLimits)
	if err != nil {
		return nil, err
	}
	defer rt.Close(context.Background())
	v, err := rt.OpenFile(ctx, index)
	if err != nil {
		return nil, err
	}
	defer v.Close(context.Background())
	if _, err := v.Next(ctx); err != nil {
		return nil, err
	}
	return toRGBA(v.Picture(size)), nil
}

// webmLengthRead is how large a WebM has its blocks read for its length, for the
// sake of a thumbnail: all of the file is downloaded for it.
const webmLengthRead = 2 << 20

// WebMFirstPicture is the first picture of the WebM of size bytes that r reads, as
// a thumbnail's JPEG, and what the file says of itself. Only the start of a large
// file is read. A video the client cannot decode has a picture too, of one color and its
// shape: it is no failure to tell every time it shows. What fails to be read is one.
func WebMFirstPicture(ctx context.Context, r io.ReaderAt, size int64) ([]byte, webm.Meta, error) {
	var head [1]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, webm.Meta{}, err
	}
	index, err := webm.ReadFirstPicture(r, size)
	if err := ctx.Err(); err != nil {
		return nil, webm.Meta{}, err
	}
	if err != nil {
		picture, err := encodeThumbnail(plainPicture(16, 9))
		return picture, webm.Meta{}, err
	}
	w, h := fitted(index.Width, index.Height, thumbSide)
	img, err := vp9Thumbnail(ctx, index, image.Pt(w, h))
	if err != nil {
		if ctx.Err() != nil {
			return nil, webm.Meta{}, ctx.Err()
		}
		img = plainPicture(w, h)
	}
	meta := index.Meta
	if !index.LengthTold {
		meta.Duration = 0
	}
	if size <= webmLengthRead {
		// It may say it lasts far less than its blocks do, or nothing.
		if whole, err := webm.ReadIndex(r, size); err == nil {
			meta.Duration = whole.Duration
		}
	}
	picture, err := encodeThumbnail(img)
	return picture, meta, err
}

// plainPicture is a picture of w×h in a gray that shows as a video's empty plate.
func plainPicture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 64, G: 64, B: 68, A: 255}), image.Point{}, draw.Src)
	return img
}

// fitted is w×h scaled down to fit a square of side pixels, keeping its shape.
func fitted(w, h, side int) (int, int) {
	if w <= side && h <= side {
		return w, h
	}
	if w >= h {
		return side, max(1, h*side/w)
	}
	return max(1, w*side/h), side
}

// exifTurn is the EXIF orientation of a turn clockwise.
func exifTurn(degrees int) int {
	switch degrees {
	case 90:
		return 6
	case 180:
		return 3
	case 270:
		return 8
	}
	return 1
}
