// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"komarugram/internal/messenger/security"
)

func randomMedia(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// openProtected opens a cache in dir, encrypted under p when it is given.
func openProtected(t *testing.T, dir string, p *security.Manager) *Cache {
	t.Helper()
	c, err := Open(filepath.Join(dir, "history"), "a", p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// protection is local-data protection, enabled, kept in dir; nil when not
// protected.
func protection(t *testing.T, dir string, protected bool) *security.Manager {
	t.Helper()
	if !protected {
		return nil
	}
	p, err := security.OpenPath(filepath.Join(dir, "security"), &fakeTPM{})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enable(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	return p
}

// mediaFilesOf are the files under the cache's media directory.
func mediaFilesOf(t *testing.T, c *Cache) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	filepath.WalkDir(c.path+".media", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out[path], _ = os.ReadFile(path)
		}
		return nil
	})
	return out
}

// Large media are files, sealed when the cache is, and read back after the
// cache opens again; small ones stay in the database.
func TestLargeMediaAreFiles(t *testing.T) {
	ctx := context.Background()
	for _, protected := range []bool{false, true} {
		dir := t.TempDir()
		p := protection(t, dir, protected)
		c := openProtected(t, dir, p)
		large, small := randomMedia(3*MediaChunk+77, 1), randomMedia(1000, 2)
		if err := c.SaveMedia(ctx, "large", large); err != nil {
			t.Fatal(err)
		}
		if err := c.SaveMedia(ctx, "small", small); err != nil {
			t.Fatal(err)
		}
		var rows int
		c.db.QueryRow(`SELECT count(*) FROM media WHERE key='large'`).Scan(&rows)
		if rows != 0 {
			t.Fatal("a large medium in the table")
		}
		files := mediaFilesOf(t, c)
		if len(files) != 1 {
			t.Fatalf("%d media files", len(files))
		}
		for path, raw := range files {
			if strings.Contains(filepath.Base(path), "large") {
				t.Fatal("the file is named by its medium")
			}
			if protected && bytes.Contains(raw, large[:64]) {
				t.Fatal("a protected medium kept as it is")
			}
		}
		c.Close()
		c = openProtected(t, dir, p)
		for key, want := range map[string][]byte{"large": large, "small": small} {
			if got, err := c.Media(ctx, key); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("protected %v, %s: %v, %d bytes", protected, key, err, len(got))
			}
		}
		if got, err := c.Media(ctx, "none"); err != nil || got != nil {
			t.Fatal(got, err)
		}
		c.Close()
	}
}

// A media file written in part keeps the chunks written after the cache
// opens again, and is not a medium whole until all are there; a chunk that
// does not open is missing again.
func TestMediaFileInParts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := protection(t, dir, true)
	c := openProtected(t, dir, p)
	data := randomMedia(5*MediaChunk+3, 3)
	chunk := func(i int) []byte { return data[i*MediaChunk : min(len(data), (i+1)*MediaChunk)] }
	f, err := c.MediaFile(ctx, "video", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{4, 1, 5} {
		if err := f.WriteChunk(i, chunk(i)); err != nil {
			t.Fatal(err)
		}
	}
	if f.Complete() || !f.Has(5) || f.Has(0) {
		t.Fatal("chunks there")
	}
	if _, err := f.ReadChunk(0, nil); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	f.Close()
	if b, err := c.Media(ctx, "video"); err != nil || b != nil {
		t.Fatal("a medium in part read whole", err)
	}
	c.Close()

	c = openProtected(t, dir, p)
	defer c.Close()
	if f, err = c.MediaFile(ctx, "video", int64(len(data))); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{1, 4, 5} {
		if b, err := f.ReadChunk(i, nil); err != nil || !bytes.Equal(b, chunk(i)) {
			t.Fatalf("chunk %d after opening again: %v", i, err)
		}
	}
	for _, i := range []int{0, 2, 3} {
		f.WriteChunk(i, chunk(i))
	}
	if !f.Complete() {
		t.Fatal("not complete")
	}
	// A chunk changed on the disk.
	for path, raw := range mediaFilesOf(t, c) {
		raw[100] ^= 1
		os.WriteFile(path, raw, 0o600)
	}
	f.Close()
	if f, err = c.MediaFile(ctx, "video", int64(len(data))); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.ReadChunk(0, nil); !errors.Is(err, ErrMissing) || f.Has(0) || f.Complete() {
		t.Fatal("a changed chunk read", err)
	}
	if b, err := f.ReadChunk(1, nil); err != nil || !bytes.Equal(b, chunk(1)) {
		t.Fatal("the chunks after it", err)
	}
}

// Media files over their budget go, the oldest first, but not one in use.
func TestMediaFilesBudget(t *testing.T) {
	ctx := context.Background()
	c := openProtected(t, t.TempDir(), nil)
	defer c.Close()
	files, err := c.files()
	if err != nil {
		t.Fatal(err)
	}
	files.budget = 8 * MediaChunk
	inUse, err := c.MediaFile(ctx, "in use", MediaChunk)
	if err != nil {
		t.Fatal(err)
	}
	defer inUse.Close()
	inUse.WriteChunk(0, randomMedia(MediaChunk, 9))
	for i := range 12 {
		if err := c.SaveMedia(ctx, string(rune('a'+i)), randomMedia(MediaChunk, int64(i))); err != nil {
			t.Fatal(err)
		}
		files.mu.Lock()
		used := files.bytes
		files.mu.Unlock()
		if used > files.budget {
			t.Fatalf("%d bytes over a budget of %d", used, files.budget)
		}
	}
	if b, _ := c.Media(ctx, "a"); b != nil {
		t.Fatal("the oldest kept")
	}
	if b, _ := c.Media(ctx, "l"); b == nil {
		t.Fatal("the newest evicted")
	}
	if !inUse.Has(0) {
		t.Fatal("a file in use evicted")
	}
	if len(mediaFilesOf(t, c)) != len(files.entries) {
		t.Fatal("files of evicted media left")
	}
}

