// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
)

// Reactions the account may choose at once, without and with Premium: the
// defaults of the app config keys reactions_user_max_default and
// reactions_user_max_premium, which Telegram Desktop falls back on too.
const (
	reactionsLimit        = 1
	reactionsLimitPremium = 3
)

var errNotConnected = errors.New("not connected to Telegram")

// reactionState is what the store knows of the reactions allowed.
type reactionState struct {
	mu sync.Mutex
	// toggling lets one change of the account's reactions through at a
	// time.
	toggling sync.Mutex
	// all are Telegram's reactions (messages.getAvailableReactions), and
	// premium the emoji among them only Premium may choose; rank is the
	// place of each emoji in Telegram's list, the retired ones too.
	all     []model.Reaction
	premium map[string]bool
	rank    map[string]int
	// top are the account's top reactions (messages.getTopReactions), whose
	// custom emoji a Premium account may choose where a chat allows them;
	// favorite, the reaction of a double click (the config's
	// reactions_default).
	top        []model.Reaction
	favorite   model.Reaction
	allLoaded  bool
	allLoading bool
	// allRetry is when ReactionRank may ask for all again after it failed.
	allRetry time.Time
	// chats are the reactions each chat allows; loading, the chats asked.
	chats   map[int64]chatReactions
	loading map[int64]bool
}

// chatReactions is a chat's ChatReactions.
type chatReactions struct {
	// all: every reaction, custom emoji too when custom; otherwise some.
	all, custom bool
	some        []model.Reaction
}

// convertReactions reads the reactions of a message. The account's own go
// last, in the order it chose them, as model.ToggleReaction keeps them.
func convertReactions(reactions tg.MessageReactions) []model.Reaction {
	var out []model.Reaction
	var order []int
	for _, r := range reactions.Results {
		reaction, ok := convertReaction(r.Reaction)
		if !ok {
			continue
		}
		reaction.Count = r.Count
		var chosen int
		chosen, reaction.Chosen = r.GetChosenOrder()
		out = append(out, reaction)
		order = append(order, chosen)
	}
	chosenLast(out, order)
	return out
}

// chosenLast moves the account's reactions to the end of list, by their
// order; the others keep theirs.
func chosenLast(list []model.Reaction, order []int) {
	type entry struct {
		r     model.Reaction
		order int
	}
	entries := make([]entry, len(list))
	for i, r := range list {
		entries[i] = entry{r, order[i]}
		if !r.Chosen {
			entries[i].order = math.MinInt
		}
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return cmp.Compare(a.order, b.order) })
	for i, e := range entries {
		list[i] = e.r
	}
}

func convertReaction(r tg.ReactionClass) (model.Reaction, bool) {
	switch kind := r.(type) {
	case *tg.ReactionEmoji:
		return model.Reaction{Emoji: kind.Emoticon}, true
	case *tg.ReactionCustomEmoji:
		return model.Reaction{DocumentID: kind.DocumentID}, true
	case *tg.ReactionPaid:
		return model.Reaction{Paid: true}, true
	}
	return model.Reaction{}, false
}

func inputReaction(r model.Reaction) tg.ReactionClass {
	if r.DocumentID != 0 {
		return &tg.ReactionCustomEmoji{DocumentID: r.DocumentID}
	}
	return &tg.ReactionEmoji{Emoticon: r.Emoji}
}

func (s *Store) reactionLimit() int {
	if s.Me().Premium {
		return reactionsLimitPremium
	}
	return reactionsLimit
}

