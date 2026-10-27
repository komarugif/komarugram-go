// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// The stories over the list of chats, as Telegram Desktop's Data::Stories
// keeps them: who has stories now, which of them the account has seen, and
// those Telegram told in full, from stories.getAllStories and the updates
// of them. The cache keeps them, with the state by which the next start
// asks only for what changed.

const (
	// storyReadDelay is how long a story seen waits before Telegram is
	// told, as Telegram Desktop's kMarkAsReadDelay: those seen meanwhile go
	// in the same request.
	storyReadDelay = 3 * time.Second
	// storyHeart is the reaction of a story's heart, Telegram Desktop's.
	storyHeart = "❤"
	// storiesKey is where the cache keeps them.
	storiesKey = "stories"
	// changelog is Telegram's service notifications, whose stories Telegram
	// Desktop's list puts before others'.
	changelog = 777000
)

// keptStory is a story of a peer: its id, when it was posted and when it
// goes, and the story in full once Telegram has told it so.
type keptStory struct {
	ID      int
	Date    int64
	Expires int64
	Full    *model.Message `json:",omitempty"`
	// min marks one Telegram told minimal, without the account's reaction.
	min bool
}

// withOld is story, as Telegram told it now, with what old knew that it
// does not tell: the story in full, skipped now, or the account's
// reaction, which a minimal one lacks.
func (story keptStory) withOld(old keptStory) keptStory {
	switch {
	case old.Full == nil:
	case story.Full == nil:
		story.Full = old.Full
	case story.min && story.Full.Story != nil && old.Full.Story != nil:
		full := *story.Full
		mine := *full.Story
		mine.Liked = old.Full.Story.Liked
		full.Story = &mine
		story.Full = &full
	}
	return story
}

// keptStories are a peer's stories now, by id, and the last of them the
// account has seen.
type keptStories struct {
	MaxRead int
	Stories []keptStory
}

// keptStoryState is what the cache keeps of the stories.
type keptStoryState struct {
	State string
	Peers map[int64]*keptStories
}

type storyState struct {
	mu sync.Mutex
	// state is getAllStories' state, by which it tells what changed since.
	state string
	peers map[int64]*keptStories
	// version counts the changes, and list is what StoryPeers returns,
	// made for listed.
	version, listed uint64
	list            []model.StoryPeer
	// told is what Telegram was told the account saw of each peer's;
	// telling, the peers whose read waits for storyReadDelay.
	told    map[int64]int
	telling map[int64]bool
	// unsaved are where the media of stories download from, not written
	// to the cache yet: they are, for a peer's stories opened.
	unsaved map[string]fileLocation
	loading bool
	expiry  *time.Timer
	// resolving are the stories of chats' messages asked for in this run:
	// see goResolveChatStories.
	resolving map[storyKey]bool
	// ringAsked are the peers whose stories PeerStoryRing asked for in this
	// run, for the ring of a header.
	ringAsked map[int64]bool
	// persist orders the writes to the cache, each of what the stories
	// are when it writes; queued is set while one waits to, which those
	// asked meanwhile leave to it.
	persist sync.Mutex
	queued  bool
}

// changedLocked marks the stories changed, st.mu held.
func (st *storyState) changedLocked() { st.version++ }

// loadStories asks Telegram for the stories over the list: all of them
// the first time, and after that only what changed since the state it
// told, as Telegram Desktop asks for them on a start.
func (s *Store) loadStories(ctx context.Context, api *tg.Client) error {
	st := &s.stories
	st.mu.Lock()
	if st.loading {
		st.mu.Unlock()
		return nil
	}
	st.loading = true
	state := st.state
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		st.loading = false
		st.mu.Unlock()
	}()
	req := &tg.StoriesGetAllStoriesRequest{State: state}
	replace := state == ""
	for {
		res, err := api.StoriesGetAllStories(ctx, req)
		if err != nil {
			return err
		}
		all, ok := res.(*tg.StoriesAllStories)
		if !ok {
			if same, ok := res.(*tg.StoriesAllStoriesNotModified); ok && same.State != "" && same.State != state {
				// Nothing changed, but the state did: the next start asks
				// from it.
				st.mu.Lock()
				st.state = same.State
				st.mu.Unlock()
				s.goPersistStories()
			}
			return nil
		}
		s.rememberPeers(all.Users, all.Chats)
		fresh := make(map[int64]*keptStories, len(all.PeerStories))
		for _, p := range all.PeerStories {
			id := peerID(p.Peer)
			fresh[id] = s.peerStories(id, p.MaxReadID, p.Stories)
		}
		st.mu.Lock()
		if replace || st.peers == nil {
			// The first page is all of them: those it has not are gone.
			old := st.peers
			st.peers = map[int64]*keptStories{}
			for id, k := range old {
				if _, ok := fresh[id]; ok {
					st.peers[id] = k
				}
			}
			replace = false
		}
		for id, k := range fresh {
			st.peers[id] = mergeStories(st.peers[id], k)
		}
		st.state = all.State
		st.changedLocked()
		st.mu.Unlock()
		if !all.HasMore {
			break
		}
		req = &tg.StoriesGetAllStoriesRequest{Next: true, State: all.State}
	}
	s.storiesChanged()
	return nil
}

