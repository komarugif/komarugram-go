// SPDX-License-Identifier: Unlicense OR MIT

// Package sendfiles decides how files chosen to be sent go out, as Telegram
// Desktop's box for sending files does: which of them are photos, videos,
// music or plain files, which are grouped in an album, where the caption
// goes, and how a photo is made ready. It knows nothing of Telegram's
// protocol.
package sendfiles

import (
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"komarugram/pkg/audiotag"
)

// MaxAlbum is how many items an album holds.
const MaxAlbum = 10

// Kind is what a file is sent as.
type Kind int

const (
	// KindFile is sent as a document.
	KindFile Kind = iota
	// KindPhoto is an image that can be sent as a photo.
	KindPhoto
	// KindVideo is sent as a video.
	KindVideo
	// KindAnimation is a GIF, which is sent on its own: no album holds it.
	KindAnimation
	// KindMusic is an audio file that says how long it plays, sent as a
	// track whatever the way; an album of music holds music alone.
	KindMusic
)

// Errors of Inspect, which tell why a file cannot be sent at all.
var (
	ErrEmpty     = errors.New("the file is empty")
	ErrDirectory = errors.New("a folder cannot be sent")
	ErrNotFile   = errors.New("not a regular file")
)

// File is a file chosen to be sent.
type File struct {
	Path string
	Name string
	Size int64
	Kind Kind
	MIME string
	// Width and Height are an image's size as it is shown, with the turn its
	// file asks for; 0 for what is not an image.
	Width, Height int
	// Duration, Title and Performer are music's, as its tags say, and Cover
	// the picture they hold, as that picture's file.
	Duration         time.Duration
	Title, Performer string
	Cover            []byte
}

// Way is how the files are sent, as the checkboxes of the box set it.
type Way struct {
	// Documents sends photos and videos as files instead of media.
	Documents bool
	// Group puts what can go in an album in one.
	Group bool
	// HighQuality lets photos keep up to 2560 pixels a side, not 1280.
	HighQuality bool
	// SilentAsVideo sends videos without sound as videos, not as Telegram takes
	// them: GIFs.
	SilentAsVideo bool
}

// AsAnimation is f, a video, to go as a GIF; anything else stays as it is.
func (f File) AsAnimation() File {
	if f.Kind == KindVideo {
		f.Kind = KindAnimation
	}
	return f
}

// Photo sizes: Telegram Desktop scales a photo down to fit a square of
// these, and no picture is wider than twenty times its height.
const (
	StandardSide    = 1280
	HighQualitySide = 2560
	maxAspect       = 20
)

// videoExtensions are what is sent as a video though the system may not
// know their type.
var videoExtensions = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
	".mkv": "video/x-matroska", ".avi": "video/x-msvideo", ".3gp": "video/3gpp", ".ogv": "video/ogg",
	".flv": "video/x-flv", ".wmv": "video/x-ms-wmv", ".asf": "video/x-ms-asf", ".mpg": "video/mpeg", ".mpeg": "video/mpeg",
	".m2ts": "video/mp2t", ".mts": "video/mp2t",
}

// MIMEOf is the type of the file at path: by its name, as Telegram Desktop
// does, then by its start.
func MIMEOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if t, ok := videoExtensions[ext]; ok {
		return t
	}
	if t := audiotag.MIME(path); t != "" {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		t, _, _ = strings.Cut(t, ";")
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return http.DetectContentType(head[:n])
}

// Inspect reads what is needed to decide how the file at path is sent.
func Inspect(path string) (File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	switch {
	case info.IsDir():
		return File{}, ErrDirectory
	case !info.Mode().IsRegular():
		return File{}, ErrNotFile
	case info.Size() == 0:
		return File{}, ErrEmpty
	}
	f := File{Path: path, Name: filepath.Base(path), Size: info.Size(), MIME: MIMEOf(path)}
	switch {
	case strings.HasPrefix(f.MIME, "video/"):
		f.Kind = KindVideo
	case f.MIME == "image/gif":
		f.Kind = KindAnimation
	case strings.HasPrefix(f.MIME, "image/"):
		if w, h, ok := imageSize(path); ok && ValidDimensions(w, h) {
			f.Kind, f.Width, f.Height = KindPhoto, w, h
		}
	case audiotag.MIME(path) != "":
		// As in Telegram Desktop, music is what says how long it plays;
		// the rest goes as a file.
		if song, err := audiotag.Read(path); err == nil {
			f.Kind, f.Duration, f.Title, f.Performer, f.Cover = KindMusic, song.Duration, song.Title, song.Performer, song.Cover
		}
	}
	return f, nil
}

