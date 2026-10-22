// SPDX-License-Identifier: Unlicense OR MIT

// Package savefile names the files of messages saved to the user's disk,
// and puts them there without replacing a file of the user's.
package savefile

import (
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"komarugram/internal/messenger/model"
)

// Place moves tmp into dir as name, or as "name (1).ext" and on when
// that is taken: a file of the user's is never replaced.
func Place(tmp, dir, name string) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; n < 10000; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		path := filepath.Join(dir, candidate)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// A link, where the system makes one, fails rather than replace
		// a file made meanwhile.
		if err := os.Link(tmp, path); err == nil {
			os.Remove(tmp)
			return path, nil
		} else if errors.Is(err, os.ErrExist) {
			continue
		}
		if err := os.Rename(tmp, path); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", errors.New("download: no free name")
}

// Name is the name m's file is downloaded as: the one the sender
// gave it, made safe, or Telegram Desktop's kind and date, as
// "photo_2026-10-21_19-14-15.jpg". A sticker is named after its document,
// as "sticker_5170233102089322756.webp": stickers mostly carry one name,
// "sticker.webp", and one sticker is sent many times.
func Name(m model.Message) string {
	if m.Kind == model.MessageSticker && m.Media != nil && m.Media.ID != "" {
		ext := filepath.Ext(m.Media.FileName)
		if ext == "" {
			ext = extensionOf(m)
		}
		return SafeName("sticker_" + m.Media.ID[strings.LastIndexByte(m.Media.ID, '/')+1:] + ext)
	}
	name := ""
	if m.Media != nil {
		name = m.Media.FileName
	}
	if name == "" {
		prefix := "file"
		switch m.Kind {
		case model.MessagePhoto:
			prefix = "photo"
		case model.MessageVideo, model.MessageGIF, model.MessageRound:
			prefix = "video"
		case model.MessageVoice, model.MessageMusic:
			prefix = "audio"
		case model.MessageSticker:
			prefix = "sticker"
		}
		date := m.Date
		if date.IsZero() {
			date = time.Now()
		}
		name = prefix + date.Format("_2006-01-02_15-04-05") + extensionOf(m)
	}
	return SafeName(name)
}

// extensionOf is the extension of m's file by its type.
func extensionOf(m model.Message) string {
	mimeType := ""
	if m.Media != nil {
		mimeType = m.Media.MIMEType
	}
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "application/x-tgsticker":
		return ".tgs"
	case "":
		if m.Kind == model.MessagePhoto {
			return ".jpg"
		}
		return ""
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// SafeName makes a name given by a stranger safe to keep, as Telegram
// Desktop's FileNameFromUserString does, on every system: no path, no
// control characters, no marks that turn the direction of the text and
// make "gepj.exe" look like "exe.jpeg", none of the characters Windows
// takes, no name Windows keeps for a device, no shortcut that runs, and
// no ending in a space or a dot.
func SafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, r < 32, r == 0x7f,
			r == 0x200e, r == 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
			strings.ContainsRune(`/\<>:"|?*`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name = b.String()
	for _, ext := range []string{".lnk", ".scf", ".url", ".desktop"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			name += ".download"
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		name = "_" + name
	default:
		if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			name = "_" + name
		}
	}
	if name == "" || strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		name += "_"
	}
	// A name of at most 200 bytes, its extension kept.
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		cut := 200 - len(ext)
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut] + ext
	}
	return name
}
