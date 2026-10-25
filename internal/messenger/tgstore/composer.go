// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/voice"

	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
)

// pickerFresh is how long a tab's catalogue is taken as it is, before
// Telegram is asked what changed in it.
const pickerFresh = 5 * time.Minute

// pickerCache keeps each tab's catalogue: its page, the hashes Telegram
// tells by whether it changed since, and when it was brought up to date;
// refreshing is the fetch of a tab that runs, which every caller waits for.
type pickerCache struct {
	mu         sync.Mutex
	pages      map[model.PickerTab]model.PickerPage
	hashes     map[model.PickerTab]pickerHashes
	at         map[model.PickerTab]time.Time
	refreshing map[model.PickerTab]*pickerRefresh
}

// pickerHashes are what a catalogue was fetched with: the hash of the
// installed sets, or of the saved GIFs, of recent stickers and of featured
// sets, and each set's, by its ID.
type pickerHashes struct {
	All, Recent, Featured int64
	Sets                  map[int64]int
}

// pickerSaved is a tab's catalogue as the cache keeps it.
type pickerSaved struct {
	Page   model.PickerPage
	Hashes pickerHashes
}

// pickerRefresh is a fetch of a catalogue, done once done is closed.
type pickerRefresh struct {
	done chan struct{}
	page model.PickerPage
	err  error
}

func pickerKey(tab model.PickerTab) string { return fmt.Sprintf("picker/catalogue/v1/%d", tab) }

func (p *pickerCache) init() {
	if p.pages == nil {
		p.pages = map[model.PickerTab]model.PickerPage{}
	}
	if p.hashes == nil {
		p.hashes = map[model.PickerTab]pickerHashes{}
	}
	if p.at == nil {
		p.at = map[model.PickerTab]time.Time{}
	}
	if p.refreshing == nil {
		p.refreshing = map[model.PickerTab]*pickerRefresh{}
	}
}

// expire has the catalogues of tabs asked for again, as an update tells
// they changed.
func (p *pickerCache) expire(tabs ...model.PickerTab) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, tab := range tabs {
		delete(p.at, tab)
	}
}

// stickerTab is the tab of sticker sets, or of emoji ones.
func stickerTab(emoji bool) model.PickerTab {
	if emoji {
		return model.PickerEmoji
	}
	return model.PickerStickers
}

func (s *Store) pickerItems(ctx context.Context, docs []tg.DocumentClass) []model.PickerItem {
	return s.pickerItemsOf(ctx, docs, false)
}

// savedGIFItems are pickerItems of the saved GIFs.
func (s *Store) savedGIFItems(ctx context.Context, docs []tg.DocumentClass) []model.PickerItem {
	return s.pickerItemsOf(ctx, docs, true)
}

