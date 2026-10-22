// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"log"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// Premium implements model.PremiumSource.
func (s *Store) Premium() model.Premium {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.premium.Limits == nil {
		return model.PremiumFromConfig(s.me.Premium, nil)
	}
	return s.premium
}

// loadAppConfig reads the client configuration for the limits Premium
// raises and whether the account is frozen. Without it the fallback values
// stand, so a failure is only logged.
func (s *Store) loadAppConfig(ctx context.Context, api *tg.Client, active bool) {
	var config map[string]any
	answer, err := api.HelpGetAppConfig(ctx, 0)
	if err != nil {
		log.Printf("tgstore: client configuration: %v", err)
	} else if c, ok := answer.(*tg.HelpAppConfig); ok {
		config, _ = jsonValue(c.Config).(map[string]any)
	}
	premium := model.PremiumFromConfig(active, config)
	if err != nil {
		s.publish(func() { s.premium = premium })
		return
	}
	freeze := freezeFromConfig(config)
	s.publish(func() { s.premium, s.freeze = premium, freeze })
}

// jsonValue turns Telegram's JSON into what encoding/json would decode it to.
func jsonValue(v tg.JSONValueClass) any {
	switch v := v.(type) {
	case *tg.JSONBool:
		return v.Value
	case *tg.JSONNumber:
		return v.Value
	case *tg.JSONString:
		return v.Value
	case *tg.JSONArray:
		values := make([]any, len(v.Value))
		for i, item := range v.Value {
			values[i] = jsonValue(item)
		}
		return values
	case *tg.JSONObject:
		values := make(map[string]any, len(v.Value))
		for _, field := range v.Value {
			values[field.Key] = jsonValue(field.Value)
		}
		return values
	}
	return nil
}

// emojiStatus is the custom emoji a user shows beside the name, 0 for none
// or one that has expired.
func emojiStatus(status tg.EmojiStatusClass, now time.Time) int64 {
	var id int64
	var until int
	switch s := status.(type) {
	case *tg.EmojiStatus:
		id, until = s.DocumentID, s.Until
	case *tg.EmojiStatusCollectible:
		id, until = s.DocumentID, s.Until
	}
	if until != 0 && time.Unix(int64(until), 0).Before(now) {
		return 0
	}
	return id
}

// userBadges are the marks beside u's name.
func userBadges(u *tg.User, now time.Time) model.Badges {
	return model.Badges{
		Premium: u.Premium, EmojiStatus: emojiStatus(u.EmojiStatus, now),
		Verified: u.Verified, Scam: u.Scam, Fake: u.Fake, BotVerification: u.BotVerificationIcon,
		User: u.ID,
	}
}

// channelBadges are the marks beside c's title. A channel has no Premium of
// its own, but may have an emoji status.
func channelBadges(c *tg.Channel, now time.Time) model.Badges {
	return model.Badges{
		EmojiStatus: emojiStatus(c.EmojiStatus, now), Channel: c.ID,
		Verified:    c.Verified, Scam: c.Scam, Fake: c.Fake, BotVerification: c.BotVerificationIcon,
	}
}
