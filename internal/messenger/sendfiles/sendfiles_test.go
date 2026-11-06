// SPDX-License-Identifier: Unlicense OR MIT

package sendfiles

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// gradient is a picture whose corners differ, so that a turn shows.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / max(w-1, 1)), G: uint8(y * 255 / max(h-1, 1)), B: 90, A: 255})
		}
	}
	return img
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withOrientation puts an Exif segment saying o after a JPEG's start.
func withOrientation(data []byte, o int, order binary.AppendByteOrder) []byte {
	tiff := make([]byte, 0, 32)
	if order == binary.AppendByteOrder(binary.LittleEndian) {
		tiff = append(tiff, 'I', 'I')
	} else {
		tiff = append(tiff, 'M', 'M')
	}
	tiff = order.AppendUint16(tiff, 42)
	tiff = order.AppendUint32(tiff, 8)
	tiff = order.AppendUint16(tiff, 1)
	tiff = order.AppendUint16(tiff, 0x0112)
	tiff = order.AppendUint16(tiff, 3)
	tiff = order.AppendUint32(tiff, 1)
	tiff = order.AppendUint16(tiff, uint16(o))
	tiff = append(tiff, 0, 0)
	tiff = order.AppendUint32(tiff, 0)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	out := append([]byte{0xff, 0xd8, 0xff, 0xe1}, binary.BigEndian.AppendUint16(nil, uint16(len(segment)+2))...)
	out = append(out, segment...)
	return append(out, data[2:]...)
}

func TestInspectSaysWhatAFileIsSentAs(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var g bytes.Buffer
	if err := gif.Encode(&g, gradient(4, 4), nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		data []byte
		kind Kind
		w, h int
	}{
		{"a.png", pngBytes(t, gradient(30, 20)), KindPhoto, 30, 20},
		{"b.jpg", jpegBytes(t, gradient(16, 9)), KindPhoto, 16, 9},
		{"c.gif", g.Bytes(), KindAnimation, 0, 0},
		{"d.mp4", []byte("not really a video"), KindVideo, 0, 0},
		{"e.MOV", []byte("not really a video"), KindVideo, 0, 0},
		{"f.txt", []byte("text"), KindFile, 0, 0},
		{"g.png", []byte("a png that is not one"), KindFile, 0, 0},
		// Too wide for a photo: no side is above twenty times the other.
		{"h.png", pngBytes(t, gradient(210, 10)), KindFile, 0, 0},
		{"i.png", pngBytes(t, gradient(200, 10)), KindPhoto, 200, 10},
	} {
		f, err := Inspect(write(c.name, c.data))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if f.Kind != c.kind || f.Width != c.w || f.Height != c.h || f.Name != c.name || f.Size != int64(len(c.data)) {
			t.Errorf("%s: %+v, want kind %d, %dx%d", c.name, f, c.kind, c.w, c.h)
		}
	}
	if _, err := Inspect(write("empty.png", nil)); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty file: %v", err)
	}
	if _, err := Inspect(dir); !errors.Is(err, ErrDirectory) {
		t.Errorf("folder: %v", err)
	}
	if _, err := Inspect(filepath.Join(dir, "missing.png")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}

