// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/video"
)

// boxMedia is what the viewer of the box of files reads: the files about to
// go, from the disk. The store is there for what a viewer asks of any source;
// embedded as the interface, it hides all a store may do besides, so that the
// viewer finds no galleries to page and nothing to forward with.
type boxMedia struct {
	model.ConversationStore
	// mu guards files, read by the viewer's workers as the editor adds to them.
	mu    sync.RWMutex
	files map[string]boxMediaFile
	// owners are the files of the box by the message each is shown as.
	owners map[model.MessageID]*boxFile
	// edits counts the results of the editor, whose media each are new to the
	// viewer: a file that changed is another picture.
	edits int
	// ffmpeg is the FFmpeg the user set; replaced tells the box a file of it
	// now lies at another path.
	ffmpeg   func() string
	replaced func(old, path string)
}

// file is what a medium's id reads, if it is of the box.
func (s *boxMedia) file(id string) (boxMediaFile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.files[id]
	return f, ok
}

// boxMediaFile is the file of a medium: at path, or thumb for its thumbnail.
type boxMediaFile struct {
	path  string
	photo bool
	size  int64
	thumb *image.RGBA
}

// Media implements model.ConversationStore: the file, as it is shown.
func (s *boxMedia) Media(_ context.Context, m model.Message) ([]byte, error) {
	if m.Media == nil {
		return nil, errors.New("no media")
	}
	f, ok := s.file(m.Media.ID)
	switch {
	case !ok:
		return nil, fmt.Errorf("no file %q", m.Media.ID)
	case f.thumb != nil:
		var b bytes.Buffer
		err := jpeg.Encode(&b, f.thumb, &jpeg.Options{Quality: 90})
		return b.Bytes(), err
	case f.photo:
		return sendfiles.ShownPhoto(f.path)
	}
	return os.ReadFile(f.path)
}

// MediaStream reads a video where it lies, however large it is.
func (s *boxMedia) MediaStream(_ context.Context, m model.Message) (io.ReaderAt, int64, func(), error) {
	if m.Media == nil {
		return nil, 0, nil, errors.New("no media")
	}
	f, ok := s.file(m.Media.ID)
	if !ok {
		return nil, 0, nil, fmt.Errorf("no file %q", m.Media.ID)
	}
	file, err := os.Open(f.path)
	if err != nil {
		return nil, 0, nil, err
	}
	return file, f.size, func() { file.Close() }, nil
}

// boxViewable tells whether f opens in the viewer: what it shows when it goes
// in a message. Music and other files have no place there.
func boxViewable(f *boxFile) bool {
	if !f.ready {
		return false
	}
	switch f.Kind {
	case sendfiles.KindPhoto, sendfiles.KindVideo, sendfiles.KindAnimation:
		return true
	}
	return false
}

// boxGallery is what the viewer goes through opened on chosen of files: those
// that open, in the box's order, and where it reads them from. They are
// messages by the file's place in the box, which no chat knows.
func boxGallery(source model.ConversationStore, chat int64, files []*boxFile, chosen *boxFile) (media *boxMedia, items []model.Message, current model.Message) {
	media = &boxMedia{ConversationStore: source, files: map[string]boxMediaFile{}, owners: map[model.MessageID]*boxFile{}}
	for i, f := range files {
		if !boxViewable(f) {
			continue
		}
		m := model.Message{Key: model.MessageKey{ChatID: chat, MessageID: model.MessageID(i + 1)}, SenderName: f.Name}
		media.owners[m.Key.MessageID] = f
		m.Kind, m.Media = media.add(fmt.Sprintf("box/%d", i), f.File, f.thumb, f.shown, f.long)
		items = append(items, m)
		if f == chosen {
			current = m
		}
	}
	return media, items, current
}

// add makes the medium of a file, what the viewer shows it as, and reads it from
// the disk under id.
func (s *boxMedia) add(id string, f sendfiles.File, picture *image.RGBA, shown image.Point, long time.Duration) (model.MessageKind, *model.MessageMedia) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := &model.MessageMedia{ID: id, FileName: f.Name, MIMEType: f.MIME, Size: f.Size, Width: f.Width, Height: f.Height}
	var kind model.MessageKind
	switch f.Kind {
	case sendfiles.KindPhoto:
		kind = model.MessagePhoto
	case sendfiles.KindVideo:
		kind = model.MessageVideo
		meta.Width, meta.Height, meta.Duration = shown.X, shown.Y, long
	case sendfiles.KindAnimation:
		kind = model.MessageGIF
		if shown.X > 0 {
			meta.Width, meta.Height = shown.X, shown.Y
		}
		meta.Duration = long
	}
	s.files[id] = boxMediaFile{path: f.Path, photo: f.Kind == sendfiles.KindPhoto, size: f.Size}
	if picture != nil {
		if meta.Width <= 0 || meta.Height <= 0 {
			// What FFmpeg did not tell, or a GIF, whose size nothing read.
			meta.Width, meta.Height = picture.Bounds().Dx(), picture.Bounds().Dy()
		}
		if f.Kind != sendfiles.KindPhoto {
			thumb := id + "/thumb"
			s.files[thumb] = boxMediaFile{thumb: picture}
			meta.Thumbnail = &model.MessageMedia{ID: thumb, MIMEType: "image/jpeg", Width: picture.Bounds().Dx(), Height: picture.Bounds().Dy()}
		}
	}
	return kind, meta
}

