// SPDX-License-Identifier: Unlicense OR MIT

package sticker

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"komarugram/pkg/vp9"

	"gio-mw/exp"
	"gio-mw/exp/examples"
	"gio-mw/exp/router"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/button"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

const (
	// stickerDir holds the .webm stickers, resolved relative to the working
	// directory the example is started from.
	stickerDir = "assets/stickers"

	// tileSize is how large a sticker is laid out. Stickers are coded at
	// 512x512; the GPU scales the decoded frame down to this.
	tileSize = unit.Dp(160)

	// playbackFPS caps repaints, as on the Lottie page.
	playbackFPS = 30

	tileGap = unit.Dp(12)
)

// Stickers are loaded once per package: the router rebuilds pages on every
// navigation, and each sticker owns two WebAssembly sandboxes.
var stickers stickerSet

type stickerSet struct {
	mu      sync.Mutex
	runtime *vp9.Runtime
	loaded  []*vp9.Sticker
	err     error
	status  string
	compile time.Duration
	ready   bool
	loading bool
	timings map[string]time.Duration
}

func (s *stickerSet) load() {
	s.mu.Lock()
	if s.loading || s.ready {
		s.mu.Unlock()
		return
	}
	s.loading = true
	s.status = "Compiling the WebAssembly decoder…"
	s.timings = map[string]time.Duration{}
	s.mu.Unlock()

	go func() {
		ctx := context.Background()
		started := time.Now()
		runtime, err := vp9.NewRuntime(ctx)
		if err != nil {
			s.finish(nil, nil, 0, err)
			return
		}
		compile := time.Since(started)

		paths, err := filepath.Glob(filepath.Join(stickerDir, "*.webm"))
		if err != nil || len(paths) == 0 {
			s.finish(runtime, nil, compile, fmt.Errorf("no *.webm stickers in %s/", stickerDir))
			return
		}
		sort.Strings(paths)

		var opened []*vp9.Sticker
		for _, path := range paths {
			s.setStatus("Opening " + filepath.Base(path) + "…")
			data, err := os.ReadFile(path)
			if err != nil {
				s.finish(runtime, opened, compile, err)
				return
			}
			sticker, err := runtime.OpenSticker(ctx, filepath.Base(path), data)
			if err != nil {
				s.finish(runtime, opened, compile, err)
				return
			}
			opened = append(opened, sticker)
		}
		s.finish(runtime, opened, compile, nil)
	}()
}

func (s *stickerSet) setStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *stickerSet) finish(runtime *vp9.Runtime, opened []*vp9.Sticker, compile time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime, s.loaded, s.compile, s.err = runtime, opened, compile, err
	s.loading, s.ready = false, true
}

func (s *stickerSet) snapshot() (loaded []*vp9.Sticker, ready bool, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, false, s.status
	}
	if s.err != nil {
		return s.loaded, true, s.err.Error()
	}
	var frames, withAlpha int
	for _, sticker := range s.loaded {
		frames += sticker.FrameCount()
		if sticker.HasAlpha() {
			withAlpha++
		}
	}
	return s.loaded, true, fmt.Sprintf("%d stickers · %d frames · %d with alpha · decoder compiled in %.0f ms · no cgo",
		len(s.loaded), frames, withAlpha, float64(s.compile.Milliseconds()))
}

func (s *stickerSet) setTiming(name string, took time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.timings[name]; ok {
		took = (previous*7 + took) / 8
	}
	s.timings[name] = took
}

func (s *stickerSet) timing(name string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timings[name]
}

type Page struct {
	started   time.Time
	paused    bool
	pausedAt  time.Duration
	playPause *button.Button
	errored   map[string]string
}

func NewPage() router.PageWidget {
	stickers.load()
	return &Page{
		started:   time.Now(),
		playPause: button.Text(),
		errored:   map[string]string{},
	}
}

func (p *Page) IsWide() bool {
	return true
}

func (p *Page) Update(gtx layout.Context) {
	if p.playPause.Clicked(gtx) {
		if p.paused {
			p.started = gtx.Now.Add(-p.pausedAt)
		} else {
			p.pausedAt = gtx.Now.Sub(p.started)
		}
		p.paused = !p.paused
	}
}

