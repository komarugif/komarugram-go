// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// downloadStore is a store whose Telegram serves data as the file of
// medium "doc", counting the parts asked for.
func downloadStore(t *testing.T, data []byte) (*Store, *atomic.Int32) {
	t.Helper()
	s := testStore(t)
	var parts atomic.Int32
	s.history.refs["doc"] = fileLocation{ID: 1, DC: 2}
	s.history.pool = testMediaPool{tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.UploadGetFileRequest)
		if !ok {
			return fmt.Errorf("unexpected %T", in)
		}
		parts.Add(1)
		end := min(int64(len(data)), req.Offset+int64(req.Limit))
		buffer := new(bin.Buffer)
		if e := (&tg.UploadFile{Type: &tg.StorageFilePartial{}, Bytes: data[req.Offset:end]}).Encode(buffer); e != nil {
			return e
		}
		return out.Decode(buffer)
	}))}
	return s, &parts
}

// A file downloads into the directory as it was sent, under a free name;
// downloaded again, it is found there.
func TestDownload(t *testing.T) {
	ctx := context.Background()
	data := make([]byte, 3*streamPartSize+10)
	rand.New(rand.NewSource(1)).Read(data)
	s, parts := downloadStore(t, data)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("the user's"), 0o644)
	m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 5, MessageID: 7}, Kind: model.MessageFile, ForumTopic: true, ReplyToTopID: 9,
		Media: &model.MessageMedia{ID: "doc", FileName: "report.pdf", Size: int64(len(data))}}
	var told int64
	path, err := s.Download(ctx, m, dir, func(done, total int64) { told = done })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "report (1).pdf" || told != int64(len(data)) {
		t.Fatal(path, told)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
		t.Fatal("the file downloaded is not the one sent")
	}
	if mine, _ := os.ReadFile(filepath.Join(dir, "report.pdf")); string(mine) != "the user's" {
		t.Fatal("a file of the user's replaced")
	}
	asked := parts.Load()
	if again, err := s.Download(ctx, m, dir, nil); err != nil || again != path || parts.Load() != asked {
		t.Fatal("downloaded again:", again, err, parts.Load()-asked)
	}
	list, err := s.Downloads(ctx)
	if err != nil || len(list) != 1 || list[0].Key != m.Key || list[0].Topic != 9 || list[0].State != model.DownloadPresent || list[0].Size != int64(len(data)) {
		t.Fatalf("%+v, %v", list, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatal("files left in the directory:", len(entries))
	}
}

// A download changed or moved shows so; deleting downloads deletes the
// files as they were written and leaves one changed since.
func TestDownloadStatesAndDeletion(t *testing.T) {
	ctx := context.Background()
	data := make([]byte, 2*streamPartSize)
	s, _ := downloadStore(t, data)
	dir := t.TempDir()
	var paths []string
	for i := range 3 {
		m := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: model.MessageID(i)}, Media: &model.MessageMedia{ID: "doc", FileName: "f.bin", Size: int64(len(data))}}
		// Each its own file: the record of the one before is changed.
		path, err := s.Download(ctx, m, filepath.Join(dir, fmt.Sprint(i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	os.WriteFile(paths[1], []byte("mine now"), 0o644)
	os.Remove(paths[2])
	list, _ := s.Downloads(ctx)
	states := map[string]model.DownloadState{}
	var ids []int64
	for _, d := range list {
		states[d.Path] = d.State
		ids = append(ids, d.ID)
	}
	if states[paths[0]] != model.DownloadPresent || states[paths[1]] != model.DownloadChanged || states[paths[2]] != model.DownloadMissing {
		t.Fatal(states)
	}
	if err := s.DeleteDownloads(ctx, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatal("a download kept")
	}
	if b, _ := os.ReadFile(paths[1]); string(b) != "mine now" {
		t.Fatal("a file changed by the user deleted")
	}
	if list, _ := s.Downloads(ctx); len(list) != 0 {
		t.Fatal("downloads remembered:", len(list))
	}
}

// Once the cache has let a medium go, its download stands in, offline too,
// each chunk checked: a chunk changed, the file's size and time kept, is
// not read.
func TestDownloadStandsInForTheCache(t *testing.T) {
	ctx := context.Background()
	data := make([]byte, 3*streamPartSize+10)
	rand.New(rand.NewSource(2)).Read(data)
	s, _ := downloadStore(t, data)
	dir := t.TempDir()
	m := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 7}, Media: &model.MessageMedia{ID: "doc", FileName: "v.mp4", Size: int64(len(data))}}
	path, err := s.Download(ctx, m, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The cache lets the medium go, and Telegram is away.
	evict := func() {
		os.RemoveAll(s.cacheOf().MediaDir())
	}
	evict()
	s.history.pool = nil
	read := func() ([]byte, error) {
		r, size, done, err := s.MediaStream(ctx, m)
		if err != nil {
			return nil, err
		}
		defer done()
		return io.ReadAll(io.NewSectionReader(r, 0, size))
	}
	if got, err := read(); err != nil || !bytes.Equal(got, data) {
		t.Fatal("read from the download:", err)
	}
	if got, err := s.Media(ctx, m); err != nil || !bytes.Equal(got, data) {
		t.Fatal("whole from the download:", err)
	}
	// Changed, keeping its size and time.
	st, _ := os.Stat(path)
	raw, _ := os.ReadFile(path)
	raw[streamPartSize+5] ^= 1
	os.WriteFile(path, raw, 0o644)
	os.Chtimes(path, time.Now(), st.ModTime())
	evict()
	if got, err := read(); err == nil || bytes.Equal(got, raw) {
		t.Fatal("a changed download read:", err)
	}
}
