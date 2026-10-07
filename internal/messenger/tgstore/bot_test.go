// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func convertOne(t *testing.T, m *tg.Message) model.Message {
	t.Helper()
	s := testStore(t)
	msgs, err := s.ingest(context.Background(), []tg.MessageClass{m}, false, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ingest: %v, %d messages", err, len(msgs))
	}
	return msgs[0]
}

// The buttons under a message keep what pressing them needs.
func TestInlineKeyboardConverted(t *testing.T) {
	callback := tg.KeyboardInlineButton{Text: "Refresh", Type: &tg.InlineButtonTypeCallback{Data: []byte{1, 2, 3}}}
	secret := tg.KeyboardInlineButton{Text: "Pay", Type: &tg.InlineButtonTypeCallback{Data: []byte{9}, RequiresPassword: true}}
	m := convertOne(t, &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 5}, Message: "menu", Date: 10, ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{
		{Buttons: []tg.KeyboardInlineButton{{Text: "Site", Type: &tg.InlineButtonTypeURL{URL: "https://example.org"}}, callback}},
		{Buttons: []tg.KeyboardInlineButton{{Text: "Code", Type: &tg.InlineButtonTypeCopy{CopyText: "123456"}}, secret, {Text: "Buy", Type: &tg.InlineButtonTypeBuy{}}}},
	}}})
	want := [][]model.MessageButton{
		{{Text: "Site", Kind: "url", URL: "https://example.org"}, {Text: "Refresh", Kind: "callback", Data: []byte{1, 2, 3}}},
		{{Text: "Code", Kind: "copy", Copy: "123456"}, {Text: "Pay", Kind: "action"}, {Text: "Buy", Kind: "action"}},
	}
	if len(m.Buttons) != 2 || len(m.Buttons[0]) != 2 || len(m.Buttons[1]) != 3 {
		t.Fatalf("buttons %+v", m.Buttons)
	}
	for y := range want {
		for x, w := range want[y] {
			g := m.Buttons[y][x]
			if g.Text != w.Text || g.Kind != w.Kind || g.URL != w.URL || g.Copy != w.Copy || string(g.Data) != string(w.Data) {
				t.Errorf("button %d,%d is %+v, want %+v", y, x, g, w)
			}
		}
	}
	if m.Keyboard != nil {
		t.Fatalf("an inline keyboard came as a reply keyboard: %+v", m.Keyboard)
	}
}

// A reply keyboard is the chat's, not the message's buttons; another
// message takes it away.
func TestReplyKeyboardConverted(t *testing.T) {
	m := convertOne(t, &tg.Message{ID: 8, PeerID: &tg.PeerUser{UserID: 5}, Message: "pick", Date: 10, ReplyMarkup: &tg.ReplyKeyboardMarkup{
		SingleUse: true, Placeholder: "Choose",
		Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButton{{Text: "Yes", Type: &tg.ButtonTypeDefault{}}, {Text: "Share", Type: &tg.ButtonTypeRequestPhone{}}}}},
	}})
	if len(m.Buttons) != 0 || m.Keyboard == nil || !m.Keyboard.SingleUse || m.Keyboard.Placeholder != "Choose" {
		t.Fatalf("message %+v", m)
	}
	row := m.Keyboard.Rows[0]
	if row[0].Kind != "text" || row[0].Text != "Yes" || row[1].Kind != "action" {
		t.Fatalf("row %+v", row)
	}
	gone := convertOne(t, &tg.Message{ID: 9, PeerID: &tg.PeerUser{UserID: 5}, Message: "bye", Date: 11, ReplyMarkup: &tg.ReplyKeyboardHide{}})
	if !gone.KeyboardHide || gone.Keyboard != nil {
		t.Fatalf("message %+v", gone)
	}
}

// Pressing a button asks the bot for the message it is under, with its data,
// and passes on the answer; a bot that stays silent is told as such.
func TestPressButton(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	var asked *tg.MessagesGetBotCallbackAnswerRequest
	var fail error
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesGetBotCallbackAnswerRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		asked = r
		if fail != nil {
			return nil, fail
		}
		answer := &tg.MessagesBotCallbackAnswer{Alert: true, HasURL: true}
		answer.SetMessage("Done")
		answer.SetURL("https://example.org/next")
		return answer, nil
	})
	key := model.MessageKey{ChatID: 5, MessageID: 42}
	got, err := s.PressButton(context.Background(), key, []byte{7})
	if err != nil {
		t.Fatal(err)
	}
	if asked.MsgID != 42 || string(asked.Data) != "\x07" {
		t.Fatalf("asked %+v", asked)
	}
	if user, ok := asked.Peer.(*tg.InputPeerUser); !ok || user.UserID != 5 || user.AccessHash != 55 {
		t.Fatalf("asked of %+v", asked.Peer)
	}
	if got.Text != "Done" || !got.Alert || got.URL != "https://example.org/next" {
		t.Fatalf("answer %+v", got)
	}
	fail = tgerr.New(400, "BOT_RESPONSE_TIMEOUT")
	if _, err := s.PressButton(context.Background(), key, nil); !errors.Is(err, model.ErrBotSilent) {
		t.Fatalf("silent bot: %v", err)
	}
}