// pickerItemsOf makes items of docs, and keeps where their media are, with
// what renews their references: their set, or the saved GIFs.
func (s *Store) pickerItemsOf(ctx context.Context, docs []tg.DocumentClass, savedGIF bool) []model.PickerItem {
	var out []model.PickerItem
	refs := map[string]fileLocation{}
	for _, raw := range docs {
		d, ok := raw.(*tg.Document)
		if !ok {
			continue
		}
		kind, meta, ref := documentMedia(d)
		ref.SavedGIF = savedGIF
		item := model.PickerItem{ID: meta.ID, DocumentID: d.ID, Media: model.Message{Kind: kind, Media: meta}}
		set := func(in tg.InputStickerSetClass) {
			if in, ok := in.(*tg.InputStickerSetID); ok {
				ref.StickerSet, ref.StickerSetHash = in.ID, in.AccessHash
			}
		}
		for _, a := range d.Attributes {
			switch a := a.(type) {
			case *tg.DocumentAttributeSticker:
				item.Emoji = a.Alt
				set(a.Stickerset)
			case *tg.DocumentAttributeCustomEmoji:
				item.Emoji = a.Alt
				item.Custom = true
				set(a.Stickerset)
			}
		}
		// The thumbnail is located too: a GIF shows it until played.
		refs[meta.ID] = *ref
		if thumb := meta.Thumbnail; thumb != nil {
			if r := *ref; thumb.Width > 0 {
				r.Thumb = thumb.ID[strings.LastIndexByte(thumb.ID, '/')+1:]
				refs[thumb.ID] = r
			}
		}
		out = append(out, item)
	}
	s.history.mu.Lock()
	for id, r := range refs {
		s.history.refs[id] = r
	}
	cache := s.history.cache
	s.history.mu.Unlock()
	if cache != nil && len(refs) > 0 {
		// In one transaction: a set has hundreds of stickers.
		values := make(map[string]any, len(refs))
		for id, r := range refs {
			values["ref/"+id] = r
		}
		_ = cache.PutAll(ctx, values)
	}
	return out
}
func (s *Store) Picker(ctx context.Context, req model.PickerRequest) (model.PickerPage, error) {
	s.history.mu.Lock()
	api := s.history.api
	peer := s.history.peers[req.ChatID]
	s.history.mu.Unlock()
	if req.Tab == model.PickerGIF && req.Query != "" {
		if api == nil {
			return model.PickerPage{}, errors.New("picker unavailable offline")
		}
		// Use the account's configured GIF provider, like Telegram Desktop.
		cfg, err := api.HelpGetConfig(ctx)
		if err != nil {
			return model.PickerPage{}, err
		}
		username := cfg.GifSearchUsername
		if username == "" {
			username = "gif"
		}
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			return model.PickerPage{}, err
		}
		var bot tg.InputUserClass
		for _, u := range resolved.Users {
			if u, ok := u.(*tg.User); ok && u.Bot {
				bot = &tg.InputUser{UserID: u.ID, AccessHash: u.AccessHash}
				break
			}
		}
		if bot == nil {
			return model.PickerPage{}, errors.New("GIF search provider unavailable")
		}
		res, err := api.MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{Bot: bot, Peer: peer.input(), Query: req.Query, Offset: req.Offset})
		if err != nil {
			return model.PickerPage{}, err
		}
		page := model.PickerPage{Next: res.NextOffset}
		for _, raw := range res.Results {
			if r, ok := raw.(*tg.BotInlineResult); ok && r.Content != nil {
				item := s.pickerWebItem(ctx, r, cfg.WebfileDCID)
				item.QueryID = res.QueryID
				item.ResultID = r.ID
				page.Items = append(page.Items, item)
			}
			if r, ok := raw.(*tg.BotInlineMediaResult); ok {
				items := s.pickerItems(ctx, []tg.DocumentClass{r.Document})
				for _, item := range items {
					item.QueryID = res.QueryID
					item.ResultID = r.ID
					page.Items = append(page.Items, item)
				}
			}
		}
		return page, nil
	}
	// The saved GIFs, or the catalogue of sets: as kept, while Telegram is
	// asked what changed, offline too.
	base, err := s.pickerPage(ctx, api, req.Tab)
	if req.Tab == model.PickerGIF {
		return base, err
	}
	if cache := s.Cache(); cache != nil {
		var recent []model.PickerItem
		_, _ = cache.Get(ctx, fmt.Sprintf("picker/recent/%d", req.Tab), &recent)
		base.Recent = model.PreferSaved(recent, base.Recent)
	}
	if err != nil {
		return base, err
	}
	// The emoji of the picker are searched by the client, in its own names
	// and keywords; what Telegram finds is custom emoji and stickers.
	if req.Query == "" {
		return base, nil
	}
	var global []model.PickerItem
	next := ""
	// Offline, the sets kept are searched alone.
	if api != nil {
		offset, _ := strconv.Atoi(req.Offset)
		res, err := api.MessagesSearchStickers(ctx, &tg.MessagesSearchStickersRequest{Emojis: req.Tab == model.PickerEmoji, Q: req.Query, LangCode: []string{req.Language}, Offset: offset, Limit: 60})
		if err != nil {
			return model.PickerPage{}, err
		}
		if found, ok := res.(*tg.MessagesFoundStickers); ok {
			global = s.pickerItems(ctx, found.Stickers)
			if n, ok := found.GetNextOffset(); ok {
				next = strconv.Itoa(n)
			}
		}
	}
	ids := map[string]bool{}
	for _, i := range global {
		ids[i.ID] = true
	}
	var saved []model.PickerItem
	q := strings.ToLower(req.Query)
	for _, pack := range base.Packs {
		for _, i := range pack.Items {
			if ids[i.ID] || strings.Contains(strings.ToLower(pack.Title), q) || strings.Contains(i.Emoji, q) || strings.Contains(strings.ToLower(i.Keywords), q) {
				saved = append(saved, i)
			}
		}
	}
	// The rest of the page stays as it was, for the list not to change as
	// the search ends.
	return model.PickerPage{Packs: base.Packs, Recent: base.Recent, Featured: base.Featured, Items: model.PreferSaved(saved, global), Next: next}, nil
}

