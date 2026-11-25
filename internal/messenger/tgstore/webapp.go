// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// webPlatform is the name this client gives Mini Apps as its platform. Apps
// tell desktop from mobile by it, and know only Telegram's own names.
const webPlatform = "tdesktop"

// bot returns the bot as an input user, and the chat's peer.
func (s *Store) webViewPeers(ctx context.Context, req model.WebViewRequest) (api *tg.Client, bot tg.InputUserClass, chat tg.InputPeerClass, err error) {
	c := s.history
	c.mu.Lock()
	api = c.api
	if api == nil {
		c.mu.Unlock()
		return nil, nil, nil, errNotConnected
	}
	b := c.peers[req.Bot]
	real, _ := c.threadChat(req.Chat)
	if p := c.peers[real]; p.ID != 0 {
		chat = p.input()
	}
	c.mu.Unlock()
	if b.ID != 0 && b.Kind == "user" {
		return api, &tg.InputUser{UserID: b.ID, AccessHash: b.Hash}, chat, nil
	}
	username := strings.TrimPrefix(strings.TrimSpace(req.BotUsername), "@")
	if username == "" {
		return nil, nil, nil, errors.New("the bot is not known")
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, nil, nil, err
	}
	s.rememberPeers(resolved.Users, resolved.Chats)
	for _, u := range resolved.Users {
		if user, ok := u.(*tg.User); ok {
			return api, &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}, chat, nil
		}
	}
	return nil, nil, nil, errors.New("the bot is not known")
}

// RequestWebView implements model.WebViewStore: a keyboard's SimpleWebView
// button asks messages.requestSimpleWebView, a Main Mini App asks
// messages.requestMainWebView, and the others messages.requestWebView.
func (s *Store) RequestWebView(ctx context.Context, req model.WebViewRequest) (model.WebView, error) {
	api, bot, chat, err := s.webViewPeers(ctx, req)
	if err != nil {
		return model.WebView{}, err
	}
	theme := tg.DataJSON{Data: req.Theme}
	if req.Kind == model.WebViewSimple {
		r := &tg.MessagesRequestSimpleWebViewRequest{Bot: bot, ThemeParams: theme, Platform: webPlatform}
		r.SetURL(req.URL)
		if req.StartParam != "" {
			r.SetStartParam(req.StartParam)
		}
		res, err := api.MessagesRequestSimpleWebView(ctx, r)
		if err != nil {
			return model.WebView{}, err
		}
		return model.WebView{URL: res.URL}, nil
	}
	if chat == nil {
		chat = &tg.InputPeerEmpty{}
	}
	if req.Kind == model.WebViewMain {
		r := &tg.MessagesRequestMainWebViewRequest{Peer: chat, Bot: bot, Platform: webPlatform}
		if req.Theme != "" {
			r.SetThemeParams(theme)
		}
		if req.StartParam != "" {
			r.SetStartParam(req.StartParam)
		}
		res, err := api.MessagesRequestMainWebView(ctx, r)
		if err != nil {
			return model.WebView{}, err
		}
		return model.WebView{URL: res.URL, QueryID: res.QueryID}, nil
	}
	r := &tg.MessagesRequestWebViewRequest{Peer: chat, Bot: bot, ThemeParams: theme, Platform: webPlatform, FromBotMenu: req.Kind == model.WebViewMenu}
	if req.URL != "" {
		r.SetURL(req.URL)
	}
	if req.StartParam != "" {
		r.SetStartParam(req.StartParam)
	}
	res, err := api.MessagesRequestWebView(ctx, r)
	if err != nil {
		return model.WebView{}, err
	}
	return model.WebView{URL: res.URL, QueryID: res.QueryID}, nil
}

// ProlongWebView implements model.WebViewStore.
func (s *Store) ProlongWebView(ctx context.Context, req model.WebViewRequest, queryID int64) error {
	api, bot, chat, err := s.webViewPeers(ctx, req)
	if err != nil {
		return err
	}
	if chat == nil {
		chat = &tg.InputPeerEmpty{}
	}
	_, err = api.MessagesProlongWebView(ctx, &tg.MessagesProlongWebViewRequest{Peer: chat, Bot: bot, QueryID: queryID})
	return err
}

// SendWebViewData implements model.WebViewStore with
// messages.sendWebViewData; the message it makes comes back in the updates.
func (s *Store) SendWebViewData(ctx context.Context, botID int64, buttonText, data string) error {
	api, bot, _, err := s.webViewPeers(ctx, model.WebViewRequest{Bot: botID})
	if err != nil {
		return err
	}
	updates, err := api.MessagesSendWebViewData(ctx, &tg.MessagesSendWebViewDataRequest{Bot: bot, RandomID: newRandomID(), ButtonText: buttonText, Data: data})
	if err != nil {
		return err
	}
	return s.Handle(ctx, updates)
}

// newRandomID is a random_id for a request that sends something.
func newRandomID() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UnixNano()
	}
	if v := int64(binary.LittleEndian.Uint64(b[:])); v != 0 {
		return v
	}
	return 1
}
