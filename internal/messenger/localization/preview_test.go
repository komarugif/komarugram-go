// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"testing"

	"komarugram/internal/messenger/model"
)

// The line says what an album holds when it has no caption, or several,
// and leaves out the word of media its thumbnails show beside text.
func TestPreviewLine(t *testing.T) {
	l := For("en")
	thumb := []model.PreviewThumb{{ID: "photo/1/s"}}
	for _, c := range []struct {
		p                   model.Preview
		sender, media, text string
	}{
		{model.Preview{Text: "hi", Sender: "Ann"}, "Ann", "", "hi"},
		{model.Preview{Text: "sunset", Media: "preview.photo"}, "", "Photo", "sunset"},
		{model.Preview{Text: "sunset", Media: "preview.photo", Thumbs: thumb}, "", "", "sunset"},
		{model.Preview{Media: "preview.photo", Thumbs: thumb}, "", "Photo", ""},
		{model.Preview{Media: "preview.photo", Album: &model.PreviewAlbum{Photos: 3}}, "", "3 photos", ""},
		{model.Preview{Media: "preview.photo", Album: &model.PreviewAlbum{Photos: 2, Videos: 1}}, "", "3 media", ""},
		{model.Preview{Text: "trip", Media: "preview.photo", Album: &model.PreviewAlbum{Photos: 2, Captions: 1}}, "", "", "trip"},
		{model.Preview{Text: "trip", Media: "preview.photo", Album: &model.PreviewAlbum{Photos: 2, Captions: 2}}, "", "2 photos", ""},
		{model.Preview{Media: "preview.file", Album: &model.PreviewAlbum{Files: 1}}, "", "File", ""},
		{model.Preview{Outgoing: true, Media: "preview.sticker", Emoji: "😀"}, "You", "😀 Sticker", ""},
		// A poll's question and a checklist's title in place of the word.
		{model.Preview{Media: "preview.poll", Title: "Lunch?"}, "", "Lunch?", ""},
		{model.Preview{Media: "preview.todo", Title: "Trip", Text: "see you"}, "", "Trip", "see you"},
	} {
		sender, media, text := l.PreviewLine(c.p, "", false)
		if sender != c.sender || media != c.media || text != c.text {
			t.Errorf("%+v: %q %q %q, want %q %q %q", c.p, sender, media, text, c.sender, c.media, c.text)
		}
	}
	if _, text := l.Preview(model.Preview{Text: "sunset", Media: "preview.photo"}, "", false); text != "Photo, sunset" {
		t.Errorf("joined %q", text)
	}
}
