// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// viewerFile is what saving or copying the photo on screen gave: a notice,
// and for a copy the picture to put on the clipboard.
type viewerFile struct {
	notice string
	png    []byte
}

// canKeep reports whether the photo m may be saved and copied: not when its
// chat protects its content, as in Telegram Desktop.
func canKeep(m model.Message) bool {
	return !m.NoForwards && m.Media != nil && m.Kind == model.MessagePhoto
}

// keepPhoto saves the photo m in the user's pictures, or copies it, in the
// background.
func (v *mediaViewer) keepPhoto(m model.Message, copy bool, l localization.Catalog) {
	if v.kept == nil {
		v.kept = make(chan viewerFile, 2)
	}
	ctx, results, source := v.ctx, v.kept, v.source
	go func() {
		defer crash.Recover("keep photo", func(e *crash.Panic) {
			select {
			case results <- viewerFile{notice: l.T("viewer.keep_failed") + ": " + mediaErrorText(e)}:
				v.invalidate()
			case <-ctx.Done():
			}
		})
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		var res viewerFile
		data, err := source.Media(ctx, m)
		switch {
		case err != nil:
		case copy:
			// Clipboards take PNG, which every program pastes.
			var im image.Image
			if im, _, err = image.Decode(bytes.NewReader(data)); err == nil {
				var b bytes.Buffer
				if err = png.Encode(&b, im); err == nil {
					res.png, res.notice = b.Bytes(), l.T("viewer.copied")
				}
			}
		default:
			var path string
			if path, err = savePhoto(data, m); err == nil {
				res.notice = l.Format("viewer.saved", map[string]string{"path": path})
			}
		}
		if err != nil {
			res.notice = l.T("viewer.keep_failed") + ": " + mediaErrorText(err)
		}
		select {
		case results <- res:
			v.invalidate()
		case <-ctx.Done():
		}
	}()
}

// savePhoto writes a photo's file in the user's pictures, named after its
// chat and message, with the extension its content tells.
func savePhoto(data []byte, m model.Message) (string, error) {
	dir := picturesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := ".jpg"
	switch http.DetectContentType(data) {
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	case "image/gif":
		ext = ".gif"
	}
	path := filepath.Join(dir, photoFileName(m)+ext)
	return path, os.WriteFile(path, data, 0o644)
}

// photoFileName names the file of photo m without its extension: after its
// date and message, or after the chat and place of a profile photo, which
// is no message.
func photoFileName(m model.Message) string {
	if model.IsProfilePhoto(m.Key.MessageID) {
		return fmt.Sprintf("komarugram-go-avatar-%d-%d", m.Key.ChatID, m.Key.MessageID-model.ProfilePhotoID(0)+1)
	}
	return fmt.Sprintf("komarugram-go-%s-%d", m.Date.Local().Format("2006-01-02-150405"), m.Key.MessageID)
}

// updateKept takes the results of saving and copying: the toast tells them for
// a few seconds, and a copied picture goes on the clipboard.
func (v *mediaViewer) updateKept(gtx layout.Context) {
	for {
		select {
		case r := <-v.kept:
			if r.png != nil {
				gtx.Execute(clipboard.WriteCmd{Type: "image/png", Data: io.NopCloser(bytes.NewReader(r.png))})
			}
			v.toast.Show(r.notice)
			continue
		default:
		}
		break
	}
}
