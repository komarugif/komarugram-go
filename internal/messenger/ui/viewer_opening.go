// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"math"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
)

const (
	viewerOpenMove = token.DurationMedium2
	viewerOpenFade = token.DurationShort3
)

// Track releases in both the viewer's and the tile's coordinate spaces.
// Gio positions a list's rows after laying them out. The difference of the
// positions from the same event gives the source's origin in the frame the
// user clicked, including scrolling, album offsets and the profile cover's
// avatar transform.
type viewerOpenPointer struct {
	releases []pointer.Event
}

func (p *viewerOpenPointer) update(gtx layout.Context) {
	p.releases = p.releases[:0]
	for {
		e, ok := gtx.Event(pointer.Filter{Target: p, Kinds: pointer.Release})
		if !ok {
			return
		}
		if release, ok := e.(pointer.Event); ok && release.Kind == pointer.Release {
			p.releases = append(p.releases, release)
		}
	}
}

func (p *viewerOpenPointer) origin(gtx layout.Context, tag event.Tag, scale float32) (image.Point, bool) {
	var origin image.Point
	found := false
	for {
		e, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Release})
		if !ok {
			return origin, found
		}
		local, ok := e.(pointer.Event)
		if !ok || local.Kind != pointer.Release {
			continue
		}
		for i := len(p.releases) - 1; i >= 0; i-- {
			root := p.releases[i]
			if root.PointerID == local.PointerID && root.Time == local.Time && root.Source == local.Source {
				d := root.Position.Sub(local.Position.Mul(scale))
				origin, found = image.Pt(int(math.Round(float64(d.X))), int(math.Round(float64(d.Y)))), true
				break
			}
		}
	}
}

func trackViewerRelease(gtx layout.Context, tag event.Tag, size image.Point) {
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
}

type viewerOpenTile struct {
	picture      image.Image
	shape        clip.RRect
	visible      image.Rectangle
	fit          widget.Fit
	position     layout.Direction
	scaleCorners bool
}

type viewerOpening struct {
	start       time.Time
	source      *widget.Clickable
	tile        viewerOpenTile
	drawn       time.Duration
	returnStart time.Time
}

func viewerOpenFadeAt(elapsed time.Duration) float32 {
	return float32(token.EasingStandardDecelerate.Ease(float64(elapsed) / float64(viewerOpenFade)))
}

func (o *viewerOpening) returning() bool { return !o.returnStart.IsZero() }

func (o *viewerOpening) elapsed(now time.Time) time.Duration {
	if o.returning() {
		return o.drawn - now.Sub(o.returnStart)
	}
	return now.Sub(o.start)
}

// The source comes back as the viewer controls fade in.
func (o *viewerOpening) sourceOpacity(gtx layout.Context, source *widget.Clickable, animate bool) float32 {
	if !animate || !wdk.AnimationAllowed(gtx, wdk.AnimationViewerAttachment) || o.source != source {
		return 1
	}
	opacity := viewerOpenFadeAt(o.elapsed(gtx.Now) - viewerOpenMove)
	if opacity < 1 {
		// A shortcut can end the entrance after the source has drawn.
		gtx.Execute(op.InvalidateCmd{})
	}
	return opacity
}

// Update before laying out the chat so it uses the same phase as the viewer
// and paints the source tile in full on the frame the return ends.
func (v *mediaViewer) updateOpening(gtx layout.Context, animate bool) {
	o := &v.opening
	if o.start.IsZero() {
		return
	}
	if o.returning() {
		if !animate || !wdk.AnimationAllowed(gtx, wdk.AnimationViewerAttachment) || o.elapsed(gtx.Now) <= 0 {
			v.closeViewer(true)
		} else {
			gtx.Execute(op.InvalidateCmd{})
		}
		return
	}
	dismiss := v.backdrop.Clicked(gtx) || v.close.Clicked(gtx)
	for !dismiss {
		e, ok := gtx.Event(key.FocusFilter{Target: &v.keys}, key.Filter{Focus: &v.keys, Name: key.NameEscape})
		if !ok {
			return
		}
		if e, ok := e.(key.Event); ok && e.State == key.Press {
			dismiss = true
		}
	}
	gtx.Execute(key.FocusCmd{})
	if !animate || !wdk.AnimationAllowed(gtx, wdk.AnimationViewerAttachment) || o.source == nil || o.drawn <= 0 {
		v.closeViewer(true)
		return
	}
	// Reverse from the frame the user actually saw, even if the close
	// event arrived after a long gap between frames.
	o.returnStart = gtx.Now
	gtx.Execute(op.InvalidateCmd{})
}

