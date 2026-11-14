// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"context"
	"errors"
	"image"
	"log"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/hwvideo"
	"komarugram/pkg/mp4"
)

// gifVideo is the video of a GIF as playVideoFrames plays it: decoded in
// the sandbox (h264.Video) or by the GPU, in the helper (hwvideo.Video).
type gifVideo interface {
	Size() image.Point
	Pictures() int
	Time(i int) time.Duration
	Next(ctx context.Context) (int, error)
	Seek(ctx context.Context, t time.Duration) (int, error)
	SetFast(ctx context.Context, fast bool) error
	// Picture and PictureInto give nil where the picture could not be
	// had, as from a helper that ended.
	Picture(size image.Point) *image.RGBA
	PictureInto(dst *image.RGBA) *image.RGBA
	Close(ctx context.Context)
}

// errNoPicture is the error of a video whose decoder gave no picture.
var errNoPicture = errors.New("the decoder gave no picture")

// SetHardware makes the GIFs of the list of saved ones (GIFRow) decode on
// the GPU with client, or, with nil, as every other GIF. It runs on the UI
// goroutine. New workers capture the setting.
func (m *Manager) SetHardware(client *hwvideo.Client) {
	if m.hardware == client {
		return
	}
	m.hardware = client
	m.hardwareGIFs = nil
	m.Release()
}

// hardwareRow reports whether the GIFs of a row of the list of saved ones
// play one by one, each decoded on the GPU, and marks them to. They do
// unless hardware decoding is off or failed, or failed for one of them:
// the row then plays as it did without, with one FFmpeg.
func (m *Manager) hardwareRow(msgs []model.Message) bool {
	if m.hardware == nil || m.paused || m.quiet || m.hardware.Failed() != nil {
		return false
	}
	for _, msg := range msgs {
		if msg.Kind != model.MessageGIF || msg.Media == nil || msg.Media.MIMEType == "image/gif" {
			return false
		}
		if _, failed := m.hardwareFailed.Load(msg.Media.ID); failed {
			return false
		}
	}
	if m.hardwareGIFs == nil {
		m.hardwareGIFs = map[string]bool{}
	}
	for _, msg := range msgs {
		m.hardwareGIFs[msg.Media.ID] = true
	}
	return len(msgs) > 0
}

// playGIFHardware plays an MP4 GIF decoded on the GPU whenever it is to
// animate, as playGIFSandbox does in the sandbox. played is false where it
// cannot: a file the hardware does not decode, or a helper that ended. The
// GIF, id, is then played in software, from now on.
func (m *Manager) playGIFHardware(ctx context.Context, e *entry, id string, data []byte, publish func(image.Image, error)) (played bool) {
	client := m.hardware
	fail := func(err error) bool {
		if ctx.Err() != nil {
			return true
		}
		if _, known := m.hardwareFailed.LoadOrStore(id, true); !known {
			log.Printf("GIF %s is decoded in software: %v", id, err)
		}
		m.changed()
		return false
	}
	if client == nil {
		return false
	}
	file, err := mp4.Demux(data)
	if err != nil {
		return fail(err)
	}
	for {
		if err := waitAnimation(ctx, e); err != nil {
			return true
		}
		from, resume := e.resumeAt()
		if !resume {
			from = -1
		}
		v, err := client.Open(file)
		if err != nil {
			return fail(err)
		}
		err = m.playVideoFrames(ctx, e, v, from, publish)
		v.Close(context.Background())
		if err != nil {
			return fail(err)
		}
		if ctx.Err() != nil {
			return true
		}
	}
}
