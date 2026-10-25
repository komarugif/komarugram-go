// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// savedGIFsAPI answers messages.saveGif and messages.saveRecentSticker,
// keeping what was asked; expired refuses each document's first call with
// an expired reference.
type savedGIFsAPI struct {
	mu      sync.Mutex
	asked   []string
	expired bool
}

func (a *savedGIFsAPI) client() *tg.Client {
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		doc := func(c tg.InputDocumentClass) string {
			d := c.(*tg.InputDocument)
			return fmt.Sprintf("%d/%d/%s", d.ID, d.AccessHash, d.FileReference)
		}
		switch r := in.(type) {
		case *tg.MessagesSaveGifRequest:
			if a.expired && string(r.ID.(*tg.InputDocument).FileReference) == "old" {
				return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			a.asked = append(a.asked, fmt.Sprintf("gif %s unsave=%v", doc(r.ID), r.Unsave))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesSaveRecentStickerRequest:
			a.asked = append(a.asked, fmt.Sprintf("recent %s unsave=%v", doc(r.ID), r.Unsave))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesGetMessagesRequest:
			// The message renews the reference.
			d := &tg.Document{ID: 9, AccessHash: 99, FileReference: []byte("new"), MimeType: "video/mp4", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{W: 1, H: 1}}}
			return &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 3, PeerID: &tg.PeerUser{UserID: 5}, Media: &tg.MessageMediaDocument{Document: d}}}}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

func (a *savedGIFsAPI) took() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.asked
	a.asked = nil
	return out
}

func savedIDs(s *Store) []string {
	s.picker.mu.Lock()
	defer s.picker.mu.Unlock()
	var out []string
	for _, item := range s.picker.pages[model.PickerGIF].Items {
		out = append(out, item.ID)
	}
	return out
}

// A GIF of a message is saved first among the saved GIFs, with the
// document Telegram knows it by, and deleted from them; over the limit the
// oldest goes.
func TestSaveGIF(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	api := &savedGIFsAPI{}
	s.history.api = api.client()
	s.picker.init()
	s.picker.pages[model.PickerGIF] = model.PickerPage{Items: []model.PickerItem{{ID: "document/1"}, {ID: "document/2"}}}
	s.history.refs["document/9"] = fileLocation{ID: 9, Hash: 99, Reference: []byte("ref")}
	gif := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 3}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/9", MIMEType: "video/mp4"}}

	if s.SavedGIF("document/9") {
		t.Fatal("saved before it is")
	}
	if err := s.SaveGIF(ctx, gif, false); err != nil {
		t.Fatal(err)
	}
	if took := api.took(); len(took) != 1 || took[0] != "gif 9/99/ref unsave=false" {
		t.Fatalf("asked %v", took)
	}
	if got := fmt.Sprint(savedIDs(s)); got != "[document/9 document/1 document/2]" || !s.SavedGIF("document/9") {
		t.Fatalf("saved GIFs %s", got)
	}
	if first := s.picker.pages[model.PickerGIF].Items[0]; first.DocumentID != 9 || first.Media.Kind != model.MessageGIF {
		t.Fatalf("the GIF saved is shown as %+v", first)
	}
	if !s.history.refs["document/9"].SavedGIF {
		t.Fatal("the saved GIF's reference is renewed by nothing in the picker")
	}
	var kept pickerSaved
	if ok, err := s.Cache().Get(ctx, pickerKey(model.PickerGIF), &kept); err != nil || !ok || len(kept.Page.Items) != 3 {
		t.Fatalf("cache keeps %+v (%v, %v)", kept.Page.Items, ok, err)
	}

	// Saved again, it moves to the top, not twice.
	s.history.refs["document/2"] = fileLocation{ID: 2, Hash: 22}
	if err := s.SaveGIF(ctx, model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/2"}}, false); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(savedIDs(s)); got != "[document/2 document/9 document/1]" {
		t.Fatalf("saved GIFs %s", got)
	}

	if err := s.SaveGIF(ctx, gif, true); err != nil {
		t.Fatal(err)
	}
	if took := api.took(); len(took) != 2 || took[1] != "gif 9/99/ref unsave=true" {
		t.Fatalf("asked %v", took)
	}
	if got := fmt.Sprint(savedIDs(s)); got != "[document/2 document/1]" || s.SavedGIF("document/9") {
		t.Fatalf("saved GIFs after the delete %s", got)
	}

	// A list as long as the limit loses its last GIF.
	limit := model.SavedGIFLimit(s.Premium())
	items := make([]model.PickerItem, limit)
	for i := range items {
		items[i].ID = fmt.Sprintf("document/%d", 100+i)
	}
	s.picker.mu.Lock()
	s.picker.pages[model.PickerGIF] = model.PickerPage{Items: items}
	s.picker.mu.Unlock()
	if err := s.SaveGIF(ctx, gif, false); !errors.Is(err, model.ErrSavedGIFLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	if ids := savedIDs(s); len(ids) != limit || ids[0] != "document/9" || ids[limit-1] != fmt.Sprintf("document/%d", 100+limit-2) {
		t.Fatalf("over the limit %d: %d GIFs, %s first, %s last", limit, len(ids), ids[0], ids[len(ids)-1])
	}
}

// A GIF whose reference expired is saved with the one its message renews.
func TestSaveGIFRenewsReference(t *testing.T) {
	s := testStore(t)
	api := &savedGIFsAPI{expired: true}
	s.history.api = api.client()
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.refs["document/9"] = fileLocation{ID: 9, Hash: 99, Reference: []byte("old")}
	gif := model.Message{Key: model.MessageKey{ChatID: 5, MessageID: 3}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/9"}}
	if err := s.SaveGIF(context.Background(), gif, false); err != nil {
		t.Fatal(err)
	}
	if took := api.took(); len(took) != 1 || took[0] != "gif 9/99/new unsave=false" {
		t.Fatalf("asked %v", took)
	}
}

// A sticker taken off the recent goes off Telegram's list and off the one
// kept of what was chosen here.
func TestRemoveRecentSticker(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	api := &savedGIFsAPI{}
	s.history.api = api.client()
	sticker := func(id string) model.PickerItem {
		return model.PickerItem{ID: id, Media: model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: id}}}
	}
	s.history.refs["document/7"] = fileLocation{ID: 7, Hash: 77, Reference: []byte("r"), StickerSet: 1}
	s.picker.init()
	s.picker.pages[model.PickerStickers] = model.PickerPage{Recent: []model.PickerItem{sticker("document/6"), sticker("document/7")}}
	if err := s.RememberPicker(ctx, model.PickerStickers, sticker("document/7")); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRecentSticker(ctx, sticker("document/7")); err != nil {
		t.Fatal(err)
	}
	if took := api.took(); len(took) != 1 || took[0] != "recent 7/77/r unsave=true" {
		t.Fatalf("asked %v", took)
	}
	// Offline, the page is the one kept.
	s.history.api = nil
	page, err := s.Picker(ctx, model.PickerRequest{Tab: model.PickerStickers})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Recent) != 1 || page.Recent[0].ID != "document/6" {
		t.Fatalf("recent stickers %+v", page.Recent)
	}
}
