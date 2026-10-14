// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
	"gioui.org/unit"
)

// TestRenderEmptySavedMessages saves the chat before its first message.
func TestRenderEmptySavedMessages(t *testing.T) {
	path := os.Getenv("SAVED_EMPTY_PNG")
	if path == "" {
		t.Skip("set SAVED_EMPTY_PNG to a path")
	}
	store := noSavedDialogStore{mockstore.New(time.Now(), 0)}
	chat := model.Chat{ID: store.Me().ID, Kind: model.KindSaved, Title: localization.For("ru").T("nav.saved")}
	p := newChatPage(store, func() {})
	p.images = &imageOps{}
	p.frozen = newFrozenView(store)
	defer p.Close()
	l := localization.For("ru")
	renderFrames(t, image.Pt(950, 650), path, func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		layoutChatPage(gtx, chat, l, func(gtx layout.Context, id int64, kind model.ChatKind, title string, size unit.Dp) layout.Dimensions {
			return avatar(gtx, id, kind, title, size)
		}, nil, nil, func(gtx layout.Context) layout.Dimensions {
			return p.Layout(gtx, chat, l, false)
		}, p)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}
