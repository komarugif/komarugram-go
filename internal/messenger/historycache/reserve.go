// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"database/sql"
	"encoding/json"

	"komarugram/internal/messenger/model"
)

// Reserve reads what the cache keeps of chat's history after message after,
// in the span that holds it (see Span), oldest first, at most limit
// messages: each one's height where its row was laid out in env, and its
// row's profile in env at any width and the encoding of version (see
// Profiled). The history reserves the room of the rows below where it is
// by them, before they are loaded (model.HistoryReserve).
func (c *Cache) Reserve(ctx context.Context, chat int64, after int, env model.RenderEnvironment, version byte, limit int) ([]model.ReserveRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, high, ok, err := c.bounds(ctx, chat, after)
	if err != nil || !ok {
		return nil, err
	}
	heights, _ := json.Marshal(env)
	unwidth := env
	unwidth.WidthPx = 0
	profiles, _ := json.Marshal(unwidth)
	var heightEnv, profileEnv sql.NullInt64
	if err = c.db.QueryRowContext(ctx, `SELECT (SELECT id FROM row_envs WHERE env=?), (SELECT id FROM row_envs WHERE env=?)`, string(heights), string(profiles)).Scan(&heightEnv, &profileEnv); err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT m.id,
 coalesce(json_extract(CAST(m.payload AS TEXT),'$.ContentRevision'),0),
 coalesce(json_extract(CAST(m.payload AS TEXT),'$.Outgoing'),0),
 coalesce(json_extract(CAST(m.payload AS TEXT),'$.GroupedID'),0),
 json_type(CAST(m.payload AS TEXT),'$.Media') IS NOT NULL AND json_type(CAST(m.payload AS TEXT),'$.Media')!='null',
 json_type(CAST(m.payload AS TEXT),'$.Service') IS NOT NULL AND json_type(CAST(m.payload AS TEXT),'$.Service')!='null',
 h.revision, h.height, p.revision, p.data
 FROM messages m
 LEFT JOIN row_heights h ON h.chat=m.chat AND h.env=? AND h.id=m.id
 LEFT JOIN row_profiles p ON p.chat=m.chat AND p.env=? AND p.id=m.id AND substr(p.data,1,1)=?
 WHERE m.chat=? AND m.deleted!=1 AND m.payload IS NOT NULL AND m.id>? AND m.id<=?
 ORDER BY m.id LIMIT ?`, heightEnv, profileEnv, []byte{version}, chat, after, high, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ReserveRow
	for rows.Next() {
		var r model.ReserveRow
		var revision uint64
		var heightRevision, profileRevision sql.NullInt64
		var height sql.NullInt64
		var profile []byte
		if err = rows.Scan(&r.ID, &revision, &r.Outgoing, &r.GroupedID, &r.Media, &r.Service, &heightRevision, &height, &profileRevision, &profile); err != nil {
			return nil, err
		}
		r.Revision, r.Height = revision, -1
		if height.Valid {
			r.Height, r.HeightRevision = int(height.Int64), uint64(heightRevision.Int64)
		}
		if profile != nil {
			r.Profile, r.ProfileRevision = profile, uint64(profileRevision.Int64)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// From reads up to limit messages of chat from message from on, oldest
// first, from the span that holds it alone (see Span): from itself, when
// the cache keeps it, and those after it as far as the span goes.
func (c *Cache) From(ctx context.Context, chat int64, from, limit int) ([]model.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, high, ok, e := c.bounds(ctx, chat, from)
	if e != nil || !ok {
		return nil, e
	}
	rows, e := c.db.QueryContext(ctx, `SELECT payload FROM messages WHERE chat=? AND deleted!=1 AND id>=? AND id<=? ORDER BY id LIMIT ?`, chat, from, high, limit)
	if e != nil {
		return nil, e
	}
	return readMessages(rows)
}