// peerStories are the stories of peer as Telegram told them, those it
// told in full converted.
func (s *Store) peerStories(peer int64, maxRead int, items []tg.StoryItemClass) *keptStories {
	k := &keptStories{MaxRead: maxRead}
	for _, item := range items {
		if story, ok := s.keptStory(peer, item); ok {
			k.Stories = append(k.Stories, story)
		}
	}
	sort.Slice(k.Stories, func(i, j int) bool { return k.Stories[i].ID < k.Stories[j].ID })
	return k
}

// mergeStories are fresh, a peer's stories as Telegram told them now, with
// what old knew of them that fresh does not tell: a story told in full
// before and skipped now, or told minimal, and what the account saw.
func mergeStories(old, fresh *keptStories) *keptStories {
	if old == nil {
		return fresh
	}
	out := &keptStories{MaxRead: max(old.MaxRead, fresh.MaxRead), Stories: slices.Clone(fresh.Stories)}
	for i, story := range out.Stories {
		if at := slices.IndexFunc(old.Stories, func(o keptStory) bool { return o.ID == story.ID }); at >= 0 {
			out.Stories[i] = story.withOld(old.Stories[at])
		}
	}
	return out
}

// keptStory converts item, a story of peer; ok is false for a story
// deleted, or of a kind not shown.
func (s *Store) keptStory(peer int64, item tg.StoryItemClass) (keptStory, bool) {
	switch it := item.(type) {
	case *tg.StoryItemSkipped:
		if it.Live {
			return keptStory{}, false
		}
		return keptStory{ID: it.ID, Date: int64(it.Date), Expires: int64(it.ExpireDate)}, true
	case *tg.StoryItem:
		m, ok := s.storyMessage(peer, it)
		if !ok {
			return keptStory{ID: it.ID, Date: int64(it.Date), Expires: int64(it.ExpireDate)}, true
		}
		return keptStory{ID: it.ID, Date: int64(it.Date), Expires: int64(it.ExpireDate), Full: &m, min: it.Min}, true
	}
	return keptStory{}, false
}

// storyMessage is it, a story of peer, as a message, its media's file
// locations kept to download it; ok is false for one without a photo or
// a video.
func (s *Store) storyMessage(peer int64, it *tg.StoryItem) (model.Message, bool) {
	c := s.history
	c.mu.Lock()
	account, rec := c.account, c.peers[peer]
	var from string
	if fwd, ok := it.GetFwdFrom(); ok {
		from = fwd.FromName
		if p, ok := fwd.GetFrom(); ok {
			if r, ok := c.peers[peerID(p)]; ok {
				from = r.Name
			}
		}
	}
	c.mu.Unlock()
	if rec.Kind == "" {
		return model.Message{}, false
	}
	raw := &tg.Message{ID: -it.ID, PeerID: rec.output(), Date: it.Date, Message: it.Caption, Entities: it.Entities, Media: it.Media, Noforwards: it.Noforwards}
	m, loc := convertMessage(account, raw, nil)
	if m.Media == nil || loc == nil || m.Kind != model.MessagePhoto && m.Kind != model.MessageVideo {
		return model.Message{}, false
	}
	m.Key.ChatID, m.SenderName, m.ForwardName = peer, rec.Name, from
	m.Views = it.Views.ViewsCount
	m.Story = &model.Story{Likes: it.Views.ReactionsCount, Liked: liked(it.SentReaction), Expires: time.Unix(int64(it.ExpireDate), 0)}
	if m.Story.Liked {
		m.Story.Likes = max(m.Story.Likes, 1)
	}
	refs := map[string]fileLocation{}
	ref := *loc
	ref.StoryPeer, ref.Story = peer, it.ID
	addMediaRefs(refs, m.Media, ref)
	st := &s.stories
	st.mu.Lock()
	if st.unsaved == nil {
		st.unsaved = map[string]fileLocation{}
	}
	for id, r := range refs {
		st.unsaved[id] = r
	}
	st.mu.Unlock()
	c.mu.Lock()
	for id, r := range refs {
		c.refs[id] = r
	}
	c.mu.Unlock()
	return m, true
}

