// SPDX-License-Identifier: Unlicense OR MIT

// Package ffmpegtest is for tests that run FFmpeg: they make their clips and
// convert them with encoders a build of FFmpeg may lack, as Haiku's has no
// libx264. The tests of pkg/video itself use their own requireEncoder.
package ffmpegtest

import (
	"testing"

	"komarugram/pkg/video"
)

// RequireEncoder skips the test when the FFmpeg lacks the encoder.
func RequireEncoder(t testing.TB, ffmpeg, name string) {
	t.Helper()
	if !video.HasEncoder(ffmpeg, name) {
		t.Skipf("this FFmpeg has no %s encoder", name)
	}
}
