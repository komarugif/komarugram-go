// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"gio-mw/wdk"
)

// The particles of spoilers, as Telegram for Android draws them: a dust of
// points drifting over a text under a spoiler, and white points carried by
// a slow flow over media under one. What they look like is Android's
// (docs/PORT_ANDROID_EFFECTS.md); how they are made is not: the points are
// moved and drawn here on the CPU, into one picture for all the spoilers a
// window shows, which is drawn again and again under each of them.

// effectsKey keeps the particles of the frame's window in its
// layout.Context's Values.
const effectsKey = "komarugram/ui.effects"

// effects are the particles of one window: every spoiler it shows draws
// the same two pictures. A window that has none (withEffects) shows its
// spoilers without particles.
type effects struct {
	text  textDust
	media mediaDust
	// still stops the particles where they are, as in a window that is
	// not the active one while its animations are paused.
	still bool
}

// withEffects makes e the particles of the frame of gtx; still stops them
// where they are.
func withEffects(gtx layout.Context, e *effects, still bool) {
	if gtx.Values == nil {
		return
	}
	e.still = still
	e.sweep(gtx.Now)
	gtx.Values[effectsKey] = e
}

// effectsOf are the particles of the frame, nil in a window without them.
func effectsOf(gtx layout.Context) *effects {
	e, _ := gtx.Values[effectsKey].(*effects)
	return e
}

// withoutEffects is gtx for what is drawn off the frame, as a snapshot is
// by another GPU context while the window goes on: its spoilers have no
// particles, whose picture the window changes meanwhile.
func withoutEffects(gtx layout.Context) layout.Context {
	if effectsOf(gtx) == nil {
		return gtx
	}
	values := make(map[string]any, len(gtx.Values))
	for k, v := range gtx.Values {
		if k != effectsKey {
			values[k] = v
		}
	}
	gtx.Values = values
	return gtx
}

// dustIdle is how long the particles' pictures are kept after the last
// spoiler that drew them.
const dustIdle = 5 * time.Second

// sweep lets go of the pictures no spoiler drew for a while.
func (e *effects) sweep(now time.Time) {
	if d := &e.text; d.alpha != nil && now.Sub(d.used) > dustIdle {
		*d = textDust{}
	}
	if d := &e.media; d.img != nil && now.Sub(d.used) > dustIdle {
		*d = mediaDust{}
	}
}

// dustStep is how often the particles move: about 30 times a second, as
// Android's do.
const dustStep = 32 * time.Millisecond

// dustSteps is how many steps a frame that comes late makes up for; past
// that the particles go on from where they are.
const dustSteps = 4

const (
	// textDustTile is the side of the text's dust, a square that repeats.
	textDustTile = unit.Dp(150)
	// textDustGrid cuts it into cells, each with particles of its own
	// that never leave it: the dust is as dense all over.
	textDustGrid = 10
	// textDustPerCell is how many particles a cell keeps.
	textDustPerCell = 45
	// The particles drift at textDustSlow…textDustFast dp a second, each
	// its own way, and live textDustLifeMin…textDustLifeMax seconds.
	textDustSlow    = 3
	textDustFast    = 7
	textDustLifeMin = 1
	textDustLifeMax = 3
)

// dustGroups are the three kinds of points the text's dust is made of:
// how much each shows, and how large it is across, in dp.
var dustGroups = [3]struct{ alpha, size float32 }{{0.3, 1.4}, {0.6, 1.2}, {1, 1.2}}

type dustParticle struct {
	x, y, vx, vy float32
	// left is how long it lives yet, in seconds.
	left  float32
	group uint8
}

// textDust is the dust over text spoilers: a square of points that
// repeats without a seam, drawn in the color of each text that asks.
type textDust struct {
	side  int
	scale float32
	rng   *rand.Rand
	parts []dustParticle
	// alpha is the dust itself, how much of a point each pixel has.
	alpha []uint8
	// stepped is when the particles moved last, version counts their
	// moves, and used is when a spoiler drew them last.
	stepped time.Time
	version uint64
	used    time.Time
	tiles   map[color.NRGBA]*dustTile
}