// A photo taken sideways is shown, measured and sent upright.
// A photo is shown turned as its Exif asks, and one that asks nothing as it is.
func TestShownPhotoIsTurned(t *testing.T) {
	dir := t.TempDir()
	plain := jpegBytes(t, gradient(40, 20))
	path := filepath.Join(dir, "plain.jpg")
	if err := os.WriteFile(path, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ShownPhoto(path); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("plain: %d bytes, %v", len(got), err)
	}
	if err := os.WriteFile(path, withOrientation(plain, 6, binary.BigEndian), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ShownPhoto(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 20 || cfg.Height != 40 {
		t.Fatalf("turned: %dx%d, %v", cfg.Width, cfg.Height, err)
	}
}

func TestExifOrientationTurnsPhotos(t *testing.T) {
	for _, order := range []binary.AppendByteOrder{binary.LittleEndian, binary.BigEndian} {
		data := withOrientation(jpegBytes(t, gradient(40, 20)), 6, order)
		if got := orientationOf(data); got != 6 {
			t.Fatalf("%v: orientation %d", order, got)
		}
		path := writeFile(t, "turned.jpg", data)
		f, err := Inspect(path)
		if err != nil || f.Width != 20 || f.Height != 40 {
			t.Fatalf("%v: inspected %+v, %v", order, f, err)
		}
		thumb, err := Thumbnail(path, 100)
		if err != nil || thumb.Bounds().Dx() != 20 || thumb.Bounds().Dy() != 40 {
			t.Fatalf("%v: thumbnail %v, %v", order, thumb.Bounds(), err)
		}
		// Turned a quarter clockwise, the left column of the picture, red
		// at its top, becomes the top row; the top left corner (least red,
		// least green) lands at the top right.
		tl := thumb.RGBAAt(thumb.Bounds().Dx()-1, 0)
		bl := thumb.RGBAAt(0, 0)
		if tl.R > 40 || tl.G > 40 || bl.G < 200 {
			t.Fatalf("%v: corners %v %v", order, tl, bl)
		}
		photo, err := PreparePhoto(path, false)
		if err != nil || photo.Width != 20 || photo.Height != 40 {
			t.Fatalf("%v: prepared %dx%d, %v", order, photo.Width, photo.Height, err)
		}
		if orientationOf(photo.JPEG) != 1 {
			t.Fatalf("%v: the prepared photo still asks for a turn", order)
		}
	}
	for _, o := range []int{0, 9, 255} {
		if got := orientationOf(withOrientation(jpegBytes(t, gradient(8, 8)), o, binary.LittleEndian)); got != 1 {
			t.Errorf("orientation %d read as %d", o, got)
		}
	}
	if orientationOf([]byte("not a jpeg")) != 1 || orientationOf(nil) != 1 {
		t.Error("data that is not a JPEG asks for a turn")
	}
}

func TestPreparePhoto(t *testing.T) {
	decode := func(b []byte) (image.Image, string) {
		t.Helper()
		img, format, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return img, format
	}
	// Large pictures fit 1280, or 2560 in high quality; the shape stays.
	big := writeFile(t, "big.png", pngBytes(t, gradient(3000, 2000)))
	for _, c := range []struct {
		high bool
		w, h int
	}{{false, 1280, 853}, {true, 2560, 1706}} {
		p, err := PreparePhoto(big, c.high)
		if err != nil || p.Width != c.w || p.Height != c.h {
			t.Fatalf("high %v: %dx%d, %v", c.high, p.Width, p.Height, err)
		}
		img, format := decode(p.JPEG)
		if format != "jpeg" || img.Bounds().Dx() != c.w || img.Bounds().Dy() != c.h {
			t.Fatalf("high %v: %s %v", c.high, format, img.Bounds())
		}
	}
	// A JPEG that fits goes as it is; a PNG that fits is saved as JPEG.
	small := jpegBytes(t, gradient(300, 200))
	p, err := PreparePhoto(writeFile(t, "small.jpg", small), false)
	if err != nil || !bytes.Equal(p.JPEG, small) || p.Width != 300 || p.Height != 200 {
		t.Fatalf("small jpeg: %+v, %v", p, err)
	}
	p, err = PreparePhoto(writeFile(t, "small.png", pngBytes(t, gradient(300, 200))), false)
	if _, format := decode(p.JPEG); err != nil || format != "jpeg" {
		t.Fatalf("small png: %s, %v", format, err)
	}
	// Alpha lies on white.
	clear := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	clear.Set(0, 0, color.NRGBA{R: 255, A: 255})
	p, err = PreparePhoto(writeFile(t, "clear.png", pngBytes(t, clear)), false)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := decode(p.JPEG)
	if r, g, b, _ := img.At(10, 10).RGBA(); r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("a clear pixel became %v %v %v", r>>8, g>>8, b>>8)
	}
	if _, err := PreparePhoto(writeFile(t, "no.png", []byte("no")), false); err == nil {
		t.Fatal("a file that is no picture was prepared")
	}
}

func files(kinds ...Kind) []File {
	var out []File
	for i, k := range kinds {
		out = append(out, File{Name: string(rune('a' + i)), Kind: k, Width: 2000, Height: 1000})
	}
	return out
}

func names(g []Group) string {
	var parts []string
	for _, group := range g {
		var n []string
		for _, f := range group.Files {
			n = append(n, f.Name)
		}
		parts = append(parts, strings.Join(n, "")+":"+[]string{"-", "media", "files", "music"}[group.Album])
	}
	return strings.Join(parts, " ")
}

// The grouping is Telegram Desktop's: neighbours of one album type go
// together, ten at most; a file that would be alone is not in an album.
func TestDivide(t *testing.T) {
	photo, video, file, anim, music := KindPhoto, KindVideo, KindFile, KindAnimation, KindMusic
	group := Way{Group: true}
	for _, c := range []struct {
		name  string
		files []File
		way   Way
		want  string
	}{
		{"three photos", files(photo, photo, photo), group, "abc:media"},
		{"photos and videos", files(photo, video, photo), group, "abc:media"},
		{"not grouped", files(photo, photo, file), Way{}, "a:- b:- c:-"},
		{"a photo among files", files(file, photo, file), group, "a:- b:- c:-"},
		{"documents", files(file, photo, video), Way{Group: true, Documents: true}, "abc:files"},
		{"files", files(file, file), group, "ab:files"},
		{"a file after photos", files(photo, photo, file, file), group, "ab:media cd:files"},
		{"a gif splits an album", files(photo, photo, anim, photo, photo), group, "ab:media c:- de:media"},
		{"two gifs", files(anim, anim), group, "a:- b:-"},
		{"one", files(photo), group, "a:-"},
		{"none", nil, group, ""},
		{"eleven", files(photo, photo, photo, photo, photo, photo, photo, photo, photo, photo, photo), group, "abcdefghij:media k:-"},
		{"music", files(music, music, file, music), group, "ab:music c:- d:-"},
		{"music is music as documents", files(music, music), Way{Group: true, Documents: true}, "ab:music"},
		{"music not grouped", files(music, music), Way{}, "a:- b:-"},
		{"twelve", files(photo, photo, photo, photo, photo, photo, photo, photo, photo, photo, photo, photo), group, "abcdefghij:media kl:media"},
	} {
		if got := names(Divide(c.files, c.way)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCaptionGoesToTheLastMessage(t *testing.T) {
	way := Way{Group: true}
	for _, c := range []struct {
		name        string
		files       []File
		group, file int
	}{
		{"an album of photos: its first", files(KindPhoto, KindPhoto, KindPhoto), 0, 0},
		{"files: the last", files(KindFile, KindFile), 0, 1},
		{"the last group's first, when it is media", files(KindFile, KindFile, KindAnimation, KindPhoto, KindVideo), 2, 0},
		{"a single file", files(KindFile), 0, 0},
	} {
		g, f := CaptionTarget(Divide(c.files, way))
		if g != c.group || f != c.file {
			t.Errorf("%s: %d, %d; want %d, %d", c.name, g, f, c.group, c.file)
		}
	}
	if g, f := CaptionTarget(nil); g != -1 || f != -1 {
		t.Errorf("nothing: %d, %d", g, f)
	}
}

func TestOptionsOfTheBox(t *testing.T) {
	photos := []File{{Kind: KindPhoto, Width: 4000, Height: 3000}, {Kind: KindPhoto, Width: 800, Height: 600}}
	if !HasGroupOption(photos) || HasGroupOption(photos[:1]) || HasGroupOption(files(KindFile, KindAnimation)) || HasGroupOption(files(KindAnimation, KindAnimation)) {
		t.Error("group option")
	}
	if !HasGroupOption(files(KindFile, KindPhoto)) || !HasGroupOption(files(KindVideo, KindPhoto)) || HasGroupOption(files(KindFile, KindVideo)) ||
		!HasGroupOption(files(KindMusic, KindMusic)) || HasGroupOption(files(KindMusic, KindFile)) || HasGroupOption(files(KindPhoto, KindMusic)) {
		t.Error("group option with mixed files")
	}
	if !HasDocumentsOption(photos) || HasDocumentsOption(files(KindFile, KindAnimation)) {
		t.Error("documents option")
	}
	if !HasHighQualityOption(photos, Way{}) || HasHighQualityOption(photos, Way{Documents: true}) || HasHighQualityOption(photos[1:], Way{}) {
		t.Error("high quality option")
	}
	for _, c := range []struct {
		files []File
		way   Way
		want  Title
	}{
		{files(KindPhoto), Way{}, TitleImage},
		{files(KindPhoto), Way{Documents: true}, TitleFile},
		{files(KindVideo), Way{}, TitleVideo},
		{files(KindFile), Way{}, TitleFile},
		{files(KindPhoto, KindPhoto), Way{}, TitleImages},
		{files(KindPhoto, KindVideo), Way{}, TitleFiles},
		{files(KindPhoto, KindPhoto), Way{Documents: true}, TitleFiles},
	} {
		if got := TitleOf(c.files, c.way); got != c.want {
			t.Errorf("%v %+v: %d, want %d", c.files, c.way, got, c.want)
		}
	}
	// As Telegram Desktop writes them: tenths cut, megabytes at most.
	sizes := []int64{0, 9, 1023, 1024, 1536, 1<<20 - 1, 5 << 20, 5<<20 + 1<<20*99/100, 3 << 30}
	want := []string{"0 B", "9 B", "1023 B", "1.0 KB", "1.5 KB", "1023.9 KB", "5.0 MB", "5.9 MB", "3072.0 MB"}
	for i, n := range sizes {
		if got := Size(n); got != want[i] {
			t.Errorf("Size(%d) = %q, want %q", n, got, want[i])
		}
	}
	if got := SizeIn(1536, "Б", "КБ", "МБ"); got != "1.5 КБ" {
		t.Errorf("in Russian: %q", got)
	}
}

// mp3 is a file of n silent frames of MPEG-1 layer III at 44.1 kHz, after
// an ID3v2.3 tag of frames.
func mp3(n int, frames ...[]byte) []byte {
	body := bytes.Join(frames, nil)
	size := len(body)
	out := append([]byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}, body...)
	frame := make([]byte, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x64})
	return append(out, bytes.Repeat(frame, n)...)
}

func id3Frame(id string, data []byte) []byte {
	return append(append([]byte(id), binary.BigEndian.AppendUint32(nil, uint32(len(data)))...), append([]byte{0, 0}, data...)...)
}

// Audio files that say how long they play are music, with what their tags
// say; the others go as files.
func TestInspectFindsMusic(t *testing.T) {
	cover := pngBytes(t, gradient(600, 500))
	apic := append([]byte("\x00image/png\x00\x03\x00"), cover...)
	f, err := Inspect(writeFile(t, "song.MP3", mp3(100, id3Frame("TIT2", []byte("\x03Песня")), id3Frame("TPE1", []byte("\x00Band")), id3Frame("APIC", apic))))
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind != KindMusic || f.MIME != "audio/mpeg" || f.Title != "Песня" || f.Performer != "Band" || !bytes.Equal(f.Cover, cover) || f.Duration.Round(time.Millisecond) != 2612*time.Millisecond {
		t.Fatalf("kind %d, %s, %q by %q, %v, cover of %d bytes", f.Kind, f.MIME, f.Title, f.Performer, f.Duration, len(f.Cover))
	}
	thumb, err := DocumentThumbnail(f.Cover)
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(thumb)); err != nil || cfg.Width != 320 || cfg.Height != 266 {
		t.Fatalf("thumbnail %+v, %v", cfg, err)
	}
	if f, err := Inspect(writeFile(t, "broken.mp3", []byte("no frames here"))); err != nil || f.Kind != KindFile {
		t.Fatalf("a broken MP3: %+v, %v", f, err)
	}
	if f, err := Inspect(writeFile(t, "sound.flac", []byte("no FLAC"))); err != nil || f.Kind != KindFile || f.MIME != "audio/flac" {
		t.Fatalf("a broken FLAC: %+v, %v", f, err)
	}
}

// Dragged files offer the areas Telegram Desktop's do.
func TestDropStateOf(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var g bytes.Buffer
	if err := gif.Encode(&g, gradient(4, 4), nil); err != nil {
		t.Fatal(err)
	}
	photo := write("a.png", pngBytes(t, gradient(30, 20)))
	jpg := write("b.jpg", jpegBytes(t, gradient(16, 9)))
	video := write("c.mp4", []byte("not really a video"))
	anim := write("d.gif", g.Bytes())
	text := write("e.txt", []byte("text"))
	broken := write("f.png", []byte("a png that is not one"))
	for _, c := range []struct {
		name  string
		paths []string
		want  DropState
	}{
		{"nothing", nil, DropNone},
		{"pictures", []string{photo, jpg}, DropPhotos},
		{"a picture and a video", []string{photo, video}, DropMedia},
		{"a gif is media, not a photo", []string{anim}, DropMedia},
		{"a picture that does not decode", []string{broken}, DropMedia},
		{"a text among pictures", []string{photo, text}, DropFiles},
		{"a folder", []string{dir}, DropNone},
		{"a folder among files", []string{text, dir}, DropNone},
		{"a file that is gone", []string{filepath.Join(dir, "gone.txt")}, DropNone},
	} {
		if got := DropStateOf(c.paths); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
