// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
)

const (
	// searchDelay lets typing settle before a search is sent.
	searchDelay = 300 * time.Millisecond
	// searchPage is how many messages a page of results has.
	searchPage = 40
	// contactsLimit is how many chats contacts.search finds by name.
	contactsLimit = 50
)

// searchState is the current search and where its next page starts.
type searchState struct {
	mu      sync.Mutex
	gen     uint64
	cancel  context.CancelFunc
	ctx     context.Context
	results model.SearchResults
	started bool

	// Telegram's cursor: the rate, peer and message the next page follows.
	offsetRate int
	offsetPeer tg.InputPeerClass
	offsetID   int
	// offset is the local cursor: how many cached messages were read.
	offset int
}

// Search implements model.Searcher.
func (s *Store) Search(q model.SearchQuery) {
	q.Text = strings.TrimSpace(q.Text)
	st := &s.search
	st.mu.Lock()
	if st.started && st.results.Query == q {
		st.mu.Unlock()
		return
	}
	if st.cancel != nil {
		st.cancel()
	}
	st.gen++
	gen := st.gen
	st.ctx, st.cancel = context.WithCancel(s.history.ctx)
	ctx := st.ctx
	st.started = true
	st.results = model.SearchResults{Query: q, Chats: s.namedChats(q), Loading: true}
	st.offsetRate, st.offsetPeer, st.offsetID, st.offset = 0, nil, 0, 0
	st.mu.Unlock()
	s.changed()
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(searchDelay):
		}
		s.runSearch(ctx, gen, q, true)
	}()
}

// SearchMore implements model.Searcher.
func (s *Store) SearchMore() {
	st := &s.search
	st.mu.Lock()
	if !st.started || st.results.Loading || !st.results.More || st.results.Err != nil {
		st.mu.Unlock()
		return
	}
	st.results.Loading = true
	gen, ctx, q := st.gen, st.ctx, st.results.Query
	st.mu.Unlock()
	s.changed()
	go s.runSearch(ctx, gen, q, false)
}

// SpendPostsSearch implements model.Searcher.
func (s *Store) SpendPostsSearch() {
	st := &s.search
	st.mu.Lock()
	quota := st.results.Posts
	if quota == nil || st.results.Loading || (!quota.Free && quota.Remains <= 0) {
		st.mu.Unlock()
		return
	}
	st.results.Posts = nil
	st.results.Loading = true
	gen, ctx, q := st.gen, st.ctx, st.results.Query
	st.mu.Unlock()
	s.changed()
	go func() {
		found, more, err := s.searchPosts(ctx, q)
		s.finishSearch(gen, found, more, err, nil)
	}()
}

// SearchResults implements model.Searcher.
func (s *Store) SearchResults() model.SearchResults {
	st := &s.search
	st.mu.Lock()
	defer st.mu.Unlock()
	r := st.results
	r.Messages = append([]model.FoundMessage(nil), r.Messages...)
	return r
}

// runSearch reads a page of q: the first one asks Telegram for chats by
// name too, or the posts search whether it would cost a free search.
func (s *Store) runSearch(ctx context.Context, gen uint64, q model.SearchQuery, first bool) {
	if !q.Global {
		found, more, err := s.searchLocal(ctx, q)
		s.finishSearch(gen, found, more, err, nil)
		return
	}
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		s.finishSearch(gen, nil, false, model.ErrSearchOffline, nil)
		return
	}
	if q.Section == model.SearchPosts {
		if first && q.Text != "" {
			flood, err := api.ChannelsCheckSearchPostsFlood(ctx, &tg.ChannelsCheckSearchPostsFloodRequest{Query: q.Text})
			if err != nil {
				s.finishSearch(gen, nil, false, err, nil)
				return
			}
			quota := &model.PostsQuota{Free: flood.QueryIsFree, Remains: flood.Remains, PerDay: flood.TotalDaily}
			if flood.WaitTill != 0 {
				quota.NextFree = time.Unix(int64(flood.WaitTill), 0)
			}
			if !quota.Free {
				// A search that is not free waits to be asked for.
				s.finishSearch(gen, nil, false, nil, quota)
				return
			}
		}
		found, more, err := s.searchPosts(ctx, q)
		s.finishSearch(gen, found, more, err, nil)
		return
	}
	if first && q.Text != "" && !q.Section.Media() {
		chats, err := s.searchContacts(ctx, api, q)
		if err != nil {
			s.finishSearch(gen, nil, false, err, nil)
			return
		}
		s.search.mu.Lock()
		if s.search.gen == gen {
			s.search.results.Chats = chats
		}
		s.search.mu.Unlock()
		s.changed()
	}
	if q.Text == "" && !q.Section.Media() {
		s.finishSearch(gen, nil, false, nil, nil)
		return
	}
	found, more, err := s.searchGlobal(ctx, api, q)
	s.finishSearch(gen, found, more, err, nil)
}

