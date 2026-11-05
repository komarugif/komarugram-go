// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"komarugram/pkg/mp4"
)

// Files as FFmpeg makes them are read as ffprobe reads them: each case makes a
// file, and an encoder this FFmpeg lacks only skips its case.
func TestReadContainerAsFFprobe(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if ResolveFFprobe("") == "" {
		t.Skip("ffprobe unavailable")
	}
	input := []string{"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=2"}
	sound := []string{"-f", "lavfi", "-i", "sine=frequency=440:duration=2"}
	x264 := []string{"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p"}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	cases := []struct {
		name      string
		args      []string
		streaming bool
		sound     bool
	}{
		{"moov-first.mp4", join(input, x264, []string{"-movflags", "+faststart"}), true, false},
		{"moov-last.mp4", join(input, x264), false, false},
		{"sound.mp4", join(input, sound, x264, []string{"-c:a", "aac", "-movflags", "+faststart"}), true, true},
		{"phone.mov", join(input, sound, x264, []string{"-c:a", "aac"}), false, true},
		{"hevc.mp4", join(input, []string{"-c:v", "libx265", "-preset", "ultrafast", "-tag:v", "hvc1", "-x265-params", "log-level=none"}), false, false},
		{"av1.mp4", join(input, []string{"-c:v", "libsvtav1", "-preset", "12"}), false, false},
		{"vp9.webm", join(input, sound, []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus"}), false, true},
		{"h264.mkv", join(input, x264), false, false},
		{"fragments.mp4", join(input, x264, []string{"-movflags", "+frag_keyframe+empty_moov+default_base_moof"}), true, false},
		{"small.3gp", join(input, []string{"-c:v", "mpeg4", "-vf", "scale=176:144,setsar=1"}), false, false},
	}
	ctx := context.Background()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), c.name)
			args := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, c.args...)
			if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
				t.Skipf("this FFmpeg cannot: %v: %s", err, out)
			}
			got, err := ReadContainer(path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := ProbeMedia(ctx, path, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Width != want.Width || got.Height != want.Height || got.Sound != want.Sound || got.Sound != c.sound || got.Streaming != c.streaming {
				t.Fatalf("%+v, ffprobe %+v", got, want)
			}
			if d := got.Duration - want.Duration; d < -100*time.Millisecond || d > 100*time.Millisecond {
				t.Fatalf("%v, ffprobe %v", got.Duration, want.Duration)
			}
		})
	}
}

// The AAC of a MOV, which lies in a wave box of its own, is found: without it the
// sandbox sends a MOV to the external player.
func TestSoundOfAMOV(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "clip.mov")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", path).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mp4.DemuxAudioAt(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
}

// A recording in a stream, as from a browser, says no length, and is added up.
func TestReadContainerStreamedWebM(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=3",
		"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-f", "webm", "-live", "1", "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		t.Skipf("this FFmpeg cannot: %v", err)
	}
	path := filepath.Join(t.TempDir(), "stream.webm")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadContainer(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Width != 320 || c.Height != 180 || c.Duration < 2800*time.Millisecond || c.Duration > 3100*time.Millisecond {
		t.Fatalf("%+v", c)
	}
}

// A WebM that says it lasts milliseconds is told as long as its blocks: ffprobe
// believes it, and the bar of a video would end at once.
func TestReadContainerWebMThatLies(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "clip.webm")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=3", "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", path).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(data, []byte{0x44, 0x89, 0x88})
	if at < 0 {
		t.Skip("no length of 8 bytes in this file")
	}
	binary.BigEndian.PutUint64(data[at+3:], math.Float64bits(32))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadContainer(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Duration < 2900*time.Millisecond || c.Duration > 3100*time.Millisecond {
		t.Fatalf("%v long", c.Duration)
	}
}

// The turn a phone records with swaps the sides, as ffprobe reports them.
func TestReadContainerTurned(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	plain, turned := filepath.Join(dir, "plain.mp4"), filepath.Join(dir, "turned.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", plain).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-display_rotation", "-90", "-i", plain, "-c", "copy", turned).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	c, err := ReadContainer(turned)
	if err != nil {
		t.Fatal(err)
	}
	if c.Width != 180 || c.Height != 320 || c.Turn != 90 {
		t.Fatalf("%+v", c)
	}
	probed, err := ProbeMedia(context.Background(), turned, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Width != probed.Width || c.Height != probed.Height {
		t.Fatalf("%+v, ffprobe %+v", c, probed)
	}
}

// What it cannot read, AVI say, is an error for whoever has ffprobe to ask.
func TestReadContainerOther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("RIFF\x00\x10\x00\x00AVI LIST and the rest of it"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadContainer(path); err == nil {
		t.Fatal("read")
	}
	if _, err := ReadContainer(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("read a file that is not there")
	}
}