// liked reports whether r is a reaction, not the lack of one.
func liked(r tg.ReactionClass) bool {
	if r == nil {
		return false
	}
	_, empty := r.(*tg.ReactionEmpty)
	return !empty
}

// applyStory takes an updateStory: a story posted, changed or deleted.
func (s *Store) applyStory(u *tg.UpdateStory) {
	peer := peerID(u.Peer)
	story, ok := s.keptStory(peer, u.Story)
	st := &s.stories
	st.mu.Lock()
	if st.peers == nil {
		st.peers = map[int64]*keptStories{}
	}
	old := st.peers[peer]
	k := &keptStories{}
	if old != nil {
		k.MaxRead, k.Stories = old.MaxRead, slices.Clone(old.Stories)
	}
	at := slices.IndexFunc(k.Stories, func(o keptStory) bool { return o.ID == u.Story.GetID() })
	switch {
	case !ok && at >= 0:
		k.Stories = slices.Delete(k.Stories, at, at+1)
	case ok && at >= 0:
		k.Stories[at] = story.withOld(k.Stories[at])
	case ok:
		k.Stories = append(k.Stories, story)
		sort.Slice(k.Stories, func(i, j int) bool { return k.Stories[i].ID < k.Stories[j].ID })
	}
	if len(k.Stories) == 0 {
		delete(st.peers, peer)
	} else {
		st.peers[peer] = k
	}
	st.changedLocked()
	st.mu.Unlock()
	s.storiesChanged()
}

// applyReadStories takes an updateReadStories: the account saw stories on
// another device.
func (s *Store) applyReadStories(u *tg.UpdateReadStories) {
	peer := peerID(u.Peer)
	st := &s.stories
	st.mu.Lock()
	k := st.peers[peer]
	if k == nil || k.MaxRead >= u.MaxID {
		st.mu.Unlock()
		return
	}
	st.peers[peer] = &keptStories{MaxRead: u.MaxID, Stories: k.Stories}
	if st.told == nil {
		st.told = map[int64]int{}
	}
	st.told[peer] = max(st.told[peer], u.MaxID)
	st.changedLocked()
	st.mu.Unlock()
	s.storiesChanged()
}

// applyStoryReaction takes an updateSentStoryReaction: the account's
// reaction to a story, from another device.
func (s *Store) applyStoryReaction(u *tg.UpdateSentStoryReaction) {
	s.setStoryLiked(peerID(u.Peer), u.StoryID, liked(u.Reaction))
}

// setStoryLiked puts the account's heart on story id of peer, or takes it
// away, and reports what it was.
func (s *Store) setStoryLiked(peer int64, id int, like bool) (was, ok bool) {
	st := &s.stories
	st.mu.Lock()
	k := st.peers[peer]
	at := -1
	if k != nil {
		at = slices.IndexFunc(k.Stories, func(o keptStory) bool { return o.ID == id })
	}
	if at < 0 || k.Stories[at].Full == nil || k.Stories[at].Full.Story == nil {
		st.mu.Unlock()
		return false, false
	}
	// The message the UI may hold is not changed: it is replaced.
	full := *k.Stories[at].Full
	story := *full.Story
	was = story.Liked
	if was != like {
		story.Liked = like
		if like {
			story.Likes++
		} else {
			story.Likes = max(story.Likes-1, 0)
		}
	}
	full.Story = &story
	stories := slices.Clone(k.Stories)
	stories[at].Full = &full
	st.peers[peer] = &keptStories{MaxRead: k.MaxRead, Stories: stories}
	st.changedLocked()
	st.mu.Unlock()
	s.storiesChanged()
	return was, true
}

