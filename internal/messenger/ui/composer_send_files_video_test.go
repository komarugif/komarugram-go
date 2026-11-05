// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"testing"
)

// The box tells a video's shape and length without FFmpeg too.
func TestBoxFileWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("KOMARUGRAM_AVCDEC", t.TempDir()+"/none.wasm")
	_, _, shown, long, err := readBoxFile(context.Background(), "", "../../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if shown.X <= 0 || shown.Y <= 0 || long <= 0 {
		t.Fatalf("shown %v, %v long", shown, long)
	}
}
