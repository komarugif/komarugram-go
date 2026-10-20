// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"time"

	"komarugram/internal/messenger/model"
)

// PeerProfile implements model.PeerProfileSource: Анна Смирнова shows a
// unique gift's cover with gifts around her avatar, collectible usernames
// and her channel; Новости Go a profile color, and its own username after
// the collectible one it put first.
func (s *Store) PeerProfile(_ context.Context, chat int64) (model.PeerProfile, error) {
	var out model.PeerProfile
	if name := usernames[chat]; name != "" {
		out.Usernames = []model.Username{{Name: name}}
	}
	switch chat {
	case 2:
		out.Usernames = append(out.Usernames, model.Username{Name: "anna", Collectible: true}, model.Username{Name: "smirnova", Collectible: true})
		out.Cover = model.ProfileCover{Gift: true, CenterColor: 0x6fae6a, EdgeColor: 0x2f6e43, PatternColor: 0x1c4f2c, TextColor: 0xffffff, Pattern: 1}
		for _, i := range []int{1, 3, 5} {
			out.Gifts = append(out.Gifts, demoGift(chat, i))
		}
		s.mu.Lock()
		for _, c := range s.chats {
			if c.ID == 13 {
				// The post ends with a custom emoji, drawn as the demo's
				// sticker.
				post := c.Last
				post.Text += " 🌅"
				out.Channel = &model.PersonalChannel{Chat: c, Post: post, Date: c.LastTime,
					Entities: []model.Entity{{Kind: "emoji", Offset: 21, Length: 2, DocumentID: 3}}}
			}
		}
		s.mu.Unlock()
	case 4:
		out.Usernames = []model.Username{{Name: "gonews", Collectible: true}, {Name: "golang_news"}}
		out.Cover.Colors = []uint32{0x5a8fd6, 0x82b4ee}
		out.Cover.DarkColors = []uint32{0x2a4f80, 0x3f6aa3}
		out.Cover.Pattern = 2
	}
	return out, nil
}

// CachedPeerProfile implements model.PeerProfileSource: the demo knows
// every profile already.
func (s *Store) CachedPeerProfile(chat int64) (model.PeerProfile, bool) {
	p, err := s.PeerProfile(context.Background(), chat)
	return p, err == nil
}

// CustomEmoji is every custom emoji of the demo, as the cover's pattern:
// its sticker.
func (s *Store) CustomEmoji(_ context.Context, id int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}}, nil
}

// CollectibleUsername implements model.PeerProfileSource: every demo
// collectible was bought on one day for 10 TON.
func (s *Store) CollectibleUsername(_ context.Context, username string) (model.CollectibleUsername, error) {
	return model.CollectibleUsername{
		Username:       username,
		Bought:         time.Date(2023, 8, 31, 19, 59, 0, 0, time.Local),
		Currency:       "USD",
		Amount:         1368,
		CryptoCurrency: "TON",
		CryptoAmount:   10_000_000_000,
		URL:            "https://fragment.com/username/" + username,
	}, nil
}
