// SPDX-License-Identifier: Unlicense OR MIT

package lottie

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"komarugram/pkg/lottie"

	"gio-mw/exp"
	"gio-mw/exp/examples"
	"gio-mw/exp/router"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

const (
	// stickerDir holds .tgs and .json animations, resolved relative to the
	// working directory the example is started from.
	stickerDir = "assets/stickers"

	// renderSize is the pixel size each animation is rendered at. Nothing is
	// cached: a frame is drawn on demand and thrown away.
	renderSize = 160

	// playbackFPS caps how often the page repaints. Stickers are authored at
	// 60 fps, but a repaint costs far more than a frame does to render, so
	// clients cap playback instead of following the animation's own rate.
	playbackFPS = 30

	stickerGap = unit.Dp(12)
)

// Animations are loaded once per package, like the other media pages: the
// router rebuilds a page on every navigation.
var animations animationSet

type animationSet struct {
	mu      sync.Mutex
	runtime *lottie.Runtime
	loaded  []*lottie.Animation
	err     error
	status  string
	compile time.Duration
	ready   bool
	loading bool

	// timings are rolling render times, written by the layout goroutine.
	timings map[string]time.Duration
}

func (s *animationSet) load() {
	s.mu.Lock()
	if s.loading || s.ready {
		s.mu.Unlock()
		return
	}
	s.loading = true
	s.status = "Compiling the WebAssembly renderer…"
	s.timings = map[string]time.Duration{}
	s.mu.Unlock()

	go func() {
		ctx := context.Background()
		started := time.Now()
		runtime, err := lottie.NewRuntime(ctx)
		if err != nil {
			s.finish(nil, nil, 0, err)
			return
		}
		compile := time.Since(started)

		paths, err := filepath.Glob(filepath.Join(stickerDir, "*"))
		if err != nil || len(paths) == 0 {
			s.finish(runtime, nil, compile, fmt.Errorf("no animations in %s/", stickerDir))
			return
		}
		sort.Strings(paths)

		var opened []*lottie.Animation
		for _, path := range paths {
			switch filepath.Ext(path) {
			case ".json", ".tgs":
			default:
				continue
			}
			s.setStatus("Opening " + filepath.Base(path) + "…")
			data, err := os.ReadFile(path)
			if err != nil {
				s.finish(runtime, opened, compile, err)
				return
			}
			animation, err := runtime.Open(ctx, filepath.Base(path), data, renderSize)
			if err != nil {
				s.finish(runtime, opened, compile, err)
				return
			}
			opened = append(opened, animation)
		}
		if len(opened) == 0 {
			s.finish(runtime, nil, compile, fmt.Errorf("no .tgs or .json animations in %s/", stickerDir))
			return
		}
		s.finish(runtime, opened, compile, nil)
	}()
}

func (s *animationSet) setStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *animationSet) finish(runtime *lottie.Runtime, opened []*lottie.Animation, compile time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime, s.loaded, s.compile, s.err = runtime, opened, compile, err
	s.loading, s.ready = false, true
}

// snapshot is what the layout needs, without holding the lock while painting.
func (s *animationSet) snapshot() (loaded []*lottie.Animation, ready bool, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, false, s.status
	}
	if s.err != nil {
		return s.loaded, true, s.err.Error()
	}
	var frames int
	for _, animation := range s.loaded {
		frames += animation.FrameCount
	}
	return s.loaded, true, fmt.Sprintf("%d animations · %d frames · renderer compiled in %.0f ms · no cgo",
		len(s.loaded), frames, float64(s.compile.Milliseconds()))
}

func (s *animationSet) setTiming(name string, took time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// An exponential average keeps the readout from flickering.
	if previous, ok := s.timings[name]; ok {
		took = (previous*7 + took) / 8
	}
	s.timings[name] = took
}

func (s *animationSet) timing(name string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timings[name]
}

type Page struct {
	started   time.Time
	paused    bool
	pausedAt  time.Duration
	playPause *button.Button
}

func NewPage() router.PageWidget {
	animations.load()
	return &Page{
		started:   time.Now(),
		playPause: button.Text(),
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
				txt := "Lottie"
				return exp.HeadlineL(gtx, txt)
			}),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "Telegram stickers drawn by a Rust renderer sandboxed in WebAssembly"
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
	_, _, summary := animations.snapshot()
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
	loaded, ready, _ := animations.snapshot()
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
	gap := gtx.Dp(stickerGap)
	var (
		pos     image.Point
		rowMax  int
		nextDue = time.Duration(1<<62 - 1)
	)
	for _, animation := range loaded {
		if pos.X > 0 && pos.X+animation.Size.X > gtx.Constraints.Max.X {
			pos = image.Point{Y: pos.Y + rowMax + gap}
			rowMax = 0
		}
		p.paintSticker(gtx, ctx, animation, pos, elapsed)
		nextDue = min(nextDue, untilNextFrame(min(animation.FPS, playbackFPS), elapsed))
		pos.X += animation.Size.X + gap
		rowMax = max(rowMax, animation.Size.Y+gtx.Dp(24))
	}
	if !p.paused {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(nextDue)})
	}

	return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X, Y: pos.Y + rowMax}}
}

func (p *Page) paintSticker(gtx layout.Context, ctx context.Context, a *lottie.Animation, at image.Point, elapsed time.Duration) {
	defer op.Offset(at).Push(gtx.Ops).Pop()

	materialTheme := wdk.GetMaterialTheme(gtx)
	shape := wdk.Box{
		Shape:    wdk.UniformCornerShapes(wdk.CornerShape{Kind: wdk.CornerKindRound, Size: 12}),
		EndPoint: a.Size,
	}
	surface := shape.Outline(gtx).Push(gtx.Ops)
	paint.Fill(gtx.Ops, materialTheme.Scheme.SurfaceContainerHighest.AsNRGBA())
	surface.Pop()

	frame, took, err := a.FrameAt(ctx, elapsed)
	if err == nil {
		animations.setTiming(a.Name, took)
		region := clip.Rect{Max: a.Size}.Push(gtx.Ops)
		imageOp := paint.NewImageOp(frame)
		imageOp.Filter = paint.FilterLinear
		imageOp.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		region.Pop()
	}

	defer op.Offset(image.Point{Y: a.Size.Y + gtx.Dp(4)}).Push(gtx.Ops).Pop()
	gtx.Constraints.Max.X = a.Size.X
	caption := a.Name
	if err != nil {
		caption = err.Error()
	} else if timing := animations.timing(a.Name); timing > 0 {
		caption = fmt.Sprintf("%s · %.2f ms", a.Name, float64(timing.Microseconds())/1000)
	}
	exp.BodyS(gtx, caption)
}

// untilNextFrame is how long the current frame of an animation stays up.
func untilNextFrame(fps float64, elapsed time.Duration) time.Duration {
	if fps <= 0 {
		return 16 * time.Millisecond
	}
	period := time.Duration(float64(time.Second) / fps)
	if period <= 0 {
		return time.Millisecond
	}
	return period - elapsed%period
}
