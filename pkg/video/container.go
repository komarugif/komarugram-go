// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"errors"
	"fmt"
	"os"
	"time"

	"komarugram/pkg/mp4"
	"komarugram/pkg/webm"
)

// Container is what a video's file says of it by itself, read here, of any codec,
// without FFmpeg.
type Container struct {
	// Width and Height are the size it shows at.
	Width, Height int
	// Turn is how many degrees clockwise it is turned to show.
	Turn int
	// Duration is how long it lasts.
	Duration time.Duration
	// Sound tells whether a sound track goes with it.
	Sound bool
	// Streaming tells whether it can be played as it comes.
	Streaming bool
}

// ReadContainer reads the file at path, an MP4, MOV, M4V, 3GP, WebM or MKV, by what
// it starts with, not by its name. It does not read the file whole.
func ReadContainer(path string) (Container, error) {
	f, err := os.Open(path)
	if err != nil {
		return Container{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Container{}, err
	}
	m, err := mp4.ReadMeta(f, info.Size())
	if err == nil {
		return Container{Width: m.Width, Height: m.Height, Turn: m.Turn, Duration: m.Duration, Sound: m.Sound, Streaming: m.Streaming}, nil
	}
	if !errors.Is(err, mp4.ErrNotMP4) {
		return Container{}, err
	}
	// All of its blocks: a WebM may say it lasts far less than they do.
	w, err := webm.ReadIndex(f, info.Size())
	if err != nil {
		if errors.Is(err, webm.ErrNotWebM) {
			return Container{}, fmt.Errorf("%s: no container read here", path)
		}
		return Container{}, err
	}
	return Container{Width: w.Width, Height: w.Height, Duration: w.Duration, Sound: w.Sound}, nil
}
