// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"komarugram/internal/messenger/model"
)

// A verified range is a run of a chat's message IDs, low..high, whose
// cached messages are as Telegram has them for as long as its chain lasts:
// read whole from Telegram, or extended by live updates, at chain, and
// changed since only by updates. A chain is a number the store gives each
// chat and raises when updates of it may have been lost (see tgstore's
// chains); a range of an older chain tells nothing, and is dropped when a
// range of a newer one comes. Unlike a span, which says the cache once had
// every message of a run, a verified range says the cache has them now.

// VerifiedChainKey is where the account's chain of its common box (private
// chats and basic groups) is kept: their updates are asked for again from
// the state kept after a restart, so their ranges outlast it.
const VerifiedChainKey = "verified-chain"

// VerifiedRunKey counts the client's runs on the cache: a channel's chain
// starts past those of the runs before, as its updates skipped what came
// meanwhile.
const VerifiedRunKey = "verified-run"

// AddVerified records low..high of chat as verified at chain, joined with
// the ranges of chain it overlaps or touches, and drops the chat's ranges
// of older chains; nothing, when the chat has a range of a newer one.
func (c *Cache) AddVerified(ctx context.Context, chat int64, low, high int, chain int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if low > high {
		return errors.New("historycache: empty verified range")
	}
	tx, e := c.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Chains only grow: a range of a newer one says this one ended while
	// the page was read, and it tells nothing.
	var newer int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM verified WHERE chat=? AND chain>?`, chat, chain).Scan(&newer); e != nil || newer > 0 {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM verified WHERE chat=? AND chain<?`, chat, chain); e != nil {
		return e
	}
	// Ranges of whole IDs touch when one ends right before the other
	// starts; high+1 would overflow at MaxInt.
	top := min(high, math.MaxInt-1)
	var lo, hi sql.NullInt64
	if e = tx.QueryRowContext(ctx, `SELECT min(low),max(high) FROM verified WHERE chat=? AND low<=?+1 AND high>=?-1`, chat, top, low).Scan(&lo, &hi); e != nil {
		return e
	}
	if lo.Valid {
		low, high = min(low, int(lo.Int64)), max(high, int(hi.Int64))
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM verified WHERE chat=? AND low<=?+1 AND high>=?-1`, chat, top, low); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO verified(chat,low,high,chain) VALUES(?,?,?,?)`, chat, low, high, chain); e != nil {
		return e
	}
	return tx.Commit()
}

// Verified reports whether low..high of chat lies whole in one range
// verified at chain.
func (c *Cache) Verified(ctx context.Context, chat int64, low, high int, chain int64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT count(*) FROM verified WHERE chat=? AND chain=? AND low<=? AND high>=?`, chat, chain, low, high).Scan(&n)
	return n > 0, err
}

// Range reads the messages of chat in low..high, oldest first, at most
// limit of them: a range checked against Telegram's.
func (c *Cache) Range(ctx context.Context, chat int64, low, high, limit int) ([]model.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, e := c.db.QueryContext(ctx, `SELECT payload FROM messages WHERE chat=? AND deleted!=1 AND id>=? AND id<=? ORDER BY id LIMIT ?`, chat, low, high, limit)
	if e != nil {
		return nil, e
	}
	return readMessages(rows)
}
