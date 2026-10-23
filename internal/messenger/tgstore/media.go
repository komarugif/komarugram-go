// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/dcpool"
	"sort"
	"sync"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// MaxMediaBytes is model.MaxMediaBytes, which downloads read whole stay
// within.
const MaxMediaBytes = model.MaxMediaBytes

// cappedBuffer enforces the limit against server data, not just metadata.
type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxMediaBytes {
		return 0, model.ErrMediaTooLarge
	}
	return b.Buffer.Write(p)
}
func (s *Store) Media(ctx context.Context, m model.Message) ([]byte, error) {
	return s.MediaProgress(ctx, m, nil)
}
func (s *Store) MediaProgress(ctx context.Context, m model.Message, progress func(int64, int64)) ([]byte, error) {
	if m.Media == nil {
		return nil, errors.New("no media")
	}
	if m.Media.Size > MaxMediaBytes {
		return nil, model.ErrMediaTooLarge
	}
	c := s.history
	c.mu.Lock()
	cache, pool := c.cache, c.pool
	ref, ok := c.refs[m.Media.ID]
	c.mu.Unlock()
	if cache == nil {
		return nil, errors.New("media: connection not ready")
	}
	start := diagnostics.Start()
	b, e := cache.Media(ctx, m.Media.ID)
	if !start.IsZero() {
		name := "media.cache-miss"
		if len(b) > 0 {
			name = "media.cache-hit"
		}
		s.profileHistory(name, m.Key.ChatID, 0, 0, len(b), start, e)
	}
	if e != nil || len(b) > 0 {
		return b, e
	}
	if b := mediaFromDownload(ctx, cache, m); b != nil {
		// The user's download of it, every chunk as it was tagged.
		return b, cache.SaveMedia(ctx, m.Media.ID, b)
	}
	if pool == nil {
		return nil, errors.New("media unavailable offline")
	}
	if !ok {
		ok, e = cache.Get(ctx, "ref/"+m.Media.ID, &ref)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, errors.New("media location missing")
		}
	}
	download := func() ([]byte, error) {
		if ref.WebURL != "" {
			return downloadPickerWeb(ctx, pool, ref)
		}
		out := &cappedFile{progress: func(n int64) {
			if progress != nil {
				progress(n, m.Media.Size)
			}
		}}
		_, err := downloader.NewDownloader().WithAllowCDN(true).Download(withDownloadSize(dcpool.DownloadClient(pool, ctx, ref.DC), m.Media.Size), ref.input()).WithThreads(4).Parallel(ctx, out)
		return out.data, err
	}
	start = diagnostics.Start()
	b, e = download()
	if !start.IsZero() {
		s.profileHistory("media.download", m.Key.ChatID, 0, 0, len(b), start, e)
	}
	if tgerr.Is(e, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_EMPTY", "FILE_REFERENCE_INVALID") {
		if e = s.refreshReference(ctx, m); e != nil {
			return nil, e
		}
		c.mu.Lock()
		ref, ok = c.refs[m.Media.ID]
		c.mu.Unlock()
		if !ok {
			return nil, errors.New("media changed")
		}
		b, e = download()
	}
	if e != nil {
		return nil, e
	}
	if m.Media.Size > 0 && int64(len(b)) != m.Media.Size {
		return nil, fmt.Errorf("media size mismatch: received %d, expected %d", len(b), m.Media.Size)
	}
	if e = cache.SaveMedia(ctx, m.Media.ID, b); e != nil {
		return nil, e
	}
	return b, nil
}
func (s *Store) refreshReference(ctx context.Context, m model.Message) error {
	c := s.history
	c.mu.Lock()
	api, peer, start := c.api, c.peers[m.Key.ChatID], c.generation
	c.mu.Unlock()
	if api == nil {
		return errors.New("media reference expired while offline")
	}
	var ref fileLocation
	c.mu.Lock()
	ref, found := c.refs[m.Media.ID]
	cache := c.cache
	c.mu.Unlock()
	if !found && cache != nil {
		_, _ = cache.Get(ctx, "ref/"+m.Media.ID, &ref)
	}
	if ref.WallpaperID != 0 {
		wall, e := api.AccountGetWallPaper(ctx, &tg.InputWallPaper{ID: ref.WallpaperID, AccessHash: ref.WallpaperHash})
		if e != nil {
			return e
		}
		s.wallpaper(ctx, wall)
		return nil
	}
	if model.IsProfilePhoto(m.Key.MessageID) {
		return s.refreshProfilePhoto(ctx, api, peer, ref)
	}
	if m.Key.MessageID == 0 && ref.StickerSet != 0 {
		// A sticker or an emoji of the picker, perhaps from its cache: its
		// set renews it.
		raw, e := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{Stickerset: &tg.InputStickerSetID{ID: ref.StickerSet, AccessHash: ref.StickerSetHash}})
		if e != nil {
			return e
		}
		if pack, ok := raw.(*tg.MessagesStickerSet); ok {
			s.pickerItems(ctx, pack.Documents)
		}
		return nil
	}
	if m.Key.MessageID == 0 && ref.SavedGIF {
		res, e := api.MessagesGetSavedGifs(ctx, 0)
		if e != nil {
			return e
		}
		if gifs, ok := res.(*tg.MessagesSavedGifs); ok {
			s.savedGIFItems(ctx, gifs.Gifs)
		}
		return nil
	}
	if ref.Gift {
		chat := ref.GiftChat
		if chat == 0 {
			chat = m.Key.ChatID
		}
		_, err := s.SharedCollection(ctx, chat, model.SharedGifts, ref.GiftOffset, 60)
		return err
	}
	if m.Key.MessageID < 0 {
		stories, e := api.StoriesGetStoriesByID(ctx, &tg.StoriesGetStoriesByIDRequest{Peer: peer.input(), ID: []int{-int(m.Key.MessageID)}})
		if e != nil {
			return e
		}
		for _, raw := range stories.Stories {
			if story, ok := raw.(*tg.StoryItem); ok {
				_, e = s.collectionMessage(ctx, peer, &tg.Message{ID: -story.ID, Media: story.Media, Date: story.Date})
				return e
			}
		}
		return errors.New("story no longer available")
	}
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: int(m.Key.MessageID)}}
	var res tg.MessagesMessagesClass
	var e error
	if peer.Kind == "channel" {
		res, e = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, ID: ids})
	} else {
		res, e = api.MessagesGetMessages(ctx, ids)
	}
	if e != nil {
		return e
	}
	mod, ok := res.AsModified()
	if !ok {
		return fmt.Errorf("message unavailable")
	}
	_, e = s.ingest(ctx, mod.GetMessages(), false, start)
	return e
}

