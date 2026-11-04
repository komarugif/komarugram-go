// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOutgoingCopyAtomicAndProtected(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "protected"}[protected], func(t *testing.T) {
			dir := t.TempDir()
			c := openProtected(t, dir, protection(t, dir, protected))
			defer c.Close()
			ctx := context.Background()
			path := filepath.Join(dir, "original")
			data := bytes.Repeat([]byte("private outgoing attachment"), 30000)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := c.PutOutgoing(ctx, 1, "request", []string{path, path + ".missing"}); err == nil {
				t.Fatal("missing file accepted")
			}
			if rows, err := c.Outgoing(ctx); err != nil || len(rows) != 0 {
				t.Fatal("partial outgoing copy was published", err)
			}
			if err := c.PutOutgoing(ctx, 1, "request", []string{path}); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			var restored bytes.Buffer
			if err := c.WriteOutgoingFile(ctx, 1, 0, &restored); err != nil || !bytes.Equal(restored.Bytes(), data) {
				t.Fatal("private copy lost", err)
			}
			if protected {
				b, err := os.ReadFile(c.path + ".secure")
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(b, []byte("private outgoing attachment")) {
					t.Fatal("outbox attachment was stored unencrypted")
				}
			}
			if err := c.SaveLocalMedia(ctx, "premium", data[:100]); err != nil {
				t.Fatal(err)
			}
			c.mediaBudget = 1
			if err := c.SaveMedia(ctx, "evicted", []byte("ordinary media")); err != nil {
				t.Fatal(err)
			}
			if b, err := c.LocalMedia(ctx, "premium"); err != nil || !bytes.Equal(b, data[:100]) {
				t.Fatal("local Premium media evicted", err)
			}
			if err := c.DropOutgoing(ctx, 1); err != nil {
				t.Fatal(err)
			}
			restored.Reset()
			if err := c.WriteOutgoingFile(ctx, 1, 0, &restored); err != nil || restored.Len() != 0 {
				t.Fatal("outgoing copy not removed", err)
			}
		})
	}
}

// Queuing a long file leaves the cache to others, and a copy that the client
// stopped making goes when the account opens.
func TestOutgoingCopyLeavesTheCacheFree(t *testing.T) {
	dir := t.TempDir()
	c := openProtected(t, dir, protection(t, dir, false))
	defer c.Close()
	ctx := context.Background()
	path := filepath.Join(dir, "long")
	if err := os.WriteFile(path, make([]byte, 64<<20), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.PutOutgoing(ctx, 1, "request", []string{path}) }()
	// A read that gets in and sees part of the copy proves it was let in.
	for {
		var n int
		c.mu.Lock()
		err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE id=1`).Scan(&n)
		var parts int
		if err == nil {
			err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_files`).Scan(&parts)
		}
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			t.Fatal("the copy kept the cache until it ended")
		}
		if parts > 0 {
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if _, err := c.db.ExecContext(ctx, `INSERT INTO outbox_files(id,file,part,data) VALUES(2,0,0,x'00')`); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Outgoing(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%d requests, %v", len(rows), err)
	}
	var orphans int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_files WHERE id=2`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("%d parts of no request, %v", orphans, err)
	}
	var restored countingWriter
	if err := c.WriteOutgoingFile(ctx, 1, 0, &restored); err != nil || restored != 64<<20 {
		t.Fatalf("restored %d bytes, %v", restored, err)
	}
}

type countingWriter int

func (w *countingWriter) Write(b []byte) (int, error) {
	*w += countingWriter(len(b))
	return len(b), nil
}
