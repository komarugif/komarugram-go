// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
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

// UploadStatus tells of every file on its way, at a share of its own but for
// the one with no size known yet.
func (s outgoingRenderSource) UploadStatus(key model.MessageKey) (model.UploadStatus, bool) {
	share := int64(-key.MessageID) % 5
	if share == 0 {
		return model.UploadStatus{}, true
	}
	return model.UploadStatus{Done: share * 200000, Total: 1000000}, true
}

func (s outgoingRenderSource) CancelUpload(model.MessageKey) {}

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
	for i, kind := range []model.MessageKind{model.MessagePhoto, model.MessageVideo, model.MessageFile, model.MessageSticker, model.MessageVoice} {
		media := &model.MessageMedia{ID: "pending", FileName: "report.pdf", Size: 250000, MIMEType: "application/pdf"}
		if kind == model.MessagePhoto || kind == model.MessageSticker {
			media.MIMEType = "image/png"
			media.Preview = encoded.Bytes()
			media.Width = 240
			media.Height = 100
		}
		if kind == model.MessageVideo {
			media.MIMEType, media.Width, media.Height, media.Duration = "video/mp4", 480, 200, 95*time.Second
			media.Thumbnail = &model.MessageMedia{ID: "pending/thumb", MIMEType: "image/png", Width: 240, Height: 100}
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
	// An album on its way, each file with its own ring, and music.
	for i, id := range []int{1073741950, 1073741951, 1073741952} {
		media := &model.MessageMedia{ID: fmt.Sprintf("pending/%d", i), MIMEType: "image/png", Preview: encoded.Bytes(), Width: 240, Height: 100}
		messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: model.MessageID(-id)}, Date: time.Now(), Kind: model.MessagePhoto, Media: media, Outgoing: true, SenderID: 1, Sending: true, GroupedID: 9, ContentRevision: 1})
	}
	messages = append(messages, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: -1073741963}, Date: time.Now(), Kind: model.MessageMusic, Media: &model.MessageMedia{ID: "pending/music", MIMEType: "audio/mpeg", Title: "Night Tram", Performer: "Band", Duration: 95 * time.Second, Size: 900000}, Outgoing: true, SenderID: 1, Sending: true, ContentRevision: 1})
	h.store.benchmarkHistory.h = model.History{Revision: 2, Messages: model.GroupAlbums(messages)}
	h.page.list.Position = layout.Position{}
	renderFrames(t, image.Pt(700, 800), path, func(gtx layout.Context) {
		h.page.images.BeginFrame()
		h.page.media.BeginFrame()
		h.page.Layout(gtx, model.Chat{ID: 1, Kind: model.KindUser}, localization.For("en"), false)
		h.page.media.EndFrame()
		h.page.images.EndFrame()
	})
}