func (s *Store) CustomEmoji(ctx context.Context, id int64) (model.Message, error) {
	c := s.history
	c.mu.Lock()
	api, cache := c.api, c.cache
	c.mu.Unlock()
	var m model.Message
	if cache == nil {
		return m, errors.New("emoji: connection not ready")
	}
	// The second version of the key: the first kept no TextColor.
	key := fmt.Sprintf("emoji/v2/%d", id)
	if ok, e := cache.Get(ctx, key, &m); e != nil || ok {
		return m, e
	}
	if api == nil {
		return m, errors.New("emoji unavailable offline")
	}
	docs, e := api.MessagesGetCustomEmojiDocuments(ctx, []int64{id})
	if e != nil {
		return m, e
	}
	for _, doc := range docs {
		if d, ok := doc.(*tg.Document); ok {
			kind, meta, ref := documentMedia(d)
			m = model.Message{Kind: kind, Media: meta}
			c.mu.Lock()
			c.refs[meta.ID] = *ref
			c.mu.Unlock()
			if e = cache.Put(ctx, "ref/"+meta.ID, ref); e != nil {
				return m, e
			}
			e = cache.Put(ctx, key, m)
			return m, e
		}
	}
	return m, errors.New("emoji unavailable")
}

// cappedFile is a bounded random-access destination for parallel DC downloads.
type cappedFile struct {
	progress func(int64)
	received int64
	mu       sync.Mutex
	data     []byte
}

func (b *cappedFile) WriteAt(p []byte, off int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if off < 0 || off > MaxMediaBytes || int64(len(p)) > MaxMediaBytes-off {
		return 0, model.ErrMediaTooLarge
	}
	end := int(off) + len(p)
	if end > len(b.data) {
		b.data = append(b.data, make([]byte, end-len(b.data))...)
	}
	copy(b.data[int(off):end], p)
	b.received += int64(len(p))
	if b.progress != nil {
		b.progress(b.received)
	}
	return len(p), nil
}

// ChatPhotos pages the chat's photos and videos; the local cache remains available offline.
// A page of the cache tells no total, and has more photos when it is full.
func (s *Store) ChatPhotos(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	return s.gallery(ctx, model.SharedPhotoVideos, (*historycache.Cache).Photos, chat, anchor, dir, limit)
}

// ChatGIFs pages the chat's GIFs, as ChatPhotos its photos and videos.
func (s *Store) ChatGIFs(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	return s.gallery(ctx, model.SharedGIFs, (*historycache.Cache).GIFs, chat, anchor, dir, limit)
}

// gallery pages the chat's media of kind from Telegram, and from the cache
// with cached when Telegram cannot be asked.
func (s *Store) gallery(ctx context.Context, kind model.SharedKind, cached func(*historycache.Cache, context.Context, int64, int, int, int) ([]model.Message, error), chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	page, err := s.searchMedia(ctx, chat, kind, anchor, dir, limit)
	if err == nil {
		sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].Key.MessageID < page.Messages[j].Key.MessageID })
		return model.GalleryPage{Messages: page.Messages, Total: page.Total, More: page.More}, nil
	}
	if cache := s.Cache(); cache != nil {
		found, err := cached(cache, ctx, chat, int(anchor), dir, limit)
		return model.GalleryPage{Messages: found, More: len(found) >= limit}, err
	}
	return model.GalleryPage{}, err
}
