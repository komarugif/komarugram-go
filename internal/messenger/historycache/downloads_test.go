// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/security"
)

// Downloads are kept, the newest first, found by their medium, and
// forgotten; the key of their tags outlives the cache opening again and
// the protection being enabled.
func TestDownloads(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p, err := security.OpenPath(filepath.Join(dir, "security"), &fakeTPM{})
	if err != nil {
		t.Fatal(err)
	}
	c := openProtected(t, dir, p)
	now := time.Now()
	const size = 3*MediaChunk + 1
	tags, err := NewTags(size)
	if err != nil {
		t.Fatal(err)
	}
	tags = append(tags, make([]byte, 4*16)...)
	older := &Download{Chat: 1, Message: 2, Media: "m", Name: "a.pdf", Path: "/d/a.pdf", Size: size, Modified: now, Added: now.Add(-time.Hour), Tags: tags}
	newer := &Download{Chat: 1, Message: 3, Media: "m", Name: "a.pdf", Path: "/d/a (1).pdf", Size: size, Modified: now, Added: now, Tags: tags}
	for _, d := range []*Download{older, newer} {
		if err := c.SaveDownload(ctx, d); err != nil || d.ID == 0 {
			t.Fatal(d.ID, err)
		}
	}
	all, err := c.Downloads(ctx)
	if err != nil || len(all) != 2 || all[0].ID != newer.ID || !all[0].Modified.Equal(now) || !bytes.Equal(all[1].Tags, tags) {
		t.Fatalf("%+v, %v", all, err)
	}
	if d, ok, _ := c.DownloadOf(ctx, "m"); !ok || d.Path != newer.Path {
		t.Fatal("download of the medium", d, ok)
	}
	if d := all[0]; d.Chunks() != 4 || len(d.Tag(0)) != 16 || d.Tag(4) != nil {
		t.Fatal("chunks of the tags", d.Chunks())
	}
	key, err := c.DownloadKey(ctx)
	if err != nil || len(key) != 32 {
		t.Fatal(key, err)
	}
	c.Close()
	c = openProtected(t, dir, p)
	if again, _ := c.DownloadKey(ctx); !bytes.Equal(again, key) {
		t.Fatal("another key after opening again")
	}
	if err := p.Enable(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if again, _ := c.DownloadKey(ctx); !bytes.Equal(again, key) {
		t.Fatal("another key after the protection")
	}
	if err := c.DeleteDownloads(ctx, []int64{newer.ID}); err != nil {
		t.Fatal(err)
	}
	if d, ok, _ := c.DownloadOf(ctx, "m"); !ok || d.ID != older.ID {
		t.Fatal("after deleting the newer", d, ok)
	}
	c.Close()
}

// A table of downloads made before topics were kept gets the column: its
// records read with no topic, new ones keep theirs.
func TestDownloadsTopicColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history")
	db, err := sql.Open("sqlite3", path+".plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE downloads(id INTEGER PRIMARY KEY,chat INTEGER,message INTEGER,media TEXT,name TEXT,path TEXT,size INTEGER,modified INTEGER,added INTEGER,tags BLOB);
		INSERT INTO downloads(chat,message,media,name,path,size,modified,added,tags) VALUES(1,2,'old','a','/a',1,0,0,x'00')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	c, err := Open(path, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if d, ok, err := c.DownloadOf(ctx, "old"); err != nil || !ok || d.Topic != 0 {
		t.Fatal(d, ok, err)
	}
	d := &Download{Chat: 1, Message: 3, Topic: 77, Media: "new", Added: time.Now()}
	if err := c.SaveDownload(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := c.DownloadOf(ctx, "new"); got.Topic != 77 {
		t.Fatal("topic", got.Topic)
	}
}