func (p *Page) View(gtx layout.Context) layout.Dimensions {
	return block.UniformPadding(examples.SpacingMedium).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return block.Line{
			Axis:     block.AxisVertical,
			Overflow: block.OverflowClip,
		}.Layout(gtx,
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "Video stickers"
				return exp.HeadlineL(gtx, txt)
			}),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "WebM demuxed in Go, VP9 and its alpha decoded in WebAssembly"
				return exp.BodyL(gtx, txt)
			}),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.sectionStatus),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.sectionStickers),
		)
	})
}

func (p *Page) sectionStatus(gtx layout.Context) layout.Dimensions {
	label := "Pause"
	if p.paused {
		label = "Play"
	}
	_, _, summary := stickers.snapshot()
	return block.Line{
		Axis:     block.AxisHorizontal,
		Overflow: block.OverflowWrap,
		Expand:   true,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return p.playPause.Layout(gtx, label)
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return exp.BodyL(gtx, summary)
		}),
	)
}

func (p *Page) sectionStickers(gtx layout.Context) layout.Dimensions {
	loaded, ready, _ := stickers.snapshot()
	if !ready {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(100 * time.Millisecond)})
		return layout.Dimensions{}
	}
	if len(loaded) == 0 {
		return layout.Dimensions{}
	}

	elapsed := gtx.Now.Sub(p.started)
	if p.paused {
		elapsed = p.pausedAt
	}

	ctx := context.Background()
	size := gtx.Dp(tileSize)
	gap := gtx.Dp(tileGap)
	caption := gtx.Dp(unit.Dp(24))
	var pos image.Point
	for _, sticker := range loaded {
		if pos.X > 0 && pos.X+size > gtx.Constraints.Max.X {
			pos = image.Point{Y: pos.Y + size + caption + gap}
		}
		p.paintSticker(gtx, ctx, sticker, pos, size, elapsed)
		pos.X += size + gap
	}
	if !p.paused {
		period := time.Second / playbackFPS
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(period - elapsed%period)})
	}
	return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X, Y: pos.Y + size + caption}}
}

func (p *Page) paintSticker(gtx layout.Context, ctx context.Context, s *vp9.Sticker, at image.Point, size int, elapsed time.Duration) {
	defer op.Offset(at).Push(gtx.Ops).Pop()

	materialTheme := wdk.GetMaterialTheme(gtx)
	shape := wdk.Box{
		Shape:    wdk.UniformCornerShapes(wdk.CornerShape{Kind: wdk.CornerKindRound, Size: 12}),
		EndPoint: image.Pt(size, size),
	}
	surface := shape.Outline(gtx).Push(gtx.Ops)
	paint.Fill(gtx.Ops, materialTheme.Scheme.SurfaceContainerHighest.AsNRGBA())
	surface.Pop()

	label := s.Name
	started := time.Now()
	frame, err := s.FrameAt(ctx, elapsed)
	took := time.Since(started)
	switch {
	case err != nil:
		p.errored[s.Name] = err.Error()
		label = err.Error()
	default:
		stickers.setTiming(s.Name, took)
		region := clip.Rect{Max: image.Pt(size, size)}.Push(gtx.Ops)
		scale := float32(size) / float32(frame.Bounds().Dx())
		transform := op.Affine(f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(scale, scale))).Push(gtx.Ops)
		imageOp := paint.NewImageOp(frame)
		imageOp.Filter = paint.FilterLinear
		imageOp.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		transform.Pop()
		region.Pop()

		alpha := "opaque"
		if s.HasAlpha() {
			alpha = "alpha"
		}
		label = fmt.Sprintf("%s · %s · %.1f ms", s.Name, alpha, float64(stickers.timing(s.Name).Microseconds())/1000)
	}

	defer op.Offset(image.Point{Y: size + gtx.Dp(unit.Dp(4))}).Push(gtx.Ops).Pop()
	gtx.Constraints.Max.X = size
	exp.BodyS(gtx, label)
}
