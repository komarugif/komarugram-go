// SPDX-License-Identifier: Unlicense OR MIT

package voice

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/internal/testenv"
)

// TestFileDurationOfEncodedFiles encodes 3.7 s of tone in each format and
// compares the duration read from the headers with it. ffprobe is no
// reference: it guesses a variable bit rate MP3 without a Xing header from
// its first frames.
func TestFileDurationOfEncodedFiles(t *testing.T) {
	testenv.Integration(t, "runs FFmpeg")
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"voice.ogg":  {"-c:a", "libopus", "-b:a", "32k"},
		"voice.opus": {"-c:a", "libopus", "-b:a", "24k"},
		"cbr.mp3":    {"-c:a", "libmp3lame", "-b:a", "64k"},
		"vbr.mp3":    {"-c:a", "libmp3lame", "-q:a", "5", "-write_xing", "0"},
		"low.mp3":    {"-c:a", "libmp3lame", "-ar", "16000", "-b:a", "24k"},
		"tag.mp3":    {"-c:a", "libmp3lame", "-metadata", "title=" + strings.Repeat("x", 3000)},
		"voice.m4a":  {"-c:a", "aac", "-b:a", "64k"},
	} {
		path := filepath.Join(dir, name)
		cmd := append([]string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3.7", "-ac", "1"}, args...)
		if out, err := exec.Command(ffmpeg, append(cmd, path)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", name, err, out)
		}
		const want = 3700 * time.Millisecond
		got, err := FileDuration(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Encoders add up to a few tens of milliseconds of padding.
		if diff := (got - want).Abs(); diff > 60*time.Millisecond {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

func TestFileDurationRejects(t *testing.T) {
	dir := t.TempDir()
	vorbis := oggPage([]byte("\x01vorbis"), 0)
	for name, data := range map[string][]byte{
		"text.mp3":   []byte("not a sound at all, only some words"),
		"vorbis.ogg": vorbis,
		"empty.m4a":  {0, 0, 0, 8, 'f', 't', 'y', 'p'},
		"voice.wav":  []byte("RIFF"),
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if d, err := FileDuration(path); err == nil {
			t.Errorf("%s: read as %v", name, d)
		}
	}
}

func TestFileDurationOggLastPage(t *testing.T) {
	head := []byte("OpusHead\x01\x01")
	head = binary.LittleEndian.AppendUint16(head, 312)
	var data []byte
	data = append(data, oggPage(head, 0)...)
	data = append(data, oggPage([]byte("audio"), 48000*2+312)...)
	// A page on which no packet ends has no position.
	data = append(data, oggPage([]byte("more"), -1)...)
	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := FileDuration(path); err != nil || d != 2*time.Second {
		t.Fatalf("got %v, %v", d, err)
	}
}

// oggPage is an OGG page holding one packet, without a valid checksum,
// which FileDuration does not check.
func oggPage(packet []byte, granule int64) []byte {
	var b bytes.Buffer
	b.WriteString("OggS")
	b.Write([]byte{0, 0})
	b.Write(binary.LittleEndian.AppendUint64(nil, uint64(granule)))
	b.Write(make([]byte, 12))
	b.WriteByte(1)
	b.WriteByte(byte(len(packet)))
	b.Write(packet)
	return b.Bytes()
}

func TestFileMIME(t *testing.T) {
	for path, want := range map[string]string{"a.OGG": "audio/ogg", "b.opus": "audio/ogg", "c.mp3": "audio/mpeg", "d.m4a": "audio/mp4", "e.wav": "", "f": ""} {
		if got := FileMIME(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}
