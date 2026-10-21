// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"bytes"
	"context"
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