// step returns the picture's travel and the opacity of the viewer controls.
// The backdrop dims during travel so the picture stays visible over the chat.
func (o *viewerOpening) step(gtx layout.Context, animate bool) (travel, chrome float32) {
	if !animate {
		o.start, o.source = time.Time{}, nil
	} else if o.source != nil && !wdk.AnimationAllowed(gtx, wdk.AnimationViewerAttachment) {
		o.start, o.source = gtx.Now, nil
	}
	if o.start.IsZero() {
		return 1, 1
	}
	elapsed := o.elapsed(gtx.Now)
	move := time.Duration(0)
	if o.source != nil {
		move = viewerOpenMove
	}
	fade := time.Duration(0)
	if wdk.AnimationAllowed(gtx, wdk.AnimationViewer) {
		fade = viewerOpenFade
	}
	if elapsed >= move+fade {
		o.start, o.source = time.Time{}, nil
		return 1, 1
	}
	if !o.returning() {
		o.drawn = elapsed
		gtx.Execute(op.InvalidateCmd{})
	}
	if elapsed < move {
		return float32(token.EasingStandard.Ease(float64(elapsed) / float64(move))), 0
	}
	return 1, viewerOpenFadeAt(elapsed - move)
}

func (t viewerOpenTile) layout(gtx layout.Context, images *imageOps, dest image.Rectangle, progress float32) {
	source := t.shape.Rect
	b := t.picture.Bounds().Size()
	scale := min(float64(source.Dx())/float64(b.X), float64(source.Dy())/float64(b.Y))
	if t.fit == widget.Cover {
		scale = max(float64(source.Dx())/float64(b.X), float64(source.Dy())/float64(b.Y))
	}
	photo := image.Pt(int(math.Round(float64(b.X)*scale)), int(math.Round(float64(b.Y)*scale)))
	at := source.Min.Add(t.position.Position(photo, source.Size()))
	from := image.Rectangle{Min: at, Max: at.Add(photo)}
	mix := func(a, b int) int { return a + int(math.Round(float64(float32(b-a)*progress))) }
	mixPoint := func(a, b image.Point) image.Point { return image.Pt(mix(a.X, b.X), mix(a.Y, b.Y)) }
	mixRect := func(a image.Rectangle) image.Rectangle {
		return image.Rectangle{Min: mixPoint(a.Min, dest.Min), Max: mixPoint(a.Max, dest.Max)}
	}
	bounds := mixRect(source)
	corners := 1 - progress
	if t.scaleCorners {
		// The avatar's roundness changes independently of its growing size.
		corners *= float32(min(bounds.Dx(), bounds.Dy())) / float32(min(source.Dx(), source.Dy()))
	}
	radius := func(r int) int { return int(math.Round(float64(float32(r) * corners))) }
	mask := clip.RRect{
		Rect: bounds,
		NW:   radius(t.shape.NW), NE: radius(t.shape.NE),
		SW: radius(t.shape.SW), SE: radius(t.shape.SE),
	}
	defer clip.Rect(mixRect(t.visible)).Push(gtx.Ops).Pop()
	defer mask.Push(gtx.Ops).Pop()
	photoAt := mixRect(from)
	defer op.Offset(photoAt.Min).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(photoAt.Size())
	widget.Image{Src: images.Op(t.picture), Fit: widget.Fill}.Layout(gtx)
}
