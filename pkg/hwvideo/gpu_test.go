// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"bytes"
	"errors"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"komarugram/pkg/mp4"
	"komarugram/pkg/program"
	"komarugram/pkg/resample"
)

// decodeAll decodes every picture of file on dev, in the order they show.
func decodeAll(t *testing.T, dev device, file *mp4.File) [][]byte {
	t.Helper()
	d, err := newDecoder(dev, file.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var out [][]byte
	var im image.YCbCr
	take := func() {
		for d.Receive() {
			if err := d.Read(&im); err != nil {
				t.Fatal(err)
			}
			out = append(out, bytes.Join([][]byte{im.Y, im.Cb, im.Cr}, nil))
		}
	}
	for i := range file.Len() {
		sample, err := file.Sample(i, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Send(sample); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		take()
	}
	d.End()
	take()
	return out
}

// TestDecodesAsFFmpeg decodes videos on the GPU and compares every picture
// with FFmpeg's: H.264 is decoded exactly, so they are the same bytes. The
// videos are assets/video.mp4 and the MP4 files of the directory
// KOMARUGRAM_HW_VIDEOS names; it needs a GPU that decodes H.264 and an
// FFmpeg.
func TestDecodesAsFFmpeg(t *testing.T) {
	ffmpeg, err := program.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no FFmpeg")
	}
	dev, err := newDevice()
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer dev.close()
	files := []string{filepath.Join("..", "..", "assets", "video.mp4")}
	if dir := os.Getenv("KOMARUGRAM_HW_VIDEOS"); dir != "" {
		more, _ := filepath.Glob(filepath.Join(dir, "*.mp4"))
		files = append(files, more...)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := mp4.Demux(data)
			if err != nil {
				t.Fatal(err)
			}
			got := decodeAll(t, dev, file)
			raw := filepath.Join(t.TempDir(), "frames.yuv")
			cmd := program.Command(ffmpeg, "-v", "error", "-i", path, "-map", "0:v:0", "-fps_mode", "passthrough", "-pix_fmt", "yuv420p", "-f", "rawvideo", raw)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ffmpeg: %v: %s", err, out)
			}
			want, err := os.ReadFile(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 {
				t.Fatal("no pictures")
			}
			size := len(got[0])
			if len(want) != size*len(got) {
				t.Fatalf("%d pictures of %d bytes, FFmpeg wrote %d bytes (%s pictures)", len(got), size, len(want), strconv.FormatFloat(float64(len(want))/float64(size), 'f', 2, 64))
			}
			bad := 0
			for i, picture := range got {
				if !bytes.Equal(picture, want[i*size:(i+1)*size]) {
					if bad == 0 {
						diff := 0
						for j := range picture {
							if picture[j] != want[i*size+j] {
								diff++
							}
						}
						t.Errorf("picture %d differs from FFmpeg's in %d bytes of %d", i, diff, size)
					}
					bad++
				}
			}
			if bad > 0 {
				t.Errorf("%d pictures of %d differ", bad, len(got))
			}
		})
	}
}

// TestRendersAsCPU decodes videos on the GPU and scales their pictures
// there to the sizes of tiles, as the helper does, and compares them with
// the same pictures read back whole and scaled on the CPU. Vulkan runs the
// CPU's filters (shaders/scale.comp), and differs by rounding alone;
// Direct3D 11's video processor filters as its driver does, and is held
// to the mean difference. It needs a GPU that decodes H.264; the videos
// are those of TestDecodesAsFFmpeg.
func TestRendersAsCPU(t *testing.T) {
	dev, err := newDevice()
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer dev.close()
	files := []string{filepath.Join("..", "..", "assets", "video.mp4")}
	if dir := os.Getenv("KOMARUGRAM_HW_VIDEOS"); dir != "" {
		more, _ := filepath.Glob(filepath.Join(dir, "*.mp4"))
		files = append(files, more...)
	}
	// What a picture may differ by: the largest difference of a channel,
	// and the mean one.
	largest, mean := 2, 0.05
	if runtime.GOOS == "windows" {
		largest, mean = 255, 4
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := mp4.Demux(data)
			if err != nil {
				t.Fatal(err)
			}
			d, err := newDecoder(dev, file.Config)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			size := d.Size()
			// Tiles of the list, a smaller one roughly, the picture's own
			// size, and larger.
			type tile struct {
				size  image.Point
				rough bool
			}
			tiles := []tile{
				{resample.Fit(size.X, size.Y, image.Pt(160, 160), true), false},
				{resample.Fit(size.X, size.Y, image.Pt(72, 72), true), true},
				{size, false},
				{image.Pt(size.X*3/2, size.Y*3/2), false},
			}
			var picture image.YCbCr
			pictures, worst, sum, n := 0, 0, 0.0, 0
			check := func() {
				for d.Receive() {
					pictures++
					if pictures%10 != 1 {
						continue
					}
					if err := d.Read(&picture); err != nil {
						t.Fatal(err)
					}
					for _, tl := range tiles {
						if tl.size.X > maxRender || tl.size.Y > maxRender {
							continue
						}
						scaler := resample.VideoScaler{Colors: resample.Colors{Matrix: resample.GuessMatrix(size.X, size.Y), Limited: true}, Rough: tl.rough}
						want := image.NewRGBA(image.Rectangle{Max: tl.size})
						scaler.ResizeInto(want, &picture, nil)
						got := image.NewRGBA(want.Rect)
						err := d.Render(scaler.Plan(size.X, size.Y, tl.size.X, tl.size.Y), got)
						if errors.Is(err, errNoRender) {
							t.Fatalf("the device decodes but does not scale: %v", err)
						}
						if err != nil {
							t.Fatal(err)
						}
						l, m := difference(got, want)
						worst, sum, n = max(worst, l), sum+m, n+1
						if l > largest || m > mean {
							t.Fatalf("picture %d at %v: differs from the CPU's by %d, mean %.4f", pictures-1, tl.size, l, m)
						}
					}
				}
			}
			for i := range file.Len() {
				sample, err := file.Sample(i, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := d.Send(sample); err != nil {
					t.Fatalf("sample %d: %v", i, err)
				}
				check()
			}
			d.End()
			check()
			if n == 0 {
				t.Fatal("no pictures")
			}
			t.Logf("%d pictures, %d scalings: the largest difference %d, the mean %.4f", pictures, n, worst, sum/float64(n))
		})
	}
}

// difference is the largest difference of a channel of a and b, and their
// mean one.
func difference(a, b *image.RGBA) (largest int, mean float64) {
	sum := 0
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		d = max(d, -d)
		largest = max(largest, d)
		sum += d
	}
	return largest, float64(sum) / float64(len(a.Pix))
}
