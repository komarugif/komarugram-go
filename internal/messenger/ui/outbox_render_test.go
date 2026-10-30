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

	"gioui.org/layout"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

type outgoingRenderSource struct {
	*menuStore
	picture []byte
}

func (s outgoingRenderSource) Media(context.Context, model.Message) ([]byte, error) {
	return s.picture, nil
}

func TestRenderOutgoingDelivery(t *testing.T) {
	path := os.Getenv("OUTBOX_PNG")
	if path == "" {
		t.Skip("set OUTBOX_PNG to render")
	}
	picture := image.NewNRGBA(image.Rect(0, 0, 240, 100))
	for y := range 100 {
		for x := range 240 {
			picture.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: 120, B: uint8(100 + y), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore { return outgoingRenderSource{s, encoded.Bytes()} })
	var messages []model.Message
	for i, kind := range []model.MessageKind{model.MessagePhoto, model.MessageFile, model.MessageSticker, model.MessageVoice} {
		media := &model.MessageMedia{ID: "pending", FileName: "report.pdf", Size: 250000, MIMEType: "application/pdf"}
		if kind == model.MessagePhoto || kind == model.MessageSticker {
			media.MIMEType = "image/png"
			media.Preview = encoded.Bytes()
			media.Width = 240
			media.Height = 100
		}
		if kind == model.MessageVoice {
			media.MIMEType = "audio/ogg"
			media.Duration = 3 * time.Second
			media.Waveform = []byte{0x9f, 0x74, 0x18}
		}
		m := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(-1073741900 - i)}, Date: time.Now(), Kind: kind, Media: media, Outgoing: true, SenderID: 1, Sending: true, ContentRevision: 1}
		if kind == model.MessageFile {
			m.SendError = "connection closed"
		}
		messages = append(messages, m)
	}
	h.store.benchmarkHistory.h = model.History{Revision: 2, Messages: messages}
	h.page.list.Position = layout.Position{}
	renderFrames(t, image.Pt(700, 800), path, func(gtx layout.Context) {
		h.page.images.BeginFrame()
		h.page.media.BeginFrame()
		h.page.Layout(gtx, model.Chat{ID: 1, Kind: model.KindUser}, localization.For("en"), false)
		h.page.media.EndFrame()
		h.page.images.EndFrame()
	})
}
