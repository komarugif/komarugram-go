// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// A phone number resolves to its user, who is remembered; a number not on
// Telegram is not found. A card's bank and pages come as Telegram sends
// them. A bot's username is known once it is seen.
func TestPhoneCardAndBotLookups(t *testing.T) {
	s := testStore(t)
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.ContactsResolvePhoneRequest:
			if req.Phone != "+15550100" {
				return &tgerr.Error{Code: 400, Type: "PHONE_NOT_OCCUPIED"}
			}
			*out.(*tg.ContactsResolvedPeer) = tg.ContactsResolvedPeer{
				Peer:  &tg.PeerUser{UserID: 7},
				Users: []tg.UserClass{&tg.User{ID: 7, AccessHash: 1, FirstName: "Anna"}},
			}
		case *tg.PaymentsGetBankCardDataRequest:
			*out.(*tg.PaymentsBankCardData) = tg.PaymentsBankCardData{Title: "Bank", OpenURLs: []tg.BankCardOpenURL{{URL: "https://bank.example/card", Name: "Open in Bank"}}}
		}
		return nil
	}))
	ctx := context.Background()
	chat, err := s.ResolvePhone(ctx, " +15550100 ")
	if err != nil || chat.ID != 7 || chat.Title != "Anna" {
		t.Fatalf("chat %+v, %v", chat, err)
	}
	if _, err := s.ResolvePhone(ctx, "+10000000"); !errors.Is(err, model.ErrLinkNotFound) {
		t.Fatalf("number not on Telegram: %v", err)
	}
	card, err := s.BankCard(ctx, "4242424242424242")
	if err != nil || card.Title != "Bank" || len(card.Links) != 1 || card.Links[0] != (model.BankCardLink{Name: "Open in Bank", URL: "https://bank.example/card"}) {
		t.Fatalf("card %+v, %v", card, err)
	}
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 8, Bot: true, Username: "helper_bot"}, &tg.User{ID: 9, Username: "person"}}, nil)
	if got := s.BotUsername(8); got != "helper_bot" {
		t.Fatalf("bot's username %q", got)
	}
	if got := s.BotUsername(9); got != "" {
		t.Fatalf("a person's username %q given as a bot's", got)
	}
}
