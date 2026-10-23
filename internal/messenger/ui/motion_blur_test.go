// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
)

// A streak is the average of the picture along it: opaque where the
// picture always was, fading out at both ends, with as much of it as the
// picture had; its colors stay premultiplied.
func TestBlurSprite(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 2))
	for i := 0; i < len(src.Pix); i += 4 {
		copy(src.Pix[i:], []uint8{200, 100, 0, 255})
	}
	out, origin := blurSprite(src, f32.Pt(4, 0))
	if origin != image.Pt(-4, 0) || out.Rect.Size() != image.Pt(12, 2) {
		t.Fatalf("streak of %v from %v", out.Rect.Size(), origin)
	}
	// Five samples, one a pixel: the picture, wider than the streak, was
	// always at x = 6, and only in one of them at the ends.
	if got := out.RGBAAt(6, 0); got != (color.RGBA{200, 100, 0, 255}) {
		t.Fatalf("the middle of the streak is %v", got)
	}
	if got := out.RGBAAt(0, 0).A; got != 51 {
		t.Fatalf("the start of the streak is %d opaque", got)
	}
	if got := out.RGBAAt(11, 1).A; got != 51 {
		t.Fatalf("the end of the streak is %d opaque", got)
	}
	total := 0
	for y := range 2 {
		for x := range 12 {
			c := out.RGBAAt(x, y)
			total += int(c.A)
			if c.R > c.A || c.G > c.A {
				t.Fatalf("%v is not premultiplied", c)
			}
		}
	}
	if total != 16*255 {
		t.Fatalf("the streak has %d of alpha, the picture %d", total, 16*255)
	}
	if _, origin := blurSprite(src, f32.Pt(.4, 0)); origin != (image.Point{}) {
		t.Fatal("a picture standing still is blurred")
	}
}

// The streak is what the picture moved in the shutter's time: none on its
// first frame, and little after it stood still for long.
func TestMotionTrack(t *testing.T) {
	var m motionTrack
	now := time.Now()
	gtx := layout.Context{Now: now}
	if s := m.Streak(gtx, f32.Pt(0, 0)); s != (f32.Point{}) {
		t.Fatalf("a first frame streaks %v", s)
	}
	gtx.Now = now.Add(time.Second / 60)
	if s := m.Streak(gtx, f32.Pt(30, 0)); s.X < 14 || s.X > 16 {
		t.Fatalf("30 px in a frame streak %v", s)
	}
	gtx.Now = gtx.Now.Add(time.Second)
	if s := m.Streak(gtx, f32.Pt(60, 0)); s.X > 1 {
		t.Fatalf("a jump after a second streaks %v", s)
	}
}

// A photo becomes the avatars' shape, its corners cut smooth.
func TestRoundedSprite(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for i := 0; i < len(src.Pix); i += 4 {
		copy(src.Pix[i:], []uint8{255, 255, 255, 255})
	}
	out := roundedSprite(src, 10, 5)
	if out.Rect.Size() != image.Pt(10, 10) {
		t.Fatalf("size %v", out.Rect.Size())
	}
	if out.RGBAAt(5, 5).A != 255 || out.RGBAAt(0, 0).A != 0 {
		t.Fatalf("middle %v, corner %v", out.RGBAAt(5, 5), out.RGBAAt(0, 0))
	}
	if a := out.RGBAAt(0, 5).A; a == 0 || a == 255 {
		t.Fatalf("the edge is %d opaque, not smooth", a)
	}
}

