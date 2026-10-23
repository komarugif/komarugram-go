// SPDX-License-Identifier: Unlicense OR MIT

package mp4

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"testing"
)

// A video read where it lies is the one read whole: its size, its times,
// its keyframes and the bytes of each sample; and it reads no more of the
// file than its movie box to be opened.
func TestDemuxAtIsDemux(t *testing.T) {
	data, err := os.ReadFile("../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	whole, err := Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	c := &counting{data: data}
	at, err := DemuxAt(c, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if c.read > len(data)/4 {
		t.Fatalf("read %d bytes of %d to open it", c.read, len(data))
	}
	if at.Width != whole.Width || at.Height != whole.Height || !bytes.Equal(at.Config, whole.Config) || at.Duration != whole.Duration {
		t.Fatalf("%dx%d %v, not %dx%d %v", at.Width, at.Height, at.Duration, whole.Width, whole.Height, whole.Duration)
	}
	if !slices.Equal(at.Times, whole.Times) || at.Len() != whole.Len() || at.Samples != nil {
		t.Fatalf("%d samples, %d times; %d and %d read whole", at.Len(), len(at.Times), whole.Len(), len(whole.Times))
	}
	var buf []byte
	for i := range whole.Len() {
		if at.Keyframe(i) != whole.Keyframe(i) {
			t.Fatalf("sample %d: keyframe %v, not %v", i, at.Keyframe(i), whole.Keyframe(i))
		}
		buf, err = at.Sample(i, buf)
		if err != nil {
			t.Fatal(err)
		}
		if want, _ := whole.Sample(i, nil); !bytes.Equal(buf, want) {
			t.Fatalf("sample %d differs", i)
		}
	}
}

// A file without H.264 video, or not MP4 at all, is told so.
func TestDemuxAtRejects(t *testing.T) {
	data, err := os.ReadFile("testdata/tone.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DemuxAt(bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("sound alone: %v", err)
	}
	junk := []byte("not a video at all, not even close")
	if _, err := DemuxAt(bytes.NewReader(junk), int64(len(junk))); err == nil {
		t.Fatal("junk opened")
	}
}

// A sound track that is not AAC is told apart from none: a video with it
// is not to play silent.
func TestDemuxAudioOtherCodec(t *testing.T) {
	data, err := os.ReadFile("testdata/tone.m4a")
	if err != nil {
		t.Fatal(err)
	}
	other := bytes.Replace(data, []byte("mp4a"), []byte("Opus"), 1)
	if _, err := DemuxAudio(other); !errors.Is(err, ErrUnsupportedAudio) {
		t.Fatalf("an Opus track: %v", err)
	}
	video, err := os.ReadFile("../../assets/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DemuxAudio(video); err != nil {
		t.Fatalf("the video's AAC: %v", err)
	}
}