// ChatReactions implements model.Reactor.
func (s *Store) ChatReactions(chat int64) ([]model.Reaction, int, bool) {
	if chat = s.realChat(chat); chat == 0 {
		return nil, 0, false
	}
	r := &s.reactions
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.allLoaded {
		if !r.allLoading {
			r.allLoading = true
			s.goReactions(func(ctx context.Context, api *tg.Client) { s.loadAvailableReactions(ctx, api) })
		}
		return nil, 0, false
	}
	allowed, ok := r.chats[chat]
	if !ok {
		if !r.loading[chat] {
			if r.loading == nil {
				r.loading = map[int64]bool{}
			}
			r.loading[chat] = true
			s.goReactions(func(ctx context.Context, api *tg.Client) { s.loadChatReactions(ctx, api, chat) })
		}
		return nil, 0, false
	}
	premium := s.Me().Premium
	var list []model.Reaction
	if allowed.all {
		for _, one := range r.all {
			if premium || !r.premium[one.Emoji] {
				list = append(list, one)
			}
		}
		if premium && allowed.custom {
			for _, one := range r.top {
				if one.DocumentID != 0 {
					list = append(list, one)
				}
			}
		}
	} else {
		list = allowed.some
	}
	return list, s.reactionLimit(), true
}

// ReactionRank implements model.ReactionOrderer. Every history with
// reactions asks it, so it asks Telegram for the list only when connected,
// and not again soon after it failed.
func (s *Store) ReactionRank(reaction model.Reaction) (int, bool) {
	r := &s.reactions
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.allLoaded {
		if !r.allLoading && time.Now().After(r.allRetry) && s.connected() {
			r.allLoading = true
			s.goReactions(func(ctx context.Context, api *tg.Client) { s.loadAvailableReactions(ctx, api) })
		}
		return 0, false
	}
	if reaction.DocumentID != 0 || reaction.Paid {
		return 0, false
	}
	rank, ok := r.rank[reaction.Emoji]
	return rank, ok
}

func (s *Store) connected() bool {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.api != nil && !c.closing
}

// goReactions runs load with the API, off the frame.
func (s *Store) goReactions(load func(context.Context, *tg.Client)) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	api, ctx := c.api, c.ctx
	if c.closing {
		return
	}
	c.wg.Go(func() {
		defer crash.Recover("reactions", nil)
		if api == nil {
			// Not connected yet: ask again later.
			time.Sleep(time.Second)
			s.forgetReactionLoads()
			s.changed()
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		load(ctx, api)
		s.changed()
	})
}

func (s *Store) forgetReactionLoads() {
	r := &s.reactions
	r.mu.Lock()
	r.allLoading = false
	clear(r.loading)
	r.mu.Unlock()
}

func (s *Store) loadAvailableReactions(ctx context.Context, api *tg.Client) {
	// Telegram Desktop's favorite without a config is the thumbs up.
	favorite := model.Reaction{Emoji: "👍"}
	if cfg, err := api.HelpGetConfig(ctx); err == nil {
		if def, ok := cfg.GetReactionsDefault(); ok {
			if one, ok := convertReaction(def); ok && !one.Paid {
				favorite = one
			}
		}
	}
	var top []model.Reaction
	if s.Me().Premium {
		if res, err := api.MessagesGetTopReactions(ctx, &tg.MessagesGetTopReactionsRequest{Limit: 50}); err == nil {
			if list, ok := res.(*tg.MessagesReactions); ok {
				for _, one := range list.Reactions {
					if reaction, ok := convertReaction(one); ok && !reaction.Paid {
						top = append(top, reaction)
					}
				}
			}
		}
	}
	res, err := api.MessagesGetAvailableReactions(ctx, 0)
	r := &s.reactions
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allLoading = false
	r.favorite, r.top = favorite, top
	list, ok := res.(*tg.MessagesAvailableReactions)
	if err != nil || !ok {
		r.allRetry = time.Now().Add(30 * time.Second)
		return
	}
	r.all, r.premium, r.rank = nil, map[string]bool{}, map[string]int{}
	for i, one := range list.Reactions {
		r.rank[one.Reaction] = i
		if one.Inactive {
			continue
		}
		r.all = append(r.all, model.Reaction{Emoji: one.Reaction})
		if one.Premium {
			r.premium[one.Reaction] = true
		}
	}
	r.allLoaded = true
}

func (s *Store) loadChatReactions(ctx context.Context, api *tg.Client, chat int64) {
	c := s.history
	c.mu.Lock()
	peer := c.peers[chat]
	c.mu.Unlock()
	allowed, err := fetchChatReactions(ctx, api, peer)
	r := &s.reactions
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.loading, chat)
	if err != nil {
		return
	}
	if r.chats == nil {
		r.chats = map[int64]chatReactions{}
	}
	r.chats[chat] = allowed
}