// storiesChanged tells the UI, keeps the stories in the cache, and sets
// the list to be made again when the first story of it goes.
func (s *Store) storiesChanged() {
	st := &s.stories
	now := time.Now().Unix()
	next := int64(0)
	st.mu.Lock()
	for _, k := range st.peers {
		for _, story := range k.Stories {
			if story.Expires > now && (next == 0 || story.Expires < next) {
				next = story.Expires
			}
		}
	}
	if st.expiry != nil {
		st.expiry.Stop()
		st.expiry = nil
	}
	if next != 0 {
		st.expiry = time.AfterFunc(time.Until(time.Unix(next, 0))+time.Second, func() {
			st.mu.Lock()
			st.changedLocked()
			st.mu.Unlock()
			s.changed()
		})
	}
	st.mu.Unlock()
	s.goPersistStories()
	s.changed()
}

// goPersistStories writes the stories to the cache, those gone left out.
func (s *Store) goPersistStories() {
	c := s.history
	c.mu.Lock()
	cache, closing := c.cache, c.closing
	c.mu.Unlock()
	if cache == nil || closing {
		return
	}
	st := &s.stories
	st.mu.Lock()
	if st.queued {
		st.mu.Unlock()
		return
	}
	st.queued = true
	st.mu.Unlock()
	c.wg.Go(func() {
		defer crash.Recover("keep stories", nil)
		st.persist.Lock()
		defer st.persist.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cache.Put(ctx, storiesKey, st.kept())
	})
}

// kept is what the cache keeps of the stories now, those gone left out;
// the next change is written again.
func (st *storyState) kept() keptStoryState {
	now := time.Now().Unix()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.queued = false
	kept := keptStoryState{State: st.state, Peers: make(map[int64]*keptStories, len(st.peers))}
	for id, k := range st.peers {
		live := &keptStories{MaxRead: k.MaxRead}
		for _, story := range k.Stories {
			if story.Expires > now {
				live.Stories = append(live.Stories, story)
			}
		}
		if len(live.Stories) > 0 {
			kept.Peers[id] = live
		}
	}
	return kept
}

// StoryPeers implements model.StorySource.
func (s *Store) StoryPeers() []model.StoryPeer {
	st := &s.stories
	st.mu.Lock()
	if st.listed == st.version && st.list != nil {
		list := st.list
		st.mu.Unlock()
		return list
	}
	version := st.version
	peers := make(map[int64]*keptStories, len(st.peers))
	for id, k := range st.peers {
		peers[id] = k
	}
	st.mu.Unlock()

	c := s.history
	c.mu.Lock()
	records := make(map[int64]peerRecord, len(peers))
	for id := range peers {
		if r, ok := c.peers[id]; ok {
			records[id] = r
		}
	}
	c.mu.Unlock()

	now := time.Now().Unix()
	list := []model.StoryPeer{}
	for id, k := range peers {
		r, ok := records[id]
		if !ok || r.StoriesHidden && !r.Rights.Self {
			continue
		}
		p := model.StoryPeer{ID: id, Kind: storyPeerKind(r), Title: r.Name, Self: r.Rights.Self}
		if r.Kind == "user" && r.First != "" {
			p.Title = r.First
		}
		for _, story := range k.Stories {
			if story.Expires <= now {
				continue
			}
			p.Count++
			if story.ID > k.MaxRead {
				if p.Unread == 0 {
					p.Open = model.StoryMessage(story.ID)
				}
				p.Unread++
			}
			if p.Count == 1 && p.Unread == 0 {
				// The first, until one is unread.
				p.Open = model.StoryMessage(story.ID)
			}
			p.Last = time.Unix(story.Date, 0)
		}
		if p.Count > 0 {
			list = append(list, p)
		}
	}
	// As Telegram Desktop's Stories::sort: the account's own first, then
	// those with stories unread, then the latest; Telegram's notifications
	// as one more.
	key := func(p model.StoryPeer) int64 {
		k := p.Last.Unix()
		if p.ID == changelog {
			k += 1 << 47
		}
		if p.Unread > 0 {
			k += 1 << 49
		}
		if p.Self {
			k += 1 << 50
		}
		return k
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := key(list[i]), key(list[j])
		if a != b {
			return a > b
		}
		return list[i].ID > list[j].ID
	})
	st.mu.Lock()
	if st.version == version {
		st.list, st.listed = list, version
	}
	st.mu.Unlock()
	return list
}

// storyPeerKind is the kind of chat of r.
func storyPeerKind(r peerRecord) model.ChatKind {
	switch {
	case r.Kind == "user":
		return model.KindUser
	case r.Kind == "channel" && r.Rights.Broadcast:
		return model.KindChannel
	}
	return model.KindGroup
}

