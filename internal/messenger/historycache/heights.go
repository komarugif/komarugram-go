// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"komarugram/internal/messenger/model"
)

// The heights of rows the history laid out are kept under the environment
// they were laid out in (model.RenderEnvironment), each environment once in
// row_envs: as text in each row, it was most of the row and of its index.
// The profiles of rows (model.MessageLayout.Profile) are kept in
// row_profiles under the environment without its width: they tell the
// layout at any width.
//
// They are disposable, and bounded so that no chat takes the room of the
// others: heightLimits.
const heightsSchema = `CREATE TABLE IF NOT EXISTS row_envs(id INTEGER PRIMARY KEY, env TEXT UNIQUE NOT NULL, used INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS row_heights(chat INTEGER NOT NULL, env INTEGER NOT NULL, id INTEGER NOT NULL, revision INTEGER NOT NULL, height INTEGER NOT NULL, PRIMARY KEY(chat, env, id));
 CREATE TABLE IF NOT EXISTS row_profiles(chat INTEGER NOT NULL, env INTEGER NOT NULL, id INTEGER NOT NULL, revision INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(chat, env, id));
 CREATE TABLE IF NOT EXISTS row_chats(chat INTEGER PRIMARY KEY, used INTEGER NOT NULL, anchor INTEGER NOT NULL);
 DROP TABLE IF EXISTS layouts;`

// heightLimits bounds what is kept: perChat heights of a chat's rows and
// all of them, the bytes of a chat's profiles and of all, and the
// environments last written to, whose rows are kept. A chat past its bound
// loses the rows farthest from where its history was viewed last, before
// or after it, as the history was scrolled to them (row_chats); all of
// them past theirs lose the chats viewed longest ago, whole. They are held
// to them at most once in every check.
type heightLimits struct {
	perChat, all                      int
	profileBytesPerChat, profileBytes int
	envs                              int
	check                             time.Duration
}

// defaultHeightLimits keep a large channel's 100 000 posts at two widths,
// ten such chats in all, 8 MB of a chat's profiles and 32 MB of all, and
// twelve environments: a few widths under the one past which no row is
// wider, a change of fonts or scale, and the profiles'. A profile of a
// post of a channel scrolled through was some 35 to 55 bytes on average:
// some 150 000 such posts for a chat, and a long post takes a few hundred.
var defaultHeightLimits = heightLimits{perChat: 200_000, all: 2_000_000, profileBytesPerChat: 8 << 20, profileBytes: 32 << 20, envs: 12, check: time.Minute}

// envID is the id of env in row_envs, made when it is new. c.mu is held.
func (c *Cache) envID(ctx context.Context, tx *sql.Tx, env model.RenderEnvironment) (int64, error) {
	if id, ok := c.envIDs[env]; ok {
		return id, nil
	}
	b, _ := json.Marshal(env)
	if _, e := tx.ExecContext(ctx, `INSERT INTO row_envs(env) VALUES(?) ON CONFLICT(env) DO NOTHING`, string(b)); e != nil {
		return 0, e
	}
	var id int64
	if e := tx.QueryRowContext(ctx, `SELECT id FROM row_envs WHERE env=?`, string(b)).Scan(&id); e != nil {
		return 0, e
	}
	// Kept once the transaction commits: saveHeights' caller drops the map
	// when it does not.
	c.envIDs[env] = id
	return id, nil
}