// dustTile is the dust in one color, as drawn.
type dustTile struct {
	img     *image.RGBA
	op      paint.ImageOp
	version uint64
	used    time.Time
}

func (d *textDust) init(gtx layout.Context) {
	d.scale = gtx.Metric.PxPerDp
	d.side = max(textDustGrid, gtx.Dp(textDustTile)/textDustGrid*textDustGrid)
	// Seeded: the dust is the same in every run, as its renders are.
	d.rng = rand.New(rand.NewPCG(0x5eed, 0xd057))
	d.parts = make([]dustParticle, textDustGrid*textDustGrid*textDustPerCell)
	for i := range d.parts {
		d.spawn(i)
		// As if it had lived a part of its life already: they do not
		// all die at once.
		d.parts[i].left *= d.rng.Float32()
	}
	d.alpha = make([]uint8, d.side*d.side)
	d.tiles = map[color.NRGBA]*dustTile{}
	d.version = 1
	d.draw()
}

// spawn gives particle i a new life at a point of its cell.
func (d *textDust) spawn(i int) {
	cell := float32(d.side) / textDustGrid
	n := i / textDustPerCell
	p := &d.parts[i]
	p.x = (float32(n%textDustGrid) + d.rng.Float32()) * cell
	p.y = (float32(n/textDustGrid) + d.rng.Float32()) * cell
	angle := d.rng.Float64() * 2 * math.Pi
	speed := (textDustSlow + d.rng.Float32()*(textDustFast-textDustSlow)) * d.scale
	p.vx, p.vy = float32(math.Cos(angle))*speed, float32(math.Sin(angle))*speed
	p.left = textDustLifeMin + d.rng.Float32()*(textDustLifeMax-textDustLifeMin)
	p.group = uint8(d.rng.IntN(len(dustGroups)))
}

// step moves the particles by dt seconds: one that lived its life, or left
// its cell, begins again.
func (d *textDust) step(dt float32) {
	cell := float32(d.side) / textDustGrid
	for i := range d.parts {
		p := &d.parts[i]
		p.x += p.vx * dt
		p.y += p.vy * dt
		p.left -= dt
		n := i / textDustPerCell
		x0, y0 := float32(n%textDustGrid)*cell, float32(n/textDustGrid)*cell
		if p.left <= 0 || p.x < x0 || p.x >= x0+cell || p.y < y0 || p.y >= y0+cell {
			d.spawn(i)
		}
	}
	d.version++
}

func (d *textDust) draw() {
	clear(d.alpha)
	for i := range d.parts {
		p := &d.parts[i]
		g := dustGroups[p.group]
		dustPoint(d.alpha, d.side, 1, p.x, p.y, g.size*d.scale/2, g.alpha)
	}
}

// dustPoint draws a round point of radius r at x, y, alpha of it showing,
// into pix, a square of side pixels of step bytes each, every one of which
// is how much of a point the pixel has. A point at an edge goes on at the
// opposite one: the square repeats without a seam.
func dustPoint(pix []uint8, side, step int, x, y, r, alpha float32) {
	x0, x1 := int(math.Floor(float64(x-r-.5))), int(math.Ceil(float64(x+r+.5)))
	y0, y1 := int(math.Floor(float64(y-r-.5))), int(math.Ceil(float64(y+r+.5)))
	for py := y0; py < y1; py++ {
		dy := float32(py) + .5 - y
		row := ((py%side + side) % side) * side
		for px := x0; px < x1; px++ {
			dx := float32(px) + .5 - x
			// How much of the pixel the point covers, as its edge
			// crosses it.
			cover := r + .5 - float32(math.Sqrt(float64(dx*dx+dy*dy)))
			if cover <= 0 {
				continue
			}
			v := uint32(min(cover, 1) * alpha * 255)
			at := (row + (px%side+side)%side) * step
			a := uint32(pix[at])
			a += (255 - a) * v / 255
			for c := range step {
				pix[at+c] = uint8(a)
			}
		}
	}
}

