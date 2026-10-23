// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"gioui.org/io/pointer"

	"komarugram/internal/messenger/model"
)

// halfStore streams files whose first half comes at once and whose rest
// never does.
type halfStore struct {
	*menuStore
}

type halfReader struct {
	ctx  context.Context
	half int64
}

func (r halfReader) ReadAt(p []byte, off int64) (int, error) {
	if off < r.half {
		return copy(p, make([]byte, r.half-off)), nil
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (s *halfStore) MediaStream(ctx context.Context, m model.Message) (io.ReaderAt, int64, func(), error) {
	return halfReader{ctx: ctx, half: m.Media.Size / 2}, m.Media.Size, func() {}, nil
}

// A file on its way shows how much of it came; a click on it cancels it,
// which tells nothing, and leaves nothing of it.
func TestFileDownloadCancels(t *testing.T) {
	const size = 2 << 20
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		m := &messages[1]
		m.Kind, m.Text = model.MessageFile, ""
		m.Media = &model.MessageMedia{ID: "document/1", MIMEType: "application/zip", Size: size, FileName: "build.zip"}
	}, func(s *menuStore) model.ConversationStore { return &halfStore{menuStore: s} })
	key := model.MessageKey{ChatID: 1, MessageID: 2}
	waitFor := func(what string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
			h.frame()
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.frame()
	h.press(pointer.ButtonPrimary, h.messageAt(2))
	waitFor("the file did not start", func() bool {
		if h.page.files == nil {
			return false
		}
		done, total, ok := h.page.files.download(key)
		return ok && done == size/2 && total == size
	})
	dir := h.page.files.dir
	h.press(pointer.ButtonPrimary, h.messageAt(2))
	waitFor("the file was not cancelled", func() bool {
		_, _, ok := h.page.files.download(key)
		return !ok
	})
	h.frames(2)
	if text := h.page.toast.Text(); text != "" {
		t.Fatalf("a cancelled file told %q", text)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left %d files", len(left))
	}
}
