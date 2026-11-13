// SPDX-License-Identifier: Unlicense OR MIT

package h264

import (
	"bytes"
	"context"
	"image"
	"math"
	"os"
	"testing"
)

// TestFastPicturesInATile checks that a video decoded without the
// deblocking filter gives other pictures, so that the setting reached the
// decoder, which in a tile are the pictures decoded with it.
func TestFastPicturesInATile(t *testing.T) {
	ctx := context.Background()
	path := os.Getenv("KOMARUGRAM_AVCDEC")
	if path == "" {
		t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm")
	}
	module, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(ctx, module)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	// The video is 720x1280: a tile a seventh of it, and itself.
	tile := image.Pt(100, 178)
	pictures := func(fast bool) (tiles, whole []*image.RGBA) {
		v, err := rt.OpenVideo(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close(ctx)
		if err := v.SetFast(ctx, fast); err != nil {
			t.Fatal(err)
		}
		for i := range 90 {
			if _, err := v.Next(ctx); err != nil {
				t.Fatal(err)
			}
			if i%30 == 29 {
				tiles, whole = append(tiles, v.Picture(tile)), append(whole, v.Picture(v.Size()))
			}
		}
		return tiles, whole
	}
	tiles, whole := pictures(false)
	fastTiles, fastWhole := pictures(true)
	differ := false
	for i := range tiles {
		differ = differ || !bytes.Equal(whole[i].Pix, fastWhole[i].Pix)
		var se float64
		for j := range tiles[i].Pix {
			d := float64(tiles[i].Pix[j]) - float64(fastTiles[i].Pix[j])
			se += d * d
		}
		if psnr := 10 * math.Log10(255*255/(se/float64(len(tiles[i].Pix))+1e-9)); psnr < 38 {
			t.Errorf("picture %d in a tile: PSNR %.1f dB against the one deblocked", (i+1)*30, psnr)
		}
	}
	if !differ {
		t.Fatal("pictures decoded fast are those decoded with the deblocking filter")
	}
}
