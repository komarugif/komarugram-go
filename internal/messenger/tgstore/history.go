// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/security"
	"komarugram/pkg/dcpool"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

type peerRecord struct {
	ID, Hash int64
	Kind     string
	Name     string
	Photo    *avatarRecord
	// Rights is what the account may do in a group or channel.
	Rights   peerRights   `json:",omitempty"`
	Metadata peerMetadata `json:",omitempty"`
	// Username is a public channel's or supergroup's, for links.
	Username string `json:",omitempty"`
}

// peerRights are what MessageRights and CanSend need of a peer.
type peerRights struct {
	Known, Admin, Gigagroup, Unrestricted bool
	Creator                               bool
	Default, Personal                     model.SendKind
	Until, DiscussionID                   int64
	HasDiscussion                         bool
	// NoForwards: the chat protects its content.
	NoForwards bool `json:",omitempty"`
	// Broadcast: a channel, not a supergroup.
	Broadcast bool `json:",omitempty"`
	// Bot and Self tell users apart.
	Bot, Self bool `json:",omitempty"`
	// Delete: the account may delete anyone's messages (creator or admin).
	Delete bool `json:",omitempty"`
	// Post: the account may post to the channel.
	Post bool `json:",omitempty"`
	// Muted: the account may not send messages (banned, left, or members
	// may not write).
	Muted bool `json:",omitempty"`
	// Left: the account is not in the channel, so Telegram does not push
	// its updates.
	Left bool `json:",omitempty"`
	// Banned: the account may not send even as a member; JoinToSend, a
	// discussion group takes comments only from members.
	Banned     bool `json:",omitempty"`
	JoinToSend bool `json:",omitempty"`
}

func (p peerRecord) input() tg.InputPeerClass {
	if p.Rights.Self {
		return &tg.InputPeerSelf{}
	}
	switch p.Kind {
	case "user":
		return &tg.InputPeerUser{UserID: p.ID, AccessHash: p.Hash}
	case "chat":
		return &tg.InputPeerChat{ChatID: p.ID}
	case "channel":
		return &tg.InputPeerChannel{ChannelID: p.ID, AccessHash: p.Hash}
	}
	return &tg.InputPeerEmpty{}
}

type conversation struct {
	persist       sync.Mutex
	avatars       map[int64]avatarSnapshot
	top           map[int64]int
	globalDeleted map[int]bool
	mu            sync.Mutex
	apply         sync.Mutex
	ctx           context.Context
	account       string
	cache         *historycache.Cache
	api           *tg.Client
	pool          dcpool.Pool
	peers         map[int64]peerRecord
	histories     map[int64]*model.History
	// epochs count the times a chat's history was started again at another
	// message (see Reveal); a page asked for in an earlier epoch is dropped.
	epochs map[int64]uint64
	// reveals are the messages the next history of a chat opens at.
	reveals     map[int64]model.MessageID
	views       map[int64]model.Viewport
	refs        map[string]fileLocation
	touched     map[model.MessageKey]uint64
	deleted     map[model.MessageKey]bool
	generation  uint64
	wg          sync.WaitGroup
	saves       chan viewSave
	closing     bool
	packs       map[string]map[string]string
	packNext    map[string]time.Time
	packLoading map[string]bool
	// watch polls the channels on screen that push no updates.
	watch channelPolls
	// threads are the discussions of channel posts, by their chat ids, and
	// threadIDs those ids by post.
	threads   map[int64]*thread
	threadIDs map[model.MessageKey]int64
	// lookups are single messages found apart from their history's pages.
	lookups map[model.MessageKey]*lookup
	// pinned are the chats' pinned messages.
	pinned map[int64]*pinned
	// forums are the topics of the forums opened.
	forums map[int64]*forum
	// live are the chats whose newest span (historycache.Span) reaches
	// their newest message, so each new message that comes as an update
	// extends it: the updates come in order and without gaps, or say they
	// cannot (too long), and liveEpoch counts those times.
	live      map[int64]bool
	liveEpoch uint64
	// firsts are the chats' first messages, when known: see noteFirst.
	firsts map[int64]int
	// drafts are the messages bots stream: see drafts.go.
	drafts streamedDrafts
}
type viewSave struct {
	view    model.Viewport
	layouts []model.MessageLayout
}

func newConversation() *conversation {
	return &conversation{top: map[int64]int{}, globalDeleted: map[int]bool{}, ctx: context.Background(), peers: map[int64]peerRecord{}, histories: map[int64]*model.History{}, epochs: map[int64]uint64{}, reveals: map[int64]model.MessageID{}, views: map[int64]model.Viewport{}, refs: map[string]fileLocation{}, touched: map[model.MessageKey]uint64{}, deleted: map[model.MessageKey]bool{}, packs: map[string]map[string]string{}, packLoading: map[string]bool{}, saves: make(chan viewSave, 64), live: map[int64]bool{}, firsts: map[int64]int{}}
}
func (s *Store) Configure(ctx context.Context, account, path string, p *security.Manager) error {
	c := s.history
	c.mu.Lock()
	defer func() { c.mu.Unlock(); s.changed() }()
	// Configure runs again when the connection is made again: the cache
	// stays open, and what it holds is read anew.
	cache, e := c.cache, error(nil)
	if cache == nil {
		if cache, e = historycache.Open(path, account, p); e != nil {
			return e
		}
		c.cache = cache
		c.wg.Go(func() {
			for save := range c.saves {
				saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				start := diagnostics.Start()
				err := cache.SaveView(saveCtx, save.view, save.layouts)
				s.profileHistory("cache.save-view.written", save.view.ChatID, 0, int(save.view.AnchorMessageID), len(save.layouts), start, err)
				cancel()
				if err != nil {
					s.historyError(save.view.ChatID, err)
				}
			}
		})
	}
	c.account = account
	c.ctx = ctx
	var chats []model.Chat
	if _, e = cache.Get(ctx, "dialogs", &chats); e != nil {
		return e
	}
	if _, e = cache.Get(ctx, "peers", &c.peers); e != nil {
		return e
	}
	if _, e = cache.Get(ctx, "tops", &c.top); e != nil {
		return e
	}
	var recent []model.Chat
	if _, e = cache.Get(ctx, recentKey, &recent); e != nil {
		return e
	}
	s.recent.mu.Lock()
	s.recent.list = recent
	s.recent.mu.Unlock()
	s.mu.Lock()
	if s.chats == nil {
		s.chats = chats
	}
	s.mu.Unlock()
	return nil
}
func (s *Store) Close() (err error) {
	c := s.history
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return nil
	}
	c.closing = true
	c.watch.stopPolls()
	close(c.saves)
	pool, cache := c.pool, c.cache
	c.mu.Unlock()
	c.wg.Wait()

	if pool != nil {
		err = errors.Join(err, pool.Close())
	}
	if cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, v := range c.views {
			err = errors.Join(err, cache.SaveView(ctx, v, nil))
		}
		err = errors.Join(err, cache.Close())
	}
	return err
}

