// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"time"

	"komarugram/pkg/program"
	"komarugram/pkg/resample"
	"komarugram/pkg/video"
	"komarugram/pkg/webm"
)

// ffmpegSticker decodes a bounded loop only after hover or autoplay requests
// playback. Static previews are handled by stickerPreview through WASM.
// Larger clips retain the streaming WASM decoder instead of growing the cache.
func (m *Manager) ffmpegSticker(ctx context.Context, e *entry, data []byte) (func(time.Duration) (image.Image, error), bool) {
	path := video.ResolveFFmpeg(e.ffmpeg)
	if path == "" {
		return nil, false
	}
	file, err := webm.Demux(data)
	if err != nil || file.CodecID != "V_VP9" || file.Width <= 0 || file.Height <= 0 || file.Width > 512 || file.Height > 512 {
		return nil, false
	}
	size := resample.Fit(file.Width, file.Height, e.box, false)
	count := len(file.Frames)
	if count == 0 || count > maxStickerClipFrames || file.FrameDuration <= 0 || file.FrameDuration > maxStickerClipDuration/time.Duration(count) || int64(size.X)*int64(size.Y)*4*int64(count) > maxStickerClipBytes {
		return nil, false
	}
	e.mu.Lock()
	e.caching = true
	e.mu.Unlock()
	clip, err := decodeFFmpegSticker(ctx, path, data, size, count, file.FrameDuration)
	e.mu.Lock()
	e.caching = false
	if err == nil && ctx.Err() == nil {
		e.clip = clip
	}
	e.mu.Unlock()
	if err != nil {
		return nil, false
	}
	return func(t time.Duration) (image.Image, error) { return clip.FrameAt(t), nil }, true
}

func decodeFFmpegSticker(ctx context.Context, path string, data []byte, size image.Point, count int, duration time.Duration) (*stickerClip, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Force libvpx: FFmpeg's native VP9 decoder does not preserve WebM alpha.
	// Input stays in memory, with no decrypted files or network protocols.
	cmd := program.CommandContext(ctx, path, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "1", "-protocol_whitelist", "pipe", "-f", "webm", "-c:v", "libvpx-vp9", "-i", "pipe:0",
		"-an", "-sn", "-dn", "-vf", fmt.Sprintf("scale=%d:%d", size.X, size.Y),
		"-threads", "1", "-filter_threads", "1", "-vsync", "0", "-frames:v", fmt.Sprint(count), "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	clip := &stickerClip{frameDuration: duration}
	for range count {
		im := image.NewRGBA(image.Rectangle{Max: size})
		if _, err := io.ReadFull(stdout, im.Pix); err != nil {
			return nil, err
		}
		// Raw FFmpeg RGBA is straight alpha; Gio expects premultiplied pixels.
		for i := 0; i < len(im.Pix); i += 4 {
			alpha := uint32(im.Pix[i+3])
			for c := 0; c < 3; c++ {
				im.Pix[i+c] = byte(uint32(im.Pix[i+c]) * alpha / 255)
			}
		}
		clip.frames = append(clip.frames, im)
		clip.bytes += int64(len(im.Pix))
	}
	// Drain EOF before Wait so a failed decoder cannot look like a valid loop.
	var extra [1]byte
	if n, err := stdout.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("unexpected FFmpeg output: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return clip, nil
}
