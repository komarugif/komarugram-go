// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"komarugram/pkg/video"
)

// needConverter skips a test on a machine without FFmpeg and ffprobe.
func needConverter(t *testing.T) string {
	t.Helper()
	ffmpeg := video.ResolveFFmpeg("")
	if ffmpeg == "" || video.ResolveFFprobe("") == "" {
		t.Skip("no FFmpeg")
	}
	return ffmpeg
}

// A real WebM of a second or so, to convert.
func realWebM(t *testing.T, ffmpeg string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "holiday.webm")
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=1", "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", path).CombinedOutput()
	if err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	return path
}

// Without FFmpeg nothing is offered: a WebM goes as it is.
func TestBoxOffersNoConversionWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := newMenuHarness(t, nil)
	c := h.page.composer
	paths := boxPaths(t, "webm")
	c.files.addPaths(c, paths, false)
	settleBox(t, h)
	c.files.send.click.Click()
	sent := waitSent(t, h, 1)
	if c.files.convert.modal.Shown() {
		t.Fatal("offered to convert with nothing to do it")
	}
	if sent[0].Files.Paths[0] != paths[0] {
		t.Fatalf("sent %q", sent[0].Files.Paths[0])
	}
}

// Nothing is asked of what Telegram keeps as a video, or what goes as a document.
func TestBoxAsksOnlyOfWhatItMustConvert(t *testing.T) {
	needConverter(t)
	for _, c := range []struct {
		kinds     []string
		documents bool
		asks      bool
	}{
		{[]string{"webm"}, false, true},
		{[]string{"photo", "webm"}, false, true},
		{[]string{"video"}, false, false},
		{[]string{"photo"}, false, false},
		{[]string{"webm"}, true, false},
	} {
		h := newMenuHarness(t, nil)
		composer := h.page.composer
		composer.files.addPaths(composer, boxPaths(t, c.kinds...), c.documents)
		settleBox(t, h)
		if asks := len(composer.toConvert()) > 0; asks != c.asks {
			t.Errorf("%v, documents %v: asks %v", c.kinds, c.documents, asks)
		}
	}
}

// Asked, it sends nothing until it is answered. Sent as it is, the file is the one
// chosen; cancelled, the box stays with it.
func TestBoxAsksBeforeSending(t *testing.T) {
	needConverter(t)
	h := newMenuHarness(t, nil)
	c := h.page.composer
	paths := boxPaths(t, "webm")
	c.files.addPaths(c, paths, false)
	settleBox(t, h)
	c.files.send.click.Click()
	h.frames(30)
	d := &c.files.convert
	if !d.modal.Shown() || len(sentSoFar(h)) != 0 {
		t.Fatalf("asking %v, %d sent", d.modal.Shown(), len(sentSoFar(h)))
	}
	if !d.convert {
		t.Fatal("converting is not what is chosen first")
	}
	d.cancel.click.Click()
	h.frames(60)
	if d.modal.Shown() || !c.files.Shown() || len(sentSoFar(h)) != 0 {
		t.Fatalf("cancelled: asking %v, the box %v, %d sent", d.modal.Shown(), c.files.Shown(), len(sentSoFar(h)))
	}
	c.files.send.click.Click()
	h.frames(30)
	d.convert = false
	d.confirm.click.Click()
	sent := waitSent(t, h, 1)
	if sent[0].Files.Paths[0] != paths[0] {
		t.Fatalf("sent %q, not the file chosen", sent[0].Files.Paths[0])
	}
}

// Converted, an MP4 goes under the file's name, the file chosen stays as it was, and
// the MP4 is removed as a pasted picture is.
func TestBoxConvertsAndSends(t *testing.T) {
	ffmpeg := needConverter(t)
	path := realWebM(t, ffmpeg)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.files.addPaths(c, []string{path}, false)
	settleBox(t, h)
	c.files.caption.SetText("Holiday")
	c.files.send.click.Click()
	h.frames(30)
	c.files.convert.confirm.click.Click()
	h.frames(2)
	if c.files.convert.job == nil && len(sentSoFar(h)) == 0 {
		t.Fatal("no conversion started")
	}
	deadline := time.Now().Add(60 * time.Second)
	for len(sentSoFar(h)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the video was not converted")
		}
		h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	m := sentSoFar(h)[0]
	got := m.Files.Paths[0]
	if filepath.Base(got) != "holiday.mp4" || got == path || m.Text != "Holiday" {
		t.Fatalf("sent %q, %q", got, m.Text)
	}
	info, err := video.ReadContainer(got)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || !info.Streaming {
		t.Fatalf("%+v", info)
	}
	if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, original) {
		t.Errorf("the file chosen changed: %v", err)
	}
	if !c.pasted[got] || !c.uploading[got] {
		t.Errorf("nothing removes the MP4: %v %v", c.pasted, c.uploading)
	}
	c.forgetPasted([]string{got})
	if _, err := os.Stat(got); err == nil {
		t.Error("the MP4 stayed")
	}
}

// Stopped, nothing goes and nothing stays: the box is as it was.
func TestBoxConversionStops(t *testing.T) {
	ffmpeg := needConverter(t)
	path := filepath.Join(t.TempDir(), "long.webm")
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=30", "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", path).CombinedOutput()
	if err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.files.addPaths(c, []string{path}, false)
	settleBox(t, h)
	c.files.send.click.Click()
	h.frames(30)
	c.files.convert.confirm.click.Click()
	h.frames(2)
	if c.files.convert.job == nil {
		t.Fatal("no conversion started")
	}
	c.files.convert.cancel.click.Click()
	deadline := time.Now().Add(30 * time.Second)
	for c.files.convert.job != nil {
		if time.Now().After(deadline) {
			t.Fatal("it did not stop")
		}
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
	// A message goes off the frame: time for one that should not.
	for range 60 {
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
	if c.files.modal.closing {
		t.Fatal("the box closes, as after sending")
	}
	if c.files.modal.toast.Text() != "" {
		t.Fatalf("told %q of what the user stopped", c.files.modal.toast.Text())
	}
	if len(sentSoFar(h)) != 0 || !c.files.Shown() || c.files.convert.modal.Shown() || c.files.files[0].Path != path || len(c.pasted) != 0 {
		t.Fatalf("%d sent, the box %v, asking %v, file %q, kept %v", len(sentSoFar(h)), c.files.Shown(), c.files.convert.modal.Shown(), c.files.files[0].Path, c.pasted)
	}
}
