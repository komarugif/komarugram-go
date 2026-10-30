// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// LocalMedia is media explicitly retained by a device-only Premium message.
// It is independent of the disposable download budget.
func (c *Cache) LocalMedia(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b []byte
	err := c.db.QueryRowContext(ctx, `SELECT data FROM local_media WHERE key=?`, key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

func (c *Cache) SaveLocalMedia(ctx context.Context, key string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.ExecContext(ctx, `INSERT INTO local_media(key,data) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data`, key, data)
	return err
}

// PinLocalMedia retains the next download too, when an emoji is still loading
// while its message is accepted.
func (c *Cache) PinLocalMedia(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.ExecContext(ctx, `INSERT INTO local_media(key,data) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key, []byte{})
	return err
}

func (c *Cache) LocalMediaPinned(ctx context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var found bool
	err := c.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM local_media WHERE key=?)`, key).Scan(&found)
	return found, err
}

// PutOutgoing atomically saves an outgoing request and private copies of its
// files. These chunks belong to the outbox, not the evictable media cache, and
// use the database's encryption. An interrupted copy publishes no request.
func (c *Cache) PutOutgoing(ctx context.Context, id int64, value any, paths []string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO outbox(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, id, b); err != nil {
		return err
	}
	buf := make([]byte, 256<<10)
	for i, path := range paths {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		e = func() error {
			defer f.Close()
			info, e := f.Stat()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return &os.PathError{Op: "queue", Path: path, Err: os.ErrInvalid}
			}
			for part := 0; ; part++ {
				n, e := io.ReadFull(f, buf)
				if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
					return e
				}
				if n > 0 {
					if _, err := tx.ExecContext(ctx, `INSERT INTO outbox_files(id,file,part,data) VALUES(?,?,?,?)`, id, i, part, buf[:n]); err != nil {
						return err
					}
				}
				if e != nil {
					return nil
				}
			}
		}()
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Outgoing reads requests in their original insertion order.
func (c *Cache) Outgoing(ctx context.Context) ([][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.db.QueryContext(ctx, `SELECT payload FROM outbox ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// WriteOutgoingFile streams a staged file without loading it all into memory.
func (c *Cache) WriteOutgoingFile(ctx context.Context, id int64, file int, dst io.Writer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.db.QueryContext(ctx, `SELECT data FROM outbox_files WHERE id=? AND file=? ORDER BY part`, id, file)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return err
		}
		if _, err := dst.Write(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

// DropOutgoing removes both the request and its private file copies.
func (c *Cache) DropOutgoing(ctx context.Context, id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM outbox_files WHERE id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM outbox WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
