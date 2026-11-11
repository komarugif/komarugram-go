// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"komarugram/pkg/program"
)

var (
	ErrNotExecutable = errors.New("FFmpeg path is not executable")
	ErrWrongProgram  = errors.New("the program is not FFmpeg")
)

// ResolveFFmpeg prefers a custom executable, falling back to PATH. It is ""
// when there is neither.
func ResolveFFmpeg(custom string) string {
	if filepath.IsAbs(custom) && program.IsExecutable(custom) {
		return custom
	}
	path, _ := program.LookPath("ffmpeg")
	return path
}

// CheckFFmpeg validates a program explicitly selected by the user.
func CheckFFmpeg(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || !program.IsExecutable(path) {
		return "", ErrNotExecutable
	}
	// One run tells the version and the encoders, which are kept for the
	// questions to come: -version alone would give the first.
	info, err := probeFFmpeg(ctx, path)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(info.version, "ffmpeg version ") {
		return "", ErrWrongProgram
	}
	remember(path, info)
	return info.version, nil
}

// ResolveFFprobe is the ffprobe beside the FFmpeg ResolveFFmpeg(custom)
// finds, or else the one on PATH. It is "" when there is neither.
func ResolveFFprobe(custom string) string {
	if ffmpeg := ResolveFFmpeg(custom); ffmpeg != "" {
		name := "ffprobe"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		sibling := filepath.Join(filepath.Dir(ffmpeg), name)
		if program.IsExecutable(sibling) {
			return sibling
		}
	}
	path, _ := program.LookPath("ffprobe")
	return path
}