func (s *Store) Cache() *historycache.Cache {
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	return s.history.cache
}
func (s *Store) Attach(client *telegram.Client) {
	s.mu.Lock()
	s.dc = client.Config().ThisDC
	s.me.DC = s.dc
	s.mu.Unlock()
	c := s.history
	c.mu.Lock()
	c.api = client.API()
	if diagnostics.Current() != nil {
		c.pool = dcpool.NewObservedPool(client, 4, func(dc int) []telegram.Middleware { return diagnostics.Middleware(c.account, dc) })
	} else {
		c.pool = dcpool.NewPool(client, 4)
	}
	c.mu.Unlock()
}
func (s *Store) Updates() *updates.Manager {
	return updates.New(updates.Config{Handler: telegram.UpdateHandlerFunc(s.Handle), Storage: s.Cache(), AccessHasher: s.Cache(), MaxChannelDifferenceConcurrency: 2,
		OnTooLong: func() {
			s.endLive(0)
			s.reloadDialogs()
		},
		OnChannelTooLong: func(id int64) {
			chat := peerID(&tg.PeerChannel{ChannelID: id})
			s.endLive(chat)
			s.resync(chat)
		}})
}

// endLive ends the live span of chat, or of every chat for 0: updates were
// missed, and the next message may not follow the newest one kept.
func (s *Store) endLive(chat int64) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	c.liveEpoch++
	if chat == 0 {
		clear(c.live)
	} else {
		delete(c.live, chat)
	}
}

// startLive marks chat live when the history read at liveEpoch epoch
// reached its newest message: see conversation.live. A channel the account
// is not in sends no updates, and is never live.
func (c *conversation) startLive(chat int64, epoch uint64) {
	if c.liveEpoch == epoch && !c.peers[chat].Rights.Left {
		c.live[chat] = true
	}
}

// extendLive adds new messages that came as updates to their chats' live
// spans.
func (s *Store) extendLive(ctx context.Context, msgs []model.Message) error {
	for _, m := range msgs {
		c := s.history
		c.mu.Lock()
		live, cache, epoch := c.live[m.Key.ChatID], c.cache, c.liveEpoch
		c.mu.Unlock()
		if !live || cache == nil {
			continue
		}
		id := int(m.Key.MessageID)
		top, ok, _, e := cache.SpanOf(ctx, m.Key.ChatID, 0)
		if e != nil {
			return e
		}
		low := id
		if ok {
			low = min(top.High, id)
		}
		c.mu.Lock()
		still := c.live[m.Key.ChatID] && c.liveEpoch == epoch
		c.mu.Unlock()
		if !still {
			continue
		}
		if _, e = cache.AddSpan(ctx, m.Key.ChatID, low, max(id, low)); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) historyError(chat int64, e error) {
	c := s.history
	c.mu.Lock()
	if h := c.histories[chat]; h != nil {
		h.Err = e
	}
	c.mu.Unlock()
	s.changed()
}
func (s *Store) rememberPeers(users []tg.UserClass, chats []tg.ChatClass) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			key := peerID(&tg.PeerUser{UserID: u.ID})
			if old, ok := c.peers[key]; !u.Min || !ok {
				c.peers[key] = peerRecord{ID: u.ID, Hash: u.AccessHash, Kind: "user", Name: userName(u), Rights: peerRights{Known: true, Bot: u.Bot, Self: u.Self, Muted: u.Deleted}, Username: userUsername(u), Metadata: peerMetadata{Known: true, Badges: userBadges(u, time.Now())}}
			} else {
				old.Name = userName(u)
				if name := userUsername(u); name != "" {
					old.Username = name
				}
				c.peers[key] = old
			}
			p := c.peers[key]
			if photo, ok := u.Photo.(*tg.UserProfilePhoto); ok {
				p.Photo = &avatarRecord{photo.PhotoID, photo.DCID, photo.HasVideo, photo.StrippedThumb}
			} else if !u.Min {
				p.Photo = nil
			}
			c.peers[key] = p
		}
	}
	for _, ch := range chats {
		switch ch := ch.(type) {
		case *tg.Chat:
			c.peers[peerID(&tg.PeerChat{ChatID: ch.ID})] = peerRecord{ID: ch.ID, Kind: "chat", Name: ch.Title, Photo: chatAvatar(ch.Photo), Rights: groupRights(ch), Metadata: peerMetadata{Known: true, MembersKnown: true, Members: ch.ParticipantsCount}}
		case *tg.ChatForbidden:
			key := peerID(&tg.PeerChat{ChatID: ch.ID})
			c.peers[key] = peerRecord{ID: ch.ID, Kind: "chat", Name: ch.Title, Rights: peerRights{Muted: true}, Metadata: c.peers[key].Metadata}
		case *tg.ChannelForbidden:
			key := peerID(&tg.PeerChannel{ChannelID: ch.ID})
			c.peers[key] = peerRecord{ID: ch.ID, Hash: ch.AccessHash, Kind: "channel", Name: ch.Title, Rights: peerRights{Muted: true, Broadcast: ch.Broadcast}, Metadata: c.peers[key].Metadata}
		case *tg.Channel:
			key := peerID(&tg.PeerChannel{ChannelID: ch.ID})
			if old, ok := c.peers[key]; !ch.Min || !ok {
				c.peers[key] = peerRecord{ID: ch.ID, Hash: ch.AccessHash, Kind: "channel", Name: ch.Title, Rights: channelRights(ch), Username: channelUsername(ch), Metadata: old.Metadata}
				p := c.peers[key]
				p.Rights.Unrestricted = old.Rights.Unrestricted
				if ch.HasLink {
					p.Rights.DiscussionID = old.Rights.DiscussionID
				}
				c.peers[key] = p
			}
			p := c.peers[key]
			p.rememberChannelMetadata(ch)
			if ch.Min && p.Rights.Known {
				p.Rights.Default = bannedKinds(ch.DefaultBannedRights)
				p.Rights.JoinToSend = ch.JoinToSend
				p.Rights.Gigagroup = ch.Gigagroup
				p.Rights.HasDiscussion = ch.HasLink
				if !ch.HasLink {
					p.Rights.DiscussionID = 0
				}
			}
			if !ch.Min || ch.Photo != nil {
				p.Photo = chatAvatar(ch.Photo)
			}
			c.peers[key] = p
		}
	}
}
func (s *Store) persistDialogs(ctx context.Context) error {
	c := s.history
	c.persist.Lock()
	defer c.persist.Unlock()
	c.mu.Lock()
	cache := c.cache
	peers, tops := maps.Clone(c.peers), maps.Clone(c.top)
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	chats := s.Chats()
	if e := cache.Put(ctx, "peers", peers); e != nil {
		return e
	}
	if e := cache.Put(ctx, "tops", tops); e != nil {
		return e
	}
	return cache.Put(ctx, "dialogs", chats)
}
func (s *Store) OpenChat(chat int64) {
	if isThread(chat) {
		// OpenComments loads it.
		return
	}
	c := s.history
	c.mu.Lock()
	if c.closing || c.cache == nil || c.histories[chat] != nil {
		c.mu.Unlock()
		return
	}
	c.histories[chat] = &model.History{LoadingOlder: true, HasOlder: true}
	epoch := c.epochs[chat]
	reveal, revealing := c.reveals[chat]
	delete(c.reveals, chat)
	c.mu.Unlock()
	c.wg.Go(func() {
		start := diagnostics.Start()
		var v model.Viewport
		found, e := c.cache.Get(c.ctx, fmt.Sprintf("viewport/%d", chat), &v)
		if e != nil {
			s.finishPageAt(epoch, chat, 0, nil, false, e)
			return
		}
		if revealing {
			v = model.Viewport{AccountID: c.account, ChatID: chat, AnchorMessageID: reveal, AtEnd: reveal == 0, UpdatedAt: time.Now()}
			found = true
		}
		anchor := 0
		if found && !v.AtEnd {
			anchor = int(v.AnchorMessageID)
		}
		msgs, e := c.cache.Around(c.ctx, chat, anchor, 200)
		s.profileHistory("cache.open-around", chat, 0, anchor, len(msgs), start, e)
		if revealing && reveal != 0 && !slices.ContainsFunc(msgs, func(m model.Message) bool { return m.Key.MessageID == reveal }) {
			// The cache does not have the message, so what it has around
			// it is from elsewhere in the history: wait for Telegram's page.
			msgs = nil
		}
		c.mu.Lock()
		if c.epochs[chat] != epoch {
			c.mu.Unlock()
			return
		}
		if found {
			c.views[chat] = v
		}
		h := c.histories[chat]
		h.Messages = msgs
		h.Revision++
		h.HasNewer = anchor != 0
		c.mu.Unlock()
		s.changed()
		if e != nil {
			s.finishPageAt(epoch, chat, 0, nil, false, e)
			return
		}
		s.fetchAt(epoch, chat, 0, anchor)
	})
}