// advance moves particles on to now, in steps of dustStep, and reports
// whether they moved; *stepped is when they moved last.
func advance(now time.Time, stepped *time.Time, step func(dt float32)) bool {
	if stepped.IsZero() || now.Before(*stepped) {
		*stepped = now
		return false
	}
	n := int(now.Sub(*stepped) / dustStep)
	if n <= 0 {
		return false
	}
	if n > dustSteps {
		// Long not drawn: on from where they are.
		n = 1
		*stepped = now
	} else {
		*stepped = stepped.Add(time.Duration(n) * dustStep)
	}
	for range n {
		step(float32(dustStep.Seconds()))
	}
	return true
}

// tile is the dust in col as it is at the frame's time, which asks for the
// frame the particles move in next.
func (d *textDust) tile(gtx layout.Context, col color.NRGBA, still bool) paint.ImageOp {
	if d.alpha == nil || d.scale != gtx.Metric.PxPerDp {
		*d = textDust{}
		d.init(gtx)
	}
	d.used = gtx.Now
	if !still {
		if advance(gtx.Now, &d.stepped, d.step) {
			d.draw()
			for c, t := range d.tiles {
				if gtx.Now.Sub(t.used) > time.Second {
					delete(d.tiles, c)
				}
			}
		}
		gtx.Execute(op.InvalidateCmd{At: d.stepped.Add(dustStep)})
	}
	t := d.tiles[col]
	if t == nil {
		t = &dustTile{img: image.NewRGBA(image.Rect(0, 0, d.side, d.side))}
		d.tiles[col] = t
	}
	t.used = gtx.Now
	if t.version != d.version {
		t.version = d.version
		// Premultiplied, as an image.RGBA is.
		r, g, b, a := uint32(col.R), uint32(col.G), uint32(col.B), uint32(col.A)
		pix := t.img.Pix
		for i, v := range d.alpha {
			if v == 0 {
				pix[i*4], pix[i*4+1], pix[i*4+2], pix[i*4+3] = 0, 0, 0, 0
				continue
			}
			va := uint32(v) * a / 255
			pix[i*4], pix[i*4+1], pix[i*4+2], pix[i*4+3] = uint8(r*va/255), uint8(g*va/255), uint8(b*va/255), uint8(va)
		}
		// A picture changed is another picture to the GPU.
		t.op = paint.NewImageOp(t.img)
	}
	return t.op
}

