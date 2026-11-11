// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/program"
)

// ErrNoH264Encoder means the FFmpeg has no encoder of H.264, the one video
// and GIFs go to Telegram in: Haiku's, for one, decodes it and cannot make it.
var ErrNoH264Encoder = errors.New("this FFmpeg has no H.264 encoder (libx264)")

// h264Encoder is the encoder the client makes H.264 with.
const h264Encoder = "libx264"

// probeLimit is how much of what ffmpeg prints is read: its banner and the
// list of its encoders, which is some 40 KB in a full build.
const probeLimit = 256 << 10

// ffmpegInfo is what an FFmpeg tells of itself.
type ffmpegInfo struct {
	// version is the first line it prints: "ffmpeg version 6.1.6 Copyright …".
	version string
	// encoders is the names of its encoders, nil when it listed none.
	encoders map[string]bool
}

var (
	infoMu sync.Mutex
	// infos is what each program asked told, kept for as long as the client
	// runs; a program that would not tell is kept as one that told nothing.
	infos = map[string]ffmpegInfo{}
)

// probeFFmpeg runs the FFmpeg at the path once and reads both its version
// and its encoders: ffmpeg prints its banner, with the version, as it starts,
// and then the list, which -version alone does not give. ctx bounds it.
func probeFFmpeg(ctx context.Context, path string) (ffmpegInfo, error) {
	out, err := program.Capture(ctx, path, probeLimit, "-encoders")
	if err != nil {
		return ffmpegInfo{}, err
	}
	return parseFFmpeg(out), nil
}

// parseFFmpeg reads what ffmpeg printed for -encoders.
func parseFFmpeg(out string) ffmpegInfo {
	var info ffmpegInfo
	listed := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if info.version == "" {
			info.version = strings.TrimSpace(line)
		}
		if !listed {
			// The list follows a line of dashes, after the legend of its flags.
			listed = strings.TrimSpace(line) == "------"
			continue
		}
		// " V....D libx264   libx264 H.264 / AVC / ...": six flags, then the name.
		if fields := strings.Fields(line); len(fields) >= 2 && len(fields[0]) == 6 {
			if info.encoders == nil {
				info.encoders = map[string]bool{}
			}
			info.encoders[fields[1]] = true
		}
	}
	return info
}

// remember keeps what the program at the path told.
func remember(path string, info ffmpegInfo) {
	infoMu.Lock()
	infos[path] = info
	infoMu.Unlock()
}

// infoOf is what the FFmpeg at the path tells of itself, asked once per path,
// and for ten seconds at most; a program that would not tell is no more asked.
func infoOf(path string) ffmpegInfo {
	infoMu.Lock()
	info, ok := infos[path]
	infoMu.Unlock()
	if ok {
		return info
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, _ = probeFFmpeg(ctx, path)
	remember(path, info)
	return info
}

// HasEncoder tells whether the FFmpeg at the path lists the encoder. It is
// false for one that would not list its encoders.
func HasEncoder(ffmpeg, name string) bool {
	return infoOf(ffmpeg).encoders[name]
}

// lacksEncoder tells whether the FFmpeg at the path is known to lack the
// encoder: one that lists its encoders and not it. A program that would not
// list them is not taken to lack any, which it may be told by failing.
func lacksEncoder(ffmpeg, name string) bool {
	names := infoOf(ffmpeg).encoders
	return names != nil && !names[name]
}

// needH264 is ErrNoH264Encoder when the FFmpeg found for ffmpeg is known to
// lack the encoder; the work that follows asks it before it probes a file.
func needH264(ffmpeg string) error {
	if path := ResolveFFmpeg(ffmpeg); path != "" && lacksEncoder(path, h264Encoder) {
		return ErrNoH264Encoder
	}
	return nil
}

// CanEncodeH264 tells whether the client can make H.264 with the FFmpeg found
// for ffmpeg ("" for the one on the system): there is one, and it is not known
// to lack the encoder. What Telegram shows as a video, and as a GIF, is H.264.
func CanEncodeH264(ffmpeg string) bool {
	path := ResolveFFmpeg(ffmpeg)
	return path != "" && !lacksEncoder(path, h264Encoder)
}