// fetchChatReactions asks which reactions peer allows. Private chats allow
// every one.
func fetchChatReactions(ctx context.Context, api *tg.Client, peer peerRecord) (chatReactions, error) {
	var available tg.ChatReactionsClass
	switch peer.Kind {
	case "chat":
		full, err := api.MessagesGetFullChat(ctx, peer.ID)
		if err != nil {
			return chatReactions{}, err
		}
		if f, ok := full.FullChat.(*tg.ChatFull); ok {
			available, _ = f.GetAvailableReactions()
		}
	case "channel":
		full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
		if err != nil {
			return chatReactions{}, err
		}
		if f, ok := full.FullChat.(*tg.ChannelFull); ok {
			available, _ = f.GetAvailableReactions()
		}
	default:
		return chatReactions{all: true}, nil
	}
	switch a := available.(type) {
	case *tg.ChatReactionsAll:
		return chatReactions{all: true, custom: a.AllowCustom}, nil
	case *tg.ChatReactionsSome:
		var some []model.Reaction
		for _, one := range a.Reactions {
			if r, ok := convertReaction(one); ok && !r.Paid {
				some = append(some, r)
			}
		}
		return chatReactions{some: some}, nil
	}
	// ChatReactionsNone, or nothing said: no reactions.
	return chatReactions{}, nil
}