// Reveal implements model.MessageRevealer. The chat's history is dropped
// and loaded again around the message the next time the chat is opened;
// pages already asked for are of the old history, and are dropped too.
func (s *Store) Reveal(chat int64, id model.MessageID) {
	if isThread(chat) {
		s.revealThreadAt(chat, id)
		return
	}
	s.reveal(chat, id, false)
}

// RevealFirst implements model.HistoryEnds: Telegram is asked for the
// messages from id 1 on, which are the oldest the chat has. A thread's
// history, which is not kept, is replaced at once: see revealThread.
func (s *Store) RevealFirst(chat int64) bool {
	if isThread(chat) {
		return s.revealThread(chat, true)
	}
	s.reveal(chat, 1, false)
	return true
}

// RevealLast implements model.HistoryEnds.
func (s *Store) RevealLast(chat int64) bool {
	if isThread(chat) {
		return s.revealThread(chat, false)
	}
	s.reveal(chat, 0, true)
	return true
}

// reveal drops chat's history to load it again around id, or at its end.
func (s *Store) reveal(chat int64, id model.MessageID, atEnd bool) {
	c := s.history
	c.mu.Lock()
	c.epochs[chat]++
	delete(c.histories, chat)
	c.reveals[chat] = id
	c.views[chat] = model.Viewport{AccountID: c.account, ChatID: chat, AnchorMessageID: id, AtEnd: atEnd, UpdatedAt: time.Now()}
	c.mu.Unlock()
	s.changed()
}

func (s *Store) History(chat int64) model.History {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if h := c.histories[chat]; h != nil {
		out := *h
		out.Messages = append([]model.Message(nil), h.Messages...)
		return out
	}
	return model.History{LoadingOlder: true}
}
func (s *Store) Viewport(chat int64) (model.Viewport, bool) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.views[chat]
	return v, ok
}
func (s *Store) SaveView(v model.Viewport, ls []model.MessageLayout) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil || c.closing || isThread(v.ChatID) {
		// A thread's id lasts only for the session: there is nothing to
		// keep.
		return
	}
	v.AccountID = c.account
	c.views[v.ChatID] = v
	// UI callers coalesce writes; bounded queue applies backpressure only by dropping
	// intermediate measurements, never the in-memory authoritative anchor.
	select {
	case c.saves <- viewSave{v, append([]model.MessageLayout(nil), ls...)}:
		s.profileHistory("cache.save-view.queued", v.ChatID, 0, int(v.AnchorMessageID), len(ls), time.Time{}, nil)
	default:
		s.profileHistory("cache.save-view.dropped", v.ChatID, 0, int(v.AnchorMessageID), len(ls), time.Time{}, nil)
	}
}
func (s *Store) Layouts(chat int64, env model.RenderEnvironment) []model.MessageLayout {
	if isThread(chat) {
		return nil
	}
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	start := diagnostics.Start()
	ls, e := cache.Layouts(c.ctx, chat, env)
	s.profileHistory("cache.layout-read", chat, 0, 0, len(ls), start, e)
	if e != nil {
		s.historyError(chat, e)
	}
	return ls
}
func (s *Store) LoadOlder(chat int64) {
	if isThread(chat) {
		s.loadOlderComments(chat)
		return
	}
	s.page(chat, -1)
}
func (s *Store) LoadNewer(chat int64) {
	if isThread(chat) {
		s.loadNewerComments(chat)
		return
	}
	s.page(chat, 1)
}
func (s *Store) page(chat int64, dir int) {
	c := s.history
	c.mu.Lock()
	h := c.histories[chat]
	if c.closing || h == nil || h.LoadingOlder || h.LoadingNewer || len(h.Messages) == 0 {
		c.mu.Unlock()
		return
	}
	anchor := int(h.Messages[0].Key.MessageID)
	if dir > 0 {
		if !h.HasNewer {
			c.mu.Unlock()
			return
		}
		anchor = int(h.Messages[len(h.Messages)-1].Key.MessageID)
		h.LoadingNewer = true
	} else {
		if !h.HasOlder {
			c.mu.Unlock()
			return
		}
		h.LoadingOlder = true
	}
	h.Err = nil
	epoch := c.epochs[chat]
	c.mu.Unlock()
	c.wg.Go(func() { ; s.guardedFetch(epoch, chat, dir, anchor) })
}

