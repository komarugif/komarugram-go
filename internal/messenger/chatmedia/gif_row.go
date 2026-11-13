// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/player"
	"komarugram/pkg/video"
)

// rowFPS is the rate a row of GIFs plays at, as Telegram Desktop plays
// animations.
const rowFPS = 30

// gifRow is a row of GIFs played by one FFmpeg as one picture
// (video.NewRowPlayer).
type gifRow struct {
	cancel context.CancelFunc
	// lastSeen is when a frame last showed the row; UI thread only.
	lastSeen time.Time
	// shown is the picture the window drew last, given back to the player
	// once another is drawn; UI thread only.
	shown *image.RGBA

	mu     sync.Mutex
	player *video.Player
	err    error
}

// rowKey names a row by its GIFs and the sizes of their tiles: another
// size is another row, played by another FFmpeg.
func rowKey(msgs []model.Message, widths []int, height int) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(height))
	for i, msg := range msgs {
		fmt.Fprintf(&b, "|%s:%d", msg.Media.ID, widths[i])
	}
	return b.String()
}

// rowIDs names a row by its GIFs alone, for one that failed to stay failed
// at any size.
func rowIDs(msgs []model.Message) string {
	ids := make([]string, len(msgs))
	for i, msg := range msgs {
		ids[i] = msg.Media.ID
	}
	return strings.Join(ids, "|")
}

// GIFRow is the picture of a row of GIFs, msgs in tiles widths wide and
// height high side by side, played by one FFmpeg: a list of saved GIFs
// plays many at once, and one process for each wasted the system's work,
// and ours, serving each file over HTTP for every loop. A tile's picture is
// at the sum of the widths before it, covering the tile. It runs on the UI
// goroutine.
//
// ok is false where a row is not played so, and the GIFs play one by one:
// GIFs in the sandbox, no FFmpeg, or a row that failed (a file FFmpeg
// cannot read, or past the memory a row may take), which a GIF of its own
// would have failed alone. im is nil while the row loads, the GIFs showing
// their stills meanwhile.
func (m *Manager) GIFRow(msgs []model.Message, widths []int, height int) (im *image.RGBA, ok bool) {
	if len(msgs) == 0 || len(msgs) != len(widths) || height <= 0 || m.SandboxGIFs() || m.paused || m.quiet {
		return nil, false
	}
	for i, msg := range msgs {
		if msg.Kind != model.MessageGIF || msg.Media == nil || widths[i] <= 0 {
			return nil, false
		}
	}
	if m.rowsFailed[rowIDs(msgs)] {
		return nil, false
	}
	key := rowKey(msgs, widths, height)
	r := m.rows[key]
	if r == nil {
		if m.rows == nil {
			m.rows = map[string]*gifRow{}
		}
		ctx, cancel := context.WithCancel(m.ctx)
		r = &gifRow{cancel: cancel}
		m.rows[key] = r
		m.wg.Add(1)
		go m.playRow(ctx, r, msgs, widths, height)
	}
	r.lastSeen = time.Now()
	r.mu.Lock()
	p, err := r.player, r.err
	r.mu.Unlock()
	if err == nil && p != nil {
		im, err = p.Frame()
	}
	if err != nil {
		log.Printf("GIFs play one by one: their row failed: %v", err)
		m.rowsFailed[rowIDs(msgs)] = true
		m.stopRow(key, r)
		return nil, false
	}
	if im != nil && im != r.shown {
		// The picture drawn before was uploaded by the frame that drew
		// it, and is drawn no more: its pixels serve a frame to come.
		if r.shown != nil {
			p.Release(r.shown)
		}
		r.shown = im
	}
	m.schedule(tickCeil(m.now.Add(time.Nanosecond), ticksPer(time.Second/rowFPS, false)))
	return im, true
}

// playRow reads the GIFs of a row, whole, and plays them with one FFmpeg.
func (m *Manager) playRow(ctx context.Context, r *gifRow, msgs []model.Message, widths []int, height int) {
	defer m.wg.Done()
	fail := func(err error) {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		m.changed()
	}
	defer crash.Recover("GIF row", func(p *crash.Panic) { fail(p) })
	inputs := make([]video.RowInput, len(msgs))
	var files []*os.File
	var streams []*player.Stream
	defer func() {
		<-ctx.Done()
		r.mu.Lock()
		p := r.player
		r.mu.Unlock()
		if p != nil {
			p.Stop()
		}
		for _, f := range files {
			f.Close()
		}
		for _, s := range streams {
			s.Close()
		}
	}()
	for i, msg := range msgs {
		// A saved GIF is small: it is read whole, not streamed.
		data, err := m.source.Media(ctx, msg)
		if err != nil {
			if ctx.Err() == nil {
				fail(err)
			}
			return
		}
		in := video.RowInput{Width: widths[i], Source: rowSource(msg)}
		if f, err := video.MemoryFile("gif", data); err == nil {
			in.File = f
			files = append(files, f)
		} else if errors.Is(err, errors.ErrUnsupported) {
			// No file in memory here: FFmpeg reads it from this process.
			s, err := player.Serve("animation", bytes.NewReader(data), int64(len(data)))
			if err != nil {
				fail(err)
				return
			}
			streams = append(streams, s)
			in.Path = s.URL()
		} else {
			fail(err)
			return
		}
		inputs[i] = in
	}
	p := video.NewRowPlayer(inputs, height, rowFPS, m.ffmpeg)
	r.mu.Lock()
	r.player = p
	r.mu.Unlock()
	p.Start()
	m.changed()
}

// rowSource is the size of a GIF's pictures as Telegram tells it, for the
// memory its FFmpeg may take; a file larger than told goes past the bound,
// fails its row, and plays alone, its own size probed.
func rowSource(msg model.Message) image.Point {
	if w, h := msg.Media.Width, msg.Media.Height; w > 0 && h > 0 {
		return image.Pt(w, h)
	}
	return image.Pt(1280, 720)
}

// stopRow stops the FFmpeg of row r, of key.
func (m *Manager) stopRow(key string, r *gifRow) {
	r.cancel()
	delete(m.rows, key)
}

// trimRows stops the rows no frame has shown for gifIdle, as a GIF's own
// FFmpeg stops.
func (m *Manager) trimRows() {
	for key, r := range m.rows {
		if time.Since(r.lastSeen) > gifIdle {
			m.stopRow(key, r)
		}
	}
}
