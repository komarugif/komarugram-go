// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// gradientPNG is a picture of w by h whose colors follow seed.
func gradientImage(w, h, seed int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8((x*255/max(w, 1) + seed*70) % 256), G: uint8(y * 255 / max(h, 1)), B: uint8(90 + seed*30), A: 255})
		}
	}
	return img
}

// pictureFile writes a picture of w by h in dir, as PNG or JPEG by its name.
func pictureFile(t *testing.T, dir, name string, w, h, seed int) string {
	t.Helper()
	var b bytes.Buffer
	if strings.HasSuffix(name, ".jpg") {
		if err := jpeg.Encode(&b, gradientImage(w, h, seed), nil); err != nil {
			t.Fatal(err)
		}
	} else if err := png.Encode(&b, gradientImage(w, h, seed)); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, dir, name, b.Bytes())
}

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// boxPaths are files for the box: several pictures of different shapes, a
// video and a text.
func boxPaths(t *testing.T, kinds ...string) []string {
	t.Helper()
	dir := t.TempDir()
	shapes := [][2]int{{1600, 1000}, {900, 1200}, {800, 800}, {1200, 700}, {640, 900}, {1000, 1000}, {1300, 900}}
	var paths []string
	photo := 0
	for _, kind := range kinds {
		switch kind {
		case "photo":
			shape := shapes[photo%len(shapes)]
			paths = append(paths, pictureFile(t, dir, fmt.Sprintf("photo-%d.jpg", photo+1), shape[0], shape[1], photo))
			photo++
		case "video":
			paths = append(paths, writeTemp(t, dir, "holiday.mp4", []byte("a video that is not one")))
		case "webm":
			paths = append(paths, writeTemp(t, dir, "holiday.webm", []byte("a video that is not one")))
		case "song":
			var cover bytes.Buffer
			if err := png.Encode(&cover, gradientImage(400, 400, 3)); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, writeTemp(t, dir, "song.mp3", mp3Song("Закат над озером", "Оркестр", cover.Bytes())))
		case "track":
			paths = append(paths, writeTemp(t, dir, "track-07.mp3", mp3Song("", "", nil)))
		case "file":
			paths = append(paths, writeTemp(t, dir, "report.pdf", bytes.Repeat([]byte("pdf"), 40000)))
		}
	}
	return paths
}

// settleBox draws frames until every file of the box has been looked at.
func settleBox(t *testing.T, h *menuHarness) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !h.page.composer.files.ready() {
		if time.Now().After(deadline) {
			t.Fatal("the box did not read its files")
		}
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
	h.frames(30)
}

func sentSoFar(h *menuHarness) []model.OutgoingMessage {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	return append([]model.OutgoingMessage(nil), h.store.sent...)
}

// waitSent draws frames until n messages went to the store.
func waitSent(t *testing.T, h *menuHarness, n int) []model.OutgoingMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sent := sentSoFar(h); len(sent) >= n {
			return sent
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d messages sent, want %d", len(sentSoFar(h)), n)
		}
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
}

// What was chosen is shown as it will go; the caption is what was typed in
// the composer, and Send sends the files the way the checkboxes ask.
func TestFilesBoxSendsWhatWasChosen(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.draft(1).editor.SetText("Holiday")
	paths := boxPaths(t, "photo", "photo", "video", "file")
	c.files.addPaths(c, paths, false)
	h.frame()
	if !c.files.Shown() || len(c.files.files) != 4 || c.files.ready() {
		t.Fatalf("shown %v with %d files, ready %v", c.files.Shown(), len(c.files.files), c.files.ready())
	}
	settleBox(t, h)
	l := english()
	if got := c.files.title(l); got != "4 files selected" {
		t.Fatalf("title %q", got)
	}
	if got := c.files.caption.Text(); got != "Holiday" {
		t.Fatalf("caption %q", got)
	}
	kinds := ""
	for _, f := range c.files.files {
		kinds += map[int]string{0: "F", 1: "P", 2: "V", 3: "G"}[int(f.Kind)]
		if f.Kind == 1 && f.thumb == nil {
			t.Errorf("%s has no picture", f.Name)
		}
	}
	if kinds != "PPVF" {
		t.Fatalf("kinds %s", kinds)
	}
	// The caption may be changed, and the way too.
	c.files.caption.SetText(" Holiday in Sochi ")
	c.files.way.HighQuality = true
	c.files.send.click.Click()
	sent := waitSent(t, h, 1)
	m := sent[0]
	if m.Files == nil || !slices.Equal(m.Files.Paths, paths) || m.Text != "Holiday in Sochi" || !m.Files.Group || m.Files.Documents || !m.Files.HighQuality {
		t.Fatalf("sent %+v %+v", m, m.Files)
	}
	h.frames(60)
	if c.files.Shown() {
		t.Fatal("the box stayed open after it sent")
	}
	if c.draft(1).editor.Text() != " Holiday in Sochi " && c.draft(1).editor.Text() != "Holiday" {
		t.Fatalf("composer text %q", c.draft(1).editor.Text())
	}
}

// A file can be taken out, and the box goes when the last has gone.
func TestFilesBoxRemovesFiles(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.files.addPaths(c, boxPaths(t, "photo", "photo", "file"), false)
	settleBox(t, h)
	second := c.files.files[1]
	second.remove.click.Click()
	h.frames(3)
	if len(c.files.files) != 2 || slices.Contains(c.files.files, second) {
		t.Fatalf("%d files after taking one out", len(c.files.files))
	}
	for len(c.files.files) > 0 {
		c.files.files[0].remove.click.Click()
		h.frames(3)
	}
	h.frames(60)
	if c.files.Shown() {
		t.Fatal("the box stayed open with no files")
	}
}