// resync reads chat's history again from Telegram, or every chat's for 0:
// updates were missed, or the dialogs were read again. A history on screen
// is read again where it is; one opened before and hidden now is dropped,
// to be read when it is opened again (UI asks for it on each frame), since
// reading every chat opened in the session at once floods Telegram.
func (s *Store) resync(chat int64) {
	c := s.history
	c.mu.Lock()
	var ids []int64
	dropped := false
	for id, h := range c.histories {
		if isThread(id) || chat != 0 && id != chat {
			continue
		}
		if c.watch.shown(id) {
			if !h.LoadingOlder && !h.LoadingNewer {
				ids = append(ids, id)
			}
			continue
		}
		c.epochs[id]++
		delete(c.histories, id)
		dropped = true
	}
	c.mu.Unlock()
	for _, id := range ids {
		s.Reload(id)
	}
	if dropped {
		s.changed()
	}
}
func (s *Store) Reload(chat int64) {
	c := s.history
	c.mu.Lock()
	h := c.histories[chat]
	if c.closing || h == nil || h.LoadingOlder || h.LoadingNewer {
		c.mu.Unlock()
		return
	}
	anchor := 0
	if v, ok := c.views[chat]; ok && !v.AtEnd {
		anchor = int(v.AnchorMessageID)
	}
	h.LoadingOlder = true
	epoch := c.epochs[chat]
	c.mu.Unlock()
	c.wg.Go(func() { ; s.guardedFetch(epoch, chat, 0, anchor) })
}

// guardedFetch turns a panic while converting a server page into a page error,
// which the history shows with a retry, instead of ending the process.
func (s *Store) guardedFetch(epoch uint64, chat int64, dir, anchor int) {
	defer crash.Recover("history fetch", func(p *crash.Panic) { s.finishPageAt(epoch, chat, dir, nil, false, p) })
	s.fetchAt(epoch, chat, dir, anchor)
}

// fetch reads a page of the chat's current history.
func (s *Store) fetch(chat int64, dir, anchor int) {
	s.history.mu.Lock()
	epoch := s.history.epochs[chat]
	s.history.mu.Unlock()
	s.fetchAt(epoch, chat, dir, anchor)
}

// fetchAt reads a page of the history of epoch: see finishPageAt.
func (s *Store) fetchAt(epoch uint64, chat int64, dir, anchor int) {
	started := diagnostics.Start()
	if !started.IsZero() {
		defer func() { s.profileHistory("history.fetch", chat, dir, anchor, 0, started, nil) }()
	}
	c := s.history
	c.mu.Lock()
	api, peer, start, liveEpoch := c.api, c.peers[chat], c.generation, c.liveEpoch
	if h := c.histories[chat]; h != nil {
		h.Offline = api == nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()

	var cached []model.Message
	if dir != 0 {
		var err error
		cacheStart := diagnostics.Start()
		cached, err = c.cache.Page(ctx, chat, anchor, dir, 80)
		s.profileHistory("cache.page-read", chat, dir, anchor, len(cached), cacheStart, err)
		if err != nil {
			s.finishPageAt(epoch, chat, dir, nil, false, err)
			return
		}
	}
	if api == nil || peer.Kind == "" {
		s.profileHistory("history.cache-only", chat, dir, anchor, len(cached), time.Time{}, nil)
		if dir <= 0 {
			s.noteFirst(ctx, chat, cached)
		}
		if dir != 0 {
			s.finishPageAt(epoch, chat, dir, cached, len(cached) >= 80, nil, start)
		} else {
			s.finishPageAt(epoch, chat, dir, nil, true, nil, start)
		}
		return
	}
	req := historyRequest(peer.input(), dir, anchor, 80)
	result, e := api.MessagesGetHistory(ctx, req)
	if e != nil {
		if len(cached) > 0 {
			s.finishPageAt(epoch, chat, dir, cached, true, nil, start)
		} else {
			s.finishPageAt(epoch, chat, dir, nil, false, e)
		}
		return
	}
	modified, ok := result.AsModified()
	if !ok {
		s.finishPageAt(epoch, chat, dir, nil, false, nil)
		return
	}
	s.rememberPeers(modified.GetUsers(), modified.GetChats())
	if e = s.reconcile(ctx, chat, modified.GetMessages(), start, dir, anchor, req.Limit); e != nil {
		s.finishPageAt(epoch, chat, dir, nil, false, e)
		return
	}
	msgs, e := s.ingest(ctx, modified.GetMessages(), false, start)
	if e == nil {
		e = s.persistDialogs(ctx)
	}
	if e == nil {
		e = s.keepSpan(ctx, epoch, liveEpoch, chat, modified.GetMessages(), start, dir, anchor, req.Limit)
	}
	if e == nil && dir <= 0 {
		s.noteFirst(ctx, chat, msgs)
	}
	// Telegram's page lacks the messages kept deleted; the cache has them.
	for _, m := range cached {
		if m.Deleted {
			msgs = append(msgs, m)
		}
	}
	s.finishPageAt(epoch, chat, dir, msgs, len(modified.GetMessages()) >= req.Limit, e, start)
}

// noteFirst keeps the chat's first message as firsts says, when the
// oldest of the history and of msgs is it: its span runs from the chat's
// start (see pageSpan), and the cache has nothing before it there. A page
// says only whether there is more before itself, and the cache may have
// given the rest, as for a chat cached whole.
func (s *Store) noteFirst(ctx context.Context, chat int64, msgs []model.Message) {
	c := s.history
	c.mu.Lock()
	oldest, cache := 0, c.cache
	if h := c.histories[chat]; h != nil && len(h.Messages) > 0 {
		oldest = int(h.Messages[0].Key.MessageID)
	}
	c.mu.Unlock()
	for _, m := range msgs {
		if id := int(m.Key.MessageID); oldest == 0 || id < oldest {
			oldest = id
		}
	}
	if oldest == 0 || cache == nil {
		return
	}
	span, ok, _, e := cache.SpanOf(ctx, chat, oldest)
	if e != nil || !ok || span.Low > 1 {
		return
	}
	if older, e := cache.Page(ctx, chat, oldest, -1, 1); e != nil || len(older) > 0 {
		return
	}
	c.mu.Lock()
	c.firsts[chat] = oldest
	c.mu.Unlock()
}

// historyRequest asks for limit messages before anchor (dir < 0), after it
// (dir > 0), around it (0), or the newest (anchor 0): see pageSpan.
func historyRequest(peer tg.InputPeerClass, dir, anchor, limit int) *tg.MessagesGetHistoryRequest {
	req := &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: anchor, Limit: limit}
	if dir == 0 && anchor != 0 {
		req.AddOffset = -limit / 2
	}
	if dir > 0 {
		req.AddOffset = -limit
		req.OffsetID = anchor + 1
	}
	return req
}

