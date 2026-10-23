// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/audio"
	"komarugram/pkg/mp4"
)

// roundDecoders skips a test of a video message without the modules it
// plays through, which are not in this repository: KOMARUGRAM_AVCDEC and
// KOMARUGRAM_AACDEC name them.
func roundDecoders(t *testing.T) {
	t.Helper()
	if os.Getenv("KOMARUGRAM_AVCDEC") == "" || os.Getenv("KOMARUGRAM_AACDEC") == "" {
		t.Skip("set KOMARUGRAM_AVCDEC to an avcdec.wasm and KOMARUGRAM_AACDEC to an aacdec.wasm")
	}
}

// shownNear waits for the picture of m that the view draws, side pixels
// across, to be the one of the time want, give or take a picture or two.
func shownNear(t *testing.T, p *chatPage, m model.Message, side int, want time.Duration) {
	t.Helper()
	p.audio.mu.Lock()
	video := p.audio.video
	p.audio.mu.Unlock()
	waitAudio(t, "the picture is not the sound's", func() bool {
		if p.audio.roundPicture(m, time.Now(), side) == nil {
			return false
		}
		at := time.Duration(video.shownAt.Load())
		return at > want-100*time.Millisecond && at < want+100*time.Millisecond
	})
}

// A video message plays in the client, with its sound: its pictures are
// those of where the sound is, a move of it included, scaled to cover the
// circle drawn; stopped, it lets its decoder go.
func TestRoundPlays(t *testing.T) {
	roundDecoders(t)
	// The demo reads the video from assets.
	t.Chdir("../../..")
	p, played, find := audioHarnessPage(t)
	m := find("demo/round")
	if !internalAudio(m) || !listenable(m) || !speedChanges(m) {
		t.Fatal("a video message does not play as a voice message does")
	}
	p.audio.toggle(p, m, 0.5)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	s := p.audio.state(m)
	if s.progress < 0.49 || s.progress > 0.51 {
		t.Fatalf("after a click in the middle: %+v", s)
	}
	shownNear(t, p, m, 240, s.elapsed)
	// assets/video.mp4 is 720×1280: it covers 240×240 at 240×427.
	if b := p.audio.roundPicture(m, time.Now(), 240).Bounds(); b.Dx() != 240 || b.Dy() != 427 {
		t.Fatalf("a picture of %v for a circle 240 across", b)
	}
	// Back, and the picture follows.
	p.audio.toggle(p, m, 0.1)
	shownNear(t, p, m, 240, p.audio.state(m).elapsed)
	p.audio.mu.Lock()
	video := p.audio.video
	p.audio.mu.Unlock()
	p.audio.stop()
	if p.audio.roundPicture(m, time.Now(), 240) != nil {
		t.Fatal("a picture of a video message that stopped")
	}
	if !played(0).isClosed() {
		t.Fatal("its sound was not let go")
	}
	waitAudio(t, "its decoder was not let go", func() bool {
		video.mu.Lock()
		defer video.mu.Unlock()
		return video.pix == nil
	})
}

// A video message the view does not draw, scrolled away, plays its sound
// only: no picture is decoded until it is drawn again.
func TestRoundUndrawnDecodesNothing(t *testing.T) {
	roundDecoders(t)
	t.Chdir("../../..")
	p, _, find := audioHarnessPage(t)
	m := find("demo/round")
	p.audio.toggle(p, m, 0.2)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	shownNear(t, p, m, 240, p.audio.state(m).elapsed)
	p.audio.mu.Lock()
	video := p.audio.video
	p.audio.mu.Unlock()
	before := video.shownAt.Load()
	time.Sleep(roundIdle + 100*time.Millisecond)
	// A little ahead, decoded on from where it is, at once when drawn.
	p.audio.toggle(p, m, 0.22)
	time.Sleep(500 * time.Millisecond)
	if video.shownAt.Load() != before {
		t.Fatal("pictures were decoded with nothing drawing them")
	}
	shownNear(t, p, m, 240, p.audio.state(m).elapsed)
}

// soundless is assets/video.mp4 without its sound track: the track's box
// is turned into a free one.
func soundless(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Clone(data)
	var boxes func(from, to int, fn func(kind string, at, end int))
	boxes = func(from, to int, fn func(kind string, at, end int)) {
		for at := from; at+8 <= to; {
			size := int(binary.BigEndian.Uint32(data[at:]))
			if size < 8 || at+size > to {
				t.Fatalf("a box of %d bytes at %d", size, at)
			}
			fn(string(data[at+4:at+8]), at, at+size)
			at += size
		}
	}
	freed := false
	boxes(0, len(data), func(kind string, at, end int) {
		if kind != "moov" {
			return
		}
		boxes(at+8, end, func(kind string, at, end int) {
			if kind == "trak" && bytes.Contains(data[at:end], []byte("soun")) {
				copy(data[at+4:], "free")
				freed = true
			}
		})
	})
	if !freed {
		t.Fatal("no sound track in assets/video.mp4")
	}
	if _, err := mp4.DemuxAudio(data); !errors.Is(err, mp4.ErrNoAudio) {
		t.Fatalf("the sound is still there: %v", err)
	}
	return data
}

// soundlessStore gives the demo's video message without its sound.
type soundlessStore struct {
	model.ConversationStore
	data []byte
}

func (s *soundlessStore) Media(ctx context.Context, m model.Message) ([]byte, error) {
	if m.Media.ID == "demo/round" {
		return s.data, nil
	}
	return s.ConversationStore.Media(ctx, m)
}

// A video message without sound plays as long as its video, in silence.
func TestRoundWithoutSoundPlays(t *testing.T) {
	roundDecoders(t)
	t.Chdir("../../..")
	p, _, find := audioHarnessPage(t)
	m := find("demo/round")
	p.source = &soundlessStore{ConversationStore: p.source, data: soundless(t)}
	p.audio.toggle(p, m, 0.5)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if s := p.audio.state(m); s.elapsed < 17900*time.Millisecond || s.elapsed > 18100*time.Millisecond {
		t.Fatalf("in the middle of the 36 s of video: %+v", s)
	}
	shownNear(t, p, m, 240, p.audio.state(m).elapsed)
}

// Silence is as long as asked, and moves.
func TestSilence(t *testing.T) {
	s := silenceOf(time.Second)
	if s.samples != audio.Rate {
		t.Fatalf("%d samples in a second", s.samples)
	}
	pcm := make([]audio.Frame, audio.Rate/3)
	pcm[0] = audio.Frame{1, 1}
	read := 0
	for {
		n, err := s.Read(pcm)
		read += n
		if pcm[0] != (audio.Frame{}) {
			t.Fatal("silence is not silent")
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if read != audio.Rate || s.Position() != audio.Rate {
		t.Fatalf("%d samples read, at %d", read, s.Position())
	}
	s.SeekSample(audio.Rate / 2)
	if n, _ := s.Read(pcm); n != audio.Rate/3 || s.Position() != audio.Rate/2+audio.Rate/3 {
		t.Fatalf("after a move to the middle, %d read, at %d", n, s.Position())
	}
}
