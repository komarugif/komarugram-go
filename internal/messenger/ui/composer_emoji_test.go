// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

type fieldEmojiSource struct {
	*mockstore.Store
	png []byte
}

func (s fieldEmojiSource) CustomEmoji(context.Context, int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "field-emoji", MIMEType: "image/png", Width: 24, Height: 24}}, nil
}
func (s fieldEmojiSource) Media(context.Context, model.Message) ([]byte, error) { return s.png, nil }

func TestComposerDrawsCustomEmojiPixels(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 24, 24))
	for y := range 24 {
		for x := range 24 {
			picture.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	media := chatmedia.New(fieldEmojiSource{mockstore.New(time.Now(), 0), data.Bytes()}, func() {})
	defer media.Close()
	images := &imageOps{}
	editor := new(widget.Editor)
	editor.SetText("😀")
	entities := []model.Entity{{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 77}}
	win, err := headless.NewWindow(200, 50)
	if err != nil {
		t.Skip("no GPU:", err)
	}
	defer win.Release()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ops := new(op.Ops)
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Constraints{Max: image.Pt(200, 50)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		images.BeginFrame()
		media.BeginFrame()
		formattedEditor(gtx, editor, "", entities, fieldEmojiMedia{media, images, false})
		media.EndFrame()
		images.EndFrame()
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rect(0, 0, 200, 50))
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		green := 0
		for y := range 50 {
			for x := range 200 {
				c := im.RGBAAt(x, y)
				if c.G > 200 && c.R < 30 && c.B < 30 {
					green++
				}
			}
		}
		if green > 30 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("custom emoji bitmap never appeared in the editor")
}

// animatedEmojiSource gives a custom emoji that moves.
type animatedEmojiSource struct {
	*mockstore.Store
	tgs []byte
}

func (s animatedEmojiSource) CustomEmoji(context.Context, int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "preview-emoji", MIMEType: "application/x-tgsticker", Width: 24, Height: 24}}, nil
}
func (s animatedEmojiSource) Media(context.Context, model.Message) ([]byte, error) { return s.tgs, nil }

// The custom emoji of a last message go on playing, however long it has been
// shown: the clock of the frames here is a minute after the line was first drawn.
func TestPreviewEmojiKeepsPlaying(t *testing.T) {
	tgs, err := os.ReadFile("../../../assets/stickers/sample.tgs")
	if err != nil {
		t.Fatal(err)
	}
	media := chatmedia.New(animatedEmojiSource{mockstore.New(time.Now(), 0), tgs}, func() {})
	defer media.Close()
	images := &imageOps{}
	win, err := headless.NewWindow(200, 40)
	if err != nil {
		t.Skip("no GPU:", err)
	}
	defer win.Release()
	var line previewLine
	emoji := []model.Entity{{Kind: model.EntityEmoji, Offset: 0, Length: 2, DocumentID: 77}}
	line.update(model.Chat{ID: 1, Last: model.Preview{Text: "😀", Entities: emoji}}, "x", localization.For("en"), 24)
	pm := &previewMedia{media: media, images: images}
	frame := func(now time.Time) *image.RGBA {
		ops := new(op.Ops)
		gtx := layout.Context{Ops: ops, Now: now, Constraints: layout.Constraints{Max: image.Pt(200, 40)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		sc := scheme(gtx)
		images.BeginFrame()
		media.BeginFrame()
		line.layout(gtx, pm, sc.Surface.OnColor, sc.Primary.Color, sc.Error.Color, true)
		media.EndFrame()
		images.EndFrame()
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rect(0, 0, 200, 40))
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		return im
	}
	first := time.Now()
	frame(first)
	seen := map[string]bool{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(seen) < 3 {
		seen[string(frame(time.Now().Add(time.Minute)).Pix)] = true
		time.Sleep(30 * time.Millisecond)
	}
	if len(seen) < 3 {
		t.Fatalf("a minute on, %d pictures", len(seen))
	}
}