// ValidDimensions reports whether Telegram takes a picture of this size as
// a photo: no side is empty, and one is at most twenty times the other.
func ValidDimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= maxAspect*h && h <= maxAspect*w
}

// imageSize reads an image's size as it is shown, without decoding it.
func imageSize(path string) (w, h int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}
	w, h = cfg.Width, cfg.Height
	if o := orientationOfFile(path); o >= 5 {
		w, h = h, w
	}
	return w, h, true
}

// AlbumType is what an album holds; albums of different types are sent
// apart.
type AlbumType int

const (
	// NoAlbum is a file sent by itself.
	NoAlbum AlbumType = iota
	// PhotoVideoAlbum holds photos and videos.
	PhotoVideoAlbum
	// FileAlbum holds documents, photos and videos sent as documents too.
	FileAlbum
	// MusicAlbum holds music.
	MusicAlbum
)

// albumOf is the album f goes in under way.
func albumOf(f File, way Way) AlbumType {
	switch f.Kind {
	case KindPhoto, KindVideo:
		switch {
		case way.Group && !way.Documents:
			if f.Kind == KindVideo && !SentAsVideo(f.MIME) {
				return NoAlbum
			}
			return PhotoVideoAlbum
		case way.Group:
			return FileAlbum
		}
	case KindFile:
		if way.Group {
			return FileAlbum
		}
	case KindMusic:
		if way.Group {
			return MusicAlbum
		}
	}
	return NoAlbum
}

// SentAsVideo tells a video Telegram keeps as one, by its type: as Telegram Desktop's
// IsMimeSentAsVideo, MP4 and MOV. Others may come back files, as a WebM does, which
// Telegram for Android shows as one and which would fail an album of media: the box
// offers to convert them first.
func SentAsVideo(mime string) bool {
	return mime == "" || mime == "video/mp4" || mime == "video/quicktime"
}

// Group is files sent in one message: an album, or a file by itself.
type Group struct {
	Files []File
	Album AlbumType
}

// Divide splits files, in their order, into the messages they are sent in:
// neighbours of the same album type go together, ten at most, and the rest
// go one by one.
func Divide(files []File, way Way) []Group {
	var groups []Group
	from, kind := 0, NoAlbum
	flush := func(till int) {
		if till-from > 1 && kind != NoAlbum {
			groups = append(groups, Group{Files: files[from:till], Album: kind})
			return
		}
		for _, f := range files[from:till] {
			groups = append(groups, Group{Files: []File{f}})
		}
	}
	for i, f := range files {
		next := albumOf(f, way)
		if i > from && (kind != next || kind != NoAlbum && i-from == MaxAlbum) {
			flush(i)
			from = i
		}
		kind = next
	}
	if len(files) > 0 {
		flush(len(files))
	}
	return groups
}

// HasGroupOption reports whether there is something to group: two
// neighbours that can share an album.
func HasGroupOption(files []File) bool {
	if len(files) < 2 {
		return false
	}
	last := Kind(-1)
	for i, f := range files {
		if i > 0 {
			same := f.Kind == last
			mixed := (f.Kind == KindVideo && last == KindPhoto) || (f.Kind == KindPhoto && last == KindVideo) ||
				(f.Kind == KindFile && last == KindPhoto) || (f.Kind == KindPhoto && last == KindFile)
			if (same || mixed) && last != KindAnimation {
				return true
			}
		}
		last = f.Kind
	}
	return false
}

