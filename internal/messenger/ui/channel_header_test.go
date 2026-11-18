// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"
	"time"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

type channelHeaderStore struct {
	*mockstore.Store
	details model.ChatDetails
}

func (s *channelHeaderStore) ChatDetails(int64) model.ChatDetails { return s.details }

func TestChannelHeaderUsesRefreshedCount(t *testing.T) {
	store := &channelHeaderStore{Store: mockstore.New(time.Now(), 0)}
	page := newChatPage(store, func() {})
	defer page.Close()
	// The link's result stays outside the dialog list and has no count.
	channel := model.Chat{ID: -1000000000099, Kind: model.KindChannel, Title: "Channel from link"}
	l := localization.For("en")
	for _, state := range []struct {
		details model.ChatDetails
		want    string
	}{
		{model.ChatDetails{}, "channel"},
		{model.ChatDetails{Members: 124, MembersKnown: true}, "124 subscribers"},
		{model.ChatDetails{MembersKnown: true}, "0 subscribers"},
	} {
		store.details = state.details
		if got, _ := page.chatStatusLine(layout.Context{}, channel, false, l); got != state.want {
			t.Errorf("channel header with %+v = %q, want %q", state.details, got, state.want)
		}
	}
}

// TestRenderChannelHeader draws the same channel before and after the
// full peer response brings its subscriber count.
func TestRenderChannelHeader(t *testing.T) {
	path := os.Getenv("CHANNEL_HEADER_PNG")
	if path == "" {
		t.Skip("set CHANNEL_HEADER_PNG to render")
	}
	skipFrames(t)
	store := &channelHeaderStore{Store: mockstore.New(time.Now(), 0)}
	page := newChatPage(store, func() {})
	defer page.Close()
	channel := model.Chat{ID: -1000000000099, Kind: model.KindChannel, Title: "Новости Go"}
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 142), path, func(gtx layout.Context) {
		side := image.Pt(gtx.Constraints.Max.X, gtx.Dp(chatHeaderSize)+gtx.Dp(1))
		for i, details := range []model.ChatDetails{{}, {Members: 12345, MembersKnown: true}} {
			store.details = details
			row := gtx
			row.Constraints = layout.Exact(side)
			offset(row, image.Pt(0, i*side.Y), func(gtx layout.Context) layout.Dimensions {
				return layoutChatPage(gtx, channel, l, avatar, nil, nil, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}, page)
			})
		}
	})
}
