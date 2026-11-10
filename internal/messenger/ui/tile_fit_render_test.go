package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
)

// tileStore is a menuStore whose media are one picture.
type tileStore struct {
	*menuStore
	data []byte
}

func (s *tileStore) Media(context.Context, model.Message) ([]byte, error) { return s.data, nil }

// TestRenderTileFit saves photos whose pictures are of about their tiles'
// proportions, and of others, for looking at their sides:
// TILE_FIT_PNG=/tmp/tiles.png.
func TestRenderTileFit(t *testing.T) {
	path := os.Getenv("TILE_FIT_PNG")
	if path == "" {
		t.Skip("set TILE_FIT_PNG to a file")
	}
	pic := image.NewRGBA(image.Rect(0, 0, 90, 86))
	for y := range 86 {
		for x := range 90 {
			pic.Set(x, y, color.RGBA{uint8(60 + x), uint8(80 + y), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, pic); err != nil {
		t.Fatal(err)
	}
	h := newMenuHarnessOn(t, func(_ *menuStore, m []model.Message) {
		for i := range m[:3] {
			m[i].Kind = model.MessagePhoto
			m[i].Text = ""
		}
		m[0].Media = &model.MessageMedia{ID: "photo/square", MIMEType: "image/png", Width: 400, Height: 400}
		m[1].Media = &model.MessageMedia{ID: "photo/wide", MIMEType: "image/png", Width: 1600, Height: 900}
		m[2].Media = &model.MessageMedia{ID: "photo/tall", MIMEType: "image/png", Width: 900, Height: 1600}
	}, func(s *menuStore) model.ConversationStore { return &tileStore{menuStore: s, data: b.Bytes()} })
	p := h.page
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 1500), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindChannel}, l, false)
		if p.restored {
			p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 0, 0, true
		}
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
