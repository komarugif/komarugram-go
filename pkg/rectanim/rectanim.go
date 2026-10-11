// SPDX-License-Identifier: Unlicense OR MIT

// Package rectanim plays animations of pictures made of rectangles: a
// scene is a tree of groups and filled rectangles, and tracks of events
// that set or tween their properties, as GSAP timelines do on SVG. The
// scenes hold the start value of every tween, so playing one is
// interpolating, with no state: Frame gives the picture at any time.
//
// It knows nothing of what it draws; the scenes come from elsewhere.
package rectanim

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"

	"gioui.org/f32"
)

// Version is the version of the scenes this package plays.
const Version = 2

// Scene is an animation: its picture's box, how many times as wide as it
// the stage it moves on is, the nodes of the picture and its tracks.
type Scene struct {
	Version int        `json:"version"`
	Name    string     `json:"name"`
	ViewBox [4]float32 `json:"viewBox"`
	Stage   float32    `json:"stage"`
	// Rest is the moment, in seconds, whose frame stands for the scene
	// where it does not move.
	Rest     float64 `json:"rest"`
	Nodes    []Node  `json:"nodes"`
	Tracks   []Track `json:"tracks"`
	Duration float64 `json:"duration"`

	// children are the nodes' children, roots under -1, in their order.
	children map[int][]int
	// events are each node's events for each property, by start.
	events map[key][]*Event
}

// Node is a group or a rectangle, placed by its transform. A group may
// cut its children to its clip.
type Node struct {
	ID      int      `json:"id"`
	Parent  int      `json:"parent"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name,omitempty"`
	Hidden  bool     `json:"hidden,omitempty"`
	X       float32  `json:"x,omitempty"`
	Y       float32  `json:"y,omitempty"`
	W       float32  `json:"w,omitempty"`
	H       float32  `json:"h,omitempty"`
	Fill    string   `json:"fill,omitempty"`
	Opacity *float32 `json:"opacity,omitempty"`
	Matrix  *Matrix  `json:"matrix,omitempty"`
	Clip    *Box     `json:"clip,omitempty"`

	color color.NRGBA
}

// Matrix is an SVG transform, [a b c d e f]: x' = a x + c y + e,
// y' = b x + d y + f.
type Matrix [6]float32

// affine is m as Gio's transform.
func (m Matrix) affine() f32.Affine2D {
	return f32.NewAffine2D(m[0], m[2], m[4], m[1], m[3], m[5])
}

// Box is a rectangle in a node's coordinates.
type Box struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	W float32 `json:"w"`
	H float32 `json:"h"`
}

// Track is a timeline: it starts after Delay, lasts Duration and, when it
// repeats, starts again from LoopFrom, its start by default.
type Track struct {
	Delay    float64  `json:"delay"`
	Duration float64  `json:"duration"`
	Repeat   bool     `json:"repeat"`
	LoopFrom *float64 `json:"loopFrom,omitempty"`
	Events   []Event  `json:"events"`
}

// Event sets a node's property at Start, or tweens it from From to To
// over Duration with Ease. The properties are GSAP's x, y, rotation,
// scaleX and scaleY, origin (svgOrigin), display, the transform attribute
// and a rectangle's attr.x, attr.y, attr.width and attr.height.
type Event struct {
	Node     int             `json:"node"`
	Prop     string          `json:"prop"`
	Start    float64         `json:"start"`
	Duration float64         `json:"duration"`
	Ease     string          `json:"ease"`
	From     float64         `json:"from"`
	To       json.RawMessage `json:"to"`

	track    *Track
	to       float64
	toOrigin f32.Point
	toShown  bool
	matrix   Matrix
	ease     func(float64) float64
}

type key struct {
	node int
	prop string
}

