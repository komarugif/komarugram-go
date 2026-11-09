// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// A span is a run of a chat's message IDs, low..high, of which the cache
// has every message Telegram had when it was read: a history page, or live
// updates after a page that reached the chat's end. Messages outside every
// span are kept (search finds them) but are not shown as history, since
// what lies between them and a span is not known: a chat opened after more
// messages came than one page holds would show the old ones next to the
// new, with the ones between missing for good.
//
// A chat without spans was cached before spans were kept; its messages are
// read as one run, as they were, until Telegram's first page of it.
type Span struct {
	Low, High int
}

// AddSpan records low..high of chat as complete, joined with the spans it
// overlaps or touches, and returns the span that holds it now.
func (c *Cache) AddSpan(ctx context.Context, chat int64, low, high int) (Span, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if low > high {
		return Span{}, errors.New("historycache: empty span")
	}
	tx, e := c.db.BeginTx(ctx, nil)
	if e != nil {
		return Span{}, e
	}
	defer tx.Rollback()
	// Spans of whole IDs touch when one ends right before the other starts:
	// there is no message between them. high+1 would overflow at MaxInt.
	var lo, hi sql.NullInt64
	if e = tx.QueryRowContext(ctx, `SELECT min(low),max(high) FROM spans WHERE chat=? AND low<=?+1 AND high>=?-1`, chat, min(high, math.MaxInt-1), low).Scan(&lo, &hi); e != nil {
		return Span{}, e
	}
	s := Span{low, high}
	if lo.Valid {
		s.Low = min(s.Low, int(lo.Int64))
		s.High = max(s.High, int(hi.Int64))
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM spans WHERE chat=? AND low<=?+1 AND high>=?-1`, chat, min(high, math.MaxInt-1), low); e != nil {
		return Span{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO spans(chat,low,high) VALUES(?,?,?)`, chat, s.Low, s.High); e != nil {
		return Span{}, e
	}
	return s, tx.Commit()
}

// SpanOf returns the span of chat that holds id, or, for id 0, the newest
// span. ok is false when there is none; legacy is true when the chat has
// no spans at all (see Span).
func (c *Cache) SpanOf(ctx context.Context, chat int64, id int) (s Span, ok, legacy bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spanOf(ctx, chat, id)
}

func (c *Cache) spanOf(ctx context.Context, chat int64, id int) (s Span, ok, legacy bool, err error) {
	query := `SELECT low,high FROM spans WHERE chat=? AND low<=? AND high>=?`
	args := []any{chat, id, id}
	if id == 0 {
		query, args = `SELECT low,high FROM spans WHERE chat=? ORDER BY high DESC LIMIT 1`, []any{chat}
	}
	err = c.db.QueryRowContext(ctx, query, args...).Scan(&s.Low, &s.High)
	if err == nil {
		return s, true, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return s, false, false, err
	}
	var n int
	if err = c.db.QueryRowContext(ctx, `SELECT count(*) FROM spans WHERE chat=?`, chat).Scan(&n); err != nil {
		return s, false, false, err
	}
	return s, false, n == 0, nil
}

// bounds are the IDs next to id a history may be read from: its span, all
// of a legacy chat, or none (ok false).
func (c *Cache) bounds(ctx context.Context, chat int64, id int) (low, high int, ok bool, err error) {
	s, found, legacy, err := c.spanOf(ctx, chat, id)
	switch {
	case err != nil:
		return 0, 0, false, err
	case legacy:
		return math.MinInt, math.MaxInt, true, nil
	case !found:
		return 0, 0, false, nil
	}
	return s.Low, s.High, true, nil
}

// SpansIn returns the spans of chat that overlap low..high, in order.
func (c *Cache) SpansIn(ctx context.Context, chat int64, low, high int) ([]Span, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.db.QueryContext(ctx, `SELECT low,high FROM spans WHERE chat=? AND low<=? AND high>=? ORDER BY low`, chat, high, low)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Span
	for rows.Next() {
		var s Span
		if err = rows.Scan(&s.Low, &s.High); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