// An avatar becomes a picture for its motion blur: a photo cut to the
// avatars' shape, and one drawn, as initials or Saved Messages' mark, by
// the GPU off the frame, a few frames later.
func TestAvatarSprite(t *testing.T) {
	if w, err := headless.NewWindow(1, 1); err != nil {
		t.Skip("no GPU:", err)
	} else {
		w.Release()
	}
	a, _ := returnApp(t)
	gtx := sharedContext(new(op.Ops), image.Pt(900, 700))
	await := func(id int64, kind model.ChatKind) image.Image {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			if im := a.avatarSprite(gtx, id, kind, "Анна Смирнова", 50); im != nil {
				return im
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("no picture of the avatar of %d", id)
		return nil
	}
	for _, c := range []struct {
		id   int64
		kind model.ChatKind
	}{{1, model.KindSaved}, {2, model.KindUser}} {
		im := await(c.id, c.kind)
		if im.Bounds().Size() != image.Pt(50, 50) {
			t.Fatalf("avatar %d of %v", c.id, im.Bounds())
		}
		_, _, _, middle := im.At(25, 25).RGBA()
		_, _, _, corner := im.At(0, 0).RGBA()
		if middle>>8 != 255 || corner != 0 {
			t.Fatalf("avatar %d: middle %d opaque, corner %d", c.id, middle>>8, corner)
		}
	}
}

// The gifts streak in flight, and not once they are out; the avatar
// streaks as it moves into the header, and not once it stands. A frame
// with a streak asks for the next, in which what stopped is sharp.
func TestCoverStreaks(t *testing.T) {
	t.Chdir("../../..")
	var images imageOps
	p := newChatInfo(mockstore.New(time.Now(), 0), &images, func() {})
	defer p.Destroy()
	p.drawAvatar = avatar
	p.avatarSprite = func(_ layout.Context, _ int64, _ model.ChatKind, _ string, px int) image.Image {
		return image.NewRGBA(image.Rect(0, 0, px, px))
	}
	now := time.Now()
	var router input.Router
	frame := func() (next bool) {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Now = now
		gtx.Source = router.Source()
		images.BeginFrame()
		p.renderer.media.BeginFrame()
		p.Layout(gtx, localization.For("en"), true)
		p.renderer.media.EndFrame()
		images.EndFrame()
		router.Frame(ops)
		now = now.Add(time.Second / 60)
		_, next = router.WakeupTime()
		return next
	}
	p.Open(model.Chat{ID: 2, Kind: model.KindUser})
	for range 500 {
		frame()
		st := p.renderer.media.StatusFullRate(p.profile.Gifts[0], true, image.Pt(24, 24), false)
		if p.modal.Entered() && (st.Frame != nil || st.Preview != nil) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for range 3 {
		frame()
	}
	if !p.motion.giftStreaks {
		t.Fatal("the gifts do not streak in flight")
	}
	for range 60 {
		frame()
	}
	if p.motion.giftStreaks {
		t.Fatal("the gifts streak once they are out")
	}
	// The avatar, as the info scrolls fast, and once it stops.
	var next bool
	for i := range 3 {
		p.list.Position.Offset = i * 20
		next = frame()
	}
	if !p.motion.avatarStreak || !next {
		t.Fatalf("the moving avatar streaks %v, asks for the next frame %v", p.motion.avatarStreak, next)
	}
	frame()
	if p.motion.avatarStreak {
		t.Fatal("the avatar streaks once it stopped")
	}

	// Where nothing else moves, its streak alone asks for the frame that
	// draws it sharp.
	p.Open(model.Chat{ID: 4, Kind: model.KindChannel})
	settled := false
	for range 300 {
		if !frame() {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatal("the channel's info never stops drawing")
	}
	for i := range 3 {
		p.list.Position.Offset = i * 20
		next = frame()
	}
	if !p.motion.avatarStreak || !next {
		t.Fatalf("the moving avatar streaks %v, asks for the next frame %v", p.motion.avatarStreak, next)
	}
	if next = frame(); p.motion.avatarStreak || next {
		t.Fatalf("the stopped avatar streaks %v, asks for another frame %v", p.motion.avatarStreak, next)
	}
}

// The avatar's streak as it moves fastest: 96 dp on a screen of 1.5, a
// streak of 40 px.
func BenchmarkBlurSprite(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 144, 144))
	for b.Loop() {
		blurSprite(src, f32.Pt(-40, 12))
	}
}

// The average by a multiplication is the rounded division, for every sum
// of every count of samples.
func TestBlurAverage(t *testing.T) {
	for n := uint32(2); n <= maxBlurSamples; n++ {
		recip := blurReciprocal(n)
		for v := uint32(0); v <= 255*n; v++ {
			if got, want := uint32(blurAverage(v, n, recip)), (v+n/2)/n; got != want {
				t.Fatalf("%d / %d is %d, not %d", v, n, got, want)
			}
		}
	}
}
