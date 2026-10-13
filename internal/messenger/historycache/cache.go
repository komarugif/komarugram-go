// Package historycache stores account history, media and disposable measurements.
package historycache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/securedb"
	"komarugram/internal/messenger/security"

	_ "github.com/ncruces/go-sqlite3/driver"
)

type Cache struct {
	mu sync.Mutex
	// keepDeleted keeps messages Telegram deleted, marked so;
	// keepEdits keeps the text others' messages had before an edit.
	keepDeleted, keepEdits bool
	db                     *sql.DB
	account, path          string
	protection             *security.Manager
	encrypted              bool
	closed                 bool
	unregister             func()
	unregisterPlain        func()
}

func Open(path, account string, protection *security.Manager) (*Cache, error) {
	c := &Cache{path: path, account: account, protection: protection}
	if err := c.open(); err != nil {
		return nil, err
	}
	if protection != nil {
		c.unregister = protection.AddMigration(c.Protect)
		c.unregisterPlain = protection.AddUnmigration(c.Unprotect)
	}
	go c.indexExisting()
	return c, nil
}
func (c *Cache) open() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return err
	}
	var err error
	c.encrypted = c.protection != nil && c.protection.Enabled()
	if c.encrypted && c.protection.Disabling() {
		if _, e := os.Stat(c.path + ".plain"); e == nil {
			c.encrypted = false
		}
	}
	if c.encrypted {
		if !c.protection.Disabling() {
			err = removePlain(c.path)
		}
		if err != nil {
			return err
		}
		c.db, err = securedb.Open(c.protection, c.path+".secure", c.account)
	} else {
		// Create with restrictive permissions before SQLite opens it.
		f, e := os.OpenFile(c.path+".plain", os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		f.Close()
		c.db, err = sql.Open("sqlite3", c.path+".plain")
	}
	if err != nil {
		return err
	}
	c.db.SetMaxOpenConns(1)
	// Telegram's language packs were kept here, a copy an account, under
	// "language/"; package langpack keeps one for all now.
	_, err = c.db.Exec(`PRAGMA temp_store=memory;
 CREATE TABLE IF NOT EXISTS messages(chat INTEGER, id INTEGER, payload BLOB, deleted INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(chat,id));
 CREATE TABLE IF NOT EXISTS global_deletions(id INTEGER PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY, value BLOB);
 CREATE TABLE IF NOT EXISTS layouts(chat INTEGER,id INTEGER,env TEXT,revision INTEGER,height INTEGER,PRIMARY KEY(chat,id,env));
 CREATE TABLE IF NOT EXISTS media(key TEXT PRIMARY KEY,data BLOB,used INTEGER);
 CREATE TABLE IF NOT EXISTS edits(chat INTEGER,id INTEGER,at INTEGER,payload BLOB,PRIMARY KEY(chat,id,at));
 CREATE TABLE IF NOT EXISTS spans(chat INTEGER,low INTEGER,high INTEGER,PRIMARY KEY(chat,low));
 DROP INDEX IF EXISTS photos;
 DELETE FROM kv WHERE key >= 'language/' AND key < 'language0';
 CREATE INDEX IF NOT EXISTS photo_videos ON messages(chat,id) WHERE ` + photoWhere + `;`)
	if err == nil {
		err = c.initSearch()
	}
	if err != nil {
		c.db.Close()
	}
	return err
}
func removePlain(path string) error {
	for _, suffix := range []string{".plain", ".plain-journal", ".plain-wal", ".plain-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func removeSecure(path string) error {
	for _, suffix := range []string{".secure", ".secure-journal", ".secure-wal", ".secure-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// PurgePlain removes an old unencrypted cache; kept for explicit cleanup.
func PurgePlain(path string) error { return removePlain(path) }

// Protect copies a live plaintext history cache to encrypted SQLite.
func (c *Cache) Protect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.encrypted {
		return nil
	}

	if err := securedb.CopyEncrypted(c.db, c.path+".secure", c.account, c.protection); err != nil {
		return err
	}
	if err := c.db.Close(); err != nil {
		return err
	}
	c.db = nil
	return c.open()
}

// ProtectFile encrypts a cache that has no active Store and preserves all
// tables, including messages and media.
func ProtectFile(path, account string, protection *security.Manager) error {
	plain, secure := path+".plain", path+".secure"
	if _, err := os.Stat(plain); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(secure); err == nil {
		return removePlain(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := sql.Open("sqlite3", plain)
	if err != nil {
		return err
	}
	err = securedb.CopyEncrypted(db, secure, account, protection)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return removePlain(path)
}

// Unprotect switches a live history cache to its plaintext copy under the
// cache mutex, so writes cannot go to the old database after the copy.
func (c *Cache) Unprotect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.encrypted {
		return nil
	}
	if err := securedb.CopyPlain(c.db, c.path+".plain"); err != nil {
		return err
	}
	if err := c.db.Close(); err != nil {
		return err
	}
	c.db = nil
	if err := c.open(); err != nil {
		return err
	}
	return removeSecure(c.path)
}
func (c *Cache) Close() error {
	if c.unregister != nil {
		c.unregister()
	}
	if c.unregisterPlain != nil {
		c.unregisterPlain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.db.Close()
}
func (c *Cache) Put(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.db.ExecContext(ctx, `INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, b)
	return err
}
func (c *Cache) Get(ctx context.Context, key string, value any) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b []byte
	err := c.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key=?`, key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, value)
}
func (c *Cache) SaveMessages(ctx context.Context, msgs []model.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range msgs {
		if m.Key.AccountID != c.account {
			return errors.New("historycache: wrong account")
		}
		if m.Key.ChatID > -1000000000000 {
			var deleted int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM global_deletions WHERE id=?`, m.Key.MessageID).Scan(&deleted); err != nil {
				return err
			}
			if deleted != 0 {
				continue
			}
		}
		if c.keepEdits && !m.Outgoing {
			if err = keepEdit(ctx, tx, m); err != nil {
				return err
			}
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		// Tombstones prevent a late history response from resurrecting a deletion.
		_, err = tx.ExecContext(ctx, `INSERT INTO messages(chat,id,payload) VALUES(?,?,?) ON CONFLICT(chat,id) DO UPDATE SET payload=excluded.payload WHERE messages.deleted=0`, m.Key.ChatID, m.Key.MessageID, b)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (c *Cache) Delete(ctx context.Context, chat int64, ids []int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, e := c.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, id := range ids {
		if chat == 0 { // Non-channel message IDs are account-wide; channels are a separate namespace.
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO global_deletions VALUES(?)`, id); e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `UPDATE messages SET payload=NULL,deleted=1 WHERE id=? AND chat > -1000000000000`, id)
		} else {
			_, e = tx.ExecContext(ctx, `INSERT INTO messages(chat,id,deleted) VALUES(?,?,1) ON CONFLICT(chat,id) DO UPDATE SET payload=NULL,deleted=1`, chat, id)
		}
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Around reads a bounded window containing the logical anchor (or the latest
// page), within the anchor's span (or the newest): see Span.
func (c *Cache) Around(ctx context.Context, chat int64, anchor int, limit int) ([]model.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	low, high, ok, err := c.bounds(ctx, chat, anchor)
	if err != nil || !ok {
		return nil, err
	}
	var rows *sql.Rows
	if anchor == 0 {
		rows, err = c.db.QueryContext(ctx, `SELECT payload FROM (SELECT id,payload FROM messages WHERE chat=? AND deleted!=1 AND id>=? AND id<=? ORDER BY id DESC LIMIT ?) ORDER BY id`, chat, low, high, limit)
	} else {
		rows, err = c.db.QueryContext(ctx, `SELECT payload FROM (SELECT id,payload FROM (SELECT id,payload FROM messages WHERE chat=? AND deleted!=1 AND id<=? AND id>=? ORDER BY id DESC LIMIT ?) UNION ALL SELECT id,payload FROM (SELECT id,payload FROM messages WHERE chat=? AND deleted!=1 AND id>? AND id<=? ORDER BY id LIMIT ?)) ORDER BY id`, chat, anchor, low, limit/2, chat, anchor, high, limit/2)
	}
	if err != nil {
		return nil, err
	}
	return readMessages(rows)
}

// readMessages reads the payloads of rows, and closes them.
func readMessages(rows *sql.Rows) ([]model.Message, error) {
	defer rows.Close()
	var out []model.Message
	var err error
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var m model.Message
		if err = json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Message returns one message of chat; ok is false when the cache does not
// have it, or has it deleted.
func (c *Cache) Message(ctx context.Context, chat int64, id int) (m model.Message, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b []byte
	err = c.db.QueryRowContext(ctx, `SELECT payload FROM messages WHERE chat=? AND id=? AND deleted!=1`, chat, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	return m, true, json.Unmarshal(b, &m)
}
func (c *Cache) SaveView(ctx context.Context, v model.Viewport, ls []model.MessageLayout) error {
	if v.AccountID != c.account {
		return errors.New("historycache: wrong viewport account")
	}
	if err := c.Put(ctx, fmt.Sprintf("viewport/%d", v.ChatID), v); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, e := c.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, l := range ls {
		env, _ := json.Marshal(l.Environment)
		_, e = tx.ExecContext(ctx, `INSERT INTO layouts VALUES(?,?,?,?,?) ON CONFLICT(chat,id,env) DO UPDATE SET revision=excluded.revision,height=excluded.height`, l.Key.ChatID, l.Key.MessageID, string(env), l.ContentRevision, l.HeightPx)
		if e != nil {
			return e
		}
	}
	// Keep only a few environment generations; measurements are disposable.
	_, e = tx.ExecContext(ctx, `DELETE FROM layouts WHERE rowid IN (SELECT rowid FROM layouts ORDER BY rowid DESC LIMIT -1 OFFSET 50000)`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (c *Cache) Layouts(ctx context.Context, chat int64, env model.RenderEnvironment) ([]model.MessageLayout, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(env)
	rows, e := c.db.QueryContext(ctx, `SELECT id,revision,height FROM layouts WHERE chat=? AND env=?`, chat, string(b))
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
func (c *Cache) Media(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b []byte
	e := c.db.QueryRowContext(ctx, `SELECT data FROM media WHERE key=?`, key).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return b, e
}
func (c *Cache) SaveMedia(ctx context.Context, key string, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, e := c.db.ExecContext(ctx, `INSERT OR REPLACE INTO media VALUES(?,?,?)`, key, b, time.Now().UnixNano())
	if e != nil {
		return e
	}
	// Account-scoped 256 MiB disk budget, including originals needed by the external player.
	_, e = c.db.ExecContext(ctx, `DELETE FROM media WHERE key IN (SELECT key FROM (SELECT key,SUM(length(data)) OVER (ORDER BY used DESC) AS total FROM media) WHERE total>268435456)`)
	return e
}

// Page reads adjacent cached messages without requiring a Telegram connection.
// Page reads up to limit messages next to anchor, oldest first: before it
// when dir is negative, after it otherwise; only from the anchor's span (see
// Span), so a page never reaches across messages the cache lacks.
func (c *Cache) Page(ctx context.Context, chat int64, anchor, dir, limit int) ([]model.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	low, high, ok, e := c.bounds(ctx, chat, anchor)
	if e != nil || !ok {
		return nil, e
	}
	query := `SELECT payload FROM (SELECT id,payload FROM messages WHERE chat=? AND deleted!=1 AND id<? AND id>=? ORDER BY id DESC LIMIT ?) ORDER BY id`
	bound := low
	if dir > 0 {
		query = `SELECT payload FROM messages WHERE chat=? AND deleted!=1 AND id>? AND id<=? ORDER BY id LIMIT ?`
		bound = high
	}
	rows, e := c.db.QueryContext(ctx, query, chat, anchor, bound, limit)
	if e != nil {
		return nil, e
	}
	return readMessages(rows)
}

// photoWhere selects photos and videos. Payloads are JSON stored as BLOBs,
// which SQLite would read as JSONB without the cast. The partial index above
// uses the same expression, is built over existing rows when created, and is
// what makes a gallery query cheap in a large chat.
const photoWhere = `json_extract(CAST(payload AS TEXT),'$.Kind') IN (1,2)`

// Photos reads a page of cached photo messages next to anchor, oldest
// first: before it when dir is negative, after it otherwise.
func (c *Cache) Photos(ctx context.Context, chat int64, anchor, dir, limit int) ([]model.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	query := `SELECT payload FROM (SELECT id,payload FROM messages WHERE chat=? AND ` + photoWhere + ` AND deleted!=1 AND id<? ORDER BY id DESC LIMIT ?) ORDER BY id`
	if dir > 0 {
		query = `SELECT payload FROM messages WHERE chat=? AND ` + photoWhere + ` AND deleted!=1 AND id>? ORDER BY id LIMIT ?`
	}
	rows, e := c.db.QueryContext(ctx, query, chat, anchor, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var m model.Message
		if e = json.Unmarshal(b, &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Reconcile removes messages absent from an authoritative server interval,
// or keeps them marked deleted when deleted messages are kept.
// IDs touched by newer live updates must be included in keep by the caller.
func (c *Cache) Reconcile(ctx context.Context, chat int64, low, high int, keep []int) (removed []int, kept []model.Message, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, e := json.Marshal(keep)
	if e != nil {
		return nil, nil, e
	}
	if c.keepDeleted {
		tx, e := c.db.BeginTx(ctx, nil)
		if e != nil {
			return nil, nil, e
		}
		defer tx.Rollback()
		rows, e := tx.QueryContext(ctx, `SELECT id FROM messages WHERE chat=? AND deleted=0 AND payload IS NOT NULL AND id>=? AND id<=? AND id NOT IN (SELECT value FROM json_each(?))`, chat, low, high, string(b))
		if e != nil {
			return nil, nil, e
		}
		var ids []int
		for rows.Next() {
			var id int
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, nil, e
			}
			ids = append(ids, id)
		}
		rows.Close()
		var kept []model.Message
		for _, id := range ids {
			m, ok, e := markDeleted(ctx, tx, chat, id)
			if e != nil {
				return nil, nil, e
			}
			if ok {
				kept = append(kept, m)
			}
		}
		return nil, kept, tx.Commit()
	}
	rows, e := c.db.QueryContext(ctx, `UPDATE messages SET payload=NULL,deleted=1 WHERE chat=? AND deleted=0 AND id>=? AND id<=? AND id NOT IN (SELECT value FROM json_each(?)) RETURNING id`, chat, low, high, string(b))
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if e = rows.Scan(&id); e != nil {
			return nil, nil, e
		}
		ids = append(ids, id)
	}
	return ids, nil, rows.Err()
}
