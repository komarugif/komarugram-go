// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"errors"
	"image"
	"io"
	"runtime"
	"strings"
	"testing"

	"komarugram/pkg/program"
)

// An FFmpeg that takes more memory than it may is stopped, and its
// reader told why; one within its bound decodes the video.
func TestFFmpegMemoryBound(t *testing.T) {
	ffmpeg := ResolveFFmpeg("")
	if ffmpeg == "" {
		t.Skip("no FFmpeg")
	}
	switch runtime.GOOS {
	case "windows", "darwin", "linux":
	default:
		t.Skip("memory is not bounded here")
	}
	decode := func(memory uint64) (int64, error) {
		// The threads the player and the frames take (player.go,
		// stream.go): by default a thread a core for the decoder and the
		// encoder, each with a stack RLIMIT_DATA counts, and on a machine
		// of many cores the FFmpeg within its bound could not start.
		args := append(LimitArgs(memory), "-hide_banner", "-loglevel", "error", "-nostdin",
			"-threads", "2", "-i", "../../assets/video.mp4", "-t", "3", "-an")
		cmd := program.Command(ffmpeg, append(args, rawOutputArgs("rgba")...)...)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		l, err := startLimited(cmd, memory)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := io.Copy(io.Discard, out)
		err = cmd.Wait()
		return n, l.explain(err)
	}
	// 720×1280 pictures, four bytes a pixel, 36 s of them: the decoder
	// alone takes more than 24 MiB.
	n, err := decode(24 << 20)
	if !errors.Is(err, program.ErrMemory) && runtime.GOOS != "linux" {
		t.Fatalf("an FFmpeg over its bound: %d bytes, %v", n, err)
	}
	if err == nil {
		t.Fatalf("an FFmpeg over its bound decoded %d bytes", n)
	}
	t.Logf("over its bound: %d bytes, %v", n, err)
	if n, err := decode(DecodeMemory(image.Pt(720, 1280))); err != nil || n == 0 {
		t.Fatalf("within its bound: %d bytes, %v", n, err)
	}
}

// The encoder of raw pictures takes one thread however many the machine
// has: by default it takes a thread a core, each with a stack the bound
// counts, and an FFmpeg on a many-core machine could not start. 64 threads
// stand for the cores, asked before the arguments under test.
func TestRawOutputOneThread(t *testing.T) {
	ffmpeg := ResolveFFmpeg("")
	if ffmpeg == "" {
		t.Skip("no FFmpeg")
	}
	if runtime.GOOS != "linux" {
		t.Skip("RLIMIT_DATA counts the stacks of threads on Linux")
	}
	memory := DecodeMemory(image.Pt(720, 1280))
	args := append(LimitArgs(memory), "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", "../../assets/video.mp4", "-t", "1", "-an", "-vf", "scale=720:1280", "-threads", "64")
	cmd := program.Command(ffmpeg, append(args, rawOutputArgs("rgba")...)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	l, err := startLimited(cmd, memory)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, out)
	if err := l.explain(cmd.Wait()); err != nil || n == 0 {
		t.Fatalf("%d bytes, %v: %s", n, err, stderr.String())
	}
}
