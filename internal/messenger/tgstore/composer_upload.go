// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/pkg/program"
	"komarugram/pkg/video"
)

// Probe before uploading: video metadata is what makes Telegram render a video
// bubble instead of an opaque file. The filename is an argument, never shell code.
func uploadAttributes(ctx context.Context, path, mime string, asMedia bool, ffmpeg string) ([]tg.DocumentAttributeClass, error) {
	attrs := []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: filepath.Base(path)}}
	if !asMedia || !strings.HasPrefix(mime, "video/") {
		return attrs, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	ffprobe := video.ResolveFFprobe(ffmpeg)
	if ffprobe == "" {
		return nil, errors.New("could not inspect video; install ffprobe or attach it as a file")
	}
	out, err := program.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,duration:format=duration", "-of", "json", "-i", path).Output()
	if err != nil {
		return nil, errors.New("could not inspect video; install ffprobe or attach it as a file")
	}
	var data struct {
		Streams []struct {
			Width, Height int
			Duration      string
		}
		Format struct{ Duration string }
	}
	if err = json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	if len(data.Streams) == 0 {
		return nil, errors.New("no video stream")
	}
	stream := data.Streams[0]
	duration, _ := strconv.ParseFloat(stream.Duration, 64)
	if duration <= 0 {
		duration, _ = strconv.ParseFloat(data.Format.Duration, 64)
	}
	if stream.Width <= 0 || stream.Height <= 0 {
		return nil, errors.New("invalid video dimensions")
	}
	attrs = append(attrs, &tg.DocumentAttributeVideo{W: stream.Width, H: stream.Height, Duration: duration, SupportsStreaming: mime == "video/mp4"})
	return attrs, nil
}