// PeerStories implements model.StorySource: the stories Telegram told only
// in part are asked for in one stories.getStoriesByID, all of a peer the
// store knows nothing of in one stories.getPeerStories, and where their
// media downloads from is written to the cache.
func (s *Store) PeerStories(ctx context.Context, peer int64) ([]model.Message, error) {
	ctx, api, rec, _, done, err := s.peerOperation(ctx, peer)
	if err != nil {
		return s.knownStories(peer), err
	}
	defer done()
	st := &s.stories
	now := time.Now().Unix()
	st.mu.Lock()
	k, known := st.peers[peer]
	var missing []int
	var full []keptStory
	if known {
		for _, story := range k.Stories {
			switch {
			case story.Expires <= now:
			case story.Full == nil:
				missing = append(missing, story.ID)
			default:
				full = append(full, story)
			}
		}
	}
	st.mu.Unlock()
	// A story told in full in an earlier run is kept with its media, but
	// not where they download from, which the cache keeps only of those
	// opened then: those are asked for again, as those told in part.
	for _, story := range full {
		if !s.storyLocated(ctx, story.Full.Media) {
			missing = append(missing, story.ID)
		}
	}
	switch {
	case !known:
		res, err := api.StoriesGetPeerStories(ctx, rec.input())
		if err != nil {
			return nil, err
		}
		s.rememberPeers(res.Users, res.Chats)
		fresh := s.peerStories(peer, res.Stories.MaxReadID, res.Stories.Stories)
		st.mu.Lock()
		if st.peers == nil {
			st.peers = map[int64]*keptStories{}
		}
		if len(fresh.Stories) > 0 {
			st.peers[peer] = mergeStories(st.peers[peer], fresh)
		}
		st.changedLocked()
		st.mu.Unlock()
		s.storiesChanged()
	case len(missing) > 0:
		res, err := api.StoriesGetStoriesByID(ctx, &tg.StoriesGetStoriesByIDRequest{Peer: rec.input(), ID: missing[:min(len(missing), 100)]})
		if err != nil {
			return s.knownStories(peer), err
		}
		s.rememberPeers(res.Users, res.Chats)
		for _, item := range res.Stories {
			s.applyStory(&tg.UpdateStory{Peer: rec.output(), Story: item})
		}
	}
	stories := s.knownStories(peer)
	return stories, s.keepStoryRefs(ctx, stories)
}

// storyLocated reports whether where media downloads from is known, in
// this run or kept by the cache: a story's, which Media and MediaStream
// cannot download without it.
func (s *Store) storyLocated(ctx context.Context, media *model.MessageMedia) bool {
	if media == nil {
		return true
	}
	c := s.history
	c.mu.Lock()
	_, ok := c.refs[media.ID]
	cache := c.cache
	c.mu.Unlock()
	if ok || cache == nil {
		return ok
	}
	var ref fileLocation
	ok, err := cache.Get(ctx, "ref/"+media.ID, &ref)
	return err != nil || ok
}

// knownStories are the stories of peer the store knows in full now, the
// oldest first.
func (s *Store) knownStories(peer int64) []model.Message {
	st := &s.stories
	now := time.Now().Unix()
	st.mu.Lock()
	defer st.mu.Unlock()
	k := st.peers[peer]
	if k == nil {
		return nil
	}
	var out []model.Message
	for _, story := range k.Stories {
		if story.Full != nil && story.Expires > now {
			out = append(out, *story.Full)
		}
	}
	return out
}

