// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/ffmpegtest"
	"komarugram/pkg/program"
	"komarugram/pkg/video"
)

// testClip makes a clip of a smooth picture, with a tone, turned as rotation
// says in its metadata, with the FFmpeg found; it skips the test without one.
func testClip(t *testing.T, ffmpeg string, size image.Point, rotation int) string {
	t.Helper()
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	dir := t.TempDir()
	pic := image.NewRGBA(image.Rectangle{Max: size})
	for y := range size.Y {
		for x := range size.X {
			pic.SetRGBA(x, y, color.RGBA{R: uint8(40 + 160*x/size.X), G: uint8(40 + 160*y/size.Y), B: 110, A: 255})
		}
	}
	src := filepath.Join(dir, "pic.png")
	savePNG(t, src, pic)
	straight := filepath.Join(dir, "straight.mp4")
	run(t, ffmpeg, "-v", "error", "-y", "-loop", "1", "-r", "25", "-t", "1.2", "-i", src, "-f", "lavfi", "-t", "1.2", "-i", "sine=frequency=440",
		"-vf", "scale=out_color_matrix=bt709:out_range=tv,format=yuv420p", "-c:v", "libx264", "-crf", "10", "-colorspace", "bt709", "-color_range", "tv", "-c:a", "aac", "-shortest", straight)
	if rotation == 0 {
		return straight
	}
	turned := filepath.Join(dir, "turned.mp4")
	run(t, ffmpeg, "-v", "error", "-y", "-display_rotation", fmt.Sprint(rotation), "-i", straight, "-c", "copy", turned)
	return turned
}

func savePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, bin string, args ...string) {
	t.Helper()
	if out, err := program.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
	}
}

// encodeScene encodes clip as the scene says, as the editor does.
func encodeScene(t *testing.T, ffmpeg, clip string, media video.Media, s *Scene) string {
	t.Helper()
	dir := t.TempDir()
	var steps []video.EditStep
	for i, step := range Layers(s, nil) {
		path := filepath.Join(dir, fmt.Sprintf("step%d.png", i))
		if step.Blur != nil {
			savePNG(t, path, &image.Gray{Pix: step.Blur.Pix, Stride: step.Blur.Stride, Rect: step.Blur.Rect})
			steps = append(steps, video.EditStep{Path: path, Radius: step.Radius})
			continue
		}
		savePNG(t, path, step.Over)
		steps = append(steps, video.EditStep{Path: path})
	}
	out := filepath.Join(dir, "out.mp4")
	tr := s.Transform.Even()
	plan := video.EditPlan{Media: media, Steps: steps, Flip: tr.Flip, Angle: tr.Angle, From: s.Trim.From, To: s.Trim.To}
	if !tr.Identity(s.Size) {
		plan.Crop = tr.Crop
	}
	var last video.GIFProgress
	if err := video.EncodeEdit(context.Background(), ffmpeg, clip, out, plan, func(p video.GIFProgress) { last = p }); err != nil {
		t.Fatal(err)
	}
	if !last.End {
		t.Errorf("the last report does not end: %+v", last)
	}
	return out
}

// frameDiffers is how far two pictures are apart: the mean difference of their
// values, and the share of pixels over 12 apart.
func frameDiffers(t *testing.T, want, got *image.RGBA) (mean, far float64) {
	t.Helper()
	if want.Rect.Size() != got.Rect.Size() {
		t.Fatalf("size %v, want %v", got.Rect.Size(), want.Rect.Size())
	}
	sum, over, n := 0, 0, 0
	for y := range want.Rect.Dy() {
		for x := range want.Rect.Dx() {
			a, b := want.RGBAAt(x, y), got.RGBAAt(x, y)
			d := abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
			sum += d
			if d > 12*3 {
				over++
			}
			n++
		}
	}
	return float64(sum) / float64(n*3), float64(over) / float64(n)
}