// paintDust draws the text's dust in col over size, inside the clip of
// what it covers, opacity of it showing. origin is where size lies in what
// the dust goes over as a whole, a message's text: its lines show one dust,
// not each the same strip of it. It draws nothing with animations off, or
// in a window without particles.
func paintDust(gtx layout.Context, size, origin image.Point, col color.NRGBA, opacity float32) {
	e := effectsOf(gtx)
	if e == nil || col.A == 0 || opacity <= 0 || size.X <= 0 || size.Y <= 0 || !wdk.AnimationEnabled(gtx, wdk.AnimationMessage) {
		return
	}
	tile := e.text.tile(gtx, col, e.still)
	side := e.text.side
	if opacity < 1 {
		defer paint.PushOpacity(gtx.Ops, opacity).Pop()
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	x0, y0 := -((origin.X%side + side) % side), -((origin.Y%side + side) % side)
	for y := y0; y < size.Y; y += side {
		for x := x0; x < size.X; x += side {
			stack := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
			area := clip.Rect{Max: image.Pt(side, side)}.Push(gtx.Ops)
			tile.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			area.Pop()
			stack.Pop()
		}
	}
}

const (
	// mediaDustTile is the side of the media's dust, drawn once over each
	// media, larger over a larger one.
	mediaDustTile = unit.Dp(320)
	// mediaDustSize is how large a point is across.
	mediaDustSize = unit.Dp(1.2)
	// mediaDustPace is how fast the flow's time runs.
	mediaDustPace = 0.65
	// mediaDustSpeed caps a point's speed, in sides of the square to a
	// unit of the flow's time.
	mediaDustSpeed = 0.06
	// A point shows over a cycle of mediaDustCycleMin…mediaDustCycleMax
	// units of the flow's time, fading in and out.
	mediaDustCycleMin = 0.56
	mediaDustCycleMax = 2.8
)

type mediaParticle struct {
	x, y, vx, vy float32
	// age is how far the point is in its cycle, and peak how much it
	// shows at the middle of it.
	age, cycle, peak float32
}

// mediaDust is the dust over media under a spoiler: white points a slow
// flow of swirls carries, in a square the flow repeats over.
type mediaDust struct {
	side  int
	scale float32
	rng   *rand.Rand
	parts []mediaParticle
	img   *image.RGBA
	op    paint.ImageOp
	// time is the flow's, and grid the flow charted for it.
	time    float32
	grid    [][2]float32
	stepped time.Time
	used    time.Time
}

func (d *mediaDust) init(gtx layout.Context) {
	d.scale = gtx.Metric.PxPerDp
	d.side = max(1, gtx.Dp(mediaDustTile))
	d.rng = rand.New(rand.NewPCG(0x5eed, 0x3ed1a))
	// As many as Android has for a square of the size: a thousand for
	// 500 of a phone's pixels squared, about three to a dp.
	k := float64(mediaDustTile) * 3 / 500
	d.parts = make([]mediaParticle, int(min(max(k*k*1000, 500), 10000)))
	d.chart()
	for i := range d.parts {
		p := &d.parts[i]
		p.x, p.y = d.rng.Float32()*float32(d.side), d.rng.Float32()*float32(d.side)
		d.restart(p)
		p.age = d.rng.Float32() * p.cycle
		p.vx, p.vy = d.flow(p.x, p.y)
	}
	d.img = image.NewRGBA(image.Rect(0, 0, d.side, d.side))
	d.draw()
}

// restart begins a point's cycle anew where it is, at rest.
func (d *mediaDust) restart(p *mediaParticle) {
	p.age, p.vx, p.vy = 0, 0, 0
	p.cycle = mediaDustCycleMin + d.rng.Float32()*(mediaDustCycleMax-mediaDustCycleMin)
	p.peak = 0.6 + 0.4*d.rng.Float32()
}

// flowWaves are the waves the flow is made of: how many of each fit in the
// square across and down, where it begins and how fast it moves.
var flowWaves = [...]struct{ m, n, phase, drift float64 }{
	{1, 2, 0.3, 0.21}, {2, -1, 1.9, -0.17}, {3, 1, 4.1, 0.13}, {1, -3, 2.6, -0.11}, {2, 3, 5.2, 0.19},
}

// flowGrid is how many cells across the flow is worked out in for a step:
// it is smooth, and a point takes it from the corners of its cell.
const flowGrid = 32

// flowAt is how fast the flow carries a point at x, y, in pixels to a unit
// of its time: the curl of a field of waves that repeats over the square,
// so that the points swirl and neither gather nor thin out, and the flow
// goes on across the square's edges.
func (d *mediaDust) flowAt(x, y float32) (vx, vy float32) {
	u, v := float64(x)/float64(d.side)*2*math.Pi, float64(y)/float64(d.side)*2*math.Pi
	var dx, dy float64
	for _, w := range flowWaves {
		c := math.Cos(w.m*u + w.n*v + w.phase + w.drift*float64(d.time))
		// A wave of more crests is lower, as much as it is steeper.
		k := 1 / math.Hypot(w.m, w.n)
		dx += w.m * k * c
		dy += w.n * k * c
	}
	// Half the cap where one wave is at its steepest: the waves together
	// reach the cap here and there.
	speed := mediaDustSpeed * float64(d.side) / 2
	return float32(dy * speed), float32(-dx * speed)
}

// chart works the flow out at the corners of its grid, for the flow's
// time as it is.
func (d *mediaDust) chart() {
	if d.grid == nil {
		d.grid = make([][2]float32, flowGrid*flowGrid)
	}
	cell := float32(d.side) / flowGrid
	for j := range flowGrid {
		for i := range flowGrid {
			vx, vy := d.flowAt(float32(i)*cell, float32(j)*cell)
			d.grid[j*flowGrid+i] = [2]float32{vx, vy}
		}
	}
}

// flow is the flow at x, y, a point of the square, as charted.
func (d *mediaDust) flow(x, y float32) (vx, vy float32) {
	gx, gy := x/float32(d.side)*flowGrid, y/float32(d.side)*flowGrid
	i, j := int(gx), int(gy)
	fx, fy := gx-float32(i), gy-float32(j)
	i, j = i%flowGrid, j%flowGrid
	// The grid goes on at the opposite edge, as the flow does.
	i1, j1 := (i+1)%flowGrid, (j+1)%flowGrid
	a, b, c, e := d.grid[j*flowGrid+i], d.grid[j*flowGrid+i1], d.grid[j1*flowGrid+i], d.grid[j1*flowGrid+i1]
	top := [2]float32{a[0] + (b[0]-a[0])*fx, a[1] + (b[1]-a[1])*fx}
	bottom := [2]float32{c[0] + (e[0]-c[0])*fx, c[1] + (e[1]-c[1])*fx}
	return top[0] + (bottom[0]-top[0])*fy, top[1] + (bottom[1]-top[1])*fy
}

// step moves the points by dt seconds.
func (d *mediaDust) step(dt float32) {
	dt *= mediaDustPace
	d.time += dt
	d.chart()
	side := float32(d.side)
	limit := mediaDustSpeed * side
	// A point takes up the flow's speed in about a quarter of a unit.
	pull := min(1, 4*dt)
	for i := range d.parts {
		p := &d.parts[i]
		p.age += dt
		if p.age >= p.cycle {
			d.restart(p)
		}
		fx, fy := d.flow(p.x, p.y)
		p.vx += (fx - p.vx) * pull
		p.vy += (fy - p.vy) * pull
		if s := float32(math.Hypot(float64(p.vx), float64(p.vy))); s > limit {
			p.vx, p.vy = p.vx*limit/s, p.vy*limit/s
		}
		// Out at an edge, in at the opposite one.
		if p.x += p.vx * dt; p.x < 0 {
			p.x += side
		} else if p.x >= side {
			p.x -= side
		}
		if p.y += p.vy * dt; p.y < 0 {
			p.y += side
		} else if p.y >= side {
			p.y -= side
		}
	}
}

func (d *mediaDust) draw() {
	pix := d.img.Pix
	clear(pix)
	r := float32(mediaDustSize) * d.scale / 2
	for i := range d.parts {
		p := &d.parts[i]
		alpha := float32(math.Sin(math.Pi*float64(p.age/p.cycle))) * p.peak
		// White, premultiplied: every channel is the alpha.
		dustPoint(pix, d.side, 4, p.x, p.y, r, alpha)
	}
	d.op = paint.NewImageOp(d.img)
}

// paintMediaDust draws the media's dust over size, inside the clip of the
// media it covers: at its own size, larger over media larger than its
// square, and turned or mirrored by index, the media's place among those
// shown, so that neighbours do not show the same dust. With animations
// off the dust stands still.
func paintMediaDust(gtx layout.Context, size image.Point, index int) {
	e := effectsOf(gtx)
	if e == nil || size.X <= 0 || size.Y <= 0 {
		return
	}
	d := &e.media
	if d.img == nil || d.scale != gtx.Metric.PxPerDp {
		*d = mediaDust{}
		d.init(gtx)
	}
	d.used = gtx.Now
	if !e.still && wdk.AnimationEnabled(gtx, wdk.AnimationMessage) {
		if advance(gtx.Now, &d.stepped, d.step) {
			d.draw()
		}
		gtx.Execute(op.InvalidateCmd{At: d.stepped.Add(dustStep)})
	}
	side := float32(d.side)
	grow := max(1, float32(size.X)/side, float32(size.Y)/side)
	sx, sy := grow, grow
	switch index % 4 {
	case 1:
		sx, sy = -grow, -grow
	case 2:
		sx = -grow
	case 3:
		sy = -grow
	}
	// The square's middle over the media's.
	middle := f32.Pt(float32(size.X)/2, float32(size.Y)/2)
	t := f32.Affine2D{}.Offset(f32.Pt(-side/2, -side/2)).Scale(f32.Point{}, f32.Pt(sx, sy)).Offset(middle)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	defer op.Affine(t).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: image.Pt(d.side, d.side)}.Push(gtx.Ops).Pop()
	d.op.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