// Parse reads a scene and checks what it refers to.
func Parse(data []byte) (*Scene, error) {
	var s Scene
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Version != Version {
		return nil, fmt.Errorf("rectanim: scene of version %d, not %d", s.Version, Version)
	}
	if s.ViewBox[2] <= 0 || s.ViewBox[3] <= 0 {
		return nil, errors.New("rectanim: empty view box")
	}
	if s.Stage < 1 {
		s.Stage = 1
	}
	s.children = map[int][]int{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		if n.ID != i {
			return nil, fmt.Errorf("rectanim: node %d is at %d", n.ID, i)
		}
		if n.Parent < -1 || n.Parent >= i {
			return nil, fmt.Errorf("rectanim: node %d has parent %d", i, n.Parent)
		}
		if n.Kind == "rect" {
			c, ok := parseColor(n.Fill)
			if !ok {
				return nil, fmt.Errorf("rectanim: node %d is filled with %q", i, n.Fill)
			}
			n.color = c
		}
		s.children[n.Parent] = append(s.children[n.Parent], i)
	}
	s.events = map[key][]*Event{}
	for ti := range s.Tracks {
		t := &s.Tracks[ti]
		for ei := range t.Events {
			e := &t.Events[ei]
			if e.Node < 0 || e.Node >= len(s.Nodes) {
				return nil, fmt.Errorf("rectanim: event of node %d", e.Node)
			}
			if err := e.decode(); err != nil {
				return nil, err
			}
			e.track = t
			k := key{e.Node, e.Prop}
			s.events[k] = append(s.events[k], e)
		}
	}
	for _, list := range s.events {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Start < list[j].Start })
	}
	return &s, nil
}

func (e *Event) decode() error {
	var err error
	switch {
	case e.Prop == "origin":
		var p [2]float32
		err = json.Unmarshal(e.To, &p)
		e.toOrigin = f32.Pt(p[0], p[1])
	case e.Prop == "display":
		err = json.Unmarshal(e.To, &e.toShown)
	case e.Prop == "transform":
		err = json.Unmarshal(e.To, &e.matrix)
	default:
		err = json.Unmarshal(e.To, &e.to)
	}
	if err != nil {
		return fmt.Errorf("rectanim: %s of node %d: %w", e.Prop, e.Node, err)
	}
	ease, ok := eases[e.Ease]
	if !ok {
		return fmt.Errorf("rectanim: unknown ease %q", e.Ease)
	}
	e.ease = ease
	return nil
}

// Shape is a rectangle of a frame: Rect in its node's coordinates, placed
// in the view box by Transform, cut to Clips.
type Shape struct {
	Rect      Box
	Transform f32.Affine2D
	Color     color.NRGBA
	Clips     []Clip
}

// Clip is a group's clip, Box placed by Transform.
type Clip struct {
	Box       Box
	Transform f32.Affine2D
}

// Frame is the picture at time t, in seconds from the start, the shapes
// in the order they are drawn.
func (s *Scene) Frame(t float64) []Shape {
	times := make([]float64, len(s.Tracks))
	for i := range s.Tracks {
		times[i] = s.Tracks[i].local(t)
	}
	var shapes []Shape
	var walk func(parent int, m f32.Affine2D, clips []Clip)
	walk = func(parent int, m f32.Affine2D, clips []Clip) {
		for _, id := range s.children[parent] {
			n := &s.Nodes[id]
			if !s.shown(n, times) {
				continue
			}
			local := s.transform(n, times)
			nm := m.Mul(local)
			if n.Kind == "rect" {
				if n.Opacity != nil && *n.Opacity == 0 {
					continue
				}
				r := Box{X: s.number(n, "attr.x", float64(n.X), times), Y: s.number(n, "attr.y", float64(n.Y), times),
					W: s.number(n, "attr.width", float64(n.W), times), H: s.number(n, "attr.height", float64(n.H), times)}
				c := n.color
				if n.Opacity != nil {
					c.A = uint8(float32(c.A) * *n.Opacity)
				}
				shapes = append(shapes, Shape{Rect: r, Transform: nm, Color: c, Clips: clips})
				continue
			}
			cl := clips
			if n.Clip != nil {
				cl = append(append([]Clip(nil), clips...), Clip{Box: *n.Clip, Transform: nm})
			}
			walk(id, nm, cl)
		}
	}
	walk(-1, f32.Affine2D{}, nil)
	return shapes
}

// local is t in the track's own time: negative before it starts, then
// within its length, looping where it repeats.
func (t *Track) local(at float64) float64 {
	at -= t.Delay
	if at < 0 || !t.Repeat || t.Duration <= 0 {
		return at
	}
	from := 0.0
	if t.LoopFrom != nil {
		from = *t.LoopFrom
	}
	if at < from || t.Duration <= from {
		return at
	}
	return from + math.Mod(at-from, t.Duration-from)
}

