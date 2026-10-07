// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"strings"

	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// ResolvePhone finds who has phone, as the menu of a phone number in
// Telegram Desktop does (contacts.resolvePhone).
func (s *Store) ResolvePhone(ctx context.Context, phone string) (model.Chat, error) {
	api := s.api()
	if api == nil {
		return model.Chat{}, errors.New("offline")
	}
	res, err := api.ContactsResolvePhone(ctx, strings.TrimSpace(phone))
	if tgerr.Is(err, "PHONE_NOT_OCCUPIED", "PHONE_NUMBER_INVALID") {
		return model.Chat{}, model.ErrLinkNotFound
	}
	if err != nil {
		return model.Chat{}, err
	}
	s.rememberPeers(res.Users, res.Chats)
	return s.chatFor(peerID(res.Peer), nil), nil
}

// BankCard is what Telegram knows of a bank card's number
// (payments.getBankCardData).
func (s *Store) BankCard(ctx context.Context, number string) (model.BankCard, error) {
	api := s.api()
	if api == nil {
		return model.BankCard{}, errors.New("offline")
	}
	res, err := api.PaymentsGetBankCardData(ctx, number)
	if err != nil {
		return model.BankCard{}, err
	}
	card := model.BankCard{Title: res.Title}
	for _, u := range res.OpenURLs {
		card.Links = append(card.Links, model.BankCardLink{Name: u.Name, URL: u.URL})
	}
	return card, nil
}

// BotUsername is the username of bot id, if the store has seen it.
func (s *Store) BotUsername(id int64) string {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.peers[id]; ok && p.Rights.Bot {
		return p.Username
	}
	return ""
}
