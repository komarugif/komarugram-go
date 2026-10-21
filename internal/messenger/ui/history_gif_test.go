// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"komarugram/internal/messenger/model"
)

// gifStore is a menu store whose media are a GIF file.
type gifStore struct {
	*menuStore
	data []byte
}

func (s gifStore) Media(context.Context, model.Message) ([]byte, error) { return s.data, nil }

// gifFile is a GIF of a few frames, of 240 by 160.
func gifFile(t *testing.T) []byte {
	t.Helper()
	g := &gif.GIF{}
	palette := color.Palette{color.RGBA{35, 48, 75, 255}, color.RGBA{130, 190, 250, 255}}
	for n := range 4 {
		im := image.NewPaletted(image.Rect(0, 0, 240, 160), palette)
		for x := n * 40; x < n*40+40; x++ {
			for y := 40; y < 120; y++ {
				im.SetColorIndex(x, y, 1)
			}
		}
		g.Image, g.Delay = append(g.Image, im), append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A click on a GIF that plays in the chat pauses it, another plays it on,
// and one of the middle button opens it in the viewer, as the primary one
// did before.
func TestGIFClickPausesMiddleOpens(t *testing.T) {
	data := gifFile(t)
	gifMessage := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 2}, Kind: model.MessageGIF,
		Media: &model.MessageMedia{ID: "gif", MIMEType: "image/gif", Width: 240, Height: 160, Size: int64(len(data)), Duration: 4 * time.Second}}
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[1].Kind, messages[1].Text, messages[1].Media = gifMessage.Kind, "", gifMessage.Media
	}, func(s *menuStore) model.ConversationStore { return gifStore{s, data} })
	var opened []model.Message
	h.page.openAlone = func(m model.Message) { opened = append(opened, m) }
	h.frames(2)
	click := func(button pointer.Buttons, at f32.Point) {
		// Clicks a moment apart, not a double click.
		h.now = h.now.Add(time.Second)
		h.press(button, at)
	}
	// The GIF is loaded once it shows, its file read and its first picture
	// decoded.
	for range 100 {
		h.frame()
		if s := h.page.media.StatusFit(gifMessage, false, image.Pt(240, 160), false); s.Frame != nil && !s.Loading {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	center := h.messageAt(2)
	var at f32.Point
	for y := center.Y - 60; y < center.Y+60 && !h.page.media.GIFPaused(gifMessage); y += 8 {
		for x := float32(30); x < 300 && !h.page.media.GIFPaused(gifMessage); x += 16 {
			at = f32.Pt(x, y)
			click(pointer.ButtonPrimary, at)
		}
	}
	if !h.page.media.GIFPaused(gifMessage) {
		t.Fatal("no click paused the GIF")
	}
	if len(opened) != 0 {
		t.Fatal("a click opened the GIF in the viewer")
	}
	click(pointer.ButtonPrimary, at)
	if h.page.media.GIFPaused(gifMessage) {
		t.Fatal("a second click did not play the GIF on")
	}
	click(pointer.ButtonTertiary, at)
	if len(opened) != 1 || opened[0].Key != gifMessage.Key {
		t.Fatalf("the middle button opened %v", opened)
	}
	if h.page.media.GIFPaused(gifMessage) {
		t.Fatal("the middle button paused the GIF")
	}

	// A press on the bar, along the bottom, of the paused GIF moves it, and
	// leaves it paused. Down from the click, a press above the bar pauses
	// it or plays it on, as one on the GIF does.
	click(pointer.ButtonPrimary, at)
	moved := false
	for y := at.Y; y < at.Y+200 && !moved; y += 3 {
		// Right of where the bar starts, for a time past its start.
		p := f32.Pt(at.X+60, y)
		click(pointer.ButtonPrimary, p)
		moved = h.page.media.GIFPosition(gifMessage) > 0
		if !moved && !h.page.media.GIFPaused(gifMessage) {
			click(pointer.ButtonPrimary, p)
		}
	}
	if !moved {
		t.Fatal("no press on the bar moved the GIF")
	}
	if !h.page.media.GIFPaused(gifMessage) {
		t.Fatal("a press on the bar played the paused GIF")
	}
}

// A timestamp in a reply to a GIF that plays in the chat moves the GIF
// there, as Telegram Desktop does not.
func TestTimestampInReplySeeksGIF(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	g := find("demo/gifv")
	g.Media.Size = 1 << 20
	reply := model.Message{Key: model.MessageKey{ChatID: g.Key.ChatID, MessageID: 9003}, Kind: model.MessageText,
		Text: "from 0:12", ReplyToMessageID: g.Key.MessageID, ContentRevision: 1}
	p.messages = []model.Message{g, reply}
	p.media.ToggleGIF(g)
	clickTimestamp(t, p, reply, "0:12")
	if at := p.media.GIFPosition(g); at != 12*time.Second {
		t.Fatalf("the GIF goes to %v, not 0:12", at)
	}
	if p.media.GIFPaused(g) {
		t.Fatal("the paused GIF stays paused")
	}
}