// saveHeights writes ls in tx, their profiles too, and keeps them within
// their bounds, the history of v.ChatID viewed at v. c.mu is held.
func (c *Cache) saveHeights(ctx context.Context, tx *sql.Tx, v model.Viewport, ls []model.MessageLayout) error {
	now := time.Now()
	if _, e := tx.ExecContext(ctx, `INSERT INTO row_chats VALUES(?,?,?) ON CONFLICT(chat) DO UPDATE SET used=excluded.used, anchor=excluded.anchor`, v.ChatID, now.UnixNano(), v.AnchorMessageID); e != nil {
		return e
	}
	used := map[int64]bool{}
	chats := map[int64]bool{}
	for _, l := range ls {
		env, e := c.envID(ctx, tx, l.Environment)
		if e != nil {
			return e
		}
		// Replaced, not updated, so that the rowid tells when it was written.
		if _, e = tx.ExecContext(ctx, `INSERT OR REPLACE INTO row_heights VALUES(?,?,?,?,?)`, l.Key.ChatID, env, l.Key.MessageID, l.ContentRevision, l.HeightPx); e != nil {
			return e
		}
		used[env] = true
		chats[l.Key.ChatID] = true
		if len(l.Profile) == 0 {
			continue
		}
		unwidth := l.Environment
		unwidth.WidthPx = 0
		if env, e = c.envID(ctx, tx, unwidth); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR REPLACE INTO row_profiles VALUES(?,?,?,?,?)`, l.Key.ChatID, env, l.Key.MessageID, l.ContentRevision, l.Profile); e != nil {
			return e
		}
		used[env] = true
	}
	for env := range used {
		if _, e := tx.ExecContext(ctx, `UPDATE row_envs SET used=? WHERE id=?`, now.UnixNano(), env); e != nil {
			return e
		}
	}
	if len(used) > 0 {
		if e := c.trimEnvs(ctx, tx); e != nil {
			return e
		}
	}
	for chat := range chats {
		if now.Sub(c.heightsChecked[chat]) < c.heightLimits.check {
			continue
		}
		c.heightsChecked[chat] = now
		var anchor int64
		if e := tx.QueryRowContext(ctx, `SELECT anchor FROM row_chats WHERE chat=?`, chat).Scan(&anchor); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e := trimFar(ctx, tx, `row_heights`, `1`, chat, anchor, c.heightLimits.perChat); e != nil {
			return e
		}
		if e := trimFar(ctx, tx, `row_profiles`, `length(data)`, chat, anchor, c.heightLimits.profileBytesPerChat); e != nil {
			return e
		}
	}
	if len(chats) > 0 && now.Sub(c.heightsCheckedAll) >= c.heightLimits.check {
		c.heightsCheckedAll = now
		if e := c.trimChats(ctx, tx, chats); e != nil {
			return e
		}
	}
	return nil
}

// trimFar deletes the rows of chat in table farthest from message anchor,
// till what measure tells of them all is no more than limit.
func trimFar(ctx context.Context, tx *sql.Tx, table, measure string, chat, anchor int64, limit int) error {
	var total int
	if e := tx.QueryRowContext(ctx, `SELECT coalesce(sum(`+measure+`),0) FROM `+table+` WHERE chat=?`, chat).Scan(&total); e != nil || total <= limit {
		return e
	}
	rows, e := tx.QueryContext(ctx, `SELECT rowid, `+measure+` FROM `+table+` WHERE chat=? ORDER BY abs(id-?) DESC, id`, chat, anchor)
	if e != nil {
		return e
	}
	var gone []int64
	for total > limit && rows.Next() {
		var rowid int64
		var n int
		if e = rows.Scan(&rowid, &n); e != nil {
			rows.Close()
			return e
		}
		gone = append(gone, rowid)
		total -= n
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	return deleteRows(ctx, tx, table, gone)
}

// deleteRows deletes the rows of table by their rowids, some at a time.
func deleteRows(ctx context.Context, tx *sql.Tx, table string, rowids []int64) error {
	for len(rowids) > 0 {
		n := min(len(rowids), 500)
		args := make([]any, n)
		for i := range args {
			args[i] = rowids[i]
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN (?`+strings.Repeat(",?", n-1)+`)`, args...); e != nil {
			return e
		}
		rowids = rowids[n:]
	}
	return nil
}

// trimChats forgets the rows of the chats viewed longest ago, whole, till
// all the heights and profiles kept are within their bounds; the chats
// written to now are kept.
func (c *Cache) trimChats(ctx context.Context, tx *sql.Tx, now map[int64]bool) error {
	for {
		var heights, bytes int
		if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM row_heights), (SELECT coalesce(sum(length(data)),0) FROM row_profiles)`).Scan(&heights, &bytes); e != nil {
			return e
		}
		if heights <= c.heightLimits.all && bytes <= c.heightLimits.profileBytes {
			return nil
		}
		// A chat with rows of before row_chats was viewed longest ago.
		rows, e := tx.QueryContext(ctx, `SELECT k.chat FROM (SELECT DISTINCT chat FROM row_heights UNION SELECT DISTINCT chat FROM row_profiles) k LEFT JOIN row_chats u ON u.chat=k.chat ORDER BY coalesce(u.used,0), k.chat`)
		if e != nil {
			return e
		}
		chat, found := int64(0), false
		for rows.Next() {
			var k int64
			if e = rows.Scan(&k); e != nil {
				rows.Close()
				return e
			}
			if !now[k] {
				chat, found = k, true
				break
			}
		}
		rows.Close()
		if !found {
			return rows.Err()
		}
		for _, table := range []string{`row_heights`, `row_profiles`, `row_chats`} {
			if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE chat=?`, chat); e != nil {
				return e
			}
		}
	}
}

