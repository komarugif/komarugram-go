// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"testing"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A WebM shows as a video, as Telegram keeps it, with no video's attributes, in the
// shape of a thumbnail Telegram told of, whose picture is made here: asked for, that
// one is FILE_REFERENCE_EXPIRED. A sticker stays one, and other files files.
func TestWebMIsAVideo(t *testing.T) {
	name := &tg.DocumentAttributeFilename{FileName: "clip.webm"}
	thumb := []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 320, H: 180, Size: 100}}
	k, m, _ := documentMedia(&tg.Document{ID: 1, MimeType: "video/webm", Attributes: []tg.DocumentAttributeClass{name}, Thumbs: thumb})
	if k != model.MessageVideo || m.Width != 320 || m.Height != 180 {
		t.Fatalf("%v, %dx%d", k, m.Width, m.Height)
	}
	if m.Thumbnail == nil || m.Thumbnail.ID != "document/1/first" {
		t.Fatalf("thumbnail %+v", m.Thumbnail)
	}
	k, m, _ = documentMedia(&tg.Document{ID: 2, MimeType: "video/webm", Attributes: []tg.DocumentAttributeClass{name}})
	if k != model.MessageVideo || m.Width != 0 || m.Thumbnail == nil || m.Thumbnail.ID != "document/2/first" {
		t.Fatalf("without a thumbnail: %v, %dx%d, %+v", k, m.Width, m.Height, m.Thumbnail)
	}
	// One told of keeps what it told.
	k, m, _ = documentMedia(&tg.Document{ID: 3, MimeType: "video/webm", Thumbs: thumb, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 640, H: 360, Duration: 2}}})
	if k != model.MessageVideo || m.Width != 640 {
		t.Fatalf("with attributes: %v, %dx%d", k, m.Width, m.Height)
	}
	if k, _, _ := documentMedia(&tg.Document{ID: 4, MimeType: "video/webm", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 512, H: 512}, &tg.DocumentAttributeSticker{Alt: "😀", Stickerset: &tg.InputStickerSetEmpty{}}}}); k != model.MessageSticker {
		t.Fatalf("a sticker: %v", k)
	}
	if k, _, _ := documentMedia(&tg.Document{ID: 5, MimeType: "application/zip", Attributes: []tg.DocumentAttributeClass{name}}); k != model.MessageFile {
		t.Fatalf("a file: %v", k)
	}
}
