// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"regexp"
	"strconv"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// The bot asked inline who a user known only by ID is, @oLolsBot. Telegram
// gives no username for an ID; AyuGram asked bots for it until they wanted
// a subscription or a /start (2026-04). Its ID is kept with its username:
// a username that went to another bot is not asked.
const (
	lookupBotID       = 7822565021
	lookupBotUsername = "oLolsBot"
)

// lookupUsername finds the username in the bot's text of a bot:
// "├ username: @BotFather".
var lookupUsername = regexp.MustCompile(`(?m)username: @([A-Za-z0-9_]{4,32})\b`)

// userFromLookupBot asks the lookup bot inline for user id, which sends no
// message. Its answer about a user mentions them, and Telegram sends the
// user mentioned with an access hash of the account's, which opens a chat
// with them; the bot's text, which it could word as it likes, is not
// read for them. Of a bot, it tells the username, which is resolved and
// taken only when it is id's.
func (s *Store) userFromLookupBot(ctx context.Context, api *tg.Client, id int64) (model.Chat, bool) {
	bot, ok := s.lookupBot(ctx, api)
	if !ok {
		return model.Chat{}, false
	}
	res, err := api.MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{
		Bot: bot, Peer: &tg.InputPeerEmpty{}, Query: strconv.FormatInt(id, 10),
	})
	if err != nil {
		return model.Chat{}, false
	}
	if chat, ok := s.takeUser(res.Users, id); ok {
		return chat, true
	}
	for _, r := range res.Results {
		text, ok := r.GetSendMessage().(*tg.BotInlineMessageText)
		if !ok {
			continue
		}
		m := lookupUsername.FindStringSubmatch(text.Message)
		if m == nil {
			continue
		}
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: m[1]})
		if err != nil {
			continue
		}
		if chat, ok := s.takeUser(resolved.Users, id); ok {
			return chat, true
		}
	}
	return model.Chat{}, false
}

// lookupBot is the lookup bot to ask, resolved by its username once.
func (s *Store) lookupBot(ctx context.Context, api *tg.Client) (*tg.InputUser, bool) {
	s.history.mu.Lock()
	peer := s.history.peers[lookupBotID]
	s.history.mu.Unlock()
	if peer.Kind == "user" && peer.Hash != 0 {
		return &tg.InputUser{UserID: lookupBotID, AccessHash: peer.Hash}, true
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: lookupBotUsername})
	if err != nil {
		return nil, false
	}
	for _, raw := range resolved.Users {
		if u, ok := raw.(*tg.User); ok && u.ID == lookupBotID && u.Bot && u.AccessHash != 0 {
			s.rememberPeers([]tg.UserClass{u}, nil)
			return &tg.InputUser{UserID: u.ID, AccessHash: u.AccessHash}, true
		}
	}
	return nil, false
}

// takeUser is the chat with user id among users, when Telegram gave them
// with an access hash.
func (s *Store) takeUser(users []tg.UserClass, id int64) (model.Chat, bool) {
	for _, raw := range users {
		if u, ok := raw.(*tg.User); ok && u.ID == id && !u.Min && !u.Deleted && (u.AccessHash != 0 || u.Self) {
			s.rememberPeers([]tg.UserClass{u}, nil)
			return s.chatFor(id, nil), true
		}
	}
	return model.Chat{}, false
}