// pageSpan is the run of IDs a page of messages.getHistory covers whole
// (see historycache.Span), as fetchAt asks for it: the limit messages
// before anchor for dir < 0, after it for dir > 0, and around it, or the
// newest, for 0. A page is one run of the chat's history, from its oldest
// message to its newest; it runs on to the chat's start when it has fewer
// messages before anchor than asked for, and reaches the end (end) when
// it has fewer after it. ok is false for a page that covers nothing.
func pageSpan(ids []int, dir, anchor, limit int) (low, high int, end, ok bool) {
	low, high = math.MaxInt, 0
	var before, at, after int // messages older than anchor, anchor, newer
	for _, id := range ids {
		low, high = min(low, id), max(high, id)
		switch {
		case anchor == 0 || id < anchor:
			before++
		case id == anchor:
			at++
		default:
			after++
		}
	}
	switch {
	case dir < 0:
		// The messages right before anchor, which touch it.
		high = anchor - 1
		if before < limit {
			low = 1
		}
	case dir > 0:
		// The messages right after anchor; with fewer than asked for,
		// Telegram moves the window down, and sends the newest.
		low = min(low, anchor+1)
		high = max(high, anchor)
		end = after < limit
	case anchor == 0:
		end = true
		if before < limit {
			low = 1
		}
	default:
		// limit/2 messages before anchor and as many from it on.
		end = at+after < limit/2
		if before < limit/2 {
			low = 1
		}
		high = max(high, anchor-1)
	}
	return low, high, end, low <= high
}

// keepSpan records the span a page read at epoch covers, and drops from a
// history started again (dir 0) what lies outside it: messages from an
// older part of the history, which the page does not reach.
func (s *Store) keepSpan(ctx context.Context, epoch, liveEpoch uint64, chat int64, raw []tg.MessageClass, start uint64, dir, anchor, limit int) error {
	ids := make([]int, 0, len(raw))
	for _, m := range raw {
		ids = append(ids, m.GetID())
	}
	low, high, end, ok := pageSpan(ids, dir, anchor, limit)
	c := s.history
	c.mu.Lock()
	if end {
		// Messages that came as updates while the page was asked for
		// follow it.
		for key, seq := range c.touched {
			if key.ChatID == chat && seq > start {
				high = max(high, int(key.MessageID))
				ok = ok || low <= high
			}
		}
	}
	cache := c.cache
	c.mu.Unlock()
	if !ok {
		if end {
			c.mu.Lock()
			c.startLive(chat, liveEpoch)
			c.mu.Unlock()
		}
		return nil
	}
	span, e := cache.AddSpan(ctx, chat, low, high)
	if e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if end {
		c.startLive(chat, liveEpoch)
	}
	if h := c.histories[chat]; dir == 0 && h != nil && c.epochs[chat] == epoch {
		out := h.Messages[:0]
		for _, m := range h.Messages {
			if id := int(m.Key.MessageID); id >= span.Low && id <= span.High {
				out = append(out, m)
			}
		}
		if len(out) != len(h.Messages) {
			h.Messages = out
			h.Revision++
		}
	}
	return nil
}
func (s *Store) finishPage(chat int64, dir int, msgs []model.Message, more bool, err error, generation ...uint64) {
	s.history.mu.Lock()
	epoch := s.history.epochs[chat]
	s.history.mu.Unlock()
	s.finishPageAt(epoch, chat, dir, msgs, more, err, generation...)
}

// finishPageAt is finishPage for a page asked for in epoch: one of an
// earlier epoch is of another part of the history, and is dropped.
func (s *Store) finishPageAt(epoch uint64, chat int64, dir int, msgs []model.Message, more bool, err error, generation ...uint64) {
	started := diagnostics.Start()
	c := s.history
	c.mu.Lock()
	if c.epochs[chat] != epoch {
		c.mu.Unlock()
		return
	}
	h := c.histories[chat]
	if h != nil {
		h.LoadingOlder = false
		h.LoadingNewer = false
		h.Err = err
		if err == nil {
			merged := map[model.MessageID]model.Message{}
			for _, m := range h.Messages {
				merged[m.Key.MessageID] = m
			}
			for _, m := range msgs {
				gone := c.deleted[m.Key] || m.Key.ChatID > -1000000000000 && c.globalDeleted[int(m.Key.MessageID)]
				// A message kept deleted stays, as its tombstone keeps
				// Telegram's version of it out.
				if (!gone || m.Deleted) && (len(generation) == 0 || c.touched[m.Key] <= generation[0]) {
					merged[m.Key.MessageID] = m
				}
			}
			h.Revision++
			h.Messages = h.Messages[:0]
			for _, m := range merged {
				h.Messages = append(h.Messages, m)
			}
			sort.Slice(h.Messages, func(i, j int) bool { return h.Messages[i].Key.MessageID < h.Messages[j].Key.MessageID })
			if dir <= 0 {
				// Nothing is older than the chat's first message,
				// whatever the page says.
				first := c.firsts[chat]
				h.HasOlder = more && (first == 0 || len(h.Messages) == 0 || first != int(h.Messages[0].Key.MessageID))
			}
			if dir >= 0 && len(h.Messages) > 0 {
				last := int(h.Messages[len(h.Messages)-1].Key.MessageID)
				h.HasNewer = last < c.top[chat]
				if c.top[chat] == 0 && dir > 0 {
					h.HasNewer = more
				}
			}
		}
	}
	if r := diagnostics.Current(); r != nil {
		scope := r.Scope(c.account)
		total := 0
		for _, history := range c.histories {
			total += len(history.Messages)
		}
		r.Gauge(scope, "history.messages", total, 0)
		r.Gauge(scope, "history.chats", len(c.histories), 0)
		r.Gauge(scope, "peer-registry", len(c.peers), 0)
		r.Gauge(scope, "media.refs", len(c.refs), 0)
	}
	c.mu.Unlock()
	s.profileHistory("history.page-merge", chat, dir, 0, len(msgs), started, err)
	s.changed()
}

// ingest serializes persistence with incoming updates. A response started before
// an edit cannot overwrite that edit, even when the network response arrives last.
func (s *Store) ingest(ctx context.Context, raw []tg.MessageClass, live bool, start uint64) ([]model.Message, error) {
	c := s.history
	c.apply.Lock()
	defer c.apply.Unlock()
	msgs, err := s.convert(ctx, raw, live, start)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache != nil {
		if e := cache.SaveMessages(ctx, msgs); e != nil {
			return nil, e
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Key.MessageID < msgs[j].Key.MessageID })
	return msgs, nil
}

// addMediaRefs keeps in refs where media, its sizes and its thumbnail
// download from, ref being where media itself does. It returns the
// thumbnail when its bytes came inline, to be kept as they are.
func addMediaRefs(refs map[string]fileLocation, media *model.MessageMedia, ref fileLocation) *model.MessageMedia {
	refs[media.ID] = ref
	for _, v := range media.Variants {
		vr := ref
		vr.Thumb = v.ID[strings.LastIndexByte(v.ID, '/')+1:]
		refs[v.ID] = vr
	}
	thumb := media.Thumbnail
	if thumb == nil {
		return nil
	}
	tr := ref
	tr.Thumb = thumb.ID[strings.LastIndexByte(thumb.ID, '/')+1:]
	if tr.Thumb == "inline" {
		return thumb
	}
	refs[thumb.ID] = tr
	return nil
}

