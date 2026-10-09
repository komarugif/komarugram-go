// SPDX-License-Identifier: Unlicense OR MIT

package rectanim

import (
	"math"
	"testing"

	"gioui.org/f32"
)

// scene is a square in a group: the group tweens x from 0 to 10 over a
// second, linearly, then turns 90° about the square's corner (10, 0) in
// another second; a second track shows the square from 0.5 s.
const scene = `{
 "version": 2, "name": "test", "viewBox": [0, 0, 10, 10], "stage": 1, "duration": 2,
 "nodes": [
  {"id": 0, "parent": -1, "kind": "group", "clip": {"x": 0, "y": 0, "w": 100, "h": 100}},
  {"id": 1, "parent": 0, "kind": "rect", "x": 0, "y": 0, "w": 10, "h": 10, "fill": "#DD775B", "hidden": true}
 ],
 "tracks": [
  {"delay": 0, "duration": 2, "repeat": true, "loopFrom": 1, "events": [
   {"node": 0, "prop": "x", "start": 0, "duration": 1, "ease": "linear", "from": 0, "to": 10},
   {"node": 0, "prop": "origin", "start": 1, "duration": 0, "ease": "none", "to": [10, 0]},
   {"node": 0, "prop": "rotation", "start": 1, "duration": 1, "ease": "power2.out", "from": 0, "to": 90}
  ]},
  {"delay": 0.5, "duration": 0, "repeat": false, "events": [
   {"node": 1, "prop": "display", "start": 0, "duration": 0, "ease": "none", "to": true}
  ]}
 ]
}`

func near(a, b f32.Point) bool {
	return math.Abs(float64(a.X-b.X)) < 1e-3 && math.Abs(float64(a.Y-b.Y)) < 1e-3
}

func TestFrame(t *testing.T) {
	s, err := Parse([]byte(scene))
	if err != nil {
		t.Fatal(err)
	}
	if shapes := s.Frame(0.25); len(shapes) != 0 {
		t.Fatalf("hidden square drawn: %v", shapes)
	}
	corner := func(at float64) f32.Point {
		shapes := s.Frame(at)
		if len(shapes) != 1 {
			t.Fatalf("at %v: %d shapes", at, len(shapes))
		}
		if len(shapes[0].Clips) != 1 {
			t.Fatalf("at %v: clips %v", at, shapes[0].Clips)
		}
		return shapes[0].Transform.Transform(f32.Pt(0, 0))
	}
	// Halfway through the linear tween.
	if p := corner(0.5); !near(p, f32.Pt(5, 0)) {
		t.Errorf("at 0.5: corner at %v", p)
	}
	// Turned 90° about (10, 0), moved by 10: (0, 0) goes to (20, -10).
	if p := corner(2 - 1e-9); !near(p, f32.Pt(20, -10)) {
		t.Errorf("at the end: corner at %v", p)
	}
	// The track loops from 1 s: at 2.5 s it is at 1.5 s again.
	if a, b := corner(1.5), corner(2.5); !near(a, b) {
		t.Errorf("loop: %v at 1.5 s, %v at 2.5 s", a, b)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, bad := range []string{
		`{"version": 1, "viewBox": [0, 0, 1, 1]}`,
		`{"version": 2, "viewBox": [0, 0, 0, 0]}`,
		`{"version": 2, "viewBox": [0, 0, 1, 1], "nodes": [{"id": 0, "parent": 3, "kind": "group"}]}`,
		`{"version": 2, "viewBox": [0, 0, 1, 1], "nodes": [{"id": 0, "parent": -1, "kind": "rect", "fill": "url(#x)"}]}`,
		`{"version": 2, "viewBox": [0, 0, 1, 1], "nodes": [{"id": 0, "parent": -1, "kind": "group"}], "tracks": [{"events": [{"node": 0, "prop": "x", "ease": "bounce", "to": 1}]}]}`,
		`{"version": 2, "viewBox": [0, 0, 1, 1], "tracks": [{"events": [{"node": 5, "prop": "x", "ease": "none", "to": 1}]}]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestEases(t *testing.T) {
	for name, ease := range eases {
		if a, b := ease(0), ease(1); math.Abs(a) > 1e-9 || math.Abs(b-1) > 1e-9 {
			t.Errorf("%s: %v at 0, %v at 1", name, a, b)
		}
	}
	if v := eases["power2.out"](0.5); math.Abs(v-0.875) > 1e-9 {
		t.Errorf("power2.out(0.5) = %v, want 0.875", v)
	}
}

// Rectangles placed by their own transform: mirrored by a matrix, turned
// about a centre, as the pixel art's frames are.
func TestMatrix(t *testing.T) {
	s, err := Parse([]byte(`{"version": 2, "viewBox": [0, 0, 20, 20], "duration": 1,
	 "nodes": [
	  {"id": 0, "parent": -1, "kind": "rect", "x": 2, "y": 0, "w": 3, "h": 1, "fill": "black", "matrix": [-1, 0, 0, 1, 10, 0]},
	  {"id": 1, "parent": -1, "kind": "rect", "x": 0, "y": 0, "w": 1, "h": 1, "fill": "black", "matrix": [0, 1, -1, 0, 20, 0]}
	 ]}`))
	if err != nil {
		t.Fatal(err)
	}
	shapes := s.Frame(0)
	// Mirrored about x = 5: (2, 0) goes to (8, 0).
	if p := shapes[0].Transform.Transform(f32.Pt(2, 0)); !near(p, f32.Pt(8, 0)) {
		t.Errorf("mirrored corner at %v", p)
	}
	// rotate(90 10 10) as a matrix: (0, 0) goes to (20, 0).
	if p := shapes[1].Transform.Transform(f32.Pt(0, 0)); !near(p, f32.Pt(20, 0)) {
		t.Errorf("turned corner at %v", p)
	}
	if p := shapes[1].Transform.Transform(f32.Pt(1, 0)); !near(p, f32.Pt(20, 1)) {
		t.Errorf("turned edge at %v", p)
	}
}
