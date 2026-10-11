// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/pkg/rectanim"
)

// mascotURL is the folder of the mascot's scenes in their repository. The
// scenes live there, not here: removing that repository removes the
// mascot from every build.
const mascotURL = "https://raw.githubusercontent.com/komarugif/clawd-animations/main/animations/"

// mascotRepository is the scenes' repository, whose README tells whose
// the character and the animations are.
const mascotRepository = "github.com/komarugif/clawd-animations"

// mascotSource is where the scenes are taken from: KOMARUGRAM_MASCOT, a
// folder or a URL with the same files, over the repository.
func mascotSource() string {
	if s := os.Getenv("KOMARUGRAM_MASCOT"); s != "" {
		return s
	}
	return mascotURL
}

const (
	// mascotUnit is about the size of a unit of the scenes, which is a
	// whole number of pixels: the mascot's body, 85 units wide, is some
	// 68dp.
	mascotUnit = unit.Dp(0.8)
	// mascotHeight is the stage's height in units: the tallest picture,
	// and the walk's jump of 90 units over its 86.
	mascotHeight = 180
)

// mascotView is Claude's mascot in the About section: one of its scenes
// plays over and over, and a click goes on to the next one. With the
// animations off it plays only under the pointer, as stickers do, and
// stands still in its scene's rest frame otherwise.
type mascotView struct {
	scenes   []*rectanim.Scene
	fetching bool
	fetched  chan mascotScenes
	// index is the scene playing, since started. A click on the stage
	// goes on to the next one.
	index   int
	started time.Time
	stage   widget.Clickable
	// repository opens mascotRepository.
	repository widget.Clickable
	// load reads the scenes; tests replace it.
	load       func(ctx context.Context) ([]*rectanim.Scene, error)
	invalidate func()
}

type mascotScenes struct {
	scenes []*rectanim.Scene
	err    error
}

func newMascotView(invalidate func(), fetch func(context.Context, string) ([]byte, error)) *mascotView {
	v := &mascotView{fetched: make(chan mascotScenes, 1), invalidate: invalidate}
	v.load = func(ctx context.Context) ([]*rectanim.Scene, error) { return loadScenes(ctx, mascotSource(), fetch) }
	return v
}

// open loads the scenes, once they are missing and not on their way.
func (v *mascotView) open() {
	if v.fetching || v.scenes != nil {
		return
	}
	v.fetching = true
	load, fetched, invalidate := v.load, v.fetched, v.invalidate
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		scenes, err := load(ctx)
		fetched <- mascotScenes{scenes, err}
		invalidate()
	}()
}

// loadScenes reads index.json and the scenes it lists from source, a
// folder or a URL ending in a slash.
func loadScenes(ctx context.Context, source string, fetch func(context.Context, string) ([]byte, error)) ([]*rectanim.Scene, error) {
	read := func(name string) ([]byte, error) {
		if strings.Contains(source, "://") {
			return fetch(ctx, strings.TrimSuffix(source, "/")+"/"+name)
		}
		return os.ReadFile(filepath.Join(source, name))
	}
	data, err := read("index.json")
	if err != nil {
		return nil, err
	}
	var index struct {
		Animations []struct {
			File string `json:"file"`
		} `json:"animations"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	var scenes []*rectanim.Scene
	for _, a := range index.Animations {
		if a.File == "" || strings.ContainsAny(a.File, `/\`) {
			return nil, fmt.Errorf("mascot: scene file %q", a.File)
		}
		data, err := read(a.File)
		if err != nil {
			return nil, err
		}
		s, err := rectanim.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("mascot: %s: %w", a.File, err)
		}
		scenes = append(scenes, s)
	}
	if len(scenes) == 0 {
		return nil, errors.New("mascot: no scenes")
	}
	return scenes, nil
}

// Update takes the scenes that came, and goes on to the next scene when
// the stage is clicked. Without scenes there is no mascot; they are asked
// for again when the section opens again.
func (v *mascotView) Update(gtx layout.Context) {
	if len(v.scenes) > 0 && v.stage.Clicked(gtx) {
		v.index, v.started = (v.index+1)%len(v.scenes), gtx.Now
	}
	if v.repository.Clicked(gtx) {
		openLater("https://" + mascotRepository)
	}
	select {
	case r := <-v.fetched:
		v.fetching = false
		if r.err != nil {
			log.Printf("mascot: %v", r.err)
			return
		}
		v.scenes, v.index, v.started = r.scenes, 0, time.Time{}
	default:
	}
}

// Layout draws the stage, then under it a card with who helps and the
// link to the scenes' repository. animate plays the scene; without it the
// scene plays only under the pointer.
func (v *mascotView) Layout(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	if len(v.scenes) == 0 {
		return layout.Dimensions{}
	}
	sc := scheme(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.layoutStage(gtx, animate)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return card(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return centeredLabel(gtx, l.T("about.claude"), token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
					}),
					vspace(6),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Spacing: layout.SpaceSides}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return v.repository.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								dims := label(gtx, mascotRepository, token.TypestyleBodyMedium, sc.Primary.Color, 1)
								defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
								pointer.CursorPointer.Add(gtx.Ops)
								return dims
							})
						}))
					}),
				)
			}, defaultCardPadding)
		}),
	)
}

// layoutStage draws the scene playing, its picture's bottom on the
// stage's ground, the stage in the middle of the width.
func (v *mascotView) layoutStage(gtx layout.Context, animate bool) layout.Dimensions {
	unitPx := float32(max(1, gtx.Dp(mascotUnit)))
	size := image.Pt(gtx.Constraints.Max.X, int(math.Ceil(float64(mascotHeight*unitPx))))
	s, at := v.scenes[v.index], v.scenes[v.index].Rest
	if animate || v.stage.Hovered() {
		if v.started.IsZero() {
			v.started = gtx.Now
		}
		at = gtx.Now.Sub(v.started).Seconds()
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 30)})
	} else {
		// The next time the pointer comes, the scene starts over.
		v.started = time.Time{}
	}
	// Still or not, a scene keeps its place on its stage: the walk, whose
	// stage is wider than it, stands at the left, where it starts.
	stageW := s.ViewBox[2] * s.Stage * unitPx
	view := f32.Affine2D{}.
		Offset(f32.Pt(-s.ViewBox[0], -s.ViewBox[1])).
		Scale(f32.Point{}, f32.Pt(unitPx, unitPx)).
		Offset(f32.Pt(float32(math.Round(float64(float32(size.X)-stageW)/2)), float32(size.Y)-s.ViewBox[3]*unitPx))
	gtx.Constraints = layout.Exact(size)
	return v.stage.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		rectanim.Paint(gtx.Ops, s.Frame(at), view)
		return layout.Dimensions{Size: size}
	})
}
