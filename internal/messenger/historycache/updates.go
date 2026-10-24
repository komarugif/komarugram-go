package historycache

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gotd/td/telegram/updates"
)

func (c *Cache) GetState(ctx context.Context, id int64) (s updates.State, found bool, err error) {
	found, err = c.Get(ctx, fmt.Sprintf("state/%d", id), &s)
	return
}
func (c *Cache) SetState(ctx context.Context, id int64, s updates.State) error {
	return c.Put(ctx, fmt.Sprintf("state/%d", id), s)
}
func (c *Cache) changeState(ctx context.Context, id int64, f func(*updates.State)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("state/%d", id)
	var b []byte
	if e := c.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key=?`, key).Scan(&b); e != nil {
		return e
	}
	var s updates.State
	if e := json.Unmarshal(b, &s); e != nil {
		return e
	}
	f(&s)
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	_, e = c.db.ExecContext(ctx, `UPDATE kv SET value=? WHERE key=?`, b, key)
	return e
}
func (c *Cache) SetPts(ctx context.Context, id int64, v int) error {
	return c.changeState(ctx, id, func(s *updates.State) { s.Pts = v })
}
func (c *Cache) SetQts(ctx context.Context, id int64, v int) error {
	return c.changeState(ctx, id, func(s *updates.State) { s.Qts = v })
}
func (c *Cache) SetDate(ctx context.Context, id int64, v int) error {
	return c.changeState(ctx, id, func(s *updates.State) { s.Date = v })
}
func (c *Cache) SetSeq(ctx context.Context, id int64, v int) error {
	return c.changeState(ctx, id, func(s *updates.State) { s.Seq = v })
}
func (c *Cache) SetDateSeq(ctx context.Context, id int64, d, sq int) error {
	return c.changeState(ctx, id, func(s *updates.State) { s.Date = d; s.Seq = sq })
}
func (c *Cache) GetChannelPts(ctx context.Context, u, ch int64) (v int, found bool, err error) {
	found, err = c.Get(ctx, fmt.Sprintf("pts/%d/%d", u, ch), &v)
	return
}
func (c *Cache) SetChannelPts(ctx context.Context, u, ch int64, v int) error {
	return c.Put(ctx, fmt.Sprintf("pts/%d/%d", u, ch), v)
}

// RaiseChannelPts sets the pts of the channels kept to those of pts, by
// channel id, which are newer, in one transaction; channels not kept stay
// so.
func (c *Cache) RaiseChannelPts(ctx context.Context, u int64, pts map[int64]int) error {
	newer := map[string]any{}
	for ch, n := range pts {
		kept, found, err := c.GetChannelPts(ctx, u, ch)
		if err != nil {
			return err
		}
		if found && kept < n {
			newer[fmt.Sprintf("pts/%d/%d", u, ch)] = n
		}
	}
	return c.PutAll(ctx, newer)
}
func (c *Cache) SetChannelAccessHash(ctx context.Context, u, ch, h int64) error {
	return c.Put(ctx, fmt.Sprintf("hash/%d/%d", u, ch), h)
}
func (c *Cache) GetChannelAccessHash(ctx context.Context, u, ch int64) (v int64, found bool, err error) {
	found, err = c.Get(ctx, fmt.Sprintf("hash/%d/%d", u, ch), &v)
	return
}
func (c *Cache) ForEachChannels(ctx context.Context, u int64, f func(context.Context, int64, int) error) error {
	c.mu.Lock()
	rows, e := c.db.QueryContext(ctx, `SELECT key,value FROM kv WHERE key LIKE ?`, fmt.Sprintf("pts/%d/%%", u))
	if e != nil {
		c.mu.Unlock()
		return e
	}
	values := map[int64]int{}
	for rows.Next() {
		var key string
		var b []byte
		if e = rows.Scan(&key, &b); e != nil {
			break
		}
		var user, ch int64
		fmt.Sscanf(key, "pts/%d/%d", &user, &ch)
		var pts int
		if e = json.Unmarshal(b, &pts); e != nil {
			break
		}
		values[ch] = pts
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	c.mu.Unlock()
	if e != nil {
		return e
	}
	for ch, pts := range values {
		if e = f(ctx, ch, pts); e != nil {
			return e
		}
	}
	return nil
}

var _ updates.StateStorage = (*Cache)(nil)
var _ updates.ChannelAccessHasher = (*Cache)(nil)
