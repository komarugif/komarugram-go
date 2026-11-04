// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
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
)

// uploadingStore tells of its files as on their way, and of what is cancelled.
type uploadingStore struct {
	*mockstore.Store
	status    model.UploadStatus
	cancelled chan model.MessageKey
}

func (s *uploadingStore) UploadStatus(model.MessageKey) (model.UploadStatus, bool) {
	return s.status, true
}

func (s *uploadingStore) CancelUpload(key model.MessageKey) { s.cancelled <- key }

// A click on a file on its way cancels it, whatever it is, and does what a
// click does otherwise once it has gone; a failed one has no ring to click.
func TestClickCancelsUpload(t *testing.T) {
	media := func(kind model.MessageKind) *model.MessageMedia {
		return &model.MessageMedia{ID: "outbox/1/0", MIMEType: map[model.MessageKind]string{
			model.MessagePhoto: "image/png", model.MessageVideo: "video/mp4", model.MessageFile: "text/plain", model.MessageMusic: "audio/mpeg", model.MessageVoice: "audio/ogg",
		}[kind], FileName: "a", Size: 1000, Width: 100, Height: 100}
	}
	for _, kind := range []model.MessageKind{model.MessagePhoto, model.MessageVideo, model.MessageFile, model.MessageMusic, model.MessageVoice} {
		store := &uploadingStore{Store: mockstore.New(time.Now(), 0), status: model.UploadStatus{Done: 400, Total: 1000}, cancelled: make(chan model.MessageKey, 4)}
		p := newChatPage(store, func() {})
		t.Cleanup(p.Close)
		p.images = &imageOps{}
		key := model.MessageKey{ChatID: 2, MessageID: -1073741900}
		m := model.Message{Key: key, Kind: kind, Media: media(kind), Sending: true, Outgoing: true}
		r := &messageRow{}
		var router input.Router
		l := localization.For("en")
		frame := func(m model.Message) {
			gtx := layout.Context{Ops: new(op.Ops), Source: router.Source(), Now: time.Now(), Constraints: layout.Exact(image.Pt(300, 300)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			p.images.BeginFrame()
			p.media.BeginFrame()
			gtx.Constraints.Min = image.Point{}
			p.mediaLayout(gtx, r, m, l, false)
			p.media.EndFrame()
			p.images.EndFrame()
			router.Frame(gtx.Ops)
		}
		click := func(m model.Message) {
			frame(m)
			for _, k := range []pointer.Kind{pointer.Press, pointer.Release} {
				router.Queue(pointer.Event{Kind: k, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(22, 22)})
				frame(m)
			}
		}
		click(m)
		select {
		case got := <-store.cancelled:
			if got != key {
				t.Fatalf("kind %d: cancelled %+v", kind, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("kind %d: the click cancelled nothing", kind)
		}
		failed := m
		failed.SendError = "no"
		if _, up := p.uploading(layout.Context{Ops: new(op.Ops), Values: map[string]any{}}, failed); up {
			t.Fatalf("kind %d: a failed file shows a ring", kind)
		}
	}
}

// The plate tells how far a file went once its size is known, under the ring,
// and stays inside a tile too low for it to lie clear of the ring.
func TestUploadPlate(t *testing.T) {
	if got := uploadPlateText(model.UploadStatus{}); got != "" {
		t.Fatalf("no size known: %q", got)
	}
	if got := uploadPlateText(model.UploadStatus{Done: 1 << 20, Total: 5 << 20}); got != "1.0 MB / 5.0 MB" {
		t.Fatalf("midway: %q", got)
	}
	plate := image.Pt(100, 20)
	if got := uploadPlateAt(image.Pt(300, 300), plate, 48, 4); got != image.Pt(100, 178) {
		t.Fatalf("in a large tile: %v", got)
	}
	if got := uploadPlateAt(image.Pt(90, 90), plate, 48, 4); got != image.Pt(0, 66) {
		t.Fatalf("in a small tile: %v", got)
	}
}

func TestUploadProgressShare(t *testing.T) {
	if got := uploadProgress(model.UploadStatus{}); got >= 0 {
		t.Fatalf("no size known: %v", got)
	}
	if got := uploadProgress(model.UploadStatus{Done: 1, Total: 4}); got != .25 {
		t.Fatalf("a quarter: %v", got)
	}
	if got := uploadProgress(model.UploadStatus{Done: 5, Total: 4}); got != 1 {
		t.Fatalf("over: %v", got)
	}
}