// ToggleReaction implements model.Reactor.
func (s *Store) ToggleReaction(msg model.Message, reaction model.Reaction, report func(error)) {
	c := s.history
	chat, id := msg.Key.ChatID, int(msg.Key.MessageID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	// The change is saved to the cache, which must not hold up a frame.
	c.wg.Go(func() {
		defer crash.Recover("send reaction", func(p *crash.Panic) { report(p) })
		// One change at a time, so that each one starts from the last.
		s.reactions.toggling.Lock()
		defer s.reactions.toggling.Unlock()
		limit := s.reactionLimit()
		var before, after []model.Reaction
		if !s.changeMessage(chat, id, func(m *model.Message) {
			before = m.Reactions
			m.Reactions = model.ToggleReaction(m.Reactions, reaction, limit)
			after = m.Reactions
		}) {
			return
		}
		c.mu.Lock()
		api, ctx, peer := c.api, c.ctx, c.peers[chat]
		c.mu.Unlock()
		err := s.sendReaction(ctx, api, peer, id, model.ChosenReactions(after))
		if err == nil {
			s.readOnInteract(chat)
			return
		}
		// Undo, unless something changed the reactions since.
		s.changeMessage(chat, id, func(m *model.Message) {
			if sameReactions(m.Reactions, after) {
				m.Reactions = before
			}
		})
		report(err)
	})
}

func (s *Store) sendReaction(ctx context.Context, api *tg.Client, peer peerRecord, id int, chosen []model.Reaction) error {
	if api == nil {
		return errNotConnected
	}
	req := &tg.MessagesSendReactionRequest{Peer: peer.input(), MsgID: id}
	for _, r := range chosen {
		req.Reaction = append(req.Reaction, inputReaction(r))
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	updates, err := api.MessagesSendReaction(ctx, req)
	if err != nil {
		return err
	}
	return s.Handle(ctx, updates)
}

func sameReactions(a, b []model.Reaction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyReactions takes the reactions an update says a message has. A min
// update does not say which are the account's, so those stay as they were.
func (s *Store) applyReactions(chat int64, id int, reactions tg.MessageReactions) {
	next := convertReactions(reactions)
	s.changeMessage(chat, id, func(m *model.Message) {
		if reactions.Min {
			// The account's keep their order too.
			order := make([]int, len(next))
			for i := range next {
				next[i].Chosen = false
				for j, old := range m.Reactions {
					if old.Same(next[i]) {
						next[i].Chosen, order[i] = old.Chosen, j
					}
				}
			}
			chosenLast(next, order)
		}
		m.Reactions = next
		if !reactions.Min {
			m.ReactionsListed = reactions.CanSeeList
		}
		m.ReactionsListed = m.ReactionsListed && len(next) > 0
	})
}

// changeMessage changes a message where it is kept: in the open history and
// in the cache. It reports whether the message was found.
func (s *Store) changeMessage(chat int64, id int, change func(*model.Message)) bool {
	c := s.history
	var cache *historycache.Cache
	var ctx context.Context
	// inThread is set when the message is only in a thread, which the
	// cache does not keep.
	inThread := false
	changed := func() *model.Message {
		c.mu.Lock()
		defer c.mu.Unlock()
		cache, ctx = c.cache, c.ctx
		key := model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.MessageID(id)}
		var found *model.Message
		for _, shown := range append([]int64{chat}, c.threadsOf(chat)...) {
			h := c.histories[shown]
			if h == nil {
				continue
			}
			for i := range h.Messages {
				if h.Messages[i].Key != key {
					continue
				}
				m := h.Messages[i]
				if found != nil {
					// The same change, once: the thread shows the
					// history's message.
					m = *found
				} else {
					change(&m)
					m.ContentRevision = model.Revision(m)
					found = &m
					inThread = shown != chat
				}
				h.Messages[i] = m
				h.Revision++
			}
		}
		return found
	}()
	if inThread {
		s.changed()
		return true
	}
	if cache == nil {
		s.changed()
		return changed != nil
	}
	c.apply.Lock()
	defer c.apply.Unlock()
	if changed == nil {
		m, ok, err := cache.Message(ctx, chat, id)
		if err != nil || !ok {
			return false
		}
		change(&m)
		m.ContentRevision = model.Revision(m)
		changed = &m
	}
	_ = cache.SaveMessages(ctx, []model.Message{*changed})
	s.changed()
	return true
}

// QuickReaction implements model.QuickReactor: the account's default
// reaction, where the chat allows it.
func (s *Store) QuickReaction(chat int64) (model.Reaction, bool) {
	available, _, ok := s.ChatReactions(chat)
	if !ok {
		return model.Reaction{}, false
	}
	s.reactions.mu.Lock()
	favorite := s.reactions.favorite
	s.reactions.mu.Unlock()
	for _, one := range available {
		if one.Same(favorite) {
			return favorite, true
		}
	}
	return model.Reaction{}, false
}

// Reacted implements model.ReactionLister with
// messages.getMessageReactionsList.
func (s *Store) Reacted(ctx context.Context, msg model.Message, reaction model.Reaction, offset string, limit int) (model.ReactedPage, error) {
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[msg.Key.ChatID]
	c.mu.Unlock()
	if api == nil {
		return model.ReactedPage{}, errNotConnected
	}
	req := &tg.MessagesGetMessageReactionsListRequest{Peer: peer.input(), ID: int(msg.Key.MessageID), Limit: limit}
	if reaction != (model.Reaction{}) {
		req.SetReaction(inputReaction(reaction))
	}
	if offset != "" {
		req.SetOffset(offset)
	}
	res, err := api.MessagesGetMessageReactionsList(ctx, req)
	if err != nil {
		return model.ReactedPage{}, err
	}
	s.rememberPeers(res.Users, res.Chats)
	names := map[int64]string{}
	for _, u := range res.Users {
		if u, ok := u.(*tg.User); ok {
			names[peerID(&tg.PeerUser{UserID: u.ID})] = personName(u)
		}
	}
	for _, ch := range res.Chats {
		switch ch := ch.(type) {
		case *tg.Chat:
			names[peerID(&tg.PeerChat{ChatID: ch.ID})] = ch.Title
		case *tg.Channel:
			names[peerID(&tg.PeerChannel{ChannelID: ch.ID})] = ch.Title
		}
	}
	page := model.ReactedPage{Count: res.Count, Next: res.NextOffset}
	for _, one := range res.Reactions {
		r, ok := convertReaction(one.Reaction)
		if !ok {
			continue
		}
		id := peerID(one.PeerID)
		page.List = append(page.List, model.Reacted{PeerID: id, Name: names[id], Reaction: r, Date: time.Unix(int64(one.Date), 0)})
	}
	return page, nil
}
