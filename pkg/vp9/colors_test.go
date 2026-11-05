// SPDX-License-Identifier: Unlicense OR MIT

package vp9_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"komarugram/pkg/vp9"
	"komarugram/pkg/webm"
)

// A video of one known color, its levels and its tags as encoder settings make them,
// comes back that color: black black, white white, however it is drawn.
func TestColorsAsTheStreamTells(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx := context.Background()
	for _, v := range []struct {
		name          string
		matrix, space string
		levels, tag   string
		w, h          int
	}{
		{"HD BT.709 limited", "bt709", "bt709", "tv", "tv", 1280, 720},
		{"small BT.601 limited", "bt601", "smpte170m", "tv", "tv", 512, 512},
		{"HD BT.709 full", "bt709", "bt709", "pc", "pc", 1280, 720},
		{"small BT.601 full", "bt601", "smpte170m", "pc", "pc", 512, 512},
	} {
		for _, c := range []struct {
			hex     string
			r, g, b uint8
		}{
			{"C02020", 192, 32, 32}, {"20C020", 32, 192, 32}, {"2020C0", 32, 32, 192},
			{"000000", 0, 0, 0}, {"FFFFFF", 255, 255, 255},
		} {
			t.Run(v.name+"/"+c.hex, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "clip.webm")
				filter := fmt.Sprintf("format=gbrp,scale=out_color_matrix=%s:out_range=%s:flags=accurate_rnd+full_chroma_int,format=yuv420p", v.matrix, v.levels)
				out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", fmt.Sprintf("color=c=0x%s:s=%dx%d:r=5:d=0.4", c.hex, v.w, v.h), "-vf", filter,
					"-c:v", "libvpx-vp9", "-lossless", "1", "-deadline", "realtime", "-cpu-used", "8", "-colorspace", v.space, "-color_range", v.tag, path).CombinedOutput()
				if err != nil {
					t.Skipf("this FFmpeg cannot: %v: %s", err, out)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				check := func(name string, img *image.RGBA) {
					t.Helper()
					b := img.Bounds()
					p := img.RGBAAt(b.Dx()/2, b.Dy()/2)
					diff := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
					if diff(p.R, c.r) > 4 || diff(p.G, c.g) > 4 || diff(p.B, c.b) > 4 {
						t.Errorf("%s: %d %d %d, not %d %d %d", name, p.R, p.G, p.B, c.r, c.g, c.b)
					}
				}
				// As a video.
				index, err := webm.ReadIndex(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				rt, err := vp9.NewRuntimeWithLimits(ctx, vp9.VideoLimits)
				if err != nil {
					t.Fatal(err)
				}
				defer rt.Close(ctx)
				video, err := rt.OpenFile(ctx, index)
				if err != nil {
					t.Fatal(err)
				}
				defer video.Close(ctx)
				if _, err := video.Next(ctx); err != nil {
					t.Fatal(err)
				}
				check("video, its own size", video.Picture(video.Size()))
				check("video, scaled", video.Picture(video.Size().Div(4)))
				// As a sticker, at its own size and scaled.
				sticker, err := rt.OpenSticker(ctx, "clip", data)
				if err != nil {
					t.Fatal(err)
				}
				defer sticker.Close(ctx)
				frame, err := sticker.FrameAt(ctx, 0)
				if err != nil {
					t.Fatal(err)
				}
				check("sticker, its own size", frame)
				scaled, err := sticker.FrameSized(ctx, 0, sticker.Size.Div(4))
				if err != nil {
					t.Fatal(err)
				}
				check("sticker, scaled", scaled)
			})
		}
	}
}
