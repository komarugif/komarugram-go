// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"

	"gio-mw/defaults/schemes"
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
)

// effectsShots draws frames of a history harness headless and saves them,
// for looking at an animation frame by frame.
type effectsShots struct {
	t    *testing.T
	h    *menuHarness
	size image.Point
	dir  string
}

func newEffectsShots(t *testing.T, h *menuHarness, dir string) *effectsShots {
	t.Helper()
	width := h.width
	if width == 0 {
		width = 500
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &effectsShots{t: t, h: h, size: image.Pt(width, 700), dir: dir}
}

// take is the frame the harness drew last, as a picture. Each is drawn in
// a context of its own, as the page's pictures off the frame are: one kept
// between them is ended by theirs.
func (s *effectsShots) take() *image.RGBA {
	s.t.Helper()
	img, err := renderSnapshot(s.h.ops, s.size)
	if err != nil {
		s.t.Skip("no GPU:", err)
	}
	return img
}

func (s *effectsShots) save(name string, img image.Image) {
	s.t.Helper()
	f, err := os.Create(filepath.Join(s.dir, name+".png"))
	if err != nil {
		s.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		s.t.Fatal(err)
	}
}

// strip puts the part crop of pictures side by side, in the order they
// were taken, for seeing an animation at a glance.
func strip(frames []*image.RGBA, crop image.Rectangle) *image.RGBA {
	const gap = 4
	out := image.NewRGBA(image.Rect(0, 0, len(frames)*(crop.Dx()+gap)-gap, crop.Dy()))
	for i, f := range frames {
		draw.Draw(out, crop.Sub(crop.Min).Add(image.Pt(i*(crop.Dx()+gap), 0)), f, crop.Min, draw.Src)
	}
	return out
}

// spoilerOf is the entity that puts part of text under a spoiler.
func spoilerOf(text, part string) model.Entity {
	at := strings.Index(text, part)
	return model.Entity{Kind: model.EntitySpoiler, Offset: len(utf16.Encode([]rune(text[:at]))), Length: len(utf16.Encode([]rune(part)))}
}

// rowBox is where message id's bubble is in the page, as drawn last.
func (h *menuHarness) rowBox(id model.MessageID) image.Rectangle {
	h.t.Helper()
	p := h.page
	for i, m := range p.messages {
		if m.Key.MessageID == id {
			r := p.rows[id]
			if r == nil {
				break
			}
			top := int(p.heights.Prefix(i)-p.heights.Prefix(p.list.Position.First)) - p.list.Position.Offset
			return image.Rectangle{Max: r.bodySize}.Add(image.Pt(r.bodyLeft, top+r.bodyTop))
		}
	}
	h.t.Fatalf("message %d not shown", id)
	return image.Rectangle{}
}

// TestRenderSpoilerDust saves frames of the dust over text spoilers, at
// rest and while a click's wave uncovers one, in both themes and at two
// scales, for looking at them:
//
//	EFFECTS_PNG_DIR=/tmp/effects go test ./internal/messenger/ui -run RenderSpoilerDust
func TestRenderSpoilerDust(t *testing.T) {
	dir := os.Getenv("EFFECTS_PNG_DIR")
	if dir == "" {
		t.Skip("set EFFECTS_PNG_DIR to a directory")
	}
	for _, look := range []struct {
		name  string
		scale float32
		dark  bool
	}{{"light", 1, false}, {"dark", 1, true}, {"light-2x", 2, false}} {
		h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
			long := "Убийца — дворецкий, а наследство получит племянница, которая весь фильм притворялась горничной. Не говорите никому!"
			messages[17].Text = long
			messages[17].Entities = []model.Entity{spoilerOf(long, long)}
			short := "Ответ: это был дворецкий"
			messages[18].Text = short
			messages[18].Entities = []model.Entity{spoilerOf(short, "это был дворецкий")}
			messages[18].Outgoing = true
			messages[19].Text = "Обычное сообщение"
		})
		h.fx, h.scale, h.dark, h.animate = new(effects), look.scale, look.dark, true
		if look.scale > 1 {
			h.width = 900
		}
		h.page.list.Position.BeforeEnd = false
		h.frames(3)
		s := newEffectsShots(t, h, dir)
		crop := h.rowBox(18).Union(h.rowBox(19)).Inset(-int(8 * look.scale))
		// At rest, a tenth of a second apart: the dust drifts.
		var frames []*image.RGBA
		for range 6 {
			frames = append(frames, s.take())
			h.frames(6)
		}
		s.save("spoiler-dust-"+look.name, frames[0])
		s.save("spoiler-dust-"+look.name+"-drift", strip(frames, crop))
		// A click on the long spoiler: the wave uncovers it.
		at := h.rowBox(18).Min.Add(image.Pt(int(60*look.scale), int(30*look.scale)))
		h.press(pointer.ButtonPrimary, f32.Pt(float32(at.X), float32(at.Y)))
		frames = frames[:0]
		for range 6 {
			frames = append(frames, s.take())
			h.frames(12)
		}
		s.save("spoiler-dust-"+look.name+"-reveal", strip(frames, crop))
	}
}

