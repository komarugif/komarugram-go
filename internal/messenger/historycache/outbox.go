// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
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

// outgoingPartSize is the size of a chunk of a queued file, and
// outgoingBatchParts how many chunks go in one transaction.
const (
	outgoingPartSize   = 256 << 10
	outgoingBatchParts = 32
)

// PutOutgoing saves an outgoing request, with private copies of the files at
// paths when it has any. These chunks belong to the outbox, not the evictable
// media cache, and use the database's encryption. The files are copied a few
// chunks at a time, as the cache has one lock and one connection, and a long
// file would stop everything else that reads it: the request comes last, so an
// interrupted copy publishes nothing (Outgoing clears what it left).
func (c *Cache) PutOutgoing(ctx context.Context, id int64, value any, paths []string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		if err := c.dropOutgoingFiles(ctx, id); err != nil {
			return err
		}
		for i, path := range paths {
			if err := c.copyOutgoingFile(ctx, id, i, path); err != nil {
				// With a context of its own: ctx may be why it failed.
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return errors.Join(err, c.dropOutgoingFiles(cleanup, id))
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.db.ExecContext(ctx, `INSERT INTO outbox(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, id, b)
	return err
}

func (c *Cache) copyOutgoingFile(ctx context.Context, id int64, file int, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return &os.PathError{Op: "queue", Path: path, Err: os.ErrInvalid}
	}
	buf := make([]byte, outgoingPartSize)
	for part, done := 0, false; !done; {
		var read int
		done, read, err = c.copyOutgoingBatch(ctx, id, file, part, f, buf)
		if err != nil {
			return err
		}
		part += read
	}
	return nil
}

// copyOutgoingBatch saves the next chunks of f from part on in one transaction.
// It reports whether f ended, and how many chunks it saved.
func (c *Cache) copyOutgoingBatch(ctx context.Context, id int64, file, part int, f io.Reader, buf []byte) (done bool, parts int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	for parts < outgoingBatchParts && !done {
		n, e := io.ReadFull(f, buf)
		if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
			return false, 0, e
		}
		done = e != nil
		if n > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO outbox_files(id,file,part,data) VALUES(?,?,?,?)`, id, file, part+parts, buf[:n]); err != nil {
				return false, 0, err
			}
			parts++
		}
	}
	return done, parts, tx.Commit()
}

func (c *Cache) dropOutgoingFiles(ctx context.Context, id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.ExecContext(ctx, `DELETE FROM outbox_files WHERE id=?`, id)
	return err
}

// Outgoing reads requests in their original insertion order. It is read when
// the account opens, and clears the copies of files the client stopped making.
func (c *Cache) Outgoing(ctx context.Context) ([][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.db.ExecContext(ctx, `DELETE FROM outbox_files WHERE id NOT IN (SELECT id FROM outbox)`); err != nil {
		return nil, err
	}
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

// WriteOutgoingFile streams a staged file without loading it all into memory,
// or keeping the cache from others meanwhile.
func (c *Cache) WriteOutgoingFile(ctx context.Context, id int64, file int, dst io.Writer) error {
	for part := 0; ; part++ {
		b, err := c.outgoingPart(ctx, id, file, part)
		if err != nil || b == nil {
			return err
		}
		if _, err := dst.Write(b); err != nil {
			return err
		}
	}
}

func (c *Cache) outgoingPart(ctx context.Context, id int64, file, part int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b []byte
	err := c.db.QueryRowContext(ctx, `SELECT data FROM outbox_files WHERE id=? AND file=? AND part=?`, id, file, part).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
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