// current is the last event of node's prop that has started, or nil.
func (s *Scene) current(node int, prop string, times []float64) *Event {
	var last *Event
	for _, e := range s.events[key{node, prop}] {
		if at := times[s.trackIndex(e.track)]; at >= e.Start {
			if last == nil || e.Start >= last.Start {
				last = e
			}
		}
	}
	return last
}

func (s *Scene) trackIndex(t *Track) int {
	for i := range s.Tracks {
		if &s.Tracks[i] == t {
			return i
		}
	}
	return 0
}

// number is the value of a numeric property at the tracks' times.
func (s *Scene) number(n *Node, prop string, base float64, times []float64) float32 {
	e := s.current(n.ID, prop, times)
	if e == nil {
		return float32(base)
	}
	at := times[s.trackIndex(e.track)]
	if e.Duration <= 0 || at >= e.Start+e.Duration {
		return float32(e.to)
	}
	p := e.ease((at - e.Start) / e.Duration)
	return float32(e.From + (e.to-e.From)*p)
}

func (s *Scene) shown(n *Node, times []float64) bool {
	if e := s.current(n.ID, "display", times); e != nil {
		return e.toShown
	}
	return !n.Hidden
}

// transform is n's own transform: GSAP's rotation and scale about its
// origin, then its translation, then the transform attribute.
func (s *Scene) transform(n *Node, times []float64) f32.Affine2D {
	attr := f32.Affine2D{}
	if n.Matrix != nil {
		attr = n.Matrix.affine()
	}
	if e := s.current(n.ID, "transform", times); e != nil {
		attr = e.matrix.affine()
	}
	origin := f32.Point{}
	if e := s.current(n.ID, "origin", times); e != nil {
		origin = e.toOrigin
	}
	x, y := s.number(n, "x", 0, times), s.number(n, "y", 0, times)
	rot := s.number(n, "rotation", 0, times)
	sx, sy := s.number(n, "scaleX", 1, times), s.number(n, "scaleY", 1, times)
	// Applied to a point in this order: about the origin, scale, rotate;
	// then translate; then the attribute.
	gsap := f32.Affine2D{}.
		Offset(origin.Mul(-1)).
		Scale(f32.Point{}, f32.Pt(sx, sy)).
		Rotate(f32.Point{}, rot*math.Pi/180).
		Offset(origin).
		Offset(f32.Pt(x, y))
	return attr.Mul(gsap)
}

// eases are GSAP's, by their names.
var eases = map[string]func(float64) float64{
	"none":         func(p float64) float64 { return p },
	"linear":       func(p float64) float64 { return p },
	"power1.in":    powerIn(2),
	"power1.out":   powerOut(2),
	"power1.inOut": powerInOut(2),
	"power2.in":    powerIn(3),
	"power2.out":   powerOut(3),
	"power2.inOut": powerInOut(3),
	"power3.in":    powerIn(4),
	"power3.out":   powerOut(4),
	"power3.inOut": powerInOut(4),
	"sine.in":      func(p float64) float64 { return 1 - math.Cos(p*math.Pi/2) },
	"sine.out":     func(p float64) float64 { return math.Sin(p * math.Pi / 2) },
	"sine.inOut":   func(p float64) float64 { return -(math.Cos(math.Pi*p) - 1) / 2 },
}

func powerIn(n float64) func(float64) float64 {
	return func(p float64) float64 { return math.Pow(p, n) }
}

func powerOut(n float64) func(float64) float64 {
	return func(p float64) float64 { return 1 - math.Pow(1-p, n) }
}

func powerInOut(n float64) func(float64) float64 {
	return func(p float64) float64 {
		if p < 0.5 {
			return math.Pow(2*p, n) / 2
		}
		return 1 - math.Pow(2*(1-p), n)/2
	}
}

// parseColor reads #rgb, #rrggbb, black and white.
func parseColor(s string) (color.NRGBA, bool) {
	switch strings.ToLower(s) {
	case "black":
		return color.NRGBA{A: 255}, true
	case "white":
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}, true
	}
	hex, ok := strings.CutPrefix(s, "#")
	if !ok {
		return color.NRGBA{}, false
	}
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil || len(hex) != 6 {
		return color.NRGBA{}, false
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}