// convert turns raw messages into the model's, and keeps where their media
// can be downloaded from. Messages deleted, or changed by an update newer
// than start, are left out. The caller holds c.apply.
func (s *Store) convert(ctx context.Context, raw []tg.MessageClass, live bool, start uint64) ([]model.Message, error) {
	c := s.history
	c.mu.Lock()
	account, cache := c.account, c.cache
	names := make(map[int64]string, len(c.peers))
	for id, p := range c.peers {
		names[id] = p.Name
	}
	c.mu.Unlock()
	var msgs []model.Message
	for _, r := range raw {
		if _, ok := r.(*tg.MessageEmpty); ok {
			continue
		}
		m, ref := convertMessage(account, r, names)
		refs := map[string]fileLocation{}
		var inlines []*model.MessageMedia
		if raw, ok := r.(*tg.Message); ok {
			if rich, ok := raw.GetRichMessage(); ok {
				inlines = richRefs(rich, refs)
			}
		}
		c.mu.Lock()
		if c.deleted[m.Key] || (m.Key.ChatID > -1000000000000 && c.globalDeleted[int(m.Key.MessageID)]) || (!live && c.touched[m.Key] > start) {
			c.mu.Unlock()
			continue
		}
		if live {
			c.generation++
			c.touched[m.Key] = c.generation
		}
		media := m.Media
		if media == nil && m.WebPage != nil {
			media = m.WebPage.Photo
			if m.WebPage.Video != nil {
				media = m.WebPage.Video
			}
		}
		if ref != nil && media != nil {
			if inline := addMediaRefs(refs, media, *ref); inline != nil {
				inlines = append(inlines, inline)
			}
		}
		for id, ref := range refs {
			c.refs[id] = ref
		}
		c.mu.Unlock()
		// SQLite/encryption may wait on disk or a media writer. Never keep the UI's
		// state mutex held across I/O; apply still serializes edits and tombstones.
		if cache != nil {
			for id, ref := range refs {
				if e := cache.Put(ctx, "ref/"+id, ref); e != nil {
					return nil, e
				}
			}
			for _, inline := range inlines {
				if e := cache.SaveMedia(ctx, inline.ID, inline.Preview); e != nil {
					return nil, e
				}
			}
		}
		m.ContentRevision = model.Revision(m)
		msgs = append(msgs, m)
	}
	return msgs, nil
}
func (s *Store) Handle(ctx context.Context, u tg.UpdatesClass) error {
	start := diagnostics.Start()
	if !start.IsZero() {
		defer func() { s.profileHistory("updates.apply", 0, 0, 0, 0, start, nil) }()
	}
	var all []tg.UpdateClass
	switch u := u.(type) {
	case *tg.Updates:
		s.rememberPeers(u.Users, u.Chats)
		all = u.Updates
	case *tg.UpdatesCombined:
		s.rememberPeers(u.Users, u.Chats)
		all = u.Updates
	case *tg.UpdateShort:
		all = []tg.UpdateClass{u.Update}
	default:
		return nil
	}
	for _, u := range all {
		var msg tg.MessageClass
		var ids []int
		var chat int64
		fresh := false
		switch u := u.(type) {
		case *tg.UpdateChannel:
			id := peerID(&tg.PeerChannel{ChannelID: u.ChannelID})
			go func() { _ = s.RefreshSendPermissions(s.history.ctx, id) }()
		case *tg.UpdateChat:
			id := peerID(&tg.PeerChat{ChatID: u.ChatID})
			go func() { _ = s.RefreshSendPermissions(s.history.ctx, id) }()
		case *tg.UpdateChatDefaultBannedRights:
			c := s.history
			c.mu.Lock()
			id := peerID(u.Peer)
			p := c.peers[id]
			p.Rights.Default = bannedKinds(u.DefaultBannedRights)
			c.peers[id] = p
			c.mu.Unlock()
		case *tg.UpdateNotifySettings:
			if p, ok := u.Peer.(*tg.NotifyPeer); ok {
				s.setMuted(peerID(p.Peer), u.NotifySettings)
			}
		case *tg.UpdateReadHistoryInbox:
			s.setUnread(peerID(u.Peer), u.StillUnreadCount)
		case *tg.UpdateReadChannelInbox:
			s.setUnread(peerID(&tg.PeerChannel{ChannelID: u.ChannelID}), u.StillUnreadCount)
		case *tg.UpdateNewMessage:
			msg, fresh = u.Message, true
		case *tg.UpdateNewChannelMessage:
			msg, fresh = u.Message, true
		case *tg.UpdateEditMessage:
			msg = u.Message
		case *tg.UpdateEditChannelMessage:
			msg = u.Message
		case *tg.UpdateDeleteMessages:
			ids = u.Messages
		case *tg.UpdateDeleteChannelMessages:
			ids = u.Messages
			chat = peerID(&tg.PeerChannel{ChannelID: u.ChannelID})
		case *tg.UpdateMessageReactions:
			s.applyReactions(peerID(u.Peer), u.MsgID, u.Reactions)
		case *tg.UpdatePeerBlocked:
			s.applyBlocked(u)
		case *tg.UpdateDialogPinned:
			s.applyPin(u)
		case *tg.UpdatePinnedDialogs:
			s.applyPinOrder(ctx, u)
		case *tg.UpdatePinnedMessages:
			s.applyPinned(ctx, peerID(u.Peer), u.Messages, u.Pinned)
		case *tg.UpdatePinnedChannelMessages:
			s.applyPinned(ctx, peerID(&tg.PeerChannel{ChannelID: u.ChannelID}), u.Messages, u.Pinned)
		case *tg.UpdateReadMessagesContents:
			s.contentsRead(0, u.Messages)
		case *tg.UpdateChannelReadMessagesContents:
			s.contentsRead(peerID(&tg.PeerChannel{ChannelID: u.ChannelID}), u.Messages)
		case *tg.UpdateChannelMessageViews:
			s.changeMessage(peerID(&tg.PeerChannel{ChannelID: u.ChannelID}), u.ID, func(m *model.Message) { m.Views = max(m.Views, u.Views) })
		case *tg.UpdateUserTyping:
			peer := &tg.PeerUser{UserID: u.UserID}
			s.applyDraftAction(peer, u.TopMsgID, peer, u.Action)
		case *tg.UpdateChatUserTyping:
			s.applyDraftAction(&tg.PeerChat{ChatID: u.ChatID}, 0, u.FromID, u.Action)
		case *tg.UpdateChannelUserTyping:
			s.applyDraftAction(&tg.PeerChannel{ChannelID: u.ChannelID}, u.TopMsgID, u.FromID, u.Action)
		}
		if msg != nil {
			if service, ok := msg.(*tg.MessageService); ok {
				switch service.Action.(type) {
				case *tg.MessageActionSetChatTheme, *tg.MessageActionSetChatWallPaper:
					s.invalidateChatTheme(peerID(service.PeerID))
				}
			}
			ms, e := s.ingest(ctx, []tg.MessageClass{msg}, true, 0)
			if e != nil {
				return e
			}
			if fresh {
				if e = s.extendLive(ctx, ms); e != nil {
					return e
				}
			}
			for _, m := range ms {
				if fresh {
					s.adoptDraft(m)
				}
				if chat, isNew := s.mergeUpdate(m); fresh && isNew {
					s.notice(m, chat, msg)
				}
			}
		}
		if len(ids) > 0 {
			tops, e := s.deleteFromUpdate(ctx, chat, ids)
			if e != nil {
				return e
			}
			s.replaceDeletedPreviews(ctx, tops)
		}
	}
	s.changed()
	return s.persistDialogs(ctx)
}

