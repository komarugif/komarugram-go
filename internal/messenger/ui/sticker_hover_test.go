// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/video"
)

// stickerHover draws one view of stickers with automatic animations off.
type stickerHover struct {
	p      *chatPage
	router input.Router
	view   string
	msg    model.Message
	row    messageRow
	// A nonzero mask keeps global animations on and disables only autoplay.
	disabledAutoplay wdk.AnimationKind
	// scroll, if set, runs before each frame.
	scroll func()
}

// newStickerHover shows count items of codec in view: chat, pack,
// readonly-pack, picker or emoji-picker.
func newStickerHover(t *testing.T, codec, view string, count int) *stickerHover {
	skipFrames(t)
	p := newChatPage(mockstore.New(time.Now(), 0), func() {})
	t.Cleanup(p.Close)
	p.images = &imageOps{}
	mime := "video/webm"
	if codec == "tgs" {
		mime = "application/x-tgsticker"
	}
	msg := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "demo/" + codec, MIMEType: mime, Width: 512, Height: 512, StickerSet: &model.StickerSetRef{Type: "id", ID: 1}}}
	if codec == "gif" {
		msg.Kind = model.MessageGIF
		msg.Media.MIMEType = "image/gif"
		msg.Media.StickerSet = nil
	}
	var items []model.PickerItem
	for i := range count {
		items = append(items, model.PickerItem{ID: "item" + string(rune('a'+i)), Emoji: "🙂", Media: msg})
	}
	p.stickers.pack = &model.StickerSet{Items: items}
	c := p.composer
	c.tab = model.PickerStickers
	c.page.Packs = []model.PickerPack{{ID: 1, Items: items}}
	c.selectedPack = 1
	if codec == "gif" {
		c.tab = model.PickerGIF
		c.page.Packs = nil
		c.page.Items = items
	}
	if view == "emoji-picker" {
		c.tab = model.PickerEmoji
	}
	if view == "readonly-pack" {
		p.composer = nil
		t.Cleanup(func() { p.composer = c })
	}
	return &stickerHover{p: p, view: view, msg: msg}
}

// frame draws a frame and returns the image of a sticker shown in it.
func (h *stickerHover) frame() image.Image {
	p := h.p
	if h.scroll != nil {
		h.scroll()
	}
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Source: h.router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(220, 260)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	animate := h.disabledAutoplay != 0
	wdk.SetAnimationsEnabled(gtx, animate)
	wdk.SetDisabledAnimations(gtx, h.disabledAutoplay)
	p.animate = animate
	p.images.BeginFrame()
	p.media.BeginFrame()
	switch h.view {
	case "chat":
		p.mediaTile(gtx, &h.row, h.msg, image.Pt(64, 64), false, localization.For("ru"), animate)
	case "pack", "readonly-pack":
		p.stickers.grid(gtx, p)
	default:
		p.composer.pickerLayout(gtx, localization.For("ru"), p, animate, image.Point{})
	}
	p.media.EndFrame()
	p.images.EndFrame()
	h.router.Frame(ops)
	for im, texture := range p.images.entries {
		if texture.seen == p.images.generation {
			return im
		}
	}
	return nil
}

// still waits for the static preview: on a slow machine, as Haiku in a
// virtual machine, behind what the tests before left decoding, it took over
// five seconds.
func (h *stickerHover) still(t *testing.T) image.Image {
	var still image.Image
	until := time.Now().Add(20 * time.Second)
	for time.Now().Before(until) && still == nil {
		still = h.frame()
		time.Sleep(10 * time.Millisecond)
	}
	if still == nil {
		// What the media came to tells why: a decode over the sandbox's
		// time limit fails with an error, not a picture.
		t.Fatalf("no static preview: %v", h.p.media.Status(h.msg, false).Err)
	}
	return still
}