// HasDocumentsOption reports whether "send as documents" means anything:
// there is a photo or a video.
func HasDocumentsOption(files []File) bool {
	for _, f := range files {
		if f.Kind == KindPhoto || f.Kind == KindVideo {
			return true
		}
	}
	return false
}

// HasHighQualityOption reports whether a photo is large enough for
// "high quality" to matter, while photos are compressed.
func HasHighQualityOption(files []File, way Way) bool {
	if way.Documents {
		return false
	}
	for _, f := range files {
		if f.Kind == KindPhoto && (f.Width > StandardSide || f.Height > StandardSide) {
			return true
		}
	}
	return false
}

// CaptionTarget is where the caption of the box goes, as Telegram Desktop
// puts it: on the last message, on the first item of an album of photos and
// videos, on the last of any other.
func CaptionTarget(groups []Group) (group, file int) {
	if len(groups) == 0 {
		return -1, -1
	}
	group = len(groups) - 1
	if g := groups[group]; g.Album == PhotoVideoAlbum {
		return group, 0
	}
	return group, len(groups[group].Files) - 1
}

// Title is what the box says of the files, by their number and kind.
type Title int

const (
	TitleImage Title = iota
	TitleVideo
	TitleFile
	TitleImages
	TitleFiles
)

// TitleOf says what to call files. Several are counted as images when all
// are photos sent as such, and as files otherwise.
func TitleOf(files []File, way Way) Title {
	if len(files) == 1 {
		switch f := files[0]; {
		case f.Kind == KindPhoto && !way.Documents:
			return TitleImage
		case f.Kind == KindVideo && !way.Documents:
			return TitleVideo
		}
		return TitleFile
	}
	for _, f := range files {
		if f.Kind != KindPhoto || way.Documents {
			return TitleFiles
		}
	}
	return TitleImages
}

// Size is a file's size as Telegram Desktop writes it (FormatSizeText):
// bytes, then kilobytes and megabytes of 1024, the tenths cut rather than
// rounded, so that a size never reads as more than it is, nor as zero.
func Size(n int64) string { return SizeIn(n, "B", "KB", "MB") }

// SizeIn is Size with the units of a language: bytes, kilobytes and
// megabytes.
func SizeIn(n int64, b, kb, mb string) string {
	switch {
	case n >= 1<<20:
		tenths := n * 10 / (1 << 20)
		return fmt.Sprintf("%d.%d %s", tenths/10, tenths%10, mb)
	case n >= 1<<10:
		tenths := n * 10 / (1 << 10)
		return fmt.Sprintf("%d.%d %s", tenths/10, tenths%10, kb)
	}
	return fmt.Sprintf("%d %s", n, b)
}

// DropState is what files dragged over a chat can be dropped as, as
// Telegram Desktop tells it: the areas shown for them.
type DropState int

const (
	// DropNone is a drag that cannot be sent: nothing, or a folder.
	DropNone DropState = iota
	// DropFiles go as documents, one area.
	DropFiles
	// DropPhotos are pictures: as documents, without compression, or as
	// photos.
	DropPhotos
	// DropMedia are photos and videos: as documents, or as media.
	DropMedia
)

// maxDropPicture is the largest picture Telegram Desktop offers to send
// as a photo when it is dropped.
const maxDropPicture = 64 << 20

// DropStateOf says what the files at paths can be dropped as. Every file
// is looked at, as Telegram Desktop does, but only what it is named and
// the header of a picture.
func DropStateOf(paths []string) DropState {
	if len(paths) == 0 {
		return DropNone
	}
	pictures, media := true, true
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return DropNone
		}
		mime := MIMEOf(path)
		if pictures && (info.Size() > maxDropPicture || mime == "image/gif" || !strings.HasPrefix(mime, "image/")) {
			pictures = false
		}
		if pictures {
			if _, _, ok := imageSize(path); !ok {
				pictures = false
			}
		}
		if !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
			media = false
		}
	}
	switch {
	case pictures:
		return DropPhotos
	case media:
		return DropMedia
	}
	return DropFiles
}