// RefreshPicker implements model.PickerRefresher: it waits for the fetch
// of the tab's catalogue that a Stale page started, and gives the page then.
func (s *Store) RefreshPicker(ctx context.Context, req model.PickerRequest) (model.PickerPage, error) {
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return model.PickerPage{}, errors.New("picker unavailable offline")
	}
	if _, err := s.pickerCatalogue(ctx, api, req.Tab); err != nil {
		return model.PickerPage{}, err
	}
	return s.Picker(ctx, req)
}

// pickerPage is the tab's catalogue: as kept, Stale while a fetch brings it
// up to date, unless it is fresh or the account offline; fetched when
// nothing is kept.
func (s *Store) pickerPage(ctx context.Context, api *tg.Client, tab model.PickerTab) (model.PickerPage, error) {
	page, fresh, ok := s.keptCatalogue(ctx, tab)
	switch {
	case ok && (fresh || api == nil):
		return page, nil
	case ok:
		s.pickerRefresh(api, tab)
		page.Stale = true
		return page, nil
	case api == nil:
		return model.PickerPage{}, errors.New("picker unavailable offline")
	}
	return s.pickerCatalogue(ctx, api, tab)
}

// keptCatalogue is the tab's catalogue kept in memory, or in the cache,
// and whether it is fresh.
func (s *Store) keptCatalogue(ctx context.Context, tab model.PickerTab) (model.PickerPage, bool, bool) {
	p := &s.picker
	p.mu.Lock()
	page, ok := p.pages[tab]
	fresh := time.Since(p.at[tab]) < pickerFresh
	p.mu.Unlock()
	if ok {
		return page, fresh, true
	}
	cache := s.Cache()
	if cache == nil {
		return model.PickerPage{}, false, false
	}
	var saved pickerSaved
	if found, err := cache.Get(ctx, pickerKey(tab), &saved); err != nil || !found {
		return model.PickerPage{}, false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	if _, ok := p.pages[tab]; !ok {
		// Not fetched here yet: it is asked for again.
		p.pages[tab], p.hashes[tab] = saved.Page, saved.Hashes
	}
	return p.pages[tab], time.Since(p.at[tab]) < pickerFresh, true
}

// pickerCatalogue is the tab's catalogue, fetched unless fresh; a fetch
// that runs already is waited for.
func (s *Store) pickerCatalogue(ctx context.Context, api *tg.Client, tab model.PickerTab) (model.PickerPage, error) {
	r, page := s.pickerRefresh(api, tab)
	if r == nil {
		return page, nil
	}
	select {
	case <-r.done:
		return r.page, r.err
	case <-ctx.Done():
		return model.PickerPage{}, ctx.Err()
	}
}

// pickerRefresh is the fetch of the tab's catalogue that runs, started when
// none does; nil, with the page, when the page kept is fresh.
func (s *Store) pickerRefresh(api *tg.Client, tab model.PickerTab) (*pickerRefresh, model.PickerPage) {
	p := &s.picker
	p.mu.Lock()
	defer p.mu.Unlock()
	if page, ok := p.pages[tab]; ok && time.Since(p.at[tab]) < pickerFresh {
		return nil, page
	}
	if r := p.refreshing[tab]; r != nil {
		return r, model.PickerPage{}
	}
	p.init()
	r := &pickerRefresh{done: make(chan struct{})}
	p.refreshing[tab] = r
	old := pickerSaved{Page: p.pages[tab], Hashes: p.hashes[tab]}
	s.history.mu.Lock()
	base := s.history.ctx
	s.history.mu.Unlock()
	if base == nil {
		base = context.Background()
	}
	go func() {
		// The store's context, not the caller's: what a picker closed
		// meanwhile asked for is kept for the next.
		ctx, cancel := context.WithTimeout(base, 90*time.Second)
		defer cancel()
		var saved pickerSaved
		err := func() (err error) {
			defer crash.Recover("picker catalogue", func(p *crash.Panic) { err = p })
			saved, err = s.fetchCatalogue(ctx, api, tab, old)
			return err
		}()
		p.mu.Lock()
		if err == nil {
			p.pages[tab], p.hashes[tab], p.at[tab] = saved.Page, saved.Hashes, time.Now()
		}
		delete(p.refreshing, tab)
		p.mu.Unlock()
		if cache := s.Cache(); cache != nil && err == nil && !sameHashes(old.Hashes, saved.Hashes) {
			_ = cache.Put(ctx, pickerKey(tab), saved)
		}
		r.page, r.err = saved.Page, err
		close(r.done)
	}()
	return r, model.PickerPage{}
}

func sameHashes(a, b pickerHashes) bool {
	return a.All == b.All && a.Recent == b.Recent && a.Featured == b.Featured && maps.Equal(a.Sets, b.Sets)
}

// fetchCatalogue asks Telegram for the tab's catalogue with the hashes of
// old: what did not change is old's, and only the sets that did are
// fetched.
func (s *Store) fetchCatalogue(ctx context.Context, api *tg.Client, tab model.PickerTab, old pickerSaved) (pickerSaved, error) {
	var out pickerSaved
	if tab == model.PickerGIF {
		res, err := api.MessagesGetSavedGifs(ctx, old.Hashes.All)
		if err != nil {
			return out, err
		}
		if gifs, ok := res.(*tg.MessagesSavedGifs); ok {
			out.Page.Items, out.Hashes.All = s.savedGIFItems(ctx, gifs.Gifs), gifs.Hash
			return out, nil
		}
		return old, nil
	}
	if tab != model.PickerEmoji {
		recent, err := api.MessagesGetRecentStickers(ctx, &tg.MessagesGetRecentStickersRequest{Hash: old.Hashes.Recent})
		if err != nil {
			return out, err
		}
		if r, ok := recent.(*tg.MessagesRecentStickers); ok {
			out.Page.Recent, out.Hashes.Recent = s.pickerItems(ctx, r.Stickers), r.Hash
		} else {
			out.Page.Recent, out.Hashes.Recent = old.Page.Recent, old.Hashes.Recent
		}
	}
	var result tg.MessagesAllStickersClass
	var err error
	if tab == model.PickerEmoji {
		result, err = api.MessagesGetEmojiStickers(ctx, old.Hashes.All)
	} else {
		result, err = api.MessagesGetAllStickers(ctx, old.Hashes.All)
	}
	if err != nil {
		return out, err
	}
	if all, ok := result.(*tg.MessagesAllStickers); ok {
		out.Page.Packs, out.Hashes.Sets, err = s.pickerPacks(ctx, api, all.Sets, old)
		if err != nil {
			return out, err
		}
		out.Hashes.All = all.Hash
	} else {
		out.Page.Packs, out.Hashes.Sets, out.Hashes.All = old.Page.Packs, old.Hashes.Sets, old.Hashes.All
	}
	installed := make(map[int64]bool, len(out.Page.Packs))
	for _, pack := range out.Page.Packs {
		installed[pack.ID] = true
	}
	var featured tg.MessagesFeaturedStickersClass
	if tab == model.PickerEmoji {
		featured, err = api.MessagesGetFeaturedEmojiStickers(ctx, old.Hashes.Featured)
	} else {
		featured, err = api.MessagesGetFeaturedStickers(ctx, old.Hashes.Featured)
	}
	listing, ok := featured.(*tg.MessagesFeaturedStickers)
	if err != nil || !ok {
		// Not changed, or not told: the recommendations kept stay, but for
		// the sets installed since. A failure leaves no hash, to ask again
		// next time; installed packs still work.
		for _, pack := range old.Page.Featured {
			if !installed[pack.ID] {
				out.Page.Featured = append(out.Page.Featured, pack)
			}
		}
		if err == nil {
			out.Hashes.Featured = old.Hashes.Featured
		}
		return out, nil
	}
	out.Hashes.Featured = listing.Hash
	seen := make(map[int64]bool, len(listing.Sets))
	for _, covered := range listing.Sets {
		set := covered.GetSet()
		_, installedDate := set.GetInstalledDate()
		if set.ID == 0 || installed[set.ID] || (installedDate && !set.Archived) || seen[set.ID] {
			continue
		}
		seen[set.ID] = true
		var docs []tg.DocumentClass
		switch covered := covered.(type) {
		case *tg.StickerSetCovered:
			docs = []tg.DocumentClass{covered.Cover}
		case *tg.StickerSetMultiCovered:
			docs = covered.Covers
		case *tg.StickerSetFullCovered:
			docs = covered.Documents
		}
		// The catalogue needs a preview; the dialog fetches the full set.
		if len(docs) > 8 {
			docs = docs[:8]
		}
		out.Page.Featured = append(out.Page.Featured, model.PickerPack{
			ID: set.ID, Title: set.Title, Items: s.pickerItems(ctx, docs),
			Ref: model.StickerSetRef{Type: "id", ID: set.ID, AccessHash: set.AccessHash},
		})
	}
	return out, nil
}

// pickerPacks are the packs of the installed sets, in their order: those
// whose hash old has as the set's are old's, and only the others are
// fetched, a few at once. The hashes are the sets', by ID.
func (s *Store) pickerPacks(ctx context.Context, api *tg.Client, sets []tg.StickerSet, old pickerSaved) ([]model.PickerPack, map[int64]int, error) {
	kept := make(map[int64]model.PickerPack, len(old.Page.Packs))
	for _, pack := range old.Page.Packs {
		if pack.Complete {
			kept[pack.ID] = pack
		}
	}
	packs := make([]model.PickerPack, len(sets))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for n, set := range sets {
		ref := model.StickerSetRef{Type: "id", ID: set.ID, AccessHash: set.AccessHash}
		prev, had := kept[set.ID]
		prev.Title, prev.Ref = set.Title, ref
		hash, hashed := old.Hashes.Sets[set.ID]
		had = had && hashed
		if had && set.Hash != 0 && hash == set.Hash {
			packs[n] = prev
			continue
		}
		group.Go(func() error {
			req := &tg.MessagesGetStickerSetRequest{Stickerset: &tg.InputStickerSetID{ID: set.ID, AccessHash: set.AccessHash}}
			if had {
				req.Hash = hash
			}
			raw, e := api.MessagesGetStickerSet(gctx, req)
			if e != nil {
				return e
			}
			switch pack := raw.(type) {
			case *tg.MessagesStickerSet:
				items := s.pickerItems(gctx, pack.Documents)
				keywords := map[int64]string{}
				for _, k := range pack.Keywords {
					keywords[k.DocumentID] = strings.Join(k.Keyword, " ")
				}
				for i := range items {
					items[i].Keywords = keywords[items[i].DocumentID]
				}
				packs[n] = model.PickerPack{ID: set.ID, Title: set.Title, Items: items, Complete: true, Ref: ref}
			case *tg.MessagesStickerSetNotModified:
				if had {
					packs[n] = prev
				}
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, nil, err
	}
	out := packs[:0]
	hashes := make(map[int64]int, len(sets))
	for n, pack := range packs {
		if pack.ID == 0 {
			continue
		}
		out = append(out, pack)
		hashes[pack.ID] = sets[n].Hash
	}
	return out, hashes, nil
}
func (s *Store) Send(ctx context.Context, chat int64, msg model.OutgoingMessage) error {
	if err := s.checkSend(chat, msg); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	shown := chat
	s.history.mu.Lock()
	// A comment goes to the discussion group, as a reply in the thread.
	general := s.history.isTopic(chat) && s.history.threads[chat].top == model.GeneralTopic
	chat, top := s.history.threadChat(chat)
	if general {
		// A message of the General topic is one to the forum, with no
		// topic to reply into.
		top = 0
	}
	api := s.history.api
	peer, ok := s.history.peers[chat]
	s.history.mu.Unlock()
	if api == nil {
		return errors.New("cannot send while offline")
	}
	if !ok {
		return errors.New("unknown chat")
	}
	var media tg.InputMediaClass
	var replyTo tg.InputReplyToClass
	if msg.ReplyTo != 0 {
		replyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(msg.ReplyTo)}
	}
	if top != 0 {
		r := &tg.InputReplyToMessage{ReplyToMsgID: top}
		if msg.ReplyTo != 0 && int(msg.ReplyTo) != top {
			r.ReplyToMsgID = int(msg.ReplyTo)
			r.SetTopMsgID(top)
		}
		replyTo = r
	}
	if msg.Files != nil {
		return s.sendFiles(ctx, api, shown, peer, replyTo, msg)
	}
	if msg.Item != nil {
		if msg.Item.ResultID != "" {
			res, err := api.MessagesSendInlineBotResult(ctx, &tg.MessagesSendInlineBotResultRequest{Peer: peer.input(), RandomID: msg.RandomID, QueryID: msg.Item.QueryID, ID: msg.Item.ResultID, ReplyTo: replyTo})
			if err != nil {
				return err
			}
			_ = s.Handle(ctx, res)
			return nil
		}
		s.history.mu.Lock()
		ref, ok := s.history.refs[msg.Item.ID]
		s.history.mu.Unlock()
		if !ok {
			if cache := s.Cache(); cache != nil {
				ok, _ = cache.Get(ctx, "ref/"+msg.Item.ID, &ref)
			}
		}
		if !ok {
			return errors.New("media reference unavailable; reopen the picker")
		}
		media = &tg.InputMediaDocument{ID: &tg.InputDocument{ID: ref.ID, AccessHash: ref.Hash, FileReference: ref.Reference}}
	}
	if len(msg.Tasks) > 0 {
		todo := tg.TodoList{Title: tg.TextWithEntities{Text: msg.Text}}
		for n, t := range msg.Tasks {
			todo.List = append(todo.List, tg.TodoItem{ID: n + 1, Title: tg.TextWithEntities{Text: t}})
		}
		media = &tg.InputMediaTodo{Todo: todo}
	}
	if msg.Path != "" {
		info, err := os.Stat(msg.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("choose a regular file")
		}
		typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(msg.Path)))
		if typ == "" {
			typ = "application/octet-stream"
		}
		attrs, err := uploadAttributes(ctx, msg.Path, typ, msg.AsMedia, msg.FFmpeg)
		if err != nil {
			return err
		}
		if v := msg.Voice; v != nil {
			typ = voice.FileMIME(msg.Path)
			if typ == "" {
				// A recording is encoded to a temporary .ogg file.
				typ = "audio/ogg"
			}
			attrs = []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true, Duration: int(v.Duration.Round(time.Second) / time.Second), Waveform: v.Waveform}}
		}
		action := model.ActionUploadFile
		switch {
		case msg.Voice != nil:
			action = model.ActionUploadVoice
		case msg.AsMedia && (typ == "image/jpeg" || typ == "image/png"):
			action = model.ActionUploadPhoto
		case msg.AsMedia && strings.HasPrefix(typ, "video/"):
			action = model.ActionUploadVideo
		}
		file, err := s.uploader(api, shown, action).FromPath(ctx, msg.Path)
		if err != nil {
			return err
		}
		if msg.AsMedia && (typ == "image/jpeg" || typ == "image/png") {
			media = &tg.InputMediaUploadedPhoto{File: file}
		} else {
			media = &tg.InputMediaUploadedDocument{File: file, MimeType: typ, ForceFile: !msg.AsMedia && msg.Voice == nil, Attributes: attrs}
		}
	}
	var res tg.UpdatesClass
	var err error
	var pending *pendingMessage
	clearDraft := s.hasDraft(shown)
	if pendingText(msg) {
		pending = s.addPending(shown, msg)
		defer s.dropPending(pending)
	}
	if media != nil {
		caption := msg.Text
		if len(msg.Tasks) > 0 {
			caption = ""
		}
		res, err = api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer.input(), RandomID: msg.RandomID, Message: caption, Media: media, ReplyTo: replyTo, ClearDraft: clearDraft})
	} else {
		req := &tg.MessagesSendMessageRequest{Peer: peer.input(), RandomID: msg.RandomID, Message: msg.Text, ReplyTo: replyTo, NoWebpage: !s.SendPermissions(chat).Allows(model.SendLinkPreview), ClearDraft: clearDraft}
		req.Entities = inputEntities(msg.Entities)
		res, err = api.MessagesSendMessage(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if clearDraft {
		// Telegram cleared it, and tells the other devices.
		s.setDraft(chat, nil)
	}
	if id := sentID(res, msg.RandomID); pending != nil && id != 0 {
		// It stands in for the message only until that is in the history:
		// never both at once.
		s.history.mu.Lock()
		s.history.sentPending(pending, id)
		s.history.mu.Unlock()
	}
	// Once the message is in the history, the chat may be read up to it.
	defer s.readOnInteract(chat)
	if short, ok := res.(*tg.UpdateShortSentMessage); ok {
		var destination tg.PeerClass
		switch peer.Kind {
		case "user":
			destination = &tg.PeerUser{UserID: peer.ID}
		case "chat":
			destination = &tg.PeerChat{ChatID: peer.ID}
		case "channel":
			destination = &tg.PeerChannel{ChannelID: peer.ID}
		}
		// Out as Telegram tells it: in Saved Messages the user's messages
		// are not outgoing, and a message marked so would change sides once
		// the history has it from Telegram. Nor do they tell who sent them,
		// which would show a name over the message.
		accepted := &tg.Message{ID: short.ID, Out: short.Out, PeerID: destination, Message: msg.Text, Date: short.Date, Media: short.Media, Entities: short.Entities}
		if me := s.Me().ID; peer.Kind != "user" || peer.ID != me && !peer.Rights.Self {
			accepted.FromID = &tg.PeerUser{UserID: me}
		}
		if msg.ReplyTo != 0 {
			// The acknowledgement leaves out what the message replies to.
			accepted.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: int(msg.ReplyTo)}
		}
		// The short acknowledgement contains the server ID and media. Publish it
		// immediately; a failed follow-up read must never invite a duplicate send.
		messages, e := s.ingest(ctx, []tg.MessageClass{accepted}, true, 0)
		if e == nil {
			for _, m := range messages {
				s.mergeUpdate(m)
			}
		}
		s.changed()
		return nil
	}

	_ = s.Handle(ctx, res)
	return nil
}

