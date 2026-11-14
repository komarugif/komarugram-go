// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"bytes"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"komarugram/pkg/mp4"
	"komarugram/pkg/program"
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