// The dust moves on the frame's clock, in steps of its own, and asks for
// the frame of its next step; with animations off there is none.
func TestSpoilerDustMoves(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[19].Text = "секрет"
		messages[19].Entities = []model.Entity{spoilerOf("секрет", "секрет")}
	})
	h.fx = new(effects)
	h.page.list.Position.BeforeEnd = false
	h.frames(2)
	d := &h.fx.text
	if d.alpha == nil {
		t.Fatal("a spoiler in view drew no dust")
	}
	was := d.version
	h.now = h.now.Add(time.Second)
	h.frame()
	if d.version == was {
		t.Fatal("the dust stood still for a second")
	}
	h.still = true
	h.fx = new(effects)
	h.frames(2)
	if h.fx.text.alpha != nil {
		t.Fatal("dust drawn with animations off")
	}
}

// leaveHarness is a history whose messages are old enough to leave with an
// effect when erased, in a window with animations on.
func leaveHarness(t *testing.T, edit func([]model.Message)) *menuHarness {
	t.Helper()
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		for i := range messages {
			messages[i].Date = messages[i].Date.Add(-time.Hour)
		}
		messages[15].Text = "Сборка прошла, можно выкатывать. Журнал приложу отдельно, там всё зелёное, кроме одного теста на Windows."
		messages[17].Text = "Выкатываю"
		messages[17].Outgoing = true
		if edit != nil {
			edit(messages)
		}
	})
	h.animate, h.fx = true, new(effects)
	h.page.list.Position.BeforeEnd = false
	h.frames(3)
	return h
}

