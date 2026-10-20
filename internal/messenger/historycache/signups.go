// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"time"

	"komarugram/internal/messenger/model"
)

// ContactSignUps are the users whose private chats begin, in the cache,
// with "joined Telegram", and the days of those messages. Only the first
// message of each private chat is read, by the index: the service message
// opens the chat, and a chat whose history is not cached up to its
// beginning tells nothing.
func (c *Cache) ContactSignUps(ctx context.Context) ([]model.RegistrationPoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Positive chat ids are users'.
	rows, err := c.db.QueryContext(ctx, `SELECT m.chat, json_extract(CAST(m.payload AS TEXT), '$.Date')
		FROM (SELECT chat, MIN(id) AS id FROM messages WHERE chat>0 GROUP BY chat) f
		JOIN messages m ON m.chat=f.chat AND m.id=f.id
		WHERE json_extract(CAST(m.payload AS TEXT), '$.Service.Kind')=?`, string(model.ServiceContactSignUp))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RegistrationPoint
	for rows.Next() {
		var user int64
		var date string
		if err := rows.Scan(&user, &date); err != nil {
			return nil, err
		}
		if at, err := time.Parse(time.RFC3339Nano, date); err == nil && !at.IsZero() {
			out = append(out, model.RegistrationPoint{User: user, Date: at})
		}
	}
	return out, rows.Err()
}