// The chat's keyboard is the newest one set; hiding, or a single-use one that
// was answered, takes it away.
func TestActiveKeyboard(t *testing.T) {
	kb := &model.ReplyKeyboard{Rows: [][]model.MessageButton{{{Text: "a", Kind: "text"}}}}
	once := &model.ReplyKeyboard{Rows: kb.Rows, SingleUse: true}
	msg := func(k *model.ReplyKeyboard, hide, out bool) model.Message {
		return model.Message{Keyboard: k, KeyboardHide: hide, Outgoing: out}
	}
	for name, tc := range map[string]struct {
		messages []model.Message
		want     *model.ReplyKeyboard
	}{
		"none":            {[]model.Message{msg(nil, false, false)}, nil},
		"set":             {[]model.Message{msg(kb, false, false), msg(nil, false, true)}, kb},
		"newest wins":     {[]model.Message{msg(once, false, false), msg(kb, false, false)}, kb},
		"hidden":          {[]model.Message{msg(kb, false, false), msg(nil, true, false)}, nil},
		"set after hide":  {[]model.Message{msg(nil, true, false), msg(kb, false, false)}, kb},
		"once unanswered": {[]model.Message{msg(once, false, false), msg(nil, false, false)}, once},
		"once answered":   {[]model.Message{msg(once, false, false), msg(nil, false, true)}, nil},
	} {
		if got, _ := model.ActiveKeyboard(tc.messages); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}

// The commands of a bot come from its full user, on the first ask, and are
// kept.
func TestBotCommands(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	var mu sync.Mutex
	asks := 0
	changed := make(chan struct{}, 4)
	s.changed = func() { changed <- struct{}{} }
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.UsersGetFullUserRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		if user, ok := r.ID.(*tg.InputUser); !ok || user.UserID != 5 || user.AccessHash != 55 {
			return nil, errors.New("asked of another user")
		}
		mu.Lock()
		asks++
		mu.Unlock()
		info := tg.BotInfo{}
		info.SetCommands([]tg.BotCommand{{Command: "start", Description: "Begin"}, {Command: "help", Description: "What it does"}})
		info.SetMenuButton(&tg.BotMenuButton{Text: "Open", URL: "https://app.example/menu"})
		full := &tg.UsersUserFull{}
		full.FullUser.SetBotInfo(info)
		return full, nil
	})
	if got := s.BotInfo(5); got.Commands != nil {
		t.Fatalf("commands %v before they were read", got)
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("the window was not told of the commands")
	}
	got := s.BotInfo(5).Commands
	if len(got) != 2 || got[0] != (model.BotCommand{Command: "start", Description: "Begin"}) {
		t.Fatalf("commands %v", got)
	}
	if menu := s.BotInfo(5).Menu; menu == nil || menu.Text != "Open" || menu.URL != "https://app.example/menu" {
		t.Fatalf("menu button %+v", menu)
	}
	mu.Lock()
	defer mu.Unlock()
	if asks != 1 {
		t.Fatalf("asked %d times", asks)
	}
}

func TestMatchCommands(t *testing.T) {
	cmds := []model.BotCommand{{Command: "start"}, {Command: "Settings"}, {Command: "stop"}}
	names := func(list []model.BotCommand) (out []string) {
		for _, c := range list {
			out = append(out, c.Command)
		}
		return out
	}
	for text, want := range map[string]string{
		"/":       "start Settings stop",
		"/st":     "start stop",
		"/SET":    "Settings",
		"/x":      "",
		"hello":   "",
		"/start ": "",
		"":        "",
	} {
		if got := strings.Join(names(model.MatchCommands(cmds, text)), " "); got != want {
			t.Errorf("%q matched %q, want %q", text, got, want)
		}
	}
}