func probeClip(t *testing.T, clip, ffmpeg string) video.Media {
	t.Helper()
	media, err := video.ProbeMedia(context.Background(), clip, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	return media
}

// What comes out of FFmpeg is what Flatten draws, turned and cropped as the
// scene says, on a video that is straight and on those a phone records turned.
func TestVideoMatchesPicture(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	for _, c := range []struct {
		rotation, angle int
		flip, blur      bool
		crop            image.Rectangle
	}{
		{rotation: 0},
		{rotation: 90},
		{rotation: -90, angle: 90, crop: image.Rect(20, 40, 200, 200)},
		{rotation: 180, angle: 270, flip: true},
		{rotation: 0, angle: 180, flip: true, crop: image.Rect(30, 10, 310, 170)},
		{rotation: 90, blur: true},
	} {
		t.Run(fmt.Sprintf("%+v", c), func(t *testing.T) {
			clip := testClip(t, ffmpeg, image.Pt(320, 240), c.rotation)
			media := probeClip(t, clip, ffmpeg)
			if c.rotation%180 != 0 && media.EditSize() != image.Pt(240, 320) {
				t.Fatalf("a turned video has size %v", media.EditSize())
			}
			size := media.EditSize()
			s := New(size)
			s.Add(stroke(Pen, 8, Point{10, 30}, Point{float64(size.X) - 10, 30}))
			s.Add(&Element{Stroke: &Stroke{Tool: Marker, Color: color.NRGBA{G: 255, A: 255}, Width: 10, Points: []Point{{30, 0}, {30, float64(size.Y)}}}})
			if c.blur {
				s.Add(stroke(Blur, 10, Point{0, 150}, Point{float64(size.X), 150}))
			}
			s.Add(&Element{Shape: &Shape{Type: ShapeStar, Color: color.NRGBA{B: 255, A: 255}, Filled: true}, Center: Point{100, 100}, Width: 60, Height: 57})
			s.Transform.Angle, s.Transform.Flip = c.angle, c.flip
			if !c.crop.Empty() {
				s.Transform.Crop = c.crop
			}
			out := encodeScene(t, ffmpeg, clip, media, s)
			after := probeClip(t, out, ffmpeg)
			if !after.Sound || after.AudioCodec != "aac" {
				t.Errorf("sound: %+v", after)
			}
			frame, err := video.FrameAt(context.Background(), ffmpeg, clip, media, 0, image.Point{})
			if err != nil {
				t.Fatal(err)
			}
			want := s.Transform.Even().Apply(Flatten(frame, s.Elements, CanvasOf(image.Rectangle{Max: size}, 1), nil, size))
			got, err := video.FrameAt(context.Background(), ffmpeg, out, after, 0, image.Point{})
			if err != nil {
				t.Fatal(err)
			}
			mean, far := frameDiffers(t, want, got)
			t.Logf("mean difference %.2f, %.3f%% of pixels far", mean, far*100)
			if mean > 3 || far > .05 { // The edges of what is drawn lose chroma in 4:2:0.
				t.Errorf("mean difference %.2f, %.2f%% of pixels far", mean, far*100)
			}
		})
	}
}

// Saturated colors come out as they went in, read by the matrix the video
// states: left to the conversions, an HD video's come back through BT.601.
func TestVideoKeepsColors(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	clip := filepath.Join(t.TempDir(), "red.mp4")
	run(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=0xe62838:s=1280x720:d=0.3:r=25", "-vf", "scale=out_color_matrix=bt709:out_range=tv,format=yuv420p", "-c:v", "libx264", "-crf", "5", "-colorspace", "bt709", clip)
	media := probeClip(t, clip, ffmpeg)
	s := New(media.EditSize())
	s.Add(stroke(Pen, 4, Point{0, 0}, Point{4, 4}))
	out := encodeScene(t, ffmpeg, clip, media, s)
	after := probeClip(t, out, ffmpeg)
	if after.ColorSpace != "bt709" {
		t.Errorf("the matrix it states: %q", after.ColorSpace)
	}
	// Read by what it states, as a player reads it.
	got, err := video.FrameAt(context.Background(), ffmpeg, out, after, 0, image.Point{})
	if err != nil {
		t.Fatal(err)
	}
	c := got.RGBAAt(640, 360)
	if abs(int(c.R)-0xe6) > 4 || abs(int(c.G)-0x28) > 4 || abs(int(c.B)-0x38) > 4 {
		t.Errorf("came out %v, want about e6 28 38", c)
	}
}

// The translucent marker comes out of FFmpeg as it does here, in the values of
// the pixels: linear light would be 13 and 21 off.
func TestVideoMarkerInGamma(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	ffmpegtest.RequireEncoder(t, ffmpeg, "libx264")
	dir := t.TempDir()
	clip := filepath.Join(dir, "gray.mp4")
	run(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=0x808080:s=128x128:d=0.5:r=25", "-vf", "scale=out_color_matrix=bt601:out_range=tv,format=yuv420p", "-c:v", "libx264", "-crf", "5", "-colorspace", "smpte170m", clip)
	media := probeClip(t, clip, ffmpeg)
	s := New(media.EditSize())
	s.Add(stroke(Marker, 20, Point{0, 64}, Point{128, 64}))
	out := encodeScene(t, ffmpeg, clip, media, s)
	got, err := video.FrameAt(context.Background(), ffmpeg, out, probeClip(t, out, ffmpeg), 0, image.Point{})
	if err != nil {
		t.Fatal(err)
	}
	c := got.RGBAAt(64, 64)
	if abs(int(c.R)-172) > 5 || abs(int(c.G)-83) > 5 || abs(int(c.B)-83) > 5 {
		t.Errorf("marker over gray comes out %v, want about 172, 83, 83", c)
	}
}

func TestVideoTrim(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	clip := testClip(t, ffmpeg, image.Pt(160, 120), 0)
	media := probeClip(t, clip, ffmpeg)
	s := New(media.EditSize())
	s.Trim = Trim{From: 300 * time.Millisecond, To: 900 * time.Millisecond}
	out := probeClip(t, encodeScene(t, ffmpeg, clip, media, s), ffmpeg)
	if d := out.Duration - 600*time.Millisecond; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Errorf("lasts %v, want about 600 ms", out.Duration)
	}
}

// Cancelled, the encoding stops at once and leaves no file.
func TestVideoCancel(t *testing.T) {
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	clip := testClip(t, ffmpeg, image.Pt(160, 120), 0)
	media := probeClip(t, clip, ffmpeg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(t.TempDir(), "out.mp4")
	err := video.EncodeEdit(ctx, ffmpeg, clip, out, video.EditPlan{Media: media}, func(video.GIFProgress) {})
	if err == nil {
		t.Fatal("no error")
	}
	if _, stat := os.Stat(out); stat == nil {
		t.Error("the file stayed")
	}
}
