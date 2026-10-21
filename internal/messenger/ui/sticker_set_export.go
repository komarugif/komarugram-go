// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"komarugram/internal/messenger/model"
)

const maxStickerArchiveBytes = 512 << 20

type stickerArchiveSource interface {
	Media(context.Context, model.Message) ([]byte, error)
}

type stickerArchiveItem struct {
	File       string `json:"file"`
	Emoji      string `json:"emoji,omitempty"`
	MIMEType   string `json:"mime_type"`
	DocumentID int64  `json:"document_id,omitempty"`
}

// writeStickerSetArchive saves the original documents, not rendered previews.
// A temporary file keeps a failed or cancelled download from replacing the
// selected destination with a partial ZIP.
func writeStickerSetArchive(ctx context.Context, source stickerArchiveSource, pack model.StickerSet, path string) error {
	if len(pack.Items) == 0 {
		return errors.New("sticker set is empty")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("archive path is empty")
	}
	path = stickerArchivePath(path)
	f, err := os.CreateTemp(filepath.Dir(path), ".komarugram-go-stickers-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	archive := zip.NewWriter(f)
	manifest := struct {
		Title string               `json:"title"`
		Emoji bool                 `json:"emoji_set"`
		Items []stickerArchiveItem `json:"items"`
	}{Title: pack.Title, Emoji: pack.Emoji}
	var total int64
	for i, item := range pack.Items {
		if err := ctx.Err(); err != nil {
			archive.Close()
			f.Close()
			return err
		}
		if item.Media.Media == nil || item.Media.Media.ID == "" {
			archive.Close()
			f.Close()
			return fmt.Errorf("sticker %d has no document", i+1)
		}
		data, err := source.Media(ctx, item.Media)
		if err != nil {
			archive.Close()
			f.Close()
			return fmt.Errorf("sticker %d: %w", i+1, err)
		}
		total += int64(len(data))
		if len(data) == 0 || total > maxStickerArchiveBytes {
			archive.Close()
			f.Close()
			return fmt.Errorf("sticker %d is empty or archive exceeds 512 MiB", i+1)
		}
		mime := item.Media.Media.MIMEType
		name := fmt.Sprintf("%03d%s", i+1, stickerArchiveExtension(mime))
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			archive.Close()
			f.Close()
			return err
		}
		if _, err := entry.Write(data); err != nil {
			archive.Close()
			f.Close()
			return err
		}
		manifest.Items = append(manifest.Items, stickerArchiveItem{File: name, Emoji: item.Emoji, MIMEType: mime, DocumentID: item.DocumentID})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		var writerErr error
		entry, createErr := archive.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Store})
		if createErr == nil {
			_, writerErr = entry.Write(append(data, '\n'))
		} else {
			writerErr = createErr
		}
		err = writerErr
	}
	if err != nil {
		archive.Close()
		f.Close()
		return err
	}
	if err := archive.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func stickerArchivePath(path string) string {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		path += ".zip"
	}
	return path
}

func stickerArchiveExtension(mime string) string {
	switch mime {
	case "application/x-tgsticker":
		return ".tgs"
	case "video/webm":
		return ".webm"
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	default:
		return ".bin"
	}
}

func stickerArchiveName(title string) string {
	var b strings.Builder
	separator := false
	for _, r := range title {
		if b.Len() >= 80 {
			break
		}
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			if separator && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			separator = false
		default:
			separator = true
		}
	}
	name := strings.Trim(b.String(), "_-")
	if name == "" {
		name = "stickers"
	}
	return name + ".zip"
}

// chooseStickerArchive asks for a local destination without interpreting the
// pack title as shell or script source. An empty path means cancellation.
func chooseStickerArchive(ctx context.Context, title string) (string, error) {
	return chooseSavePath(ctx, stickerArchiveName(title), "ZIP archives")
}
