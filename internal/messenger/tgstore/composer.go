// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/voice"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
)

type pickerCache struct {
	mu    sync.Mutex
	pages map[model.PickerTab]model.PickerPage
	at    map[model.PickerTab]time.Time
}

func (s *Store) pickerItems(ctx context.Context, docs []tg.DocumentClass) []model.PickerItem {
	var out []model.PickerItem
	for _, raw := range docs {
		d, ok := raw.(*tg.Document)
		if !ok {
			continue
		}
		kind, meta, ref := documentMedia(d)
		item := model.PickerItem{ID: meta.ID, DocumentID: d.ID, Media: model.Message{Kind: kind, Media: meta}}
		for _, a := range d.Attributes {
			switch a := a.(type) {
			case *tg.DocumentAttributeSticker:
				item.Emoji = a.Alt
			case *tg.DocumentAttributeCustomEmoji:
				item.Emoji = a.Alt
				item.Custom = true
			}
		}
		// The thumbnail is located too: a GIF shows it until played.
		refs := map[string]fileLocation{meta.ID: *ref}
		if thumb := meta.Thumbnail; thumb != nil {
			if r := *ref; thumb.Width > 0 {
				r.Thumb = thumb.ID[strings.LastIndexByte(thumb.ID, '/')+1:]
				refs[thumb.ID] = r
			}
		}
		s.history.mu.Lock()
		for id, r := range refs {
			s.history.refs[id] = r
		}
		cache := s.history.cache
		s.history.mu.Unlock()
		if cache != nil {
			for id, r := range refs {
				_ = cache.Put(ctx, "ref/"+id, r)
			}
		}
		out = append(out, item)
	}
	return out
}
func (s *Store) Picker(ctx context.Context, req model.PickerRequest) (model.PickerPage, error) {
	s.history.mu.Lock()
	api := s.history.api
	peer := s.history.peers[req.ChatID]
	s.history.mu.Unlock()
	if api == nil {
		return model.PickerPage{}, errors.New("picker unavailable offline")
	}
	if req.Tab == model.PickerGIF {
		if req.Query == "" {
			res, err := api.MessagesGetSavedGifs(ctx, 0)
			if err != nil {
				return model.PickerPage{}, err
			}
			if gifs, ok := res.(*tg.MessagesSavedGifs); ok {
				return model.PickerPage{Items: s.pickerItems(ctx, gifs.Gifs)}, nil
			}
			return model.PickerPage{}, nil
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
	base, err := s.pickerCatalogue(ctx, api, req.Tab)
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
	offset, _ := strconv.Atoi(req.Offset)
	res, err := api.MessagesSearchStickers(ctx, &tg.MessagesSearchStickersRequest{Emojis: req.Tab == model.PickerEmoji, Q: req.Query, LangCode: []string{req.Language}, Offset: offset, Limit: 60})
	if err != nil {
		return model.PickerPage{}, err
	}
	var global []model.PickerItem
	next := ""
	if found, ok := res.(*tg.MessagesFoundStickers); ok {
		global = s.pickerItems(ctx, found.Stickers)
		if n, ok := found.GetNextOffset(); ok {
			next = strconv.Itoa(n)
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
func (s *Store) pickerCatalogue(ctx context.Context, api *tg.Client, tab model.PickerTab) (model.PickerPage, error) {
	s.picker.mu.Lock()
	defer s.picker.mu.Unlock()
	if time.Since(s.picker.at[tab]) < 5*time.Minute {
		return s.picker.pages[tab], nil
	}
	page := model.PickerPage{}
	var result tg.MessagesAllStickersClass
	var err error
	if tab == model.PickerEmoji {
		result, err = api.MessagesGetEmojiStickers(ctx, 0)
	} else {
		recent, e := api.MessagesGetRecentStickers(ctx, &tg.MessagesGetRecentStickersRequest{})
		if e != nil {
			return page, e
		}
		if r, ok := recent.(*tg.MessagesRecentStickers); ok {
			page.Recent = s.pickerItems(ctx, r.Stickers)
		}
		result, err = api.MessagesGetAllStickers(ctx, 0)
	}
	if err != nil {
		return page, err
	}
	if all, ok := result.(*tg.MessagesAllStickers); ok {
		installed := make(map[int64]bool, len(all.Sets))
		page.Packs = make([]model.PickerPack, len(all.Sets))
		group, gctx := errgroup.WithContext(ctx)
		group.SetLimit(4)
		for n, set := range all.Sets {
			installed[set.ID] = true
			group.Go(func() error {
				raw, e := api.MessagesGetStickerSet(gctx, &tg.MessagesGetStickerSetRequest{Stickerset: &tg.InputStickerSetID{ID: set.ID, AccessHash: set.AccessHash}})
				if e != nil {
					return e
				}
				if pack, ok := raw.(*tg.MessagesStickerSet); ok {
					items := s.pickerItems(gctx, pack.Documents)
					keywords := map[int64]string{}
					for _, k := range pack.Keywords {
						keywords[k.DocumentID] = strings.Join(k.Keyword, " ")
					}
					for i := range items {
						items[i].Keywords = keywords[items[i].DocumentID]
					}
					page.Packs[n] = model.PickerPack{ID: set.ID, Title: set.Title, Items: items, Complete: true,
						Ref: model.StickerSetRef{Type: "id", ID: set.ID, AccessHash: set.AccessHash}}
				}
				return nil
			})
		}
		if err = group.Wait(); err != nil {
			return page, err
		}
		var featured tg.MessagesFeaturedStickersClass
		if tab == model.PickerEmoji {
			featured, err = api.MessagesGetFeaturedEmojiStickers(ctx, 0)
		} else {
			featured, err = api.MessagesGetFeaturedStickers(ctx, 0)
		}
		if err != nil {
			// Installed packs still work. Retry recommendations next time.
			return page, nil
		}
		if listing, ok := featured.(*tg.MessagesFeaturedStickers); ok {
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
				page.Featured = append(page.Featured, model.PickerPack{
					ID: set.ID, Title: set.Title, Items: s.pickerItems(ctx, docs),
					Ref: model.StickerSetRef{Type: "id", ID: set.ID, AccessHash: set.AccessHash},
				})
			}
		}
	}
	if s.picker.pages == nil {
		s.picker.pages = map[model.PickerTab]model.PickerPage{}
		s.picker.at = map[model.PickerTab]time.Time{}
	}
	s.picker.pages[tab] = page
	s.picker.at[tab] = time.Now()
	return page, nil
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
		return s.sendFiles(ctx, api, chat, peer, replyTo, msg)
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
		file, err := uploader.NewUploader(api).FromPath(ctx, msg.Path)
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
	if pendingText(msg) {
		pending = s.addPending(shown, msg)
		defer s.dropPending(pending)
	}
	if media != nil {
		caption := msg.Text
		if len(msg.Tasks) > 0 {
			caption = ""
		}
		res, err = api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer.input(), RandomID: msg.RandomID, Message: caption, Media: media, ReplyTo: replyTo})
	} else {
		req := &tg.MessagesSendMessageRequest{Peer: peer.input(), RandomID: msg.RandomID, Message: msg.Text, ReplyTo: replyTo, NoWebpage: !s.SendPermissions(chat).Allows(model.SendLinkPreview)}
		for _, e := range msg.Entities {
			if e.Kind == "emoji" {
				req.Entities = append(req.Entities, &tg.MessageEntityCustomEmoji{Offset: e.Offset, Length: e.Length, DocumentID: e.DocumentID})
			}
		}
		res, err = api.MessagesSendMessage(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("send: %w", err)
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
		accepted := &tg.Message{ID: short.ID, Out: true, PeerID: destination, FromID: &tg.PeerUser{UserID: s.Me().ID}, Message: msg.Text, Date: short.Date, Media: short.Media, Entities: short.Entities}
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
