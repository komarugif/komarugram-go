// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"komarugram/pkg/program"
)

// requireEncoder skips the test when the FFmpeg lacks the encoder.
func requireEncoder(t testing.TB, ffmpeg, name string) {
	t.Helper()
	if !HasEncoder(ffmpeg, name) {
		t.Skipf("this FFmpeg has no %s encoder", name)
	}
}

// fakeListingEnv, set, makes the test binary act as an ffmpeg that lists the
// encoders it holds: a copy of the binary stands for the program.
const fakeListingEnv = "KOMARUGRAM_FAKE_FFMPEG_ENCODERS"

func TestMain(m *testing.M) {
	if listing := os.Getenv(fakeListingEnv); listing != "" {
		fmt.Print(listing)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeFFmpeg is a program that lists the encoders given as ffmpeg does, and
// nothing besides; with no listing it lists nothing and succeeds.
func fakeFFmpeg(t *testing.T, listing string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if listing == "" {
		listing = "\n"
	}
	t.Setenv(fakeListingEnv, listing)
	return path
}

const withoutX264 = "Encoders:\n" +
	" ------\n" +
	" V....D mpeg4               MPEG-4 part 2 (libx264 is not it)\n" +
	" A....D aac                 AAC (Advanced Audio Coding)\n"

const withX264 = withoutX264 + " V....D libx264             libx264 H.264 / AVC\n"

// The encoders are read from what ffmpeg lists: the name in the second
// column, not a word of a description.
func TestHasEncoder(t *testing.T) {
	ffmpeg := fakeFFmpeg(t, withoutX264)
	if !HasEncoder(ffmpeg, "mpeg4") || !HasEncoder(ffmpeg, "aac") {
		t.Error("an encoder listed was not found")
	}
	if HasEncoder(ffmpeg, "libx264") {
		t.Error("libx264 found in a description")
	}
	if HasEncoder(filepath.Join(t.TempDir(), "none"), "aac") {
		t.Error("an encoder found without a program")
	}
}

// An FFmpeg that lists its encoders without libx264 cannot make what Telegram
// shows, and the work says so before it starts; one that would not list them
// is left to try.
func TestWithoutH264Encoder(t *testing.T) {
	lacking := fakeFFmpeg(t, withoutX264)
	if CanEncodeH264(lacking) {
		t.Error("an FFmpeg without libx264 can encode H.264")
	}
	if err := ConvertToMP4(context.Background(), lacking, "in.webm", filepath.Join(t.TempDir(), "out.mp4"), func(GIFProgress) {}); !errors.Is(err, ErrNoH264Encoder) {
		t.Errorf("ConvertToMP4: %v", err)
	}
	if err := EncodeEdit(context.Background(), lacking, "in.mp4", filepath.Join(t.TempDir(), "out.mp4"), EditPlan{}, func(GIFProgress) {}); !errors.Is(err, ErrNoH264Encoder) {
		t.Errorf("EncodeEdit: %v", err)
	}
	if _, err := ConvertGIF(context.Background(), lacking, "in.mp4", filepath.Join(t.TempDir(), "out.mp4"), func(GIFProgress) {}); !errors.Is(err, ErrNoH264Encoder) {
		t.Errorf("ConvertGIF: %v", err)
	}
	if !CanEncodeH264(fakeFFmpeg(t, withX264)) {
		t.Error("an FFmpeg with libx264 cannot encode H.264")
	}
	if !CanEncodeH264(fakeFFmpeg(t, "")) {
		t.Error("an FFmpeg that lists nothing is taken to lack the encoder")
	}
}

// What ffmpeg prints for -encoders is a banner, a legend of the flags, a line
// of dashes and the list: the version is the first line and only the list is
// of encoders, not a word of the banner or the legend.
func TestParseFFmpeg(t *testing.T) {
	out := "ffmpeg version 6.1.6 Copyright (c) 2000-2026 the FFmpeg developers\n" +
		"  built with gcc 13\n" +
		"  configuration: --enable-gpl\n" +
		"  libavutil      58. 29.100 / 58. 29.100\n" +
		"Encoders:\n" +
		" V..... = Video\n" +
		" A..... = Audio\n" +
		" ------\n" +
		" V....D libx264              libx264 H.264 / AVC\n" +
		" A....D aac                  AAC (Advanced Audio Coding)\n"
	info := parseFFmpeg(out)
	if info.version != "ffmpeg version 6.1.6 Copyright (c) 2000-2026 the FFmpeg developers" {
		t.Errorf("version %q", info.version)
	}
	if !info.encoders["libx264"] || !info.encoders["aac"] || len(info.encoders) != 2 {
		t.Errorf("encoders %v", info.encoders)
	}
	if got := parseFFmpeg("ffmpeg version 7\n"); got.encoders != nil {
		t.Errorf("encoders %v of an FFmpeg that listed none", got.encoders)
	}
}

// The real FFmpeg tells its version and the same encoders as its own listing
// does, from the one run.
func TestProbeFFmpeg(t *testing.T) {
	path := ResolveFFmpeg("")
	if path == "" {
		t.Skip("no FFmpeg")
	}
	version, err := CheckFFmpeg(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "ffmpeg version ") {
		t.Errorf("version %q", version)
	}
	listing, err := program.Command(path, "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aac", "libx264", "mpeg4", "libvpx-vp9"} {
		want := strings.Contains(string(listing), " "+name+" ")
		if got := HasEncoder(path, name); got != want {
			t.Errorf("%s: %v, the listing %v", name, got, want)
		}
	}
	if CanEncodeH264(path) != strings.Contains(string(listing), " libx264 ") {
		t.Error("CanEncodeH264 says otherwise than the listing")
	}
}
