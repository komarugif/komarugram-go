// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/savefile"
	"komarugram/pkg/sealedfile"
)

// Download writes m's file into dir as it comes, from the cache or from
// Telegram, tagging each chunk; a file downloaded before and as it was is
// found instead.
func (s *Store) Download(ctx context.Context, m model.Message, dir string, progress func(done, total int64)) (string, error) {
	if m.Media == nil || m.Media.Size <= 0 {
		return "", errors.New("download: no file")
	}
	cache := s.cacheOf()
	if cache == nil {
		return "", errors.New("download: cache unavailable")
	}
	if d, ok, err := cache.DownloadOf(ctx, m.Media.ID); err != nil {
		return "", err
	} else if ok && d.Size == m.Media.Size && downloadState(d) == model.DownloadPresent && sameDir(filepath.Dir(d.Path), dir) {
		return d.Path, nil
	}
	r, size, closeStream, err := s.MediaStream(ctx, m)
	if err != nil {
		return "", err
	}
	defer closeStream()
	key, err := cache.DownloadKey(ctx)
	if err != nil {
		return "", err
	}
	tags, err := historycache.NewTags(size)
	if err != nil {
		return "", err
	}
	d := &historycache.Download{Chat: m.Key.ChatID, Message: int(m.Key.MessageID), Media: m.Media.ID,
		Name: savefile.Name(m), Size: size, Added: time.Now(), Tags: tags}
	tagger, err := d.Tagger(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".komarugram-*.part")
	if err != nil {
		return "", err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	buf := make([]byte, historycache.MediaChunk)
	chunks := d.Chunks()
	for i := range chunks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		off := int64(i) * historycache.MediaChunk
		n := int(min(historycache.MediaChunk, size-off))
		if _, err := r.ReadAt(buf[:n], off); err != nil && !(errors.Is(err, io.EOF) && off+int64(n) == size) {
			return "", err
		}
		tag := tagger.Tag(i, buf[:n], i == chunks-1)
		d.Tags = append(d.Tags, tag[:]...)
		if _, err := tmp.Write(buf[:n]); err != nil {
			return "", err
		}
		if progress != nil {
			progress(off+int64(n), size)
		}
	}
	_ = tmp.Chmod(0o644)
	if err := tmp.Close(); err != nil {
		return "", err
	}
	path, err := savefile.Place(tmp.Name(), dir, d.Name)
	if err != nil {
		return "", err
	}
	done = true
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	d.Path, d.Modified = path, st.ModTime()
	return path, cache.SaveDownload(ctx, d)
}

// Downloads are the files downloaded, the newest first, with what became
// of each.
func (s *Store) Downloads(ctx context.Context) ([]model.DownloadedFile, error) {
	cache := s.cacheOf()
	if cache == nil {
		return nil, nil
	}
	ds, err := cache.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	s.history.mu.Lock()
	account := s.history.account
	s.history.mu.Unlock()
	out := make([]model.DownloadedFile, 0, len(ds))
	for _, d := range ds {
		out = append(out, model.DownloadedFile{
			ID:   d.ID,
			Key:  model.MessageKey{AccountID: account, ChatID: d.Chat, MessageID: model.MessageID(d.Message)},
			Name: d.Name, Path: d.Path, Size: d.Size, Added: d.Added, State: downloadState(d),
		})
	}
	return out, nil
}

// DeleteDownloads deletes the files of ids that are as they were written,
// and forgets them all: a file changed since is no longer the download's,
// and stays.
func (s *Store) DeleteDownloads(ctx context.Context, ids []int64) error {
	cache := s.cacheOf()
	if cache == nil {
		return nil
	}
	ds, err := cache.Downloads(ctx)
	if err != nil {
		return err
	}
	byID := map[int64]historycache.Download{}
	for _, d := range ds {
		byID[d.ID] = d
	}
	var errs []error
	for _, id := range ids {
		if d, ok := byID[id]; ok && downloadState(d) == model.DownloadPresent {
			if err := os.Remove(d.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	if err := cache.DeleteDownloads(ctx, ids); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Store) cacheOf() *historycache.Cache {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cache
}

// downloadState tells, from its size and time, whether d's file is as it
// was written: a hint, which the tags of its chunks prove when it is read.
func downloadState(d historycache.Download) model.DownloadState {
	st, err := os.Stat(d.Path)
	if err != nil || !st.Mode().IsRegular() {
		return model.DownloadMissing
	}
	if st.Size() != d.Size || !st.ModTime().Equal(d.Modified) {
		return model.DownloadChanged
	}
	return model.DownloadPresent
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// standIn reads the chunks of a medium from the user's download of it,
// once the cache has let it go, each checked against its tag: a file
// changed since, even keeping its size and time, is not read.
type standIn struct {
	once   sync.Once
	file   *os.File
	tagger *sealedfile.Tagger
	d      historycache.Download
	broken bool
	mu     sync.Mutex
}

// open finds the download of m, as large as size, when there is one as it
// was written.
func (si *standIn) open(ctx context.Context, cache *historycache.Cache, m model.Message, size int64) {
	si.once.Do(func() {
		d, ok, err := cache.DownloadOf(ctx, m.Media.ID)
		if err != nil || !ok || d.Size != size || downloadState(d) != model.DownloadPresent {
			return
		}
		key, err := cache.DownloadKey(ctx)
		if err != nil {
			return
		}
		tagger, err := d.Tagger(key)
		if err != nil {
			return
		}
		f, err := os.Open(d.Path)
		if err != nil {
			return
		}
		si.file, si.tagger, si.d = f, tagger, d
	})
}

// chunk is chunk i read from the download, or nil when there is no
// download or the chunk is not the one tagged: the download is not read
// again then.
func (si *standIn) chunk(i int) []byte {
	si.mu.Lock()
	broken := si.broken
	si.mu.Unlock()
	if si.file == nil || broken || i < 0 || i >= si.d.Chunks() {
		return nil
	}
	off := int64(i) * historycache.MediaChunk
	b := make([]byte, min(historycache.MediaChunk, si.d.Size-off))
	if _, err := si.file.ReadAt(b, off); err != nil && !(errors.Is(err, io.EOF) && off+int64(len(b)) == si.d.Size) {
		return nil
	}
	if !si.tagger.Check(i, b, i == si.d.Chunks()-1, si.d.Tag(i)) {
		si.mu.Lock()
		si.broken = true
		si.mu.Unlock()
		return nil
	}
	return b
}

func (si *standIn) close() {
	if si.file != nil {
		si.file.Close()
	}
}

// mediaFromDownload is m whole from the user's download of it, every chunk
// checked, or nil.
func mediaFromDownload(ctx context.Context, cache *historycache.Cache, m model.Message) []byte {
	var si standIn
	si.open(ctx, cache, m, m.Media.Size)
	defer si.close()
	if si.file == nil {
		return nil
	}
	out := make([]byte, 0, m.Media.Size)
	for i := range si.d.Chunks() {
		b := si.chunk(i)
		if b == nil {
			return nil
		}
		out = append(out, b...)
	}
	return out
}