// finishSearch adds a page to the results of search gen.
func (s *Store) finishSearch(gen uint64, found []model.FoundMessage, more bool, err error, quota *model.PostsQuota) {
	if errors.Is(err, context.Canceled) {
		return
	}
	st := &s.search
	st.mu.Lock()
	if st.gen != gen {
		st.mu.Unlock()
		return
	}
	st.results.Loading = false
	st.results.Messages = append(st.results.Messages, found...)
	st.results.More = more && err == nil
	st.results.Err = err
	st.results.Posts = quota
	st.mu.Unlock()
	s.changed()
}

// normalize folds a name or query for comparison: case, and Ё as Е.
func normalize(text string) string {
	return strings.ReplaceAll(strings.ToLower(text), "ё", "е")
}

// namedChats are the account's chats whose title contains the query, of
// the section's kind. Without a query, all of them.
func (s *Store) namedChats(q model.SearchQuery) []model.Chat {
	if q.Section != model.SearchChats && q.Section != model.SearchChannels {
		return nil
	}
	if q.Global && q.Text == "" {
		return nil
	}
	text := normalize(q.Text)
	var out []model.Chat
	for _, c := range s.Chats() {
		if q.Section == model.SearchChannels && c.Kind != model.KindChannel {
			continue
		}
		if strings.Contains(normalize(c.Title), text) {
			out = append(out, c)
		}
	}
	return out
}

// searchLocal reads a page of cached messages.
func (s *Store) searchLocal(ctx context.Context, q model.SearchQuery) ([]model.FoundMessage, bool, error) {
	s.history.mu.Lock()
	cache := s.history.cache
	s.history.mu.Unlock()
	if cache == nil {
		return nil, false, nil
	}
	query := historycache.Query{Text: q.Text}
	switch q.Section {
	case model.SearchChats:
		if q.Text == "" {
			return nil, false, nil // The chats are the result.
		}
	case model.SearchChannels:
		if q.Text == "" {
			return nil, false, nil
		}
		query.Chats = []int64{}
		for _, c := range s.Chats() {
			if c.Kind == model.KindChannel {
				query.Chats = append(query.Chats, c.ID)
			}
		}
	case model.SearchPhotos:
		query.Kinds = []model.MessageKind{model.MessagePhoto}
	case model.SearchVideos:
		query.Kinds = []model.MessageKind{model.MessageVideo}
	case model.SearchLinks:
		query.Links = true
	case model.SearchFiles:
		query.Kinds = []model.MessageKind{model.MessageFile}
	case model.SearchMusic:
		query.Kinds = []model.MessageKind{model.MessageMusic}
	case model.SearchVoice:
		query.Kinds = []model.MessageKind{model.MessageVoice}
	default:
		return nil, false, nil
	}
	s.search.mu.Lock()
	offset := s.search.offset
	s.search.mu.Unlock()
	msgs, err := cache.Search(ctx, query, offset, searchPage)
	if err != nil {
		return nil, false, err
	}
	s.search.mu.Lock()
	s.search.offset += len(msgs)
	s.search.mu.Unlock()
	found := make([]model.FoundMessage, len(msgs))
	for i, m := range msgs {
		found[i] = model.FoundMessage{Message: m, Chat: s.chatFor(m.Key.ChatID, nil)}
	}
	return found, len(msgs) == searchPage, nil
}

// chatFor is the chat of id as the chat list has it, or else as l or the
// peers seen make it.
func (s *Store) chatFor(id int64, l *list) model.Chat {
	s.history.mu.Lock()
	p := s.history.peers[id]
	s.history.mu.Unlock()
	for _, c := range s.Chats() {
		if c.ID == id {
			return p.withMetadata(c)
		}
	}
	if l != nil {
		if peer := chatPeer(id); peer != nil {
			c, _ := l.chat(&tg.Dialog{Peer: peer})
			if c.Title != "" {
				return p.withMetadata(c)
			}
		}
	}
	return p.withMetadata(model.Chat{ID: id})
}

