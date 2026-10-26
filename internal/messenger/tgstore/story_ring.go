// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

var _ model.PeerStoryRing = (*Store)(nil)

// PeerStoryRing implements model.PeerStoryRing: the stories of peer known,
// those of the list or asked for before, also when the account archived
// them; or, when Telegram told with the peer that it has some
// (stories_max_id) and none is known, one unread, its latest, while its
// stories are asked for with stories.getPeerStories, once in this run, as
// Telegram Desktop asks for those of a peer it opens.
func (s *Store) PeerStoryRing(peer int64) (model.StoryPeer, bool) {
	c := s.history
	c.mu.Lock()
	rec, known := c.peers[peer]
	c.mu.Unlock()
	st := &s.stories
	now := time.Now().Unix()
	st.mu.Lock()
	ring := model.StoryPeer{ID: peer}
	if known {
		ring.Kind, ring.Title, ring.Self = storyPeerKind(rec), rec.Name, rec.Rights.Self
	}
	if k := st.peers[peer]; k != nil {
		for _, story := range k.Stories {
			if story.Expires <= now {
				continue
			}
			ring.Count++
			if story.ID > k.MaxRead {
				if ring.Unread == 0 {
					ring.Open = model.StoryMessage(story.ID)
				}
				ring.Unread++
			}
			if ring.Count == 1 && ring.Unread == 0 {
				ring.Open = model.StoryMessage(story.ID)
			}
			ring.Last = time.Unix(story.Date, 0)
		}
	}
	if ring.Count > 0 || !known || rec.StoriesMax == 0 {
		st.mu.Unlock()
		return ring, ring.Count > 0
	}
	if st.ringAsked == nil {
		st.ringAsked = map[int64]bool{}
	}
	ask := !st.ringAsked[peer]
	st.ringAsked[peer] = true
	st.mu.Unlock()
	if ask {
		s.goPeerStories(peer)
	}
	ring.Count, ring.Unread, ring.Open = 1, 1, model.StoryMessage(rec.StoriesMax)
	return ring, true
}

// storiesMax is the latest story of a peer's stories_max_id, 0 for none.
func storiesMax(r tg.RecentStory, ok bool) int {
	if !ok {
		return 0
	}
	id, _ := r.GetMaxID()
	return id
}

// goPeerStories asks for peer's stories in the background, which tell the
// UI when they come.
func (s *Store) goPeerStories(peer int64) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.api == nil {
		return
	}
	ctx := c.ctx
	c.wg.Go(func() {
		defer crash.Recover("peer stories", nil)
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if _, err := s.PeerStories(ctx, peer); err != nil {
			// Asked again when the ring is next drawn: the peer may have
			// none any more, or the connection failed.
			s.stories.mu.Lock()
			delete(s.stories.ringAsked, peer)
			s.stories.mu.Unlock()
		}
	})
}
