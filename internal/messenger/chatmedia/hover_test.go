package chatmedia

import (
	"image"
	"testing"
	"time"
)

func TestHoverFinishesOnlyTheCurrentLoop(t *testing.T) {
	var m Manager
	e := &entry{lottieLoop: true, loopFrames: 4, frameNumber: 5, still: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	a := HoverPlayback{Target: "first copy", Hovered: true}
	b := HoverPlayback{Target: "second copy"}
	if play, _ := m.hoverFrame(e, a, false); !play {
		t.Fatal("the hovered copy did not start")
	}
	if play, _ := m.hoverFrame(e, b, false); play {
		t.Fatal("the other copy followed the hover")
	}
	last := image.NewRGBA(image.Rect(0, 0, 1, 1))
	last.Pix[0], last.Pix[3] = 80, 255
	if !prepareLoopEnd(e, func(at time.Duration) (image.Image, error) {
		if at != 35*time.Millisecond {
			t.Errorf("decoded at %v, want the middle of the last frame", at)
		}
		return last, nil
	}, 10*time.Millisecond, 6) {
		t.Fatal("did not retain the last frame")
	}
	last.Pix[0] = 0
	a.Hovered = false
	e.frameNumber = 6
	if play, _ := m.hoverFrame(e, a, false); !play {
		t.Fatal("leaving cut the loop short")
	}
	// A delayed frame may skip the end of one cycle and arrive in the next.
	b.Hovered = true
	e.frameNumber = 8
	if play, _ := m.hoverFrame(e, b, false); !play {
		t.Fatal("the second copy did not play independently")
	}
	if play, im := m.hoverFrame(e, a, false); play || im == nil || im.(*image.RGBA).Pix[0] != 80 {
		t.Fatalf("the first copy did not stop on the last picture: play %v, image %v", play, im)
	}
	e.frameNumber = 0
	if play, im := m.hoverFrame(e, a, false); play || im != e.loopEnd {
		t.Fatal("another playback restarted the finished copy")
	}
	a.Hovered = true
	if play, _ := m.hoverFrame(e, a, false); !play {
		t.Fatal("hovering again did not resume playback")
	}
}

func TestVideoLoopFinishingIsOptIn(t *testing.T) {
	var m Manager
	e := &entry{loopFrames: 4, still: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	for _, gift := range []bool{false, true} {
		hover := HoverPlayback{Target: "card", Hovered: true, VideoLoop: gift}
		m.hoverFrame(e, hover, false)
		hover.Hovered = false
		if play, _ := m.hoverFrame(e, hover, false); play != gift {
			t.Errorf("video gift %v: playback after hover = %v", gift, play)
		}
	}
}