// chatPeer is the peer of a chat id: see peerID.
func chatPeer(id int64) tg.PeerClass {
	switch {
	case id > 0:
		return &tg.PeerUser{UserID: id}
	case id <= -1000000000000:
		return &tg.PeerChannel{ChannelID: -1000000000000 - id}
	case id < 0:
		return &tg.PeerChat{ChatID: -id}
	}
	return nil
}

// peerList is a list that knows the users and chats of a response.
func (s *Store) peerList(users []tg.UserClass, chats []tg.ChatClass) *list {
	s.rememberPeers(users, chats)
	l := newList(s.Me().ID)
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			l.users[u.ID] = u
		}
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Chat:
			l.groups[c.ID] = c
		case *tg.Channel:
			l.channels[c.ID] = c
		case *tg.ChatForbidden:
			l.titles[peerID(&tg.PeerChat{ChatID: c.ID})] = c.Title
		case *tg.ChannelForbidden:
			l.titles[peerID(&tg.PeerChannel{ChannelID: c.ID})] = c.Title
		}
	}
	return l
}

// searchContacts finds chats by name: the account's own, then public ones.
func (s *Store) searchContacts(ctx context.Context, api *tg.Client, q model.SearchQuery) ([]model.Chat, error) {
	found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: q.Text, Limit: contactsLimit})
	if err != nil {
		return nil, err
	}
	l := s.peerList(found.Users, found.Chats)
	out := s.namedChats(q)
	seen := map[int64]bool{}
	for _, c := range out {
		seen[c.ID] = true
	}
	for _, p := range append(append([]tg.PeerClass(nil), found.MyResults...), found.Results...) {
		c := s.chatFor(peerID(p), l)
		if seen[c.ID] || c.Title == "" {
			continue
		}
		if q.Section == model.SearchChannels && c.Kind != model.KindChannel {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}

// searchFilter is the filter of messages.searchGlobal for a section.
func searchFilter(section model.SearchSection) tg.MessagesFilterClass {
	switch section {
	case model.SearchPhotos:
		return &tg.InputMessagesFilterPhotos{}
	case model.SearchVideos:
		return &tg.InputMessagesFilterVideo{}
	case model.SearchLinks:
		return &tg.InputMessagesFilterURL{}
	case model.SearchFiles:
		return &tg.InputMessagesFilterDocument{}
	case model.SearchMusic:
		return &tg.InputMessagesFilterMusic{}
	case model.SearchVoice:
		return &tg.InputMessagesFilterRoundVoice{}
	}
	return &tg.InputMessagesFilterEmpty{}
}

// searchGlobal reads the next page of messages.searchGlobal.
func (s *Store) searchGlobal(ctx context.Context, api *tg.Client, q model.SearchQuery) ([]model.FoundMessage, bool, error) {
	req := &tg.MessagesSearchGlobalRequest{Q: q.Text, Filter: searchFilter(q.Section), Limit: searchPage}
	req.BroadcastsOnly = q.Section == model.SearchChannels
	s.search.mu.Lock()
	req.OffsetRate, req.OffsetPeer, req.OffsetID = s.search.offsetRate, s.search.offsetPeer, s.search.offsetID
	s.search.mu.Unlock()
	if req.OffsetPeer == nil {
		req.OffsetPeer = &tg.InputPeerEmpty{}
	}
	res, err := api.MessagesSearchGlobal(ctx, req)
	if err != nil {
		return nil, false, err
	}
	return s.foundPage(ctx, res)
}

// SearchChat implements model.ChatSearcher: messages.search in the chat
// while connected, the cache's index otherwise. next is "r<id>", the
// message Telegram's next page starts under, or "l<n>", how many cached
// messages were read. The messages found are not saved, as no found message
// is: see foundPage.
func (s *Store) SearchChat(ctx context.Context, chat int64, text string, next string, limit int) (model.ChatSearchPage, error) {
	// A thread, a topic or a post's comments, is searched in its group,
	// as Telegram Desktop searches it: under its root (top_msg_id).
	thread := isThread(chat)
	s.history.mu.Lock()
	chat, top := s.history.threadChat(chat)
	api, cache, peer := s.history.api, s.history.cache, s.history.peers[chat]
	s.history.mu.Unlock()
	text = strings.TrimSpace(text)
	if text == "" || chat == 0 {
		return model.ChatSearchPage{}, nil
	}
	remote := api != nil && peer.ID != 0 && !strings.HasPrefix(next, "l")
	if !remote && thread {
		// The cache keeps no thread's messages.
		return model.ChatSearchPage{}, model.ErrSearchOffline
	}
	if !remote {
		if cache == nil {
			return model.ChatSearchPage{}, model.ErrSearchOffline
		}
		offset, _ := strconv.Atoi(strings.TrimPrefix(next, "l"))
		msgs, err := cache.Search(ctx, historycache.Query{Text: text, Chats: []int64{chat}}, offset, limit)
		if err != nil {
			return model.ChatSearchPage{}, err
		}
		page := model.ChatSearchPage{Messages: msgs}
		if len(msgs) == limit {
			page.Next = "l" + strconv.Itoa(offset+len(msgs))
		}
		return page, nil
	}
	offsetID, _ := strconv.Atoi(strings.TrimPrefix(next, "r"))
	req := &tg.MessagesSearchRequest{Peer: peer.input(), Q: text, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offsetID, Limit: limit}
	if top != 0 {
		req.SetTopMsgID(top)
	}
	res, err := api.MessagesSearch(ctx, req)
	if err != nil {
		return model.ChatSearchPage{}, err
	}
	mod, ok := res.AsModified()
	if !ok {
		return model.ChatSearchPage{}, nil
	}
	s.rememberPeers(mod.GetUsers(), mod.GetChats())
	raw := mod.GetMessages()
	c := s.history
	c.apply.Lock()
	msgs, err := s.convert(ctx, raw, false, math.MaxUint64)
	c.apply.Unlock()
	if err != nil {
		return model.ChatSearchPage{}, err
	}
	page := model.ChatSearchPage{Messages: msgs}
	switch res := res.(type) {
	case *tg.MessagesMessagesSlice:
		page.Count = res.Count
	case *tg.MessagesChannelMessages:
		page.Count = res.Count
	}
	sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].Key.MessageID > page.Messages[j].Key.MessageID })
	if len(raw) == limit {
		page.Next = "r" + strconv.Itoa(raw[len(raw)-1].GetID())
	}
	return page, nil
}