// mergeUpdate applies a new or edited message. It returns the chat it is
// in, and whether it is newer than every message the chat had; chat is
// zero for a channel the account is not in.
func (s *Store) mergeUpdate(m model.Message) (chat model.Chat, isNew bool) {
	c := s.history
	c.mu.Lock()
	isNew = int(m.Key.MessageID) > c.top[m.Key.ChatID]
	c.top[m.Key.ChatID] = max(c.top[m.Key.ChatID], int(m.Key.MessageID))
	if l := c.lookups[m.Key]; l != nil {
		// An edit of a message looked up, as the one a reply quotes.
		c.lookups[m.Key] = &lookup{msg: m, state: model.LookupFound}
	}
	if h := c.histories[m.Key.ChatID]; h != nil {
		h.Revision++
		found := false
		for i, old := range h.Messages {
			if old.Key == m.Key {
				h.Messages[i] = m
				found = true
				break
			}
		}
		// An edit of a message the history has not loaded is not added:
		// it is from another part of the history.
		newest := len(h.Messages) == 0 || m.Key.MessageID > h.Messages[len(h.Messages)-1].Key.MessageID
		if !found && !h.HasNewer && newest {
			h.Messages = append(h.Messages, m)
			sort.Slice(h.Messages, func(i, j int) bool { return h.Messages[i].Key.MessageID < h.Messages[j].Key.MessageID })
		}
	}
	c.mergeThreads(m, isNew)
	refreshTopics := c.noteTopic(m, isNew)
	p := c.peers[m.Key.ChatID]
	c.mu.Unlock()
	if refreshTopics {
		s.readTopics(m.Key.ChatID, false)
	}
	s.mu.Lock()
	chats := append([]model.Chat(nil), s.chats...)
	found := false
	for i := range chats {
		if chats[i].ID == m.Key.ChatID {
			chats[i] = p.withMetadata(chats[i])
			if isNew && !m.Outgoing {
				chats[i].Unread++
			}
			if !m.Date.Before(chats[i].LastTime) {
				setPreview(&chats[i], m)
			}
			chat = chats[i]
			found = true
			break
		}
	}
	// A channel the account is not in has no place among its chats; its
	// messages come from polling it while it is open.
	if !found && !p.Rights.Left {
		chat = p.withMetadata(model.Chat{ID: m.Key.ChatID})
		setPreview(&chat, m)
		chats = append(chats, chat)
	}
	model.SortChats(chats)
	s.chats = chats
	s.mu.Unlock()
	return chat, isNew
}

// deleteMessages removes messages from histories and the cache. It returns
// the chats whose last message was among them, with that message's ID.
// A zero chat means IDs shared by private chats and basic groups.
func (s *Store) deleteMessages(ctx context.Context, chat int64, ids []int) (map[int64]int, error) {
	c := s.history
	c.apply.Lock()
	defer c.apply.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	cache := c.cache
	c.mu.Unlock()
	var deleteErr error
	if cache != nil {
		deleteErr = cache.Delete(ctx, chat, ids)
	}
	c.mu.Lock()
	if deleteErr != nil {
		return nil, deleteErr
	}
	removed := map[int]bool{}
	for _, id := range ids {
		removed[id] = true
		if chat == 0 {
			c.globalDeleted[id] = true
		}
		if chat != 0 {
			c.deleted[model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.MessageID(id)}] = true
		}
	}
	for key, l := range c.lookups {
		if removed[int(key.MessageID)] && (key.ChatID == chat || chat == 0 && key.ChatID > -1000000000000) {
			l.state, l.msg = model.LookupGone, model.Message{}
		}
	}
	threads := c.threadsOf(chat)
	for id, h := range c.histories {
		if id != chat && (chat != 0 || id <= -1000000000000) && !slices.Contains(threads, id) {
			continue
		}
		out := h.Messages[:0]
		for _, m := range h.Messages {
			if removed[int(m.Key.MessageID)] {
				c.deleted[m.Key] = true
				// A thread counts one fewer; a deletion of a message it has
				// not loaded is not known to be in it.
				if h.Counted && id != chat {
					h.Count = max(0, h.Count-1)
				}
			} else {
				out = append(out, m)
			}
		}
		h.Messages = out
		h.Revision++
	}
	tops := map[int64]int{}
	for id, top := range c.top {
		if (id == chat || chat == 0 && id > -1000000000000) && removed[top] {
			tops[id] = top
		}
	}
	return tops, nil
}

// replaceDeletedPreviews shows the newest remaining message of each chat
// whose last message was deleted. A loaded history that reaches the end is
// exact; otherwise the cache gives a provisional preview and Telegram is
// asked for the actual last message. Deleted text never stays in the list.
func (s *Store) replaceDeletedPreviews(ctx context.Context, tops map[int64]int) {
	c := s.history
	for chat, top := range tops {
		c.mu.Lock()
		var last *model.Message
		if h := c.histories[chat]; h != nil && !h.HasNewer && len(h.Messages) > 0 {
			// A message kept deleted is not the chat's last one.
			for i := len(h.Messages) - 1; i >= 0 && last == nil; i-- {
				if m := h.Messages[i]; !m.Deleted {
					last = &m
				}
			}
		}
		cache, api, peer, start := c.cache, c.api, c.peers[chat], c.generation
		c.mu.Unlock()
		exact := last != nil
		if last == nil && cache != nil {
			if ms, e := cache.Page(ctx, chat, top, -1, 20); e == nil {
				for i := len(ms) - 1; i >= 0 && last == nil; i-- {
					if !ms[i].Deleted {
						last = &ms[i]
					}
				}
			}
		}
		top = s.replacePreview(chat, top, last)
		if exact || api == nil || peer.Kind == "" {
			continue
		}
		c.mu.Lock()
		if c.closing {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		c.wg.Go(func() {
			defer crash.Recover("dialog preview", nil)
			ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
			defer cancel()
			result, e := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer.input(), Limit: 1})
			if e != nil {
				return
			}
			modified, ok := result.AsModified()
			if !ok {
				return
			}
			s.rememberPeers(modified.GetUsers(), modified.GetChats())
			msgs, e := s.ingest(ctx, modified.GetMessages(), false, start)
			if e != nil {
				return
			}
			var last *model.Message
			if len(msgs) > 0 {
				last = &msgs[len(msgs)-1]
			}
			s.replacePreview(chat, top, last)
			s.changed()
			s.persistDialogs(ctx)
		})
	}
}

