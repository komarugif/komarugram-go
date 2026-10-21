// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"komarugram/pkg/sealedfile"
)

// Download is a file the user downloaded: where it is, what it was when it
// was written there, and a tag of each of its chunks, which tell a chunk read
// back from one changed since. The file is the user's: nothing deletes it
// but the user.
type Download struct {
	ID      int64
	Chat    int64
	Message int
	// Media is the medium's id; Name the name the message gave it.
	Media, Name, Path string
	Size              int64
	// Modified is the file's modification time once written.
	Modified time.Time
	Added    time.Time
	// Tags are the algorithm, a salt and a tag a chunk: see Tagger.
	Tags []byte
}

const tagsHeader = 1 + 16

// NewTags starts the tags of a download of size bytes under a salt of its
// own, so that no two contents are ever tagged under one key and nonce.
func NewTags(size int64) ([]byte, error) {
	chunks := (size + MediaChunk - 1) / MediaChunk
	tags := make([]byte, tagsHeader, tagsHeader+chunks*sealedfile.TagSize)
	tags[0] = byte(sealedfile.Fastest())
	if _, err := rand.Read(tags[1:tagsHeader]); err != nil {
		return nil, err
	}
	return tags, nil
}

// Tagger tags the chunks of d with key (DownloadKey).
func (d *Download) Tagger(key []byte) (*sealedfile.Tagger, error) {
	if len(d.Tags) < tagsHeader {
		return nil, sealedfile.ErrCorrupt
	}
	return sealedfile.NewTagger(key, sealedfile.Algorithm(d.Tags[0]), "download "+hex.EncodeToString(d.Tags[1:tagsHeader]))
}

// Tag is the tag of chunk i of d.
func (d *Download) Tag(i int) []byte {
	at := tagsHeader + i*sealedfile.TagSize
	if i < 0 || at+sealedfile.TagSize > len(d.Tags) {
		return nil
	}
	return d.Tags[at : at+sealedfile.TagSize]
}

// Chunks is the number of chunks of d.
func (d *Download) Chunks() int { return int((d.Size + MediaChunk - 1) / MediaChunk) }

// DownloadKey is the key the tags of downloads are made with: a random one
// the database keeps, encrypted with it when the cache is protected, so that
// it outlives the protection being enabled or disabled. Without protection
// it tells a file changed by mistake, not by one who reads the database.
func (c *Cache) DownloadKey(ctx context.Context) ([]byte, error) {
	c.downloadKeyMu.Lock()
	defer c.downloadKeyMu.Unlock()
	var key []byte
	if ok, err := c.Get(ctx, "download-key", &key); err != nil || (ok && len(key) == 32) {
		return key, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, c.Put(ctx, "download-key", key)
}

// SaveDownload keeps d, a new one when its ID is 0, which it sets.
func (c *Cache) SaveDownload(ctx context.Context, d *Download) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d.ID == 0 {
		res, err := c.db.ExecContext(ctx, `INSERT INTO downloads(chat,message,media,name,path,size,modified,added,tags) VALUES(?,?,?,?,?,?,?,?,?)`,
			d.Chat, d.Message, d.Media, d.Name, d.Path, d.Size, d.Modified.UnixNano(), d.Added.UnixNano(), d.Tags)
		if err != nil {
			return err
		}
		d.ID, err = res.LastInsertId()
		return err
	}
	_, err := c.db.ExecContext(ctx, `UPDATE downloads SET chat=?,message=?,media=?,name=?,path=?,size=?,modified=?,added=?,tags=? WHERE id=?`,
		d.Chat, d.Message, d.Media, d.Name, d.Path, d.Size, d.Modified.UnixNano(), d.Added.UnixNano(), d.Tags, d.ID)
	return err
}

// Downloads are the downloads kept, the newest first.
func (c *Cache) Downloads(ctx context.Context) ([]Download, error) {
	return c.downloads(ctx, `ORDER BY added DESC, id DESC`)
}

// DownloadOf is the newest download of medium media.
func (c *Cache) DownloadOf(ctx context.Context, media string) (Download, bool, error) {
	ds, err := c.downloads(ctx, `WHERE media=? ORDER BY added DESC, id DESC LIMIT 1`, media)
	if err != nil || len(ds) == 0 {
		return Download{}, false, err
	}
	return ds[0], true, nil
}

func (c *Cache) downloads(ctx context.Context, where string, args ...any) ([]Download, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.db.QueryContext(ctx, `SELECT id,chat,message,media,name,path,size,modified,added,tags FROM downloads `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Download
	for rows.Next() {
		var d Download
		var modified, added int64
		if err := rows.Scan(&d.ID, &d.Chat, &d.Message, &d.Media, &d.Name, &d.Path, &d.Size, &modified, &added, &d.Tags); err != nil {
			return nil, err
		}
		d.Modified, d.Added = time.Unix(0, modified), time.Unix(0, added)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDownloads forgets the downloads of ids; their files are the
// caller's to remove.
func (c *Cache) DeleteDownloads(ctx context.Context, ids []int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM downloads WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