// moves reports whether a frame other than still is drawn within d.
func (h *stickerHover) moves(still image.Image, d time.Duration) bool {
	until := time.Now().Add(d)
	for time.Now().Before(until) {
		if im := h.frame(); im != nil && im != still {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (h *stickerHover) point(pos f32.Point) {
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos})
}

// stickerAt is a point over the first sticker of view.
func stickerAt(view string) f32.Point {
	if view == "picker" || view == "emoji-picker" {
		return f32.Pt(30, 115)
	}
	return f32.Pt(25, 25)
}

func TestStickerHoverSurfaces(t *testing.T) {
	t.Chdir("../../..")
	for _, codec := range []string{"tgs", "webm", "gif"} {
		for _, view := range []string{"chat", "pack", "readonly-pack", "picker", "emoji-picker"} {
			if codec == "gif" && view != "picker" {
				continue
			}
			t.Run(codec+"/"+view, func(t *testing.T) {
				// A GIF file, unlike the stickers, is played by FFmpeg only.
				if codec == "gif" && video.ResolveFFmpeg("") == "" {
					t.Skip("no FFmpeg")
				}
				h := newStickerHover(t, codec, view, 1)
				still := h.still(t)
				h.point(stickerAt(view))
				if !h.moves(still, 3*time.Second) {
					t.Fatal("hover left the sticker static")
				}
				h.point(f32.Pt(500, 500))
				h.frame()
				if codec == "tgs" {
					// The picker can draw another copy in its pack strip.
					// Sample more than one frame so the still copy cannot
					// hide the moving one in the texture map.
					if !h.moves(still, 250*time.Millisecond) {
						t.Fatal("leaving hover cut the Lottie loop short")
					}
					return
				}
				time.Sleep(60 * time.Millisecond)
				if im := h.frame(); im != still {
					t.Fatal("leaving hover did not restore the still frame")
				}
			})
		}
	}
}

func TestMediaHoverWithAutoplayOff(t *testing.T) {
	t.Chdir("../../..")
	for _, media := range []struct {
		codec string
		kind  wdk.AnimationKind
		views []string
	}{
		{"tgs", wdk.AnimationSticker, []string{"chat", "pack", "picker"}},
		{"gif", wdk.AnimationGIF, []string{"chat", "picker"}},
	} {
		for _, view := range media.views {
			t.Run(media.codec+"/"+view, func(t *testing.T) {
				if media.codec == "gif" && video.ResolveFFmpeg("") == "" {
					t.Skip("no FFmpeg")
				}
				h := newStickerHover(t, media.codec, view, 1)
				h.disabledAutoplay = media.kind
				still := h.still(t)
				if h.moves(still, 300*time.Millisecond) {
					t.Fatal("media started without hovering while autoplay was off")
				}
				h.point(stickerAt(view))
				if !h.moves(still, 3*time.Second) {
					t.Fatal("media stayed static on hover while autoplay was off")
				}
				h.point(f32.Pt(500, 500))
				h.frame()
				if media.codec == "tgs" && !h.moves(still, 250*time.Millisecond) {
					t.Fatal("leaving hover cut the Lottie loop short")
				}
				if media.codec == "gif" && h.frame() != still {
					t.Fatal("leaving hover did not restore the still GIF frame")
				}
			})
		}
	}
}

// TestStickerHoverWaitsForRest checks that a video sticker of a set's grid
// plays only once the pointer rests on it and the grid does not scroll, while
// one in a chat plays under a moving pointer.
func TestStickerHoverWaitsForRest(t *testing.T) {
	t.Chdir("../../..")
	t.Run("chat/pointer", func(t *testing.T) {
		h := newStickerHover(t, "tgs", "chat", 1)
		still := h.still(t)
		at := stickerAt("chat")
		for i := range 200 {
			h.point(at.Add(f32.Pt(float32(i%4), 0)))
			if im := h.frame(); im != nil && im != still {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatal("a chat sticker did not play under a moving pointer")
	})
	for _, view := range []string{"pack", "picker"} {
		t.Run(view+"/pointer", func(t *testing.T) {
			h := newStickerHover(t, "webm", view, 1)
			still := h.still(t)
			at := stickerAt(view)
			for i := range 40 {
				h.point(at.Add(f32.Pt(float32(i%4), 0)))
				if im := h.frame(); im != still {
					t.Fatalf("played under a moving pointer after %d moves", i)
				}
				time.Sleep(15 * time.Millisecond)
			}
			if !h.moves(still, 3*time.Second) {
				t.Fatal("did not play under the resting pointer")
			}
		})
		t.Run(view+"/scroll", func(t *testing.T) {
			h := newStickerHover(t, "webm", view, 40)
			list := &h.p.stickers.list.List
			if view == "picker" {
				list = &h.p.composer.list.List
			}
			still := h.still(t)
			h.point(stickerAt(view))
			scrolling := true
			h.scroll = func() {
				if scrolling {
					list.Position.Offset++
				}
			}
			for i := range 40 {
				if im := h.frame(); im != still {
					t.Fatalf("played in a scrolling list after %d frames", i)
				}
				time.Sleep(15 * time.Millisecond)
			}
			scrolling = false
			if !h.moves(still, 3*time.Second) {
				t.Fatal("did not play once the list stopped")
			}
		})
	}
}

// TestStickerHoverCheapPlaysAtOnce checks that a sticker cheap to play plays
// under a moving pointer: a Lottie one, and a video one whose loop is
// decoded already.
func TestStickerHoverCheapPlaysAtOnce(t *testing.T) {
	t.Chdir("../../..")
	playsMoving := func(t *testing.T, h *stickerHover, still image.Image, at f32.Point) {
		t.Helper()
		for i := range 20 {
			h.point(at.Add(f32.Pt(float32(i%4), 0)))
			if im := h.frame(); im != nil && im != still {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatal("did not play under a moving pointer")
	}
	for _, view := range []string{"pack", "picker"} {
		t.Run("tgs/"+view, func(t *testing.T) {
			h := newStickerHover(t, "tgs", view, 1)
			still := h.still(t)
			playsMoving(t, h, still, stickerAt(view))
		})
		t.Run("webm/"+view, func(t *testing.T) {
			h := newStickerHover(t, "webm", view, 1)
			still := h.still(t)
			at := stickerAt(view)
			h.point(at)
			if !h.moves(still, 3*time.Second) {
				t.Fatal("did not play under the resting pointer")
			}
			until := time.Now().Add(5 * time.Second)
			for !h.p.media.CheapToPlay(h.msg, image.Pt(56, 56), false) {
				if time.Now().After(until) {
					t.Fatal("the loop was not decoded")
				}
				h.frame()
				time.Sleep(10 * time.Millisecond)
			}
			h.point(f32.Pt(500, 500))
			h.frame()
			time.Sleep(60 * time.Millisecond)
			if im := h.frame(); im != still {
				t.Fatal("leaving hover did not restore the still frame")
			}
			playsMoving(t, h, still, at)
		})
	}
}

// TestHoverPlayDelays checks the two waits: after the hovered item changes,
// and the longer one after a list scrolls.
func TestHoverPlayDelays(t *testing.T) {
	var h hoverPlay
	var list layout.List
	t0 := time.Now()
	play := func(at time.Duration, key any, cheap bool) bool {
		gtx := layout.Context{Ops: new(op.Ops), Now: t0.Add(at)}
		h.Update(gtx, &list)
		return h.Play(gtx, key, true, cheap)
	}
	if play(0, 1, false) || play(hoverMoveDelay-time.Millisecond, 1, false) {
		t.Fatal("played before the pointer rested")
	}
	if !play(hoverMoveDelay, 1, false) {
		t.Fatal("did not play once the pointer rested")
	}
	scrolled := time.Second
	list.Position.Offset = 10
	if play(scrolled, 1, false) {
		t.Fatal("kept playing in a scrolling list")
	}
	// The scroll brings another item under the pointer a frame later.
	if play(scrolled+10*time.Millisecond, 2, false) || play(scrolled+hoverMoveDelay+10*time.Millisecond, 2, false) || play(scrolled+hoverScrollDelay-time.Millisecond, 2, false) {
		t.Fatal("played before the list rested")
	}
	if !play(scrolled+hoverScrollDelay, 2, false) {
		t.Fatal("did not play once the list rested")
	}
	if !play(scrolled+hoverScrollDelay, 3, true) {
		t.Fatal("an item cheap to play waited")
	}
}
