// SPDX-License-Identifier: Unlicense OR MIT

package h264

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"komarugram/pkg/mp4"
)

// An MPEG-4 Part 2 video (mp4v, as Telegram's GIFs of codec "mpeg4" are)
// decodes in the module, each picture as FFmpeg decodes it.
// testdata/mpeg4.mp4 is FFmpeg's testsrc2, 96x64, 12 pictures:
//
//	ffmpeg -f lavfi -i testsrc2=size=96x64:rate=30 -frames:v 12 -c:v mpeg4 -q:v 3 -an testdata/mpeg4.mp4
func TestMPEG4(t *testing.T) {
	ctx := context.Background()
	path := os.Getenv("KOMARUGRAM_AVCDEC")
	if path == "" {
		t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm")
	}
	module, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/mpeg4.mp4")
	if err != nil {
		t.Fatal(err)
	}
	file, err := mp4.Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Codec != mp4.MPEG4 || file.Width != 96 || file.Height != 64 || file.Len() != 12 {
		t.Fatalf("codec %d, %dx%d, %d samples", file.Codec, file.Width, file.Height, file.Len())
	}
	rt, err := NewRuntime(ctx, module)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	v, err := rt.OpenFile(ctx, file)
	if errors.Is(err, mp4.ErrUnsupported) {
		t.Skip("the module is built without MPEG-4 Part 2")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(ctx)
	var luma bytes.Buffer
	for range 12 {
		if _, err := v.Next(ctx); err != nil {
			t.Fatal(err)
		}
		p := v.picture
		for y := range p.Rect.Dy() {
			luma.Write(p.Y[y*p.YStride : y*p.YStride+p.Rect.Dx()])
		}
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Log("no ffmpeg to compare the pictures with")
		return
	}
	// The planes as they are: "gray" would scale the luma to full range.
	planes, err := exec.Command(ffmpeg, "-v", "error", "-i", "testdata/mpeg4.mp4", "-f", "rawvideo", "-pix_fmt", "yuv420p", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	for frame := 96 * 64 * 3 / 2; len(planes) >= frame; planes = planes[frame:] {
		want = append(want, planes[:96*64]...)
	}
	got := luma.Bytes()
	if len(got) != len(want) {
		t.Fatalf("%d bytes of pictures, FFmpeg's %d", len(got), len(want))
	}
	worst, total := 0, 0
	for i := range got {
		d := int(got[i]) - int(want[i])
		d = max(d, -d)
		worst, total = max(worst, d), total+d
	}
	t.Logf("worst %d, mean %.3f", worst, float64(total)/float64(len(got)))
	if !bytes.Equal(got, want) {
		t.Fatalf("the pictures differ from FFmpeg's")
	}
}
