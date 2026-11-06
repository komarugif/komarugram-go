// SPDX-License-Identifier: Unlicense OR MIT

package sendfiles

import (
	"context"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// needDecoder skips a test on a machine that has not named the decoder.
func needDecoder(t *testing.T) {
	t.Helper()
	if os.Getenv("KOMARUGRAM_AVCDEC") == "" {
		t.Skip("KOMARUGRAM_AVCDEC is not set")
	}
}

// Without FFmpeg a video has its first picture, from the client's decoder.
func TestVideoThumbnailWithoutFFmpeg(t *testing.T) {
	needDecoder(t)
	img, err := VideoThumbnail(context.Background(), "", "../../../assets/video.mp4", 320)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) != 320 {
		t.Fatalf("%v", b)
	}
	if blank(img) {
		t.Fatal("no picture in it")
	}
	if jpeg, err := VideoDocumentThumbnail(context.Background(), "", "../../../assets/video.mp4"); err != nil || len(jpeg) == 0 {
		t.Fatalf("%d bytes: %v", len(jpeg), err)
	}
}

// What the decoder takes no file of fails without the decoder being fetched.
func TestVideoThumbnailOfOthers(t *testing.T) {
	t.Setenv("KOMARUGRAM_AVCDEC", filepath.Join(t.TempDir(), "none.wasm"))
	path := filepath.Join(t.TempDir(), "clip.avi")
	if err := os.WriteFile(path, []byte("RIFF....AVI"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VideoThumbnail(context.Background(), "", path, 320); err == nil {
		t.Fatal("a picture of nothing")
	}
}

// A VP9 WebM has its first picture without FFmpeg, as FFmpeg gives it, and for it no
// more of a large file is read than the start; VP8 has none, the module being VP9's.
func TestWebMThumbnail(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	make := func(name, codec string) string {
		path := filepath.Join(dir, name)
		if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=1", "-c:v", codec, "-deadline", "realtime", "-cpu-used", "8", path).CombinedOutput(); err != nil {
			t.Skipf("this FFmpeg cannot: %v: %s", err, out)
		}
		return path
	}
	path := make("clip.webm", "libvpx-vp9")
	ours, err := VideoThumbnail(context.Background(), "", path, 320)
	if err != nil {
		t.Fatal(err)
	}
	if ours.Bounds().Size() != image.Pt(320, 180) || blank(ours) {
		t.Fatalf("%v, blank %v", ours.Bounds(), blank(ours))
	}
	theirs, err := ffmpegThumbnail(context.Background(), ffmpeg, path, 320)
	if err != nil {
		t.Fatal(err)
	}
	if theirs.Bounds() != ours.Bounds() {
		t.Fatalf("%v, FFmpeg %v", ours.Bounds(), theirs.Bounds())
	}
	// Scaled by other filters, they differ at edges, not on the whole.
	diff := 0
	for i := range ours.Pix {
		d := int(ours.Pix[i]) - int(theirs.Pix[i])
		diff += max(d, -d)
	}
	if mean := diff / len(ours.Pix); mean > 16 {
		t.Errorf("%d of 255 from FFmpeg's on the mean", mean)
	}
	if jpeg, err := VideoDocumentThumbnail(context.Background(), "", path); err != nil || len(jpeg) == 0 {
		t.Fatalf("%d bytes: %v", len(jpeg), err)
	}
	if _, err := VideoThumbnail(context.Background(), "", make("vp8.webm", "libvpx"), 320); err == nil {
		t.Fatal("a picture of VP8")
	}
}

// An album of media takes MP4 and MOV; a WebM goes apart from it, as Telegram gives
// it back a file.
func TestWebMGoesApartFromAnAlbum(t *testing.T) {
	photo := File{Kind: KindPhoto, MIME: "image/jpeg"}
	mp4 := File{Kind: KindVideo, MIME: "video/mp4"}
	clip := File{Kind: KindVideo, MIME: "video/webm"}
	groups := Divide([]File{photo, mp4, clip}, Way{Group: true})
	if len(groups) != 2 || groups[0].Album != PhotoVideoAlbum || len(groups[0].Files) != 2 || groups[1].Album != NoAlbum {
		t.Fatalf("%+v", groups)
	}
	// As documents it is a file like any other.
	if groups := Divide([]File{mp4, clip}, Way{Group: true, Documents: true}); len(groups) != 1 || groups[0].Album != FileAlbum {
		t.Fatalf("documents: %+v", groups)
	}
}

// The picture H.264's decoder makes has the colors of the video: its levels read as
// their range, and not as JPEG's, which makes it pale.
func TestThumbnailKeepsColors(t *testing.T) {
	needDecoder(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=0xC02020:s=1280x720:r=5:d=0.4", "-vf", "format=gbrp,scale=out_color_matrix=bt709:out_range=tv:flags=accurate_rnd+full_chroma_int,format=yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-qp", "0", "-colorspace", "bt709", "-color_range", "tv", path).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	img, err := decodedThumbnail(context.Background(), path, 320)
	if err != nil {
		t.Fatal(err)
	}
	if !near(img.At(160, 90), color.RGBA{0xC0, 0x20, 0x20, 255}) {
		t.Fatalf("%v", img.At(160, 90))
	}
}

// A turned video's picture is turned, as FFmpeg's own is: the same way.
func TestVideoThumbnailTurnedAsFFmpeg(t *testing.T) {
	needDecoder(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	plain, turned := filepath.Join(dir, "plain.mp4"), filepath.Join(dir, "turned.mp4")
	// Red over blue: each way of turning puts them apart.
	source := "color=red:size=160x90:rate=25:duration=1[a];color=blue:size=160x90:rate=25:duration=1[b];[a][b]vstack,format=yuv420p"
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", source, "-c:v", "libx264", "-preset", "ultrafast", plain).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-display_rotation", "-90", "-i", plain, "-c", "copy", turned).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	ours, err := decodedThumbnail(context.Background(), turned, 320)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := ffmpegThumbnail(context.Background(), ffmpeg, turned, 320)
	if err != nil {
		t.Fatal(err)
	}
	if ours.Bounds().Size() != theirs.Bounds().Size() {
		t.Fatalf("%v, FFmpeg %v", ours.Bounds(), theirs.Bounds())
	}
	b := ours.Bounds()
	for _, p := range []image.Point{{b.Dx() / 4, b.Dy() / 2}, {3 * b.Dx() / 4, b.Dy() / 2}} {
		if !near(ours.At(p.X, p.Y), theirs.At(p.X, p.Y)) {
			t.Fatalf("at %v: %v, FFmpeg %v", p, ours.At(p.X, p.Y), theirs.At(p.X, p.Y))
		}
	}
}

func near(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	diff := func(x, y uint32) uint32 {
		if x > y {
			return x - y
		}
		return y - x
	}
	return diff(ar, br)+diff(ag, bg)+diff(ab, bb) < 0x3000
}

// blank tells a picture of one color, as a decoder that gave nothing leaves.
func blank(img *image.RGBA) bool {
	first := img.RGBAAt(0, 0)
	b := img.Bounds()
	for y := range b.Dy() {
		for x := range b.Dx() {
			if img.RGBAAt(x, y) != first {
				return false
			}
		}
	}
	return true
}
