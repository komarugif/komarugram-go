// SPDX-License-Identifier: Unlicense OR MIT

package model

import "context"

// WebViewKind is how a Mini App is asked for, which decides what Telegram
// tells the bot about the user.
type WebViewKind int

const (
	// WebViewInline is a button under a message, or a keyboard's WebView
	// button: messages.requestWebView, from the chat the button is in.
	WebViewInline WebViewKind = iota
	// WebViewSimple is a keyboard's SimpleWebView button:
	// messages.requestSimpleWebView, which tells the bot nothing of the user.
	WebViewSimple
	// WebViewMenu is the bot's menu button beside the composer: like
	// WebViewInline, from the bot menu.
	WebViewMenu
	// WebViewMain is a bot's Main Mini App (messages.requestMainWebView),
	// used for example by the Wallet entry in the main menu.
	WebViewMain
)

// WebViewRequest asks Telegram for the link that opens a bot's Mini App.
type WebViewRequest struct {
	Kind WebViewKind
	// Chat is the chat the button is in, and Bot the bot's user id.
	Chat, Bot int64
	// BotUsername names the bot when Bot is not known yet (for example,
	// Wallet's bot from appConfig).
	BotUsername string
	// URL is the button's; empty for the bot's menu button without one.
	URL        string
	StartParam string
	// Theme is the colors the app is told, as JSON.
	Theme string
}

// WebView is Telegram's answer: the link to open, whose fragment holds the
// user's init data.
type WebView struct {
	URL string
	// QueryID names an inline launch, which is kept alive with
	// ProlongWebView while it is open; 0 for a simple one.
	QueryID int64
}

// WebViewStore is a Store that can ask for Mini Apps.
type WebViewStore interface {
	RequestWebView(ctx context.Context, req WebViewRequest) (WebView, error)
	// ProlongWebView tells Telegram that an inline launch is still open.
	ProlongWebView(ctx context.Context, req WebViewRequest, queryID int64) error
	// SendWebViewData sends what a keyboard button's Mini App hands over
	// (Telegram.WebApp.sendData) to its bot.
	SendWebViewData(ctx context.Context, bot int64, buttonText, data string) error
}

// BotMenu is the button a bot puts beside the composer: it opens the URL as
// a Mini App.
type BotMenu struct {
	Text, URL string
}

// BotInfo is what a bot chat knows of its bot besides the chat itself.
type BotInfo struct {
	Commands []BotCommand
	// Menu is the menu button, nil if the bot has none that opens an app.
	Menu *BotMenu
}

// BotInfoSource is a Store that knows the bots the account has a chat with.
type BotInfoSource interface {
	// BotInfo returns what the bot chat is with tells: empty until it is read,
	// which starts on the first call and redraws the window when done.
	BotInfo(chat int64) BotInfo
}

// WalletConfig is what Telegram's client configuration says about Wallet, as
// Telegram Desktop's AppConfig::walletAvailable and oldWalletBotUsername.
type WalletConfig struct {
	Available   bool
	BotUsername string
}

// WalletSource is a Store that knows whether Wallet is offered for the account.
type WalletSource interface {
	Wallet() WalletConfig
}
