// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/audio"
	"komarugram/pkg/video"
	"komarugram/pkg/webm"
)

func webmClip(t *testing.T, args ...string) []byte {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "clip.webm")
	all := append([]string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=3"}, args...)
	if out, err := exec.Command(ffmpeg, append(all, path)...).CombinedOutput(); err != nil {
		t.Skipf("this FFmpeg cannot: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func isFormat(err error) bool { return errors.Is(err, errFormat) }

func webmFile(data []byte) *audioFile {
	return &audioFile{r: bytes.NewReader(data), size: int64(len(data))}
}

// A VP9 WebM plays in the sandbox: pictures as the sound's clock comes to them, the
// sound itself being Opus decoded, a tone and no silence.
func TestWebMPlaysInTheSandbox(t *testing.T) {
	data := webmClip(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:v", "libvpx-vp9", "-g", "25", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	round, err := openVideoFile(ctx, webmFile(data))
	if err != nil {
		t.Fatal(err)
	}
	defer round.close()
	if round.index == nil || round.duration() < 2900*time.Millisecond || round.duration() > 3100*time.Millisecond {
		t.Fatalf("%v long", round.duration())
	}
	sound, samples, err := openWebMSound(ctx, round.index, round.duration())
	if err != nil {
		t.Fatal(err)
	}
	defer sound.(*webmSound).close()
	if d := time.Duration(samples) * time.Second / audio.Rate; d < 2900*time.Millisecond || d > 3100*time.Millisecond {
		t.Fatalf("%v of sound", d)
	}
	if err := sound.SeekSample(audio.Rate); err != nil {
		t.Fatal(err)
	}
	pcm := make([]audio.Frame, 4800)
	loud := false
	for range 4 {
		n, err := sound.Read(pcm)
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		for _, f := range pcm[:n] {
			loud = loud || f[0] > 1000 || f[0] < -1000
		}
	}
	if !loud {
		t.Fatal("no sound in it")
	}
	// The clock runs; the pictures follow, the first at once.
	start := time.Now()
	shown := make(chan struct{}, 512)
	go round.run(ctx, func() time.Duration { return time.Since(start) }, func() {
		select {
		case shown <- struct{}{}:
		default:
		}
	})
	deadline := time.After(20 * time.Second)
	count := 0
	var first image.Image
	for count < 10 {
		select {
		case <-shown:
			count++
			if im := round.takeIn(time.Now(), image.Pt(320, 180)); im != nil && first == nil {
				first = im
			}
		case <-deadline:
			t.Fatalf("%d pictures in 20 s", count)
		}
	}
	if first == nil || first.Bounds().Size() != image.Pt(320, 180) {
		t.Fatalf("picture %v", first)
	}
}

// A video without sound plays over silence; sound the sandbox does not decode, and
// video in a codec it does not, go to the external player.
func TestWebMOtherwise(t *testing.T) {
	ctx := context.Background()
	silent := webmClip(t, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8")
	round, err := openVideoFile(ctx, webmFile(silent))
	if err != nil {
		t.Fatal(err)
	}
	defer round.close()
	sound, samples, err := openWebMSound(ctx, round.index, round.duration())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sound.(*silence); !ok || samples <= 0 {
		t.Fatalf("%T, %d samples", sound, samples)
	}
	vp8 := webmClip(t, "-c:v", "libvpx", "-deadline", "realtime", "-cpu-used", "8")
	if _, err := openVideoFile(ctx, webmFile(vp8)); !isFormat(err) {
		t.Fatalf("VP8: %v", err)
	}
	index := &webm.Index{AudioCodec: "A_VORBIS"}
	if _, _, err := openWebMSound(ctx, index, time.Second); !isFormat(err) {
		t.Fatalf("Vorbis: %v", err)
	}
}

// A file that says it lasts milliseconds, as some do, plays as long as its blocks: in
// the sandbox, and with FFmpeg, which believes what it says.
func TestWebMThatLiesOfItsLength(t *testing.T) {
	data := webmClip(t, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8")
	// Matroska's Duration, a float of 8 bytes.
	at := bytes.Index(data, []byte{0x44, 0x89, 0x88})
	if at < 0 {
		t.Skip("no length of 8 bytes in this file")
	}
	binary.BigEndian.PutUint64(data[at+3:], math.Float64bits(32))
	round, err := openVideoFile(context.Background(), webmFile(data))
	if err != nil {
		t.Fatal(err)
	}
	defer round.close()
	if round.duration() < 2900*time.Millisecond {
		t.Fatalf("in the sandbox, %v", round.duration())
	}
	m := model.Message{Media: &model.MessageMedia{MIMEType: "video/webm"}}
	if got := webmLength(m, webmFile(data), video.Media{Duration: 32 * time.Millisecond}); got.Duration < 2900*time.Millisecond {
		t.Fatalf("with FFmpeg, %v", got.Duration)
	}
	m.Media.MIMEType = "video/mp4"
	if got := webmLength(m, webmFile(data), video.Media{Duration: time.Second}); got.Duration != time.Second {
		t.Fatalf("an MP4 read as a WebM: %v", got.Duration)
	}
}

// A WebM plays in the sandbox whatever is told of its size, unless it is told to be
// over HD; with FFmpeg it plays there.
func TestWebMEngine(t *testing.T) {
	video := func(w, h int) model.Message {
		return model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{MIMEType: "video/webm", Width: w, Height: h}}
	}
	for _, c := range []struct {
		m       model.Message
		sandbox bool
		want    string
	}{
		{video(0, 0), true, videoWASM},
		{video(320, 180), true, videoWASM},
		{video(1920, 1080), true, videoExternal},
		{video(0, 0), false, videoFFmpeg},
	} {
		if got := videoEngineOf(c.m, "", c.sandbox); got != c.want {
			t.Errorf("%dx%d, sandbox %v: %s, not %s", c.m.Media.Width, c.m.Media.Height, c.sandbox, got, c.want)
		}
	}
}
