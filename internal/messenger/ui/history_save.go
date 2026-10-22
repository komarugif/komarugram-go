// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/savefile"
)

// The item of a message's menu that saves its file. A photo goes into the
// user's pictures, as the viewer's button saves it; a video, a GIF or a
// document is downloaded into the user's downloads, which the Downloads
// section lists; the others (music, voice and video messages, stickers) are
// saved where the user chooses. Telegram Desktop hides it where the chat
// protects its content.

// saveWay is what saving the file of a message does.
type saveWay int

const (
	saveNone saveWay = iota
	saveToPictures
	saveToDownloads
	saveAs
)

// saveWayOf is what saving m's file does, saveNone when it cannot be saved.
func (p *chatPage) saveWayOf(m model.Message) saveWay {
	if m.Media == nil || m.Media.ID == "" || m.NoForwards || len(m.Attachments) > 1 {
		return saveNone
	}
	_, downloads := p.source.(model.Downloader)
	switch m.Kind {
	case model.MessagePhoto:
		return saveToPictures
	case model.MessageVideo, model.MessageGIF, model.MessageFile:
		if downloads {
			return saveToDownloads
		}
		return saveAs
	case model.MessageVoice, model.MessageMusic, model.MessageRound:
		return saveAs
	case model.MessageSticker:
		// A Lottie sticker is saved as a video sticker, which other
		// programs play; the set's archive keeps the TGS.
		return saveAs
	}
	return saveNone
}

// lottieSticker reports whether m is a sticker drawn from Lottie (TGS).
func lottieSticker(m model.Message) bool {
	return m.Media != nil && m.Media.MIMEType == "application/x-tgsticker"
}

// saveLabel is the text of the menu's item that saves a file so.
func saveLabel(way saveWay, l localization.Catalog) string {
	switch way {
	case saveToPictures:
		return l.T("menu.save_to_pictures")
	case saveToDownloads:
		return l.T("menu.download")
	}
	return l.T("menu.save_as")
}

// saveMessage saves m's file as way has it, in the background, and tells
// where it went.
func (p *chatPage) saveMessage(m model.Message, way saveWay, l localization.Catalog) {
	switch way {
	case saveToDownloads:
		p.downloadFile(m, false, l.T("menu.downloaded"))
	case saveToPictures:
		source := p.source
		go func() {
			defer crash.Recover("save photo", func(e *crash.Panic) { p.reportMedia(e) })
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			data, err := source.Media(ctx, m)
			var path string
			if err == nil {
				path, err = savePhoto(data, m)
			}
			if err != nil {
				p.reportMedia(err)
				return
			}
			p.reportNotice(l.Format("viewer.saved", map[string]string{"path": path}))
		}()
	case saveAs:
		if lottieSticker(m) {
			p.stickerVideo.open(m)
			return
		}
		name := savefile.Name(m)
		kind := l.Format("menu.save_files", map[string]string{"ext": strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))})
		go func() {
			defer crash.Recover("save file", func(e *crash.Panic) { p.reportMedia(e) })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			path, err := chooseSavePath(ctx, name, kind)
			if err == nil && path != "" {
				err = p.writeMedia(ctx, m, path)
			}
			switch {
			case err != nil:
				p.reportMedia(err)
			case path != "":
				p.reportNotice(l.Format("menu.saved", map[string]string{"path": path}))
			}
		}()
	}
}

// writeMedia writes m's file at path, which the user chose.
func (p *chatPage) writeMedia(ctx context.Context, m model.Message, path string) error {
	return writeWhole(ctx, path, func(f *os.File) error {
		if source, ok := p.source.(interface {
			MediaStream(context.Context, model.Message) (io.ReaderAt, int64, func(), error)
		}); ok && !m.Media.Whole() {
			r, size, closeStream, err := source.MediaStream(ctx, m)
			if err != nil {
				return err
			}
			defer closeStream()
			_, err = io.Copy(f, io.NewSectionReader(r, 0, size))
			return err
		}
		data, err := p.source.Media(ctx, m)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	})
}

// writeWhole writes a file at path with write: into a file beside it
// first, which takes its place once whole.
func writeWhole(ctx context.Context, path string, write func(*os.File) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".komarugram-*.part")
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := write(tmp); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = tmp.Chmod(0o644)
	if err := tmp.Close(); err != nil {
		return err
	}
	// The dialog asked before a file was replaced.
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	done = true
	return nil
}