// The WebView buttons of a keyboard keep their link: a message's buttons
// open a Mini App, a reply keyboard's a simple one.
func TestWebViewKeyboardConverted(t *testing.T) {
	m := convertOne(t, &tg.Message{ID: 30, PeerID: &tg.PeerUser{UserID: 5}, Message: "shop", Date: 10, ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{
		{Buttons: []tg.KeyboardInlineButton{{Text: "Shop", Type: &tg.InlineButtonTypeWebView{URL: "https://shop.example/a"}}}},
	}}})
	if row := m.Buttons[0]; row[0].Kind != "webview" || row[0].URL != "https://shop.example/a" {
		t.Fatalf("row %+v", row)
	}
	m = convertOne(t, &tg.Message{ID: 31, PeerID: &tg.PeerUser{UserID: 5}, Message: "app", Date: 11, ReplyMarkup: &tg.ReplyKeyboardMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButton{{Text: "Simple", Type: &tg.ButtonTypeSimpleWebView{URL: "https://shop.example/b"}}}},
	}}})
	if row := m.Keyboard.Rows[0]; row[0].Kind != "simple_webview" || row[0].URL != "https://shop.example/b" {
		t.Fatalf("row %+v", row)
	}
}

// A Mini App is asked for from the chat, the menu or as a simple one, with
// the bot, the link and the colors, and the answer's link and query come back;
// what an app sends is sent to its bot.
func TestRequestWebView(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.peers[9] = peerRecord{Kind: "chat", ID: 9}
	var inline []*tg.MessagesRequestWebViewRequest
	var simple []*tg.MessagesRequestSimpleWebViewRequest
	var sent []*tg.MessagesSendWebViewDataRequest
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesRequestWebViewRequest:
			inline = append(inline, r)
			return &tg.WebViewResultURL{QueryID: 99, URL: "https://app.example/i#tgWebAppData=x"}, nil
		case *tg.MessagesRequestSimpleWebViewRequest:
			simple = append(simple, r)
			return &tg.WebViewResultURL{URL: "https://app.example/s#tgWebAppData=y"}, nil
		case *tg.MessagesSendWebViewDataRequest:
			sent = append(sent, r)
			return &tg.UpdatesTooLong{}, nil
		}
		return nil, errors.New("unexpected request")
	})
	ctx := context.Background()
	got, err := s.RequestWebView(ctx, model.WebViewRequest{Kind: model.WebViewInline, Chat: 9, Bot: 5, URL: "https://app.example/i", Theme: `{"bg_color":"#fff"}`})
	if err != nil || got.QueryID != 99 || got.URL != "https://app.example/i#tgWebAppData=x" {
		t.Fatalf("inline %+v, %v", got, err)
	}
	r := inline[0]
	if user, ok := r.Bot.(*tg.InputUser); !ok || user.UserID != 5 || user.AccessHash != 55 {
		t.Fatalf("bot %+v", r.Bot)
	}
	if chat, ok := r.Peer.(*tg.InputPeerChat); !ok || chat.ChatID != 9 {
		t.Fatalf("peer %+v", r.Peer)
	}
	if url, _ := r.GetURL(); url != "https://app.example/i" || r.FromBotMenu || r.ThemeParams.Data != `{"bg_color":"#fff"}` || r.Platform != "tdesktop" {
		t.Fatalf("request %+v", r)
	}

	if _, err := s.RequestWebView(ctx, model.WebViewRequest{Kind: model.WebViewMenu, Chat: 5, Bot: 5, URL: "https://app.example/m"}); err != nil {
		t.Fatal(err)
	}
	if !inline[1].FromBotMenu {
		t.Fatal("the menu button did not ask from the bot menu")
	}

	got, err = s.RequestWebView(ctx, model.WebViewRequest{Kind: model.WebViewSimple, Chat: 5, Bot: 5, URL: "https://app.example/s"})
	if err != nil || got.QueryID != 0 || len(simple) != 1 {
		t.Fatalf("simple %+v, %v, %d asked", got, err, len(simple))
	}
	if url, _ := simple[0].GetURL(); url != "https://app.example/s" {
		t.Fatalf("simple url %q", url)
	}

	if err := s.SendWebViewData(ctx, 5, "Open", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].ButtonText != "Open" || sent[0].Data != "hello" || sent[0].RandomID == 0 {
		t.Fatalf("sent %+v", sent)
	}
	if _, err := s.RequestWebView(ctx, model.WebViewRequest{Kind: model.WebViewInline, Chat: 9, Bot: 404}); err == nil {
		t.Fatal("an unknown bot was asked")
	}
}
