// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
)

func sharedFilter(kind model.SharedKind) (tg.MessagesFilterClass, error) {
	switch kind {
	case model.SharedPhotos:
		return &tg.InputMessagesFilterPhotos{}, nil
	case model.SharedVideos:
		return &tg.InputMessagesFilterVideo{}, nil
	case model.SharedPhotoVideos:
		return &tg.InputMessagesFilterPhotoVideo{}, nil
	case model.SharedFiles:
		return &tg.InputMessagesFilterDocument{}, nil
	case model.SharedMusic:
		return &tg.InputMessagesFilterMusic{}, nil
	case model.SharedLinks:
		return &tg.InputMessagesFilterURL{}, nil
	case model.SharedVoice:
		return &tg.InputMessagesFilterRoundVoice{}, nil
	case model.SharedGIFs:
		return &tg.InputMessagesFilterGif{}, nil
	case model.SharedPolls:
		return &tg.InputMessagesFilterPoll{}, nil
	case model.SharedSaved:
		return &tg.InputMessagesFilterEmpty{}, nil
	}
	return nil, errors.New("unknown shared media section")
}

// peerOperation protects the cache from Close while an independent UI request runs.
func (s *Store) peerOperation(ctx context.Context, chat int64) (context.Context, *tg.Client, peerRecord, uint64, func(), error) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	peer, ok := c.peers[chat]
	if c.closing || c.api == nil || !ok {
		return nil, nil, peer, 0, nil, errors.New("Telegram is offline or the chat is unavailable")
	}
	c.wg.Add(1)
	child, cancel := context.WithTimeout(ctx, 30*time.Second)
	stop := context.AfterFunc(c.ctx, cancel)
	return child, c.api, peer, c.generation, func() { stop(); cancel(); c.wg.Done() }, nil
}

func (s *Store) SharedCounts(ctx context.Context, chat int64) (map[model.SharedKind]int, error) {
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return nil, err
	}
	defer done()
	var filters []tg.MessagesFilterClass
	kinds := map[uint32]model.SharedKind{}
	for _, kind := range model.SharedKinds {
		if kind == model.SharedSaved {
			continue
		}
		f, _ := sharedFilter(kind)
		filters = append(filters, f)
		kinds[f.TypeID()] = kind
	}
	res, err := api.MessagesGetSearchCounters(ctx, &tg.MessagesGetSearchCountersRequest{Peer: peer.input(), Filters: filters})
	if err != nil {
		return nil, err
	}
	out := map[model.SharedKind]int{}
	for _, count := range res {
		if kind, ok := kinds[count.Filter.TypeID()]; ok {
			out[kind] = count.Count
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	count := func(kind model.SharedKind, fetch func() (int, error)) {
		wg.Go(func() {
			n, e := fetch()
			if e == nil {
				mu.Lock()
				out[kind] = n
				mu.Unlock()
			}
		})
	}
	count(model.SharedSaved, func() (int, error) {
		req := &tg.MessagesSearchRequest{Peer: &tg.InputPeerSelf{}, Filter: &tg.InputMessagesFilterEmpty{}, Limit: 1}
		req.SetSavedPeerID(peer.input())
		res, e := api.MessagesSearch(ctx, req)
		if e != nil {
			return 0, e
		}
		return searchTotal(res), nil
	})
	if peer.Kind == "user" || peer.Kind == "channel" {
		count(model.SharedStories, func() (int, error) {
			res, e := api.StoriesGetPinnedStories(ctx, &tg.StoriesGetPinnedStoriesRequest{Peer: peer.input(), Limit: 1})
			if e != nil {
				return 0, e
			}
			return res.Count, nil
		})
		count(model.SharedGifts, func() (int, error) {
			res, e := api.PaymentsGetSavedStarGifts(ctx, &tg.PaymentsGetSavedStarGiftsRequest{Peer: peer.input(), Limit: 1, ExcludeUnsaved: true})
			if e != nil {
				return 0, e
			}
			return res.Count, nil
		})
	}
	if peer.Rights.Self {
		count(model.SharedStoryArchive, func() (int, error) {
			res, e := api.StoriesGetStoriesArchive(ctx, &tg.StoriesGetStoriesArchiveRequest{Peer: peer.input(), Limit: 1})
			if e != nil {
				return 0, e
			}
			return res.Count, nil
		})
	}
	if peer.Kind == "user" {
		count(model.SharedGroups, func() (int, error) {
			res, e := api.MessagesGetCommonChats(ctx, &tg.MessagesGetCommonChatsRequest{UserID: &tg.InputUser{UserID: peer.ID, AccessHash: peer.Hash}, Limit: 1})
			if e != nil {
				return 0, e
			}
			if slice, ok := res.(*tg.MessagesChatsSlice); ok {
				return slice.Count, nil
			}
			return len(res.GetChats()), nil
		})
	}
	wg.Wait()
	return out, nil
}
func searchTotal(res tg.MessagesMessagesClass) int {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return len(v.Messages)
	case *tg.MessagesMessagesSlice:
		return v.Count
	case *tg.MessagesChannelMessages:
		return v.Count
	}
	return 0
}
func (s *Store) SharedMedia(ctx context.Context, chat int64, kind model.SharedKind, before model.MessageID, limit int) (model.SharedPage, error) {
	return s.searchMedia(ctx, chat, kind, before, -1, limit)
}
func (s *Store) searchMedia(ctx context.Context, chat int64, kind model.SharedKind, anchor model.MessageID, dir, limit int) (model.SharedPage, error) {
	filter, err := sharedFilter(kind)
	if err != nil {
		return model.SharedPage{}, err
	}
	ctx, api, peer, generation, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return model.SharedPage{}, err
	}
	defer done()
	limit = min(100, max(1, limit))
	req := &tg.MessagesSearchRequest{Peer: peer.input(), Filter: filter, OffsetID: int(anchor), Limit: limit}
	if dir > 0 {
		req.OffsetID = int(anchor) + 1
		req.AddOffset = -limit
		req.MinID = int(anchor)
	}
	if kind == model.SharedSaved {
		req.Peer = &tg.InputPeerSelf{}
		req.SetSavedPeerID(peer.input())
	}
	res, err := api.MessagesSearch(ctx, req)
	if err != nil {
		return model.SharedPage{}, err
	}
	page := model.SharedPage{Total: searchTotal(res)}
	modified, ok := res.AsModified()
	if !ok {
		return page, nil
	}
	s.rememberPeers(modified.GetUsers(), modified.GetChats())
	raw := modified.GetMessages()
	// Advance using raw IDs: unsupported or concurrently deleted messages must not stall paging.
	for _, m := range raw {
		id := model.MessageID(m.GetID())
		if id > 0 && (page.Next == 0 || id < page.Next) {
			page.Next = id
		}
	}
	page.More = len(raw) >= limit && page.Next != 0 && (anchor == 0 || page.Next < anchor || dir > 0)
	page.Messages, err = s.ingest(ctx, raw, false, generation)
	sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].Key.MessageID > page.Messages[j].Key.MessageID })
	return page, err
}
