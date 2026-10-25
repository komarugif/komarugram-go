// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
)

// The stories a chat's history carries: forwarded to it, or those the
// account was mentioned in. Telegram often leaves the story out of the
// message, and Telegram Desktop resolves it (Stories::resolve); so does
// the store, once a story, with one stories.getStoriesByID a peer for the
// messages of a page, and keeps it with the message, a story gone as
// expired: shown again, the history asks nothing.

// storyKey is a story of a peer.
type storyKey struct {
	peer int64
	id   int
}

// goResolveChatStories asks for the stories msgs carry that Telegram left
// out, in the background, those not asked for already.
func (s *Store) goResolveChatStories(msgs []model.Message) {
	want := map[storyKey][]model.MessageKey{}
	st := &s.stories
	st.mu.Lock()
	for _, m := range msgs {
		ref := m.StoryOf
		if ref == nil || ref.Expired || m.Media != nil {
			continue
		}
		key := storyKey{ref.Peer, ref.ID}
		if st.resolving[key] {
			continue
		}
		want[key] = append(want[key], m.Key)
	}
	if len(want) == 0 {
		st.mu.Unlock()
		return
	}
	if st.resolving == nil {
		st.resolving = map[storyKey]bool{}
	}
	for key := range want {
		st.resolving[key] = true
	}
	st.mu.Unlock()
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.api == nil {
		s.forgetResolving(want)
		return
	}
	api, ctx := c.api, c.ctx
	c.wg.Go(func() {
		defer crash.Recover("chat stories", func(*crash.Panic) { s.forgetResolving(want) })
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		s.resolveChatStories(ctx, api, want)
	})
}

// keepResolvedStories gives the messages of msgs that carry a story
// Telegram left out what the cache knows of it already: told again, a
// message would lose the story asked for before, which is not asked for
// again.
func keepResolvedStories(ctx context.Context, cache *historycache.Cache, msgs []model.Message) {
	for i := range msgs {
		m := &msgs[i]
		if m.StoryOf == nil || m.StoryOf.Expired || m.Media != nil {
			continue
		}
		old, ok, err := cache.Message(ctx, m.Key.ChatID, int(m.Key.MessageID))
		if err != nil || !ok || old.StoryOf == nil || old.StoryOf.Peer != m.StoryOf.Peer || old.StoryOf.ID != m.StoryOf.ID {
			continue
		}
		if old.Media == nil && !old.StoryOf.Expired {
			continue
		}
		m.StoryOf, m.Media = old.StoryOf, old.Media
		if !m.StoryOf.Mention {
			m.Kind, m.Text, m.Entities = old.Kind, old.Text, old.Entities
		}
		m.ContentRevision = model.Revision(*m)
	}
}

// forgetResolving lets the stories of want be asked for again: their
// request failed.
func (s *Store) forgetResolving(want map[storyKey][]model.MessageKey) {
	st := &s.stories
	st.mu.Lock()
	defer st.mu.Unlock()
	for key := range want {
		delete(st.resolving, key)
	}
}

// resolveChatStories asks for the stories of want, a peer's in one
// request, and gives the messages carrying them their media, or marks the
// stories gone as expired.
func (s *Store) resolveChatStories(ctx context.Context, api *tg.Client, want map[storyKey][]model.MessageKey) {
	byPeer := map[int64][]int{}
	for key := range want {
		byPeer[key.peer] = append(byPeer[key.peer], key.id)
	}
	for peer, ids := range byPeer {
		sort.Ints(ids)
		ids = ids[:min(len(ids), 100)]
		c := s.history
		c.mu.Lock()
		rec, ok := c.peers[peer]
		c.mu.Unlock()
		failed := map[storyKey][]model.MessageKey{}
		for _, id := range ids {
			failed[storyKey{peer, id}] = want[storyKey{peer, id}]
		}
		if !ok {
			s.forgetResolving(failed)
			continue
		}
		res, err := api.StoriesGetStoriesByID(ctx, &tg.StoriesGetStoriesByIDRequest{Peer: rec.input(), ID: ids})
		if err != nil {
			s.forgetResolving(failed)
			continue
		}
		s.rememberPeers(res.Users, res.Chats)
		items := map[int]*tg.StoryItem{}
		for _, raw := range res.Stories {
			if item, ok := raw.(*tg.StoryItem); ok {
				items[item.ID] = item
			}
		}
		refs := map[string]fileLocation{}
		for _, id := range ids {
			item := items[id]
			for _, key := range want[storyKey{peer, id}] {
				s.changeMessage(key.ChatID, int(key.MessageID), func(m *model.Message) {
					if m.StoryOf == nil {
						return
					}
					if item == nil {
						m.StoryOf.Expired = true
						return
					}
					if loc := storyMedia(m, peer, item); loc != nil {
						addMediaRefs(refs, m.Media, *loc)
					}
				})
			}
		}
		c.mu.Lock()
		for id, r := range refs {
			c.refs[id] = r
		}
		c.mu.Unlock()
		_ = s.keepRefs(ctx, refs)
	}
}

// keepRefs writes refs, where media downloads from, to the cache.
func (s *Store) keepRefs(ctx context.Context, refs map[string]fileLocation) error {
	if len(refs) == 0 {
		return nil
	}
	cache := s.Cache()
	if cache == nil {
		return nil
	}
	fresh := make(map[string]any, len(refs))
	for id, r := range refs {
		fresh["ref/"+id] = r
	}
	return cache.PutAll(ctx, fresh)
}

// Story implements model.StorySource: from the stories over the list when
// it is one of them, and otherwise asked of Telegram.
func (s *Store) Story(ctx context.Context, peer int64, id model.MessageID) (model.Message, error) {
	if m, ok := s.StoryNow(peer, id); ok {
		return m, nil
	}
	ctx, api, rec, _, done, err := s.peerOperation(ctx, peer)
	if err != nil {
		return model.Message{}, err
	}
	defer done()
	res, err := api.StoriesGetStoriesByID(ctx, &tg.StoriesGetStoriesByIDRequest{Peer: rec.input(), ID: []int{model.StoryID(id)}})
	if err != nil {
		return model.Message{}, err
	}
	s.rememberPeers(res.Users, res.Chats)
	for _, raw := range res.Stories {
		if item, ok := raw.(*tg.StoryItem); ok && item.ID == model.StoryID(id) {
			m, ok := s.storyMessage(peer, item)
			if !ok {
				break
			}
			return m, s.keepStoryRefs(ctx, []model.Message{m})
		}
	}
	return model.Message{}, errStoryGone
}

// errStoryGone is a story Telegram has no more.
var errStoryGone = errors.New("the story is no longer available")

// ChatStories implements model.ChatStoryGallery from the cache: the
// stories the history carries, shown as the viewer shows them.
func (s *Store) ChatStories(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	cache := s.Cache()
	if cache == nil {
		return model.GalleryPage{}, errors.New("no cache")
	}
	found, err := cache.Stories(ctx, chat, int(anchor), dir, limit)
	if err != nil {
		return model.GalleryPage{}, err
	}
	page := model.GalleryPage{More: len(found) >= limit}
	for _, m := range found {
		if m.StoryShown() {
			page.Messages = append(page.Messages, m.AsStory())
		}
	}
	return page, nil
}

var _ model.ChatStoryGallery = (*Store)(nil)
