// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/styledtext"
)

func TestSpoilerUncoversOnClick(t *testing.T) {
	var s spoiler
	drawn := false
	h := &focusHarness{draw: func(gtx layout.Context) {
		drawn = false
		s.Layout(gtx, true, false, func(gtx layout.Context) layout.Dimensions {
			drawn = true
			return layout.Dimensions{Size: image.Pt(100, 20)}
		})
	}}
	h.frame()
	if s.shown {
		t.Fatal("uncovered without a click")
	}
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(10, 10), Buttons: pointer.ButtonPrimary})
		h.frame()
	}
	if !s.shown || !drawn {
		t.Fatal("a click did not uncover the value")
	}
	s.Hide()
	h.frame()
	if s.shown {
		t.Fatal("Hide left the value uncovered")
	}
}

// TestSpoilerWaveKeepsItsSpeed checks that the wave crosses a short text as
// fast as a long one, rather than taking the same time over both.
func TestSpoilerWaveKeepsItsSpeed(t *testing.T) {
	now := time.Unix(1000, 0)
	reveal := spoilerReveal{started: now, pxPerDp: 2}
	short, long := image.Pt(240, 40), image.Pt(600, 40)
	if reveal.duration(short) >= reveal.duration(long) {
		t.Fatal("a short text takes as long as a long one")
	}
	// Before either ends, the wave is as far from the click in both.
	midway := now.Add(reveal.duration(short) / 2)
	if a, b := reveal.radius(midway, short), reveal.radius(midway, long); math.Abs(float64(a-b)) > 1 {
		t.Fatalf("radius %v in the short text, %v in the long one", a, b)
	}
	if d := reveal.duration(image.Pt(4, 4)); d != spoilerMinDuration {
		t.Errorf("a tiny spoiler takes %v", d)
	}
	if d := reveal.duration(image.Pt(8000, 8000)); d != spoilerMaxDuration {
		t.Errorf("a huge spoiler takes %v", d)
	}
}

// TestSmallSpoilerIsNotSlowed checks that the lower bound of the duration
// does not slow the wave down over a single word, which is how a word came
// to open slower than a paragraph.
func TestSmallSpoilerIsNotSlowed(t *testing.T) {
	word := image.Pt(52, 23) // "секрет" at 1 px per dp.
	reveal := spoilerReveal{started: time.Unix(1000, 0), center: f32.Pt(26, 11), pxPerDp: 1}
	speed := reveal.reach(word) / reveal.duration(word).Seconds()
	if speed < spoilerSpeed*0.75 {
		t.Fatalf("the wave crosses a word at %.0f dp/s, want about %d", speed, spoilerSpeed)
	}
}

// A spoiler over runs of several styles, a custom emoji among them, is one
// band in a line: the parts are square where it goes on beside them, and
// round at its ends and where a line breaks it.
func TestSpoilerPartsJoin(t *testing.T) {
	runs := []model.TextRun{{Text: "Ответ: "}, {Text: "это был ", Spoiler: true}, {Text: "🔥", Emoji: 7, Spoiler: true}, {Text: " дворецкий", Spoiler: true}}
	edges := runEdges(runs, nil)
	if want := []int{0, 7, 15, 16, 26}; !slices.Equal(edges, want) {
		t.Fatalf("edges %v, want %v", edges, want)
	}
	frag := func(x, start, end int) styledtext.Fragment {
		return styledtext.Fragment{Bounds: image.Rect(x, 0, x+10, 20), Clusters: []styledtext.Cluster{{Start: start, End: end}}}
	}
	for _, c := range []struct {
		name string
		run  int
		f    styledtext.Fragment
		want spoilerJoin
	}{
		{"after plain text", 1, frag(50, 7, 15), spoilerJoin{right: true}},
		{"the emoji between", 2, frag(100, 15, 16), spoilerJoin{left: true, right: true}},
		{"the last", 3, frag(120, 16, 26), spoilerJoin{left: true}},
		{"cut by the line's end", 1, frag(50, 7, 12), spoilerJoin{}},
		{"at a line's start", 3, frag(0, 16, 20), spoilerJoin{}},
		{"the rest on the next line", 3, frag(0, 20, 26), spoilerJoin{}},
		{"with no clusters", 2, styledtext.Fragment{Bounds: image.Rect(100, 0, 110, 20)}, spoilerJoin{}},
	} {
		if got := fragmentJoin(runs, edges, c.run, c.f); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
