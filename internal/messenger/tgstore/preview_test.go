// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

func TestSpoilerLoginCode(t *testing.T) {
	for _, c := range []struct {
		text     string
		entities []model.Entity
		min      int
		want     []model.Entity
	}{
		{"Login code: 52107. Do not give it", nil, 5, []model.Entity{{Kind: "spoiler", Offset: 12, Length: 5}}},
		{"Код для входа в Telegram: 52107", nil, 5, []model.Entity{{Kind: "spoiler", Offset: 26, Length: 5}}},
		{"Your code is 123-456", nil, 4, []model.Entity{{Kind: "spoiler", Offset: 13, Length: 7}}},
		// In a word, too short, or under an entity that goes on into it.
		{"Order A52107 shipped", nil, 5, nil},
		{"Code 1234", nil, 5, nil},
		{"Code 52107", []model.Entity{{Kind: "italic", Offset: 3, Length: 4}}, 5, []model.Entity{{Kind: "italic", Offset: 3, Length: 4}}},
		// An entity before it stays first.
		{"Code 52107", []model.Entity{{Kind: "italic", Offset: 0, Length: 4}}, 5, []model.Entity{{Kind: "italic", Offset: 0, Length: 4}, {Kind: "spoiler", Offset: 5, Length: 5}}},
	} {
		if got := spoilerLoginCode(c.text, c.entities, c.min); !slices.Equal(got, c.want) {
			t.Errorf("%q: %+v, want %+v", c.text, got, c.want)
		}
	}
}

// Telegram's codes are hidden in its chat, and only there.
func TestLoginCodeHiddenInServiceChat(t *testing.T) {
	m := model.Message{Key: model.MessageKey{ChatID: serviceNotifications, MessageID: 1}, Text: "Login code: 52107"}
	if p := messagePreview(m, false); len(p.Entities) != 1 || p.Entities[0].Kind != "spoiler" {
		t.Fatalf("service chat: %+v", p.Entities)
	}
	m.Key.ChatID = 5
	if p := messagePreview(m, false); len(p.Entities) != 0 {
		t.Fatalf("other chat: %+v", p.Entities)
	}
}

func photoMessage(id model.MessageID, group int64, text string) model.Message {
	media := &model.MessageMedia{ID: "photo/1/y", Width: 1280, Height: 960, Preview: []byte{1},
		Variants: []model.MessageMedia{{ID: "photo/1/s", Width: 90, Height: 68}, {ID: "photo/1/m", Width: 320, Height: 240}}}
	return model.Message{Key: model.MessageKey{ChatID: 5, MessageID: id}, Kind: model.MessagePhoto, Media: media, GroupedID: group, Text: text, Date: time.Unix(1000, 0)}
}

// A thumbnail downloads the smallest size that covers the list's square;
// the blurred picture is kept only for media under a spoiler, which is
// not downloaded.
func TestPreviewThumb(t *testing.T) {
	m := photoMessage(1, 0, "")
	th, ok := previewThumb(m)
	if !ok || th.ID != "photo/1/s" || th.Blurred != nil || th.Spoiler || th.Message != 1 {
		t.Fatalf("photo: %+v", th)
	}
	m.MediaSpoiler = true
	if th, _ = previewThumb(m); !th.Spoiler || th.Blurred == nil {
		t.Fatalf("spoiler: %+v", th)
	}
	round := model.Message{Kind: model.MessageRound, Media: &model.MessageMedia{ID: "document/2", Thumbnail: &model.MessageMedia{ID: "document/2/thumb/m", Width: 320, Height: 320}}}
	if th, _ = previewThumb(round); th.ID != "document/2/thumb/m" || !th.Video || !th.Round {
		t.Fatalf("round: %+v", th)
	}
	inline := model.Message{Kind: model.MessageFile, Media: &model.MessageMedia{ID: "document/3", Thumbnail: &model.MessageMedia{ID: "document/3/thumb/inline", Preview: []byte{2}}}}
	if th, _ = previewThumb(inline); !th.Spoiler || th.Blurred == nil {
		t.Fatalf("inline: %+v", th)
	}
	for _, m := range []model.Message{
		{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "document/4"}},
		{Kind: model.MessageMusic, Media: &model.MessageMedia{ID: "document/5"}},
		{Kind: model.MessageFile, Media: &model.MessageMedia{ID: "document/6"}},
	} {
		if _, ok := previewThumb(m); ok {
			t.Fatalf("%v has a thumbnail", m.Kind)
		}
	}
}

