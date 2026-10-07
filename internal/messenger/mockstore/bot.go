// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"strings"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/miniapp"
)

// DemoNotesBot is the demo's bot whose chat is empty: it has the Start button,
// and answers /start with a keyboard.
const DemoNotesBot = 11

// DemoWeatherBot is the demo's bot with a menu button and buttons that open a
// Mini App.
const DemoWeatherBot = 6

// DemoMiniAppURL is what the demo bot's Mini App buttons name: the store
// answers for it with the bundled demo app.
const DemoMiniAppURL = "https://demo.miniapp.invalid/app"

// isBot reports whether chat is a chat with a bot. The caller holds s.mu.
func (s *Store) isBot(chat int64) bool {
	for _, c := range s.chats {
		if c.ID == chat {
			return c.Kind == model.KindBot
		}
	}
	return false
}

// PressButton implements model.BotStore with made-up bot answers, by the
// button's data: a notice, an alert, a link, a bot that stays silent.
func (s *Store) PressButton(ctx context.Context, key model.MessageKey, data []byte) (model.BotAnswer, error) {
	select {
	case <-ctx.Done():
		return model.BotAnswer{}, ctx.Err()
	case <-time.After(400 * time.Millisecond): // As long as a round trip.
	}
	switch string(data) {
	case "refresh":
		return model.BotAnswer{Text: "Обновлено: сейчас +18°, без осадков"}, nil
	case "alert":
		return model.BotAnswer{Text: "Это предупреждение бота: его закрывают кнопкой.", Alert: true}, nil
	case "link":
		return model.BotAnswer{URL: "https://telegram.org"}, nil
	case "silent":
		return model.BotAnswer{}, model.ErrBotSilent
	}
	return model.BotAnswer{}, nil
}

// demoBotMenu is the message a bot chat ends with: buttons of every kind.
func demoBotMenu(chat int64, id model.MessageID, now time.Time) model.Message {
	return model.Message{
		Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: id}, Date: now, SenderName: "Бот", ContentRevision: 1,
		Text: "Кнопки бота: нажмите, и он ответит.",
		Buttons: [][]model.MessageButton{
			{{Text: "Обновить", Kind: "callback", Data: []byte("refresh")}, {Text: "Предупреждение", Kind: "callback", Data: []byte("alert")}},
			{{Text: "Открыть ссылку", Kind: "callback", Data: []byte("link")}, {Text: "Молчит", Kind: "callback", Data: []byte("silent")}},
			{{Text: "Telegram", Kind: "url", URL: "https://telegram.org"}, {Text: "Копировать код", Kind: "copy", Copy: "123-456"}},
			{{Text: "Mini App", Kind: "webview", URL: DemoMiniAppURL}, {Text: "Простой Mini App", Kind: "simple_webview", URL: DemoMiniAppURL}},
			{{Text: "Оплата", Kind: "action"}},
		},
	}
}

// botReply is what a bot says to text sent in its chat with a message of the
// id given, or none: /start brings a keyboard, "Настройки" takes it away.
func botReply(chat int64, id model.MessageID, text string, now time.Time) (model.Message, bool) {
	m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: id}, Date: now, SenderName: "Бот", ContentRevision: 1}
	switch text {
	case "/start":
		m.Text = "Привет! Выберите действие на клавиатуре под полем ввода."
		m.Keyboard = &model.ReplyKeyboard{Rows: [][]model.MessageButton{
			{{Text: "Новая заметка", Kind: "text"}, {Text: "Мои заметки", Kind: "text"}},
			{{Text: "Настройки", Kind: "text"}, {Text: "Приложение", Kind: "simple_webview", URL: DemoMiniAppURL}, {Text: "Отправить номер", Kind: "action"}},
		}}
	case "Настройки":
		m.Text = "Клавиатура убрана."
		m.KeyboardHide = true
	case "Новая заметка", "Мои заметки":
		m.Text = "«" + text + "»: заметок пока нет."
	default:
		if !strings.HasPrefix(text, "/") {
			return model.Message{}, false
		}
		m.Text = "Команда " + text + " принята."
	}
	return m, true
}

// BotInfo implements model.BotInfoSource with the commands of a demo bot,
// and, for the weather bot, a menu button that opens the bundled Mini App.
func (s *Store) BotInfo(chat int64) model.BotInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isBot(chat) {
		return model.BotInfo{}
	}
	info := model.BotInfo{Commands: []model.BotCommand{
		{Command: "start", Description: "Начать работу с ботом"},
		{Command: "help", Description: "Что умеет бот"},
		{Command: "settings", Description: "Настройки"},
		{Command: "stop", Description: "Остановить уведомления"},
		{Command: "stream", Description: "Ответить статьёй, которую видно, пока бот её пишет"},
	}}
	if chat == DemoWeatherBot {
		info.Menu = &model.BotMenu{Text: "Открыть", URL: DemoMiniAppURL}
	}
	return info
}

// RequestWebView implements model.WebViewStore: every Mini App of the demo is
// the bundled one, served from the loopback with the demo's init data in the
// link, as Telegram would answer.
func (s *Store) RequestWebView(ctx context.Context, req model.WebViewRequest) (model.WebView, error) {
	select {
	case <-ctx.Done():
		return model.WebView{}, ctx.Err()
	case <-time.After(300 * time.Millisecond): // As long as a round trip.
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.miniApp == nil {
		demo, err := miniapp.ServeDemo()
		if err != nil {
			return model.WebView{}, err
		}
		s.miniApp = demo
	}
	link := s.miniApp.URL + "#tgWebAppData=" + s.miniApp.Params.InitData
	view := model.WebView{URL: link}
	if req.Kind != model.WebViewSimple {
		view.QueryID = 1
	}
	return view, nil
}

// ProlongWebView implements model.WebViewStore.
func (s *Store) ProlongWebView(context.Context, model.WebViewRequest, int64) error { return nil }

// SendWebViewData implements model.WebViewStore: the demo's chat gets the
// service message Telegram makes.
func (s *Store) SendWebViewData(_ context.Context, bot int64, buttonText, data string) error {
	s.History(bot)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.histories[bot]
	id := model.MessageID(1)
	if len(h.Messages) > 0 {
		id = h.Messages[len(h.Messages)-1].Key.MessageID + 1
	}
	m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: bot, MessageID: id}, Date: time.Now(), Kind: model.MessageService, Outgoing: true, ContentRevision: 1,
		Service: &model.ServiceAction{Kind: model.ServiceWebViewData, Title: buttonText}}
	h.Messages = append(append([]model.Message(nil), h.Messages...), m)
	h.Revision++
	s.histories[bot] = h
	return nil
}