func (s *Store) pickerWebItem(ctx context.Context, r *tg.BotInlineResult, dc int) model.PickerItem {
	doc := r.Content
	id := fmt.Sprintf("inline/%x", sha256.Sum256([]byte(doc.GetURL())))
	m := &model.MessageMedia{ID: id, MIMEType: doc.GetMimeType(), Size: int64(doc.GetSize())}
	for _, attr := range doc.GetAttributes() {
		switch a := attr.(type) {
		case *tg.DocumentAttributeVideo:
			m.Width, m.Height = a.W, a.H
		case *tg.DocumentAttributeImageSize:
			m.Width, m.Height = a.W, a.H
		}
	}
	ref := fileLocation{WebURL: doc.GetURL(), DC: dc}
	if d, ok := doc.(*tg.WebDocument); ok {
		ref.WebHash = d.AccessHash
	} else {
		ref.WebNoProxy = true
	}
	s.history.mu.Lock()
	s.history.refs[id] = ref
	cache := s.history.cache
	s.history.mu.Unlock()
	if cache != nil {
		_ = cache.Put(ctx, "ref/"+id, ref)
	}
	return model.PickerItem{ID: id, Media: model.Message{Kind: model.MessageGIF, Media: m}}
}

func (s *Store) RememberPicker(ctx context.Context, tab model.PickerTab, item model.PickerItem) error {
	cache := s.Cache()
	if cache == nil {
		return nil
	}
	s.picker.mu.Lock()
	defer s.picker.mu.Unlock()
	key := fmt.Sprintf("picker/recent/%d", tab)
	var items []model.PickerItem
	_, err := cache.Get(ctx, key, &items)
	if err != nil {
		return err
	}
	items = model.PreferSaved([]model.PickerItem{item}, items)
	if len(items) > 40 {
		items = items[:40]
	}
	return cache.Put(ctx, key, items)
}