// The large media of the table move to files, the parts of streamed videos
// go, and a file no entry names is removed.
func TestMediaMoveToFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := openProtected(t, dir, nil)
	large := randomMedia(2*MediaChunk, 4)
	if _, err := c.db.Exec(`INSERT INTO media VALUES('old', ?, 1), ('v/range/0', ?, 1), ('thumb', ?, 1)`, large, large[:MediaChunk], large[:100]); err != nil {
		t.Fatal(err)
	}
	// Media enough to leave more than compactFree free once moved.
	for i := range compactFree/(1<<20) + 8 {
		if _, err := c.db.Exec(`INSERT INTO media VALUES(?, ?, 1)`, fmt.Sprintf("big/%d", i), randomMedia(1<<20, int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	size := func() int64 {
		st, err := os.Stat(c.path + ".plain")
		if err != nil {
			t.Fatal(err)
		}
		return st.Size()
	}
	full := size()
	stray := filepath.Join(c.path+".media", "ab", "abstray")
	os.MkdirAll(filepath.Dir(stray), 0o700)
	os.WriteFile(stray, []byte("x"), 0o600)
	if _, err := c.files(); err != nil {
		t.Fatal(err)
	}
	c.moving.Wait()
	var keys []string
	rows, _ := c.db.Query(`SELECT key FROM media ORDER BY key`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		keys = append(keys, k)
	}
	rows.Close()
	if len(keys) != 1 || keys[0] != "thumb" {
		t.Fatal("left in the table:", keys)
	}
	if after := size(); after > full/4 {
		t.Fatalf("the database takes %d bytes of %d after its media moved", after, full)
	}
	if b, err := c.Media(ctx, "old"); err != nil || !bytes.Equal(b, large) {
		t.Fatal("moved medium", err)
	}
	if _, err := os.Stat(stray); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a stray file kept")
	}
	c.Close()
}

// Media files kept as they are go when the cache is protected, and those
// sealed go when it no longer is.
func TestMediaFilesFollowProtection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p, err := security.OpenPath(filepath.Join(dir, "security"), &fakeTPM{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open(filepath.Join(dir, "history"), "a", p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	plain := randomMedia(2*MediaChunk, 5)
	if err := c.SaveMedia(ctx, "m", plain); err != nil {
		t.Fatal(err)
	}
	if err := p.Enable(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range mediaFilesOf(t, c) {
		if bytes.Contains(raw, plain[:64]) {
			t.Fatal("a medium kept as it is after protection")
		}
	}
	if b, _ := c.Media(ctx, "m"); b != nil {
		t.Fatal("a medium of before the protection read")
	}
	sealed := randomMedia(2*MediaChunk, 6)
	if err := c.SaveMedia(ctx, "s", sealed); err != nil {
		t.Fatal(err)
	}
	if b, err := c.Media(ctx, "s"); err != nil || !bytes.Equal(b, sealed) {
		t.Fatal("a medium after the protection", err)
	}
	p.SetUnmigration(func() error { return nil })
	if err := p.Disable(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if len(mediaFilesOf(t, c)) != 0 {
		t.Fatal("sealed files kept without their key")
	}
}

// A medium read in part takes only the segments read; segments swapped, or
// a segment of another medium put in place of one, do not open.
func TestMediaSegments(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := openProtected(t, dir, protection(t, dir, true))
	defer c.Close()
	const chunks = 3*segmentChunks + 2
	data := randomMedia(chunks*MediaChunk, 7)
	chunk := func(i int) []byte { return data[i*MediaChunk : (i+1)*MediaChunk] }
	f, err := c.MediaFile(ctx, "long", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	f.WriteChunk(2*segmentChunks+5, chunk(2*segmentChunks+5))
	if files := mediaFilesOf(t, c); len(files) != 1 {
		t.Fatalf("%d files for a chunk", len(files))
	}
	for _, i := range []int{0, segmentChunks} {
		f.WriteChunk(i, chunk(i))
	}
	f.Close()
	other, _ := c.MediaFile(ctx, "other", int64(len(data)))
	other.WriteChunk(0, chunk(0))
	other.Close()

	files, _ := c.files()
	name, otherName := files.nameOf("long"), files.nameOf("other")
	seg := func(name string, k int) string { return files.pathOf(name, k) }
	// The first two segments of long swapped.
	os.Rename(seg(name, 0), seg(name, 9))
	os.Rename(seg(name, 1), seg(name, 0))
	os.Rename(seg(name, 9), seg(name, 1))
	if f, err = c.MediaFile(ctx, "long", int64(len(data))); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, segmentChunks} {
		if _, err := f.ReadChunk(i, nil); !errors.Is(err, ErrMissing) {
			t.Fatalf("chunk %d of a segment swapped: %v", i, err)
		}
	}
	if b, err := f.ReadChunk(2*segmentChunks+5, nil); err != nil || !bytes.Equal(b, chunk(2*segmentChunks+5)) {
		t.Fatal("the segment left in place", err)
	}
	f.WriteChunk(0, chunk(0))
	f.Close()
	// other's first segment, sealed under the same key, in place of long's.
	raw, _ := os.ReadFile(seg(otherName, 0))
	os.WriteFile(seg(name, 0), raw, 0o600)
	if f, err = c.MediaFile(ctx, "long", int64(len(data))); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.ReadChunk(0, nil); !errors.Is(err, ErrMissing) {
		t.Fatal("a segment of another medium read", err)
	}
}
