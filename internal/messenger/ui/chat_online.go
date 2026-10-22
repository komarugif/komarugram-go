// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// onlineRefresh is how often a group's members online are asked again
// while it is shown.
const onlineRefresh = time.Minute

// groupOnline is how many members of the open group are online, which the
// header tells as Telegram Desktop's does. Telegram counts them all, not
// only the members loaded, so big groups get a count too, as in
// materialgram.
type groupOnline struct {
	mu    sync.Mutex
	chat  int64
	count int
	// asked is when the count was last asked for, zero never.
	asked time.Time
}

// chatStatusOnline is chatStatus with the members online, when more than
// the account is.
func (p *chatPage) chatStatusOnline(c model.Chat, now time.Time, l localization.Catalog) string {
	status := chatStatus(c, l)
	details, ok := p.source.(model.ChatDetailer)
	if !ok || c.Kind != model.KindGroup || c.Members <= 0 {
		return status
	}
	o := &p.online
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.chat != c.ID {
		o.chat, o.count, o.asked = c.ID, 0, time.Time{}
	}
	if o.asked.IsZero() || now.Sub(o.asked) >= onlineRefresh {
		o.asked = now
		chat := c.ID
		go func() {
			defer crash.Recover("online count", nil)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			n, err := details.Online(ctx, chat)
			if err != nil {
				return
			}
			o.mu.Lock()
			changed := o.chat == chat && o.count != n
			if changed {
				o.count = n
			}
			o.mu.Unlock()
			if changed {
				p.invalidate()
			}
		}()
	}
	// The account itself may be the one online.
	if o.count <= 1 {
		return status
	}
	online := l.Count("status.online", o.count, map[string]string{"count": groupDigits(o.count)})
	return l.Format("status.members_online", map[string]string{"members_count": status, "online_count": online})
}
