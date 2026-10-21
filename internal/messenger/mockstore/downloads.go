// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/savefile"
)

// demoDownloads are the files the demo downloaded, for as long as it runs.
type demoDownloads struct {
	mu   sync.Mutex
	list []demoDownload
	next int64
}

type demoDownload struct {
	file     model.DownloadedFile
	modified time.Time
}

// Download writes m's file into dir, as Telegram's store does.
func (s *Store) Download(ctx context.Context, m model.Message, dir string, progress func(done, total int64)) (string, error) {
	data, err := s.media(ctx, m)
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
	_, err = tmp.Write(data)
	_ = tmp.Chmod(0o644)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	name := savefile.Name(m)
	path, err := savefile.Place(tmp.Name(), dir, name)
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if progress != nil {
		progress(int64(len(data)), int64(len(data)))
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	s.downloads.mu.Lock()
	defer s.downloads.mu.Unlock()
	s.downloads.next++
	s.downloads.list = append(s.downloads.list, demoDownload{
		file:     model.DownloadedFile{ID: s.downloads.next, Key: m.Key, Name: name, Path: path, Size: int64(len(data)), Added: time.Now()},
		modified: st.ModTime(),
	})
	return path, nil
}

// Downloads are the files the demo downloaded, the newest first.
func (s *Store) Downloads(context.Context) ([]model.DownloadedFile, error) {
	s.downloads.mu.Lock()
	defer s.downloads.mu.Unlock()
	out := make([]model.DownloadedFile, 0, len(s.downloads.list))
	for i := len(s.downloads.list) - 1; i >= 0; i-- {
		d := s.downloads.list[i]
		d.file.State = demoState(d)
		out = append(out, d.file)
	}
	return out, nil
}

// DeleteDownloads deletes the files of ids as they were downloaded.
func (s *Store) DeleteDownloads(_ context.Context, ids []int64) error {
	gone := map[int64]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	s.downloads.mu.Lock()
	defer s.downloads.mu.Unlock()
	var errs []error
	kept := s.downloads.list[:0]
	for _, d := range s.downloads.list {
		if !gone[d.file.ID] {
			kept = append(kept, d)
			continue
		}
		if demoState(d) == model.DownloadPresent {
			if err := os.Remove(d.file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	s.downloads.list = kept
	return errors.Join(errs...)
}

func demoState(d demoDownload) model.DownloadState {
	st, err := os.Stat(d.file.Path)
	if err != nil {
		return model.DownloadMissing
	}
	if st.Size() != d.file.Size || !st.ModTime().Equal(d.modified) {
		return model.DownloadChanged
	}
	return model.DownloadPresent
}