// The messages of an album, as they come, join one preview: counted, with
// the first three thumbnails and the caption one of them has.
func TestUpdatePreviewJoinsAlbum(t *testing.T) {
	chat := model.Chat{ID: 5, Kind: model.KindUser}
	updatePreview(&chat, photoMessage(1, 77, "Caption"))
	for id := model.MessageID(2); id <= 4; id++ {
		updatePreview(&chat, photoMessage(id, 77, ""))
	}
	p := chat.Last
	if p.Album == nil || p.Album.Photos != 4 || p.Album.Captions != 1 || p.Album.Last != 4 || p.ID != 4 || len(p.Thumbs) != 3 || p.Text != "Caption" {
		t.Fatalf("album %+v %+v", p, p.Album)
	}
	// The caption edited, and an edit of the older item counted already.
	updatePreview(&chat, photoMessage(1, 77, "Edited"))
	updatePreview(&chat, photoMessage(2, 77, ""))
	if p = chat.Last; p.Text != "Edited" || p.Album.Photos != 4 || p.ID != 4 {
		t.Fatalf("edited %+v %+v", p, p.Album)
	}
	// A message before it edited, not of the album, leaves it.
	older := photoMessage(0, 0, "old")
	updatePreview(&chat, older)
	if chat.Last.Text != "Edited" {
		t.Fatalf("older edit took the preview: %+v", chat.Last)
	}
	// The next message starts anew.
	updatePreview(&chat, photoMessage(5, 0, "next"))
	if p = chat.Last; p.Album != nil || p.Text != "next" || len(p.Thumbs) != 1 {
		t.Fatalf("next %+v", p)
	}
}

// A page of dialogs has the last message of an album alone: what the
// client saw of the album stays while it is the last.
func TestKeepAlbums(t *testing.T) {
	kept := model.Chat{ID: 5}
	for id := model.MessageID(1); id <= 3; id++ {
		updatePreview(&kept, photoMessage(id, 77, map[bool]string{true: "Caption"}[id == 1]))
	}
	page := model.Chat{ID: 5}
	setPreview(&page, photoMessage(3, 77, ""))
	got := keepAlbums([]model.Chat{page}, []model.Chat{kept})[0].Last
	if got.Album.Photos != 3 || len(got.Thumbs) != 3 || got.Text != "Caption" {
		t.Fatalf("kept %+v %+v", got, got.Album)
	}
	setPreview(&page, photoMessage(4, 77, ""))
	if got = keepAlbums([]model.Chat{page}, []model.Chat{kept})[0].Last; got.Album.Photos != 1 {
		t.Fatalf("newer message took the kept album: %+v", got.Album)
	}
}

// A dialog's last message, which is in no history, brings its formatting
// and thumbnail, and where the thumbnail downloads from, kept once.
func TestDialogPreviewContent(t *testing.T) {
	s := testStore(t)
	l := newList(1)
	msg := &tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 5}, Message: "a *b*", Date: 1000,
		Entities: []tg.MessageEntityClass{&tg.MessageEntityItalic{Offset: 2, Length: 3}},
		Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 42, AccessHash: 1, FileReference: []byte{1}, DCID: 2, Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 90, H: 90, Size: 900}, &tg.PhotoSize{Type: "y", W: 1280, H: 1280, Size: 90000}}}}}
	var p model.Preview
	l.previewContent(&p, msg)
	if len(p.Thumbs) != 1 || p.Thumbs[0].ID != "photo/42/s" || len(p.Entities) != 1 || p.Entities[0].Kind != "italic" {
		t.Fatalf("preview %+v", p)
	}
	if ref, ok := l.refs["photo/42/s"]; !ok || ref.Thumb != "s" || ref.ID != 42 {
		t.Fatalf("refs %+v", l.refs)
	}
	ctx := context.Background()
	if err := s.keepPreviewRefs(ctx, l.refs); err != nil {
		t.Fatal(err)
	}
	var kept fileLocation
	if ok, err := s.history.cache.Get(ctx, "ref/photo/42/s", &kept); !ok || err != nil || kept.Thumb != "s" {
		t.Fatalf("cache %v %v %+v", ok, err, kept)
	}
	// The same again writes nothing: the cache's copy is changed to see.
	if err := s.history.cache.Put(ctx, "ref/photo/42/s", fileLocation{Thumb: "marked"}); err != nil {
		t.Fatal(err)
	}
	if err := s.keepPreviewRefs(ctx, l.refs); err != nil {
		t.Fatal(err)
	}
	if _, _ = s.history.cache.Get(ctx, "ref/photo/42/s", &kept); kept.Thumb != "marked" {
		t.Fatal("the same reference was written again")
	}
}