// awaitDust draws frames, the clock standing still, until the picture of
// the messages that leave came and they turned to dust.
func (h *menuHarness) awaitDust(t *testing.T) *leaving {
	t.Helper()
	waitFor(t, "the picture of the messages that leave", func() {
		h.frame()
		h.now = h.now.Add(-16 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}, func() bool {
		return len(h.page.leavings) == 0 || h.page.leavings[0].dust != nil || !h.page.leavings[0].dissolves
	})
	if len(h.page.leavings) == 0 || h.page.leavings[0].dust == nil {
		t.Skip("no GPU to draw the picture of the messages off the frame")
	}
	return h.page.leavings[0]
}

// TestRenderMessageLeaves saves frames of deleted messages leaving the
// history: crumbling into dust, and fading, the rows around them moving
// into their places, for looking at them:
//
//	EFFECTS_PNG_DIR=/tmp/effects go test ./internal/messenger/ui -run RenderMessageLeaves
func TestRenderMessageLeaves(t *testing.T) {
	dir := os.Getenv("EFFECTS_PNG_DIR")
	if dir == "" {
		t.Skip("set EFFECTS_PNG_DIR to a directory")
	}
	for _, look := range []struct {
		name  string
		scale float32
		dark  bool
		fade  bool
		ids   []model.MessageID
	}{
		{"dissolve", 1, false, false, []model.MessageID{16}},
		{"dissolve-dark-2x", 2, true, false, []model.MessageID{16}},
		{"dissolve-many", 1, false, false, []model.MessageID{14, 16, 18}},
		{"fade", 1, false, true, []model.MessageID{16}},
	} {
		h := leaveHarness(t, nil)
		h.scale, h.dark = look.scale, look.dark
		if look.scale > 1 {
			h.width = 900
		}
		if look.fade {
			h.page.deleteEffect = func() preferences.DeleteEffect { return preferences.DeleteFade }
		}
		h.frames(3)
		s := newEffectsShots(t, h, dir)
		crop := h.rowBox(13).Union(h.rowBox(19))
		crop.Min.X, crop.Max.X = 0, s.size.X
		crop = crop.Inset(-int(40 * look.scale)).Intersect(image.Rectangle{Max: s.size})
		frames := []*image.RGBA{s.take()}
		h.store.erase(look.ids...)
		h.frame()
		every, n := 6, 17
		if look.fade {
			every, n = 3, 7
		} else {
			h.awaitDust(t)
		}
		for range n {
			frames = append(frames, s.take())
			h.frames(every)
		}
		frames = append(frames, s.take())
		if len(h.page.leavings) != 0 {
			t.Errorf("%s: the messages are still leaving after %d frames", look.name, n*every)
		}
		s.save("leave-"+look.name, strip(frames, crop))
		s.save("leave-"+look.name+"-middle", frames[len(frames)/3])
	}
}

// A message erased while in view leaves it with the effect the settings
// choose, the rows around it moving into its place from where they were,
// and is forgotten once gone; one that only left what is loaded, or is
// erased with animations off, goes at once.
func TestErasedMessageLeaves(t *testing.T) {
	h := leaveHarness(t, nil)
	p := h.page
	// What is loaded moved: not a deletion.
	h.store.unload(1)
	h.frame()
	if len(p.leavings) != 0 || p.moves != nil {
		t.Fatal("a message that left what is loaded left with an effect")
	}
	// Fading, at the history's end: the rows above stand where they were,
	// then move down; those below stay.
	p.deleteEffect = func() preferences.DeleteEffect { return preferences.DeleteFade }
	above, below := h.rowBox(15), h.rowBox(17)
	h.store.erase(16)
	h.frame()
	if len(p.leavings) != 1 || p.leavings[0].dissolves {
		t.Fatalf("leaving %d, want one that fades", len(p.leavings))
	}
	if shift := p.moves.shifts[15]; shift >= 0 || h.rowBox(15).Add(image.Pt(0, shift)) != above {
		t.Fatalf("the row above starts %d px off its place %v, was at %v", shift, h.rowBox(15), above)
	}
	if shift := p.moves.shifts[17]; shift != 0 || h.rowBox(17) != below {
		t.Fatalf("the row below starts %d px off its place %v, was at %v", shift, h.rowBox(17), below)
	}
	h.frames(int(2*leaveMove/(16*time.Millisecond)) + 2)
	if len(p.leavings) != 0 || p.moves != nil {
		t.Fatal("the message is still leaving after its time")
	}
	if p.rows[16] != nil {
		t.Fatal("the row of the message that left is kept")
	}
	// Scrolled up, the rows below move up.
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 0, 0, true
	h.frames(2)
	below = h.rowBox(6)
	h.store.erase(5)
	h.frame()
	if shift := p.moves.shifts[6]; shift <= 0 || h.rowBox(6).Add(image.Pt(0, shift)) != below {
		t.Fatalf("the row below starts %d px off its place %v, was at %v", shift, h.rowBox(6), below)
	}
	// It follows the history as it scrolls.
	p.list.Position.Offset += 20
	h.frame()
	if got := p.leavings[0].scrolled; got != -20 {
		t.Fatalf("the message that leaves moved %d px with a history scrolled by 20", got)
	}
	h.frames(int(2*leaveMove/(16*time.Millisecond)) + 2)
	// With animations off it goes at once.
	h.still = true
	h.store.erase(15)
	h.frame()
	if len(p.leavings) != 0 || p.moves != nil {
		t.Fatal("a message left with an effect while animations are off")
	}
}

// A message that dissolves stands as it was until its picture comes, then
// crumbles, and is gone when its last piece is.
func TestErasedMessageDissolves(t *testing.T) {
	h := leaveHarness(t, nil)
	p := h.page
	h.store.erase(16, 18)
	h.frame()
	if len(p.leavings) != 1 || !p.leavings[0].dissolves || len(p.leavings[0].rows) != 2 {
		t.Fatalf("leaving %+v, want two messages that dissolve together", p.leavings)
	}
	lv := h.awaitDust(t)
	if n := len(lv.dust.parts); n == 0 || n > dissolveParticles {
		t.Fatalf("%d pieces", n)
	}
	// As long as its last piece lives, on the frames' clock.
	last := time.Duration(float64(lv.dust.end) / dissolvePace * float64(time.Second))
	h.frames(int(last/(16*time.Millisecond)) - 3)
	if len(p.leavings) != 1 {
		t.Fatal("the dust is gone before its last piece")
	}
	h.frames(8)
	if len(p.leavings) != 0 {
		t.Fatal("the dust is still there after its last piece")
	}
}

// A picture that does not come leaves the messages to fade; a layout of
// them that panics drops them, and the history goes on.
func TestLeavingRecovers(t *testing.T) {
	h := leaveHarness(t, nil)
	p := h.page
	h.store.erase(16)
	h.frame()
	lv := p.leavings[0]
	// The picture asked for is never read.
	lv.picture = make(chan dissolvePicture)
	h.now = h.now.Add(leavePictureWait)
	h.frame()
	if lv.dissolves || len(p.leavings) != 1 {
		t.Fatal("a message whose picture did not come does not fade")
	}
	// Buttons the row kept none for: its layout fails.
	lv.rows[0].msg.Buttons = [][]model.MessageButton{{{Text: "Fault"}}}
	var failure any
	func() {
		defer func() { failure = recover() }()
		h.frame()
	}()
	if failure == nil {
		t.Fatal("fault injection did not panic")
	}
	if !p.recoverLeaving() || len(p.leavings) != 0 {
		t.Fatal("the messages that failed are not dropped")
	}
	h.frames(3)
	if p.recoverLeaving() {
		t.Fatal("a frame without them is taken for a failed one")
	}
}

func TestMediaDustKeepsNativeResolution(t *testing.T) {
	skipFrames(t)
	tiles := []image.Rectangle{
		image.Rect(0, 0, 160, 120),
		image.Rect(160, 0, 321, 121),
		image.Rect(321, 0, 968, 423),
	}
	size := image.Pt(968, 423)
	gtx := layout.Context{
		Ops: new(op.Ops), Now: time.Unix(1000, 0), Constraints: layout.Exact(size),
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{},
	}
	withEffects(gtx, new(effects), true)
	for _, tile := range tiles {
		offset(gtx, tile.Min, func(gtx layout.Context) layout.Dimensions {
			paintMediaDust(gtx, tile.Size(), 0)
			return layout.Dimensions{Size: tile.Size()}
		})
	}
	img, err := renderSnapshot(gtx.Ops, size)
	if err != nil {
		t.Skip("no GPU:", err)
	}
	center := func(r image.Rectangle) image.Point { return r.Min.Add(r.Size().Div(2)) }
	foundDust := false
	for y := -24; y < 24; y++ {
		for x := -24; x < 24; x++ {
			want := img.RGBAAt(center(tiles[0]).X+x, center(tiles[0]).Y+y)
			foundDust = foundDust || want.A != 0
			for _, tile := range tiles[1:] {
				at := center(tile).Add(image.Pt(x, y))
				if got := img.RGBAAt(at.X, at.Y); got != want {
					t.Fatalf("dust at (%d,%d) changed with cover size %v: got %v, want %v", x, y, tile.Size(), got, want)
				}
			}
		}
	}
	if !foundDust {
		t.Fatal("the cover drew no dust")
	}
}

// TestRenderMediaDust saves frames of the dust over media under a
// spoiler, a fifth of a second apart, over a tile and over one larger
// than the dust's square, for looking at them:
//
//	EFFECTS_PNG_DIR=/tmp/effects go test ./internal/messenger/ui -run RenderMediaDust
func TestRenderMediaDust(t *testing.T) {
	dir := os.Getenv("EFFECTS_PNG_DIR")
	if dir == "" {
		t.Skip("set EFFECTS_PNG_DIR to a directory")
	}
	skipFrames(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	size := image.Pt(660, 420)
	fx := new(effects)
	now := time.Unix(1000, 0)
	var frames []*image.RGBA
	for range 5 {
		var ops *op.Ops
		// A frame every 16 ms: the dust moves in its own steps.
		for range 12 {
			ops = new(op.Ops)
			gtx := layout.Context{Ops: ops, Now: now, Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineDark()))
			withEffects(gtx, fx, false)
			fillRect(gtx, scheme(gtx).Surface.Color, size)
			for i, tile := range []image.Rectangle{image.Rect(10, 10, 210, 160), image.Rect(220, 10, 650, 410), image.Rect(10, 170, 210, 410)} {
				offset(gtx, tile.Min, func(gtx layout.Context) layout.Dimensions {
					defer clip.UniformRRect(image.Rectangle{Max: tile.Size()}, 12).Push(gtx.Ops).Pop()
					fillRect(gtx, token.NewMatColorFromHexRGB(0x4a5a7a), tile.Size())
					paintMediaSpoiler(gtx, tile.Size(), i)
					return layout.Dimensions{Size: tile.Size()}
				})
			}
			now = now.Add(16 * time.Millisecond)
		}
		img, err := renderSnapshot(ops, size)
		if err != nil {
			t.Skip("no GPU:", err)
		}
		frames = append(frames, img)
	}
	s := &effectsShots{t: t, dir: dir}
	s.save("media-dust", frames[0])
	s.save("media-dust-drift", strip(frames, image.Rect(10, 10, 210, 160)))
	t.Run("history", func(t *testing.T) { checkHistoryMediaSpoilers(t, dir, true) })
}