// A file that is empty is not taken, and says so; the others stay.
func TestFilesBoxRefusesAnEmptyFile(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	paths := boxPaths(t, "photo")
	paths = append(paths, writeTemp(t, filepath.Dir(paths[0]), "nothing.txt", nil))
	c.files.addPaths(c, paths, false)
	settleBox(t, h)
	if len(c.files.files) != 1 {
		t.Fatalf("%d files", len(c.files.files))
	}
	if got := c.files.modal.toast.Text(); got != "File: nothing.txt is empty and can't be sent." {
		t.Fatalf("toast %q", got)
	}
	// Alone, it closes the box.
	c.files.close()
	h.frames(5)
	c.files.addPaths(c, []string{paths[1]}, false)
	settleBox2(t, h)
	if c.files.Shown() {
		t.Fatal("a box with no valid file stayed open")
	}
}

// settleBox2 draws frames until the box has nothing left to read.
func settleBox2(t *testing.T, h *menuHarness) {
	t.Helper()
	for range 400 {
		h.frame()
		time.Sleep(2 * time.Millisecond)
		if !h.page.composer.files.Shown() {
			return
		}
	}
	h.frames(60)
}

// A caption too long for Telegram is not sent, and the box says by how much.
func TestFilesBoxCaptionLimit(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	c.files.addPaths(c, boxPaths(t, "photo"), false)
	settleBox(t, h)
	c.files.caption.SetText(strings.Repeat("a", 1030))
	c.files.send.click.Click()
	h.frames(5)
	if got := len(sentSoFar(h)); got != 0 {
		t.Fatalf("%d messages sent with a caption too long", got)
	}
	if got := c.files.modal.toast.Text(); !strings.Contains(got, "6 characters") {
		t.Fatalf("toast %q", got)
	}
	c.files.caption.SetText(strings.Repeat("a", 1024))
	c.files.send.click.Click()
	waitSent(t, h, 1)
}

// The attachment menu asks the chooser for files and shows them in the box:
// photos as photos, "File" as documents; the box's "Add" adds to them.
func TestAttachmentMenuOpensTheBox(t *testing.T) {
	h := newMenuHarness(t, nil)
	c := h.page.composer
	paths := boxPaths(t, "photo", "photo", "photo")
	var filters []*fileFilter
	c.chooser = func(_ context.Context, filter *fileFilter, several bool) fileChoice {
		filters = append(filters, filter)
		if !several {
			t.Error("one file asked for")
		}
		return fileChoice{paths: paths[:2], path: paths[0]}
	}
	c.attachmentActions[0].click.Click()
	h.frames(2)
	settleBoxUntil(t, h, func() bool { return len(c.files.files) == 2 })
	settleBox(t, h)
	if len(filters) != 1 || filters[0] == nil || c.files.way.Documents || c.files.title(english()) != "2 images selected" {
		t.Fatalf("photo item: filters %v, documents %v", filters, c.files.way.Documents)
	}
	// Add takes one more, from any kind of file.
	c.chooser = func(_ context.Context, filter *fileFilter, _ bool) fileChoice {
		if filter != nil {
			t.Error("the box's Add narrowed the chooser")
		}
		return fileChoice{paths: paths[2:], path: paths[2]}
	}
	c.files.add.click.Click()
	settleBoxUntil(t, h, func() bool { return len(c.files.files) == 3 })
	settleBox(t, h)
	c.files.close()
	h.frames(3)
	c.chooser = func(_ context.Context, filter *fileFilter, _ bool) fileChoice {
		return fileChoice{paths: paths[:1], path: paths[0]}
	}
	c.attachmentActions[1].click.Click()
	settleBoxUntil(t, h, func() bool { return len(c.files.files) == 1 })
	if !c.files.way.Documents {
		t.Fatal("the File item did not send as documents")
	}
}

func settleBoxUntil(t *testing.T, h *menuHarness, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		h.frame()
		time.Sleep(5 * time.Millisecond)
	}
}

// english is the catalog the tests read texts from.
func english() localization.Catalog { return localization.For("en") }

// mp3Song is an MP3 of 4 minutes of silent frames, whose ID3v2.3 tag says
// what is set of its title, performer and cover.
func mp3Song(title, performer string, cover []byte) []byte {
	frame := func(id string, data []byte) []byte {
		return append(append([]byte(id), byte(len(data)>>24), byte(len(data)>>16), byte(len(data)>>8), byte(len(data)), 0, 0), data...)
	}
	var body []byte
	if title != "" {
		body = append(body, frame("TIT2", append([]byte{3}, title...))...)
	}
	if performer != "" {
		body = append(body, frame("TPE1", append([]byte{3}, performer...))...)
	}
	if cover != nil {
		body = append(body, frame("APIC", append([]byte("\x00image/png\x00\x03\x00"), cover...))...)
	}
	n := len(body)
	data := append([]byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}, body...)
	audio := make([]byte, 417)
	copy(audio, []byte{0xff, 0xfb, 0x90, 0x64})
	return append(data, bytes.Repeat(audio, 9188)...)
}