// trimEnvs forgets the environments past the last envs written to, with
// their heights and profiles.
func (c *Cache) trimEnvs(ctx context.Context, tx *sql.Tx) error {
	var n int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM row_envs`).Scan(&n); e != nil || n <= c.heightLimits.envs {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM row_envs WHERE id NOT IN (SELECT id FROM row_envs ORDER BY used DESC, id DESC LIMIT ?)`, c.heightLimits.envs); e != nil {
		return e
	}
	for _, table := range []string{`row_heights`, `row_profiles`} {
		if _, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE env NOT IN (SELECT id FROM row_envs)`); e != nil {
			return e
		}
	}
	clear(c.envIDs)
	return nil
}

// Layouts reads the heights of chat's rows laid out in env.
func (c *Cache) Layouts(ctx context.Context, chat int64, env model.RenderEnvironment) ([]model.MessageLayout, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(env)
	rows, e := c.db.QueryContext(ctx, `SELECT h.id,h.revision,h.height FROM row_heights h JOIN row_envs e ON e.id=h.env WHERE h.chat=? AND e.env=?`, chat, string(b))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.MessageLayout
	for rows.Next() {
		l := model.MessageLayout{Key: model.MessageKey{AccountID: c.account, ChatID: chat}, Environment: env}
		if e = rows.Scan(&l.Key.MessageID, &l.ContentRevision, &l.HeightPx); e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Profiled tells the revisions of the messages of chat whose rows'
// profiles are kept, laid out in env at any width, in the version of their
// encoding, their first byte: one of another is as none.
func (c *Cache) Profiled(ctx context.Context, chat int64, env model.RenderEnvironment, version byte) (map[model.MessageID]uint64, error) {
	out := map[model.MessageID]uint64{}
	err := c.profiles(ctx, chat, env, `p.revision`, []byte{version}, func(id model.MessageID, rows *sql.Rows) error {
		var revision uint64
		e := rows.Scan(&id, &revision)
		out[id] = revision
		return e
	})
	return out, err
}

// Profiles reads the profiles of chat's rows laid out in env at any width.
func (c *Cache) Profiles(ctx context.Context, chat int64, env model.RenderEnvironment) (map[model.MessageID][]byte, error) {
	out := map[model.MessageID][]byte{}
	err := c.profiles(ctx, chat, env, `p.data`, nil, func(id model.MessageID, rows *sql.Rows) error {
		var data []byte
		e := rows.Scan(&id, &data)
		out[id] = data
		return e
	})
	return out, err
}

// profiles reads the id and column of the profiles of chat in env that
// start with prefix, each row for read to scan.
func (c *Cache) profiles(ctx context.Context, chat int64, env model.RenderEnvironment, column string, prefix []byte, read func(model.MessageID, *sql.Rows) error) error {
	env.WidthPx = 0
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(env)
	rows, e := c.db.QueryContext(ctx, `SELECT p.id,`+column+` FROM row_profiles p JOIN row_envs e ON e.id=p.env WHERE p.chat=? AND e.env=? AND substr(p.data,1,?)=?`, chat, string(b), len(prefix), prefix)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		if e = read(0, rows); e != nil {
			return e
		}
	}
	return rows.Err()
}

// ProfilesBetween reads the profiles of the rows of chat's messages low to
// high laid out in env at any width, in the encoding of version: for the
// shapes of skeletons, which their revisions do not change much. It reads
// the profiles alone, not the messages.
func (c *Cache) ProfilesBetween(ctx context.Context, chat int64, env model.RenderEnvironment, version byte, low, high model.MessageID) (map[model.MessageID][]byte, error) {
	env.WidthPx = 0
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(env)
	rows, e := c.db.QueryContext(ctx, `SELECT p.id, p.data FROM row_profiles p WHERE p.chat=? AND p.env=(SELECT id FROM row_envs WHERE env=?) AND p.id>=? AND p.id<=? AND substr(p.data,1,1)=?`, chat, string(b), low, high, []byte{version})
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[model.MessageID][]byte{}
	for rows.Next() {
		var id model.MessageID
		var data []byte
		if e = rows.Scan(&id, &data); e != nil {
			return nil, e
		}
		out[id] = data
	}
	return out, rows.Err()
}