// editedFile is what the editor made of a file of the box: the message showing
// it. commit, on the window's goroutine, makes it the box's; discard, for one
// nobody is there to take, removes it.
type editedFile struct {
	message model.Message
	commit  func()
	discard func()
}

// edit makes the file at path, the editor's result, m's in place of what it was,
// under the name it had, in a directory of its own for the box to remove with
// the temporary files it has. The file the user chose stays as it was.
func (s *boxMedia) edit(ctx context.Context, m model.Message, path string) (editedFile, error) {
	f := s.owners[m.Key.MessageID]
	if f == nil || m.Media == nil {
		return editedFile{}, fmt.Errorf("no file of the box for %d", m.Key.MessageID)
	}
	dir, err := os.MkdirTemp("", "komarugram-edited-")
	if err != nil {
		return editedFile{}, err
	}
	discard := func() { os.RemoveAll(dir) }
	stem := strings.TrimSuffix(m.Media.FileName, filepath.Ext(m.Media.FileName))
	if stem == "" {
		stem = "file"
	}
	kept := filepath.Join(dir, stem+filepath.Ext(path))
	if err := os.Rename(path, kept); err != nil {
		discard()
		return editedFile{}, err
	}
	ffmpeg := ""
	if s.ffmpeg != nil {
		ffmpeg = video.ResolveFFmpeg(s.ffmpeg())
	}
	info, picture, shown, long, err := readBoxFile(ctx, ffmpeg, kept)
	if err != nil {
		discard()
		return editedFile{}, err
	}
	if m.Kind == model.MessageGIF {
		info = info.AsAnimation()
	}
	s.mu.Lock()
	s.edits++
	id := fmt.Sprintf("%s/edit%d", m.Media.ID, s.edits)
	s.mu.Unlock()
	replaced := m
	replaced.Kind, replaced.Media = s.add(id, info, picture, shown, long)
	replaced.SenderName = info.Name
	return editedFile{message: replaced, discard: discard, commit: func() {
		old := f.Path
		f.File, f.thumb, f.shown, f.long, f.ready = info, picture, shown, long, true
		if s.replaced != nil {
			s.replaced(old, kept)
		}
	}}, nil
}

// openBoxFiles shows current among items of media over the whole window, in a
// viewer of its own: one that reads the box's files has nothing to do with the
// viewer of the chat. closed is called on the window's goroutine as it closes.
func (a *App) openBoxFiles(p *chatPage, media *boxMedia, chat int64, current model.Message, items []model.Message, closed func()) {
	v := newMediaViewer(media, &a.images, a.window.Invalidate)
	v.shortcuts = a.shortcutPrefs
	a.configureViewer(v)
	// What it makes goes back to the box, and its stickers come from the account.
	v.editSink, v.stickerStore = media.edit, p.source
	v.play = func(gtx layout.Context, m model.Message, start time.Duration, l localization.Catalog) {
		p.playWith(gtx, v.full, m, start, v.reportPlay, l)
	}
	v.openList(chat, current, mediaList{items: items, ended: [2]bool{true, true}, gallery: galleryBox})
	a.boxViewer, a.boxViewerClosed = v, closed
}

// layoutBoxViewer draws the viewer of the box of files, while there is one,
// and lets it go once it closes. Its workers may be busy with a large file,
// which is no reason to hold a frame.
func (a *App) layoutBoxViewer(gtx layout.Context) {
	if a.boxViewer == nil {
		return
	}
	a.boxViewer.Layout(gtx, a.catalog(), a.window.Motion.AnimationsEnabled())
	if a.boxViewer.open {
		return
	}
	v, closed := a.boxViewer, a.boxViewerClosed
	a.boxViewer, a.boxViewerClosed = nil, nil
	go func() {
		defer crash.Recover("close viewer of files", nil)
		v.Destroy()
	}()
	if closed != nil {
		closed()
	}
}
