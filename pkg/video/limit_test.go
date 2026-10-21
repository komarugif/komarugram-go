// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"errors"
	"image"
	"io"
	"runtime"
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
		cmd := program.Command(ffmpeg, append(LimitArgs(memory), "-hide_banner", "-loglevel", "error", "-nostdin",
			"-i", "../../assets/video.mp4", "-t", "3", "-an", "-pix_fmt", "rgba", "-f", "rawvideo", "-")...)
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
