// SPDX-License-Identifier: Unlicense OR MIT

package model

import "context"

// PhoneResolver finds who has a phone number, for the menu a phone number
// in a message opens. It only reads.
type PhoneResolver interface {
	ResolvePhone(ctx context.Context, phone string) (Chat, error)
}

// BankCard is what Telegram knows of a bank card's number: the bank's name,
// and its pages for the card.
type BankCard struct {
	Title string
	Links []BankCardLink
}

// BankCardLink is a page of a card's bank.
type BankCardLink struct {
	Name, URL string
}

// BankCardSource looks up bank card numbers, for the menu a card number in
// a message opens. It only reads.
type BankCardSource interface {
	BankCard(ctx context.Context, number string) (BankCard, error)
}

// BotUsernames knows the usernames of bots the store has seen, for the
// commands of a bot's messages pressed in a group: they are sent with the
// bot's username, as Telegram Desktop sends them.
type BotUsernames interface {
	// BotUsername is the username of bot id, "" when id is not a bot or
	// has none.
	BotUsername(id int64) string
}
