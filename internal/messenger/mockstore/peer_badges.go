// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import "komarugram/internal/messenger/model"

// demoSender is the sender of the demo's histories, who is not a chat of
// the list.
const demoSender = 42

// PeerBadges implements model.PeerBadgesSource: the badges of the demo's
// chats, whose people write in its groups, and the Premium of the sender of
// its histories.
func (s *Store) PeerBadges(id int64) (model.Badges, bool) {
	if id == demoSender {
		return model.Badges{Premium: true, User: demoSender}, true
	}
	for _, c := range s.Chats() {
		if c.ID != id || c.Badges == (model.Badges{}) {
			continue
		}
		b := c.Badges
		if c.Kind == model.KindChannel {
			b.Channel = c.ID
		} else {
			b.User = c.ID
		}
		return b, true
	}
	return model.Badges{}, false
}