// replacePreview sets the chat's last message to m, or clears the preview
// when m is nil. It does nothing when a message newer than top has arrived
// in the meantime, and returns the chat's top message ID.
func (s *Store) replacePreview(chat int64, top int, m *model.Message) int {
	c := s.history
	c.mu.Lock()
	if c.top[chat] != top {
		top = c.top[chat]
		c.mu.Unlock()
		return top
	}
	if m != nil {
		top = int(m.Key.MessageID)
		c.top[chat] = top
	}
	c.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID != chat {
			continue
		}
		if m != nil {
			setPreview(&chats[i], *m)
		} else {
			// The time keeps the chat's place in the list.
			chats[i].LastMessage, chats[i].LastSender = "", ""
		}
	}
	s.chats = chats
	return top
}

// setPreview shows m as the chat's last message, the way dialogs loaded
// from Telegram show it.
func setPreview(chat *model.Chat, m model.Message) {
	text := strings.Join(strings.Fields(m.Text), " ")
	if m.Kind == model.MessageService && m.Service != nil {
		text = serviceSummary(m, *chat)
	}
	if kind := messageKindName(m); kind != "" {
		if text == "" {
			text = kind
		} else if m.Kind != model.MessageService {
			text = kind + ", " + text
		}
	}
	chat.LastMessage, chat.LastTime, chat.LastSender = text, m.Date, ""
	if chat.Kind == model.KindGroup && m.Kind != model.MessageService {
		chat.LastSender = m.SenderName
		if m.Outgoing {
			chat.LastSender = "Вы"
		}
	}
}

func messageKindName(m model.Message) string {
	if m.Rich != nil && strings.TrimSpace(m.Text) == "" {
		return richFallbackName(*m.Rich)
	}
	switch m.Kind {
	case model.MessagePhoto:
		return "Фото"
	case model.MessageVideo:
		return "Видео"
	case model.MessageFile:
		return "Файл"
	case model.MessageVoice:
		return "Голосовое сообщение"
	case model.MessageSticker:
		return "Стикер"
	case model.MessageGIF:
		return "GIF"
	case model.MessageService:
		return "Служебное сообщение"
	}
	return ""
}

// richFallbackName names what a rich message without text shows, as
// Telegram Desktop's summary does.
func richFallbackName(p model.RichPage) string {
	switch p.Fallback() {
	case "photo":
		return "Фото"
	case "video":
		return "Видео"
	case "album":
		return "Альбом"
	case "audio":
		return "Аудиофайл"
	case "file":
		return "Файл"
	case "map":
		return "Геопозиция"
	case "table":
		return "Таблица"
	}
	return ""
}

func (s *Store) setUnread(id int64, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == id {
			chats[i].Unread = max(n, 0)
		}
	}
	s.chats = chats
}

func (s *Store) reconcile(ctx context.Context, chat int64, raw []tg.MessageClass, start uint64, dir, anchor, limit int) error {
	c := s.history
	c.apply.Lock()
	defer c.apply.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	low, high := int(^uint(0)>>1), 0
	var keep []int
	for _, m := range raw {
		id := m.GetID()
		keep = append(keep, id)
		low = min(low, id)
		high = max(high, id)
	}
	if len(raw) == 0 {
		if dir < 0 {
			low, high = 0, anchor-1
		} else if dir > 0 {
			low, high = anchor+1, int(^uint(0)>>1)
		} else if anchor == 0 {
			low, high = 0, int(^uint(0)>>1)
		} else {
			return nil
		}
	}
	if dir < 0 {
		high = anchor - 1
		if len(raw) < limit {
			low = 0
		}
	}
	if dir > 0 {
		low = anchor + 1
		if len(raw) < limit {
			high = int(^uint(0) >> 1)
		}
	}
	if dir == 0 && anchor == 0 {
		high = int(^uint(0) >> 1)
		if len(raw) < limit {
			low = 0
		}
	}
	for key, seq := range c.touched {
		if key.ChatID == chat && seq > start {
			keep = append(keep, int(key.MessageID))
		}
	}
	cache := c.cache
	c.mu.Unlock()
	ids, kept, e := cache.Reconcile(ctx, chat, low, high, keep)
	c.mu.Lock()
	if e != nil {
		return e
	}
	s.showKept(kept)
	removed := map[int]bool{}
	for _, id := range ids {
		removed[id] = true
		c.deleted[model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.MessageID(id)}] = true
	}
	if h := c.histories[chat]; h != nil {
		out := h.Messages[:0]
		for _, m := range h.Messages {
			if !removed[int(m.Key.MessageID)] {
				out = append(out, m)
			}
		}
		h.Messages = out
		h.Revision++
	}
	return nil
}

// Disconnect leaves the local cache available while the window remains open.
func (s *Store) Disconnect() {
	c := s.history
	c.mu.Lock()
	pool := c.pool
	c.pool = nil
	c.api = nil
	c.liveEpoch++
	clear(c.live)
	c.mu.Unlock()
	if pool != nil {
		pool.Close()
	}
	s.changed()
}

// HistorySince avoids copying the entire loaded history on animation frames.
// Status flags are always returned; message snapshots change only on mutation.
func (s *Store) HistorySince(chat int64, revision uint64) (model.History, bool) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.histories[chat]
	if h == nil {
		return model.History{LoadingOlder: true}, false
	}
	out := *h
	changed := revision == 0 || revision != h.Revision
	if changed {
		out.Messages = append(append([]model.Message(nil), h.Messages...), c.draftMessages(chat)...)
	} else {
		out.Messages = nil
	}
	return out, changed
}

func (s *Store) profileHistory(name string, chat int64, dir, anchor, count int, start time.Time, err error) {
	if r := diagnostics.Current(); r != nil {
		duration := time.Duration(0)
		if !start.IsZero() {
			duration = time.Since(start)
		}
		r.Event(r.Scope(s.history.account), name, fmt.Sprintf("chat:%d dir:%d anchor:%d %s", chat, dir, anchor, diagnostics.ErrorClass(err)), duration, count)
	}
}
