// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"strings"
	"time"
	"unicode/utf16"

	"komarugram/internal/messenger/model"
)

// DemoPhone is the demo's phone number that someone has: a number kept for
// fiction.
const DemoPhone = "+1 555 0100"

// EntitiesExample is a message of the demo with what clicks act on: a
// hashtag, a cashtag, a command, an address, phone numbers, a card's
// number and dates, one of them counted from now.
func EntitiesExample(now time.Time) (string, []model.Entity) {
	text := "Клики по тексту: #komarugram, $TON, /start, team@example.com, " + DemoPhone + ", +1 555 0199, карта 4242 4242 4242 4242. " +
		"Встреча в пятницу, то есть 2026-10-09 18:00."
	entity := func(kind, part string) model.Entity {
		i := strings.Index(text, part)
		return model.Entity{Kind: kind, Offset: len(utf16.Encode([]rune(text[:i]))), Length: len(utf16.Encode([]rune(part)))}
	}
	meeting := now.Add(3*24*time.Hour + time.Hour).Truncate(time.Hour)
	relative, full := entity("date", "в пятницу"), entity("date", "2026-10-09 18:00")
	relative.Date, relative.DateFormat = meeting.Unix(), model.DateRelative
	full.Date, full.DateFormat = meeting.Unix(), model.DateDayOfWeek|model.DateLongDate|model.DateShortTime
	return text, []model.Entity{
		entity("hashtag", "#komarugram"), entity("cashtag", "$TON"), entity("bot_command", "/start"),
		entity("email", "team@example.com"), entity("phone", DemoPhone), entity("phone", "+1 555 0199"),
		entity("bank_card", "4242 4242 4242 4242"), relative, full,
	}
}

// ResolvePhone finds the demo's first private chat for DemoPhone, and no
// one for other numbers, after a round trip.
func (s *Store) ResolvePhone(ctx context.Context, phone string) (model.Chat, error) {
	select {
	case <-ctx.Done():
		return model.Chat{}, ctx.Err()
	case <-time.After(300 * time.Millisecond):
	}
	if strings.TrimSpace(phone) == DemoPhone {
		for _, c := range s.Chats() {
			if c.Kind == model.KindUser {
				return c, nil
			}
		}
	}
	return model.Chat{}, model.ErrLinkNotFound
}

// BankCard is a made-up bank for any number, after a round trip.
func (s *Store) BankCard(ctx context.Context, number string) (model.BankCard, error) {
	select {
	case <-ctx.Done():
		return model.BankCard{}, ctx.Err()
	case <-time.After(300 * time.Millisecond):
	}
	return model.BankCard{Title: "Демо-банк", Links: []model.BankCardLink{{Name: "Открыть в демо-банке", URL: "https://example.com/card"}}}, nil
}

// BotUsername is the username of the demo's bot.
func (s *Store) BotUsername(id int64) string {
	if id == DemoNotesBot {
		return "demo_notes_bot"
	}
	return ""
}
