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
	"time"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

// boxMedia is what the viewer of the box of files reads: the files about to
// go, from the disk. The store is there for what a viewer asks of any source;
// embedded as the interface, it hides all a store may do besides, so that the
// viewer finds no galleries to page and nothing to forward with.
type boxMedia struct {
	model.ConversationStore
	files map[string]boxMediaFile
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
	f, ok := s.files[m.Media.ID]
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
	f, ok := s.files[m.Media.ID]
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
	media = &boxMedia{ConversationStore: source, files: map[string]boxMediaFile{}}
	for i, f := range files {
		if !boxViewable(f) {
			continue
		}
		id := fmt.Sprintf("box/%d", i)
		m := model.Message{Key: model.MessageKey{ChatID: chat, MessageID: model.MessageID(i + 1)}, SenderName: f.Name}
		meta := &model.MessageMedia{ID: id, FileName: f.Name, MIMEType: f.MIME, Size: f.Size, Width: f.Width, Height: f.Height}
		switch f.Kind {
		case sendfiles.KindPhoto:
			m.Kind = model.MessagePhoto
		case sendfiles.KindVideo:
			m.Kind = model.MessageVideo
			meta.Width, meta.Height, meta.Duration = f.shown.X, f.shown.Y, f.long
		case sendfiles.KindAnimation:
			m.Kind = model.MessageGIF
		}
		media.files[id] = boxMediaFile{path: f.Path, photo: f.Kind == sendfiles.KindPhoto, size: f.Size}
		if f.thumb != nil {
			if meta.Width <= 0 || meta.Height <= 0 {
				// What FFmpeg did not tell, or a GIF, whose size nothing read.
				meta.Width, meta.Height = f.thumb.Bounds().Dx(), f.thumb.Bounds().Dy()
			}
			if f.Kind != sendfiles.KindPhoto {
				thumb := id + "/thumb"
				media.files[thumb] = boxMediaFile{thumb: f.thumb}
				meta.Thumbnail = &model.MessageMedia{ID: thumb, MIMEType: "image/jpeg", Width: f.thumb.Bounds().Dx(), Height: f.thumb.Bounds().Dy()}
			}
		}
		m.Media = meta
		items = append(items, m)
		if f == chosen {
			current = m
		}
	}
	return media, items, current
}

// openBoxFiles shows current among items of media over the whole window, in a
// viewer of its own: one that reads the box's files has nothing to do with the
// viewer of the chat. closed is called on the window's goroutine as it closes.
func (a *App) openBoxFiles(p *chatPage, media *boxMedia, chat int64, current model.Message, items []model.Message, closed func()) {
	v := newMediaViewer(media, &a.images, a.window.Invalidate)
	v.shortcuts = a.shortcutPrefs
	a.configureViewer(v)
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