// searchPosts reads the next page of public posts.
func (s *Store) searchPosts(ctx context.Context, q model.SearchQuery) ([]model.FoundMessage, bool, error) {
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return nil, false, model.ErrSearchOffline
	}
	req := &tg.ChannelsSearchPostsRequest{Query: q.Text, Limit: searchPage}
	req.SetQuery(q.Text)
	s.search.mu.Lock()
	req.OffsetRate, req.OffsetPeer, req.OffsetID = s.search.offsetRate, s.search.offsetPeer, s.search.offsetID
	s.search.mu.Unlock()
	if req.OffsetPeer == nil {
		req.OffsetPeer = &tg.InputPeerEmpty{}
	}
	res, err := api.ChannelsSearchPosts(ctx, req)
	if err != nil {
		return nil, false, err
	}
	return s.foundPage(ctx, res)
}

// foundPage turns a page of found messages into the model's, and moves the
// cursor past it. The messages are not saved: the history of a chat is kept
// without gaps, and these are single messages from anywhere in it.
func (s *Store) foundPage(ctx context.Context, res tg.MessagesMessagesClass) ([]model.FoundMessage, bool, error) {
	page, ok := res.AsModified()
	if !ok {
		return nil, false, nil
	}
	raw := page.GetMessages()
	l := s.peerList(page.GetUsers(), page.GetChats())
	c := s.history
	c.apply.Lock()
	// Every message is shown as found, whatever updates came since.
	msgs, err := s.convert(ctx, raw, false, math.MaxUint64)
	c.apply.Unlock()
	if err != nil {
		return nil, false, err
	}
	found := make([]model.FoundMessage, len(msgs))
	for i, m := range msgs {
		found[i] = model.FoundMessage{Message: m, Chat: s.chatFor(m.Key.ChatID, l)}
	}
	more := false
	if len(raw) > 0 {
		last := raw[len(raw)-1]
		peer, _ := messagePeer(last)
		c.mu.Lock()
		input := c.peers[peerID(peer)].input()
		c.mu.Unlock()
		s.search.mu.Lock()
		defer s.search.mu.Unlock()
		if slice, ok := res.(*tg.MessagesMessagesSlice); ok {
			if rate, ok := slice.GetNextRate(); ok {
				s.search.offsetRate = rate
			}
			more = len(raw) >= searchPage || slice.Count > len(s.search.results.Messages)+len(raw)
		} else if _, ok := res.(*tg.MessagesChannelMessages); ok {
			more = len(raw) >= searchPage
		}
		s.search.offsetPeer, s.search.offsetID = input, last.GetID()
	}
	return found, more, nil
}
