// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// pickerAPI answers the picker's requests from sets, by ID, and counts the
// sets fetched.
type pickerAPI struct {
	mu      sync.Mutex
	sets    []tg.StickerSet
	all     int64
	fetched []string
	// hold, when set, keeps every answer until it is closed.
	hold chan struct{}
}

func (p *pickerAPI) client() *tg.Client {
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		p.mu.Lock()
		hold := p.hold
		p.mu.Unlock()
		if hold != nil {
			<-hold
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		switch r := in.(type) {
		case *tg.MessagesGetRecentStickersRequest:
			if r.Hash == 7 {
				return &tg.MessagesRecentStickersNotModified{}, nil
			}
			return &tg.MessagesRecentStickers{Hash: 7}, nil
		case *tg.MessagesGetAllStickersRequest:
			if r.Hash == p.all {
				return &tg.MessagesAllStickersNotModified{}, nil
			}
			return &tg.MessagesAllStickers{Hash: p.all, Sets: p.sets}, nil
		case *tg.MessagesGetStickerSetRequest:
			id := r.Stickerset.(*tg.InputStickerSetID).ID
			for _, set := range p.sets {
				if set.ID != id {
					continue
				}
				p.fetched = append(p.fetched, fmt.Sprint(id, "@", r.Hash))
				if r.Hash == set.Hash {
					return &tg.MessagesStickerSetNotModified{}, nil
				}
				doc := &tg.Document{ID: id*10 + int64(set.Hash), AccessHash: 1, MimeType: "image/webp", Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeSticker{Alt: "🙂", Stickerset: &tg.InputStickerSetID{ID: id, AccessHash: set.AccessHash}},
				}}
				return &tg.MessagesStickerSet{Set: set, Documents: []tg.DocumentClass{doc}}, nil
			}
			return nil, fmt.Errorf("no set %d", id)
		case *tg.MessagesGetFeaturedStickersRequest:
			return &tg.MessagesFeaturedStickersNotModified{}, nil
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

func (p *pickerAPI) took() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.fetched
	p.fetched = nil
	return out
}

// The catalogue asks again only for the sets whose hash changed, and the
// stickers of each keep the set that renews their references.
func TestPickerFetchesChangedSets(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	api := &pickerAPI{all: 1, sets: []tg.StickerSet{{ID: 1, AccessHash: 11, Hash: 100, Title: "One"}, {ID: 2, AccessHash: 22, Hash: 200, Title: "Two"}}}
	client := api.client()
	page, err := s.pickerCatalogue(ctx, client, model.PickerStickers)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Packs) != 2 || len(api.took()) != 2 {
		t.Fatalf("first fetch: %+v", page)
	}
	ref := s.history.refs[page.Packs[0].Items[0].ID]
	if ref.StickerSet != 1 || ref.StickerSetHash != 11 {
		t.Fatalf("a sticker's reference keeps no set: %+v", ref)
	}

	// Nothing changed: no set is asked for.
	s.picker.expire(model.PickerStickers)
	if _, err = s.pickerCatalogue(ctx, client, model.PickerStickers); err != nil {
		t.Fatal(err)
	}
	if took := api.took(); len(took) != 0 {
		t.Fatalf("unchanged sets fetched: %v", took)
	}

	// The second set changed, and a third came.
	api.mu.Lock()
	api.all = 2
	api.sets[1].Hash = 201
	api.sets = append(api.sets, tg.StickerSet{ID: 3, AccessHash: 33, Hash: 300, Title: "Three"})
	api.mu.Unlock()
	s.picker.expire(model.PickerStickers)
	page, err = s.pickerCatalogue(ctx, client, model.PickerStickers)
	if err != nil {
		t.Fatal(err)
	}
	took := map[string]bool{}
	for _, f := range api.took() {
		took[f] = true
	}
	if len(took) != 2 || !took["2@200"] || !took["3@0"] {
		t.Fatalf("fetched %v, want the second, by its old hash, and the third", took)
	}
	if len(page.Packs) != 3 || page.Packs[0].Items[0].DocumentID != 10+100 || page.Packs[1].Items[0].DocumentID != 20+201 || page.Packs[2].Title != "Three" {
		t.Fatalf("packs %+v", page.Packs)
	}
}

// A new window's picker, on a store just started, shows the catalogue the
// cache kept at once, offline too, and the one Telegram tells after it.
func TestPickerShowsCachedFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "h")
	ctx := context.Background()
	api := &pickerAPI{all: 1, sets: []tg.StickerSet{{ID: 1, AccessHash: 11, Hash: 100, Title: "One"}}}
	first := New(nil)
	if err := first.Configure(ctx, "a", dir, nil); err != nil {
		t.Fatal(err)
	}
	first.history.api = api.client()
	if _, err := first.Picker(ctx, model.PickerRequest{Tab: model.PickerStickers}); err != nil {
		t.Fatal(err)
	}
	first.Close()
	api.took()

	s := New(nil)
	if err := s.Configure(ctx, "a", dir, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	offline, err := s.Picker(ctx, model.PickerRequest{Tab: model.PickerStickers})
	if err != nil || len(offline.Packs) != 1 || offline.Stale {
		t.Fatalf("offline page %+v, %v", offline, err)
	}

	hold := make(chan struct{})
	api.mu.Lock()
	api.hold = hold
	api.all = 2
	api.sets = append(api.sets, tg.StickerSet{ID: 2, AccessHash: 22, Hash: 200, Title: "Two"})
	api.mu.Unlock()
	s.history.mu.Lock()
	s.history.api = api.client()
	s.history.mu.Unlock()
	// Telegram does not answer yet: the page kept is shown.
	page, err := s.Picker(ctx, model.PickerRequest{Tab: model.PickerStickers})
	if err != nil || !page.Stale || len(page.Packs) != 1 || page.Packs[0].Title != "One" {
		t.Fatalf("page kept %+v, %v", page, err)
	}
	close(hold)
	page, err = s.RefreshPicker(ctx, model.PickerRequest{Tab: model.PickerStickers})
	if err != nil || page.Stale || len(page.Packs) != 2 {
		t.Fatalf("refreshed page %+v, %v", page, err)
	}
	if took := api.took(); len(took) != 1 || took[0] != "2@0" {
		t.Fatalf("fetched %v, want the new set alone", took)
	}
}