// keepStoryRefs writes where the media of stories downloads from to the
// cache, those not written yet.
func (s *Store) keepStoryRefs(ctx context.Context, stories []model.Message) error {
	st := &s.stories
	refs := map[string]fileLocation{}
	st.mu.Lock()
	for _, m := range stories {
		for id, r := range st.unsaved {
			if mediaOf(m.Media, id) {
				refs[id] = r
				delete(st.unsaved, id)
			}
		}
	}
	st.mu.Unlock()
	if len(refs) == 0 {
		return nil
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	fresh := make(map[string]any, len(refs))
	for id, r := range refs {
		fresh["ref/"+id] = r
	}
	return cache.PutAll(ctx, fresh)
}

// mediaOf reports whether id is media's, one of its sizes or its
// thumbnail's.
func mediaOf(media *model.MessageMedia, id string) bool {
	if media == nil {
		return false
	}
	if media.ID == id {
		return true
	}
	for _, v := range media.Variants {
		if v.ID == id {
			return true
		}
	}
	return mediaOf(media.Thumbnail, id)
}

// ReadStory implements model.StorySource. Telegram is told after
// storyReadDelay, of the last story seen by then, as Ghost allows; never of
// the account's own, as Telegram Desktop does not.
func (s *Store) ReadStory(peer int64, id model.MessageID) {
	story := model.StoryID(id)
	if story <= 0 {
		return
	}
	st := &s.stories
	st.mu.Lock()
	k := st.peers[peer]
	if k == nil || story <= k.MaxRead {
		st.mu.Unlock()
		return
	}
	st.peers[peer] = &keptStories{MaxRead: story, Stories: k.Stories}
	st.changedLocked()
	tell := s.ghostSettings().SendStories && !st.telling[peer]
	if tell {
		if st.telling == nil {
			st.telling = map[int64]bool{}
		}
		st.telling[peer] = true
	}
	st.mu.Unlock()
	s.storiesChanged()
	if tell {
		time.AfterFunc(storyReadDelay, func() { s.tellStoriesRead(peer) })
	}
}

// tellStoriesRead tells Telegram the last story of peer the account saw,
// when it is not told already.
func (s *Store) tellStoriesRead(peer int64) {
	st := &s.stories
	st.mu.Lock()
	delete(st.telling, peer)
	k := st.peers[peer]
	if k == nil || k.MaxRead <= st.told[peer] || !s.ghostSettings().SendStories {
		st.mu.Unlock()
		return
	}
	if st.told == nil {
		st.told = map[int64]int{}
	}
	st.told[peer] = k.MaxRead
	maxID := k.MaxRead
	st.mu.Unlock()
	c := s.history
	c.mu.Lock()
	rec, ok := c.peers[peer]
	c.mu.Unlock()
	if !ok || rec.Rights.Self {
		return
	}
	s.goSend("read stories", func(ctx context.Context, api *tg.Client) error {
		_, err := api.StoriesReadStories(ctx, &tg.StoriesReadStoriesRequest{Peer: rec.input(), MaxID: maxID})
		return err
	})
}

// LikeStory implements model.StorySource with stories.sendReaction, the
// heart shown at once and taken back when Telegram refuses it.
func (s *Store) LikeStory(ctx context.Context, peer int64, id model.MessageID, like bool) error {
	ctx, api, rec, _, done, err := s.peerOperation(ctx, peer)
	if err != nil {
		return err
	}
	defer done()
	story := model.StoryID(id)
	was, ok := s.setStoryLiked(peer, story, like)
	if !ok {
		return errors.New("story unknown")
	}
	if was == like {
		return nil
	}
	var reaction tg.ReactionClass = &tg.ReactionEmpty{}
	if like {
		reaction = &tg.ReactionEmoji{Emoticon: storyHeart}
	}
	res, err := api.StoriesSendReaction(ctx, &tg.StoriesSendReactionRequest{Peer: rec.input(), StoryID: story, Reaction: reaction})
	if err != nil {
		s.setStoryLiked(peer, story, was)
		return err
	}
	_ = s.Handle(ctx, res)
	return nil
}

// StoryNow implements model.StorySource.
func (s *Store) StoryNow(peer int64, id model.MessageID) (model.Message, bool) {
	st := &s.stories
	st.mu.Lock()
	defer st.mu.Unlock()
	k := st.peers[peer]
	if k == nil {
		return model.Message{}, false
	}
	at := slices.IndexFunc(k.Stories, func(o keptStory) bool { return o.ID == model.StoryID(id) })
	if at < 0 || k.Stories[at].Full == nil {
		return model.Message{}, false
	}
	return *k.Stories[at].Full, true
}

// ShareStory implements model.StorySource: the story is sent as media of
// a message, as Telegram Desktop's share box sends it.
func (s *Store) ShareStory(ctx context.Context, peer int64, id model.MessageID, to int64) error {
	c := s.history
	c.mu.Lock()
	api := c.api
	source, okFrom := c.peers[peer]
	target, okTo := c.peers[to]
	c.mu.Unlock()
	if api == nil {
		return errors.New("cannot share while offline")
	}
	if !okFrom || !okTo {
		return errors.New("unknown chat")
	}
	res, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: target.input(), Media: &tg.InputMediaStory{Peer: source.input(), ID: model.StoryID(id)}, RandomID: newRandomID()})
	if err != nil {
		return err
	}
	_ = s.Handle(ctx, res)
	return nil
}

var _ model.StorySource = (*Store)(nil)
