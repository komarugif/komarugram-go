// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"log"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// The pages of history the cache has whole, and can vouch for, are not
// asked of Telegram again: most pages scrolled through were the cache's
// already, and each was read again only to be compared with it. The cache
// can vouch for what was read from Telegram (or came as live updates) and
// changed since only by updates that all came: a verified range
// (historycache) of the chat's current chain.
//
// A chain lasts as long as no update of the chat can have been lost:
//   - the common box (private chats, basic groups, Saved Messages) is asked
//     for its difference from the state kept, after a reconnection or a
//     restart, so its one chain, kept in the cache, outlasts both; it ends
//     when the difference is too long (OnTooLong), or there was no state to
//     ask it from;
//   - a channel's updates skip what came before its pts was raised to its
//     dialog's (keepChannelPts), as after every start and reconnection, so
//     its chain ends there, and on OnChannelTooLong and OnTooLong; it is not
//     kept across a restart;
//   - a channel the account is not in gets no updates: it has no chain.
//
// What updates never bring to old messages (views, most reactions, reply
// counts) stays as it was read while its range is served.

// channelChat is whether chat is a channel's or a supergroup's.
func channelChat(chat int64) bool { return chat <= -1000000000000 }

// chainOf is chat's current chain, and false for a chat whose cache cannot
// be vouched for at all. The caller holds c.mu.
func (c *conversation) chainOf(chat int64) (int64, bool) {
	if !channelChat(chat) {
		return c.commonChain, c.commonChain != 0
	}
	if p := c.peers[chat]; p.Kind == "" || p.Rights.Left {
		return 0, false
	}
	if v, ok := c.channelChains[chat]; ok {
		return v, true
	}
	return c.channelChain, true
}

// endChain ends the chain of chat, of every chat for 0, after updates of
// it may have been lost. The caller holds c.mu; the common box's new chain
// is kept by keepCommonChain, after the lock.
func (c *conversation) endChain(chat int64) {
	c.chainSeq++
	if chat == 0 {
		if c.commonChain != 0 {
			c.commonChain++
		}
		c.channelChain = c.chainSeq
		clear(c.channelChains)
	} else if channelChat(chat) {
		if c.channelChains == nil {
			c.channelChains = map[int64]int64{}
		}
		c.channelChains[chat] = c.chainSeq
	}
}

// keepCommonChain keeps the common box's chain in the cache.
func (s *Store) keepCommonChain() {
	c := s.history
	c.mu.Lock()
	cache, chain := c.cache, c.commonChain
	c.mu.Unlock()
	if cache == nil || chain == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cache.Put(ctx, historycache.VerifiedChainKey, chain); err != nil {
		log.Printf("tgstore: verified chain not kept: %v", err)
	}
}

// loadChains reads the common box's chain kept, or starts one, and, once
// in a run, starts the channels' chains past those of every run before:
// the runs are counted in the cache, as a clock may go back. The caller
// holds c.mu.
func (c *conversation) loadChains(ctx context.Context) error {
	var chain int64
	found, err := c.cache.Get(ctx, historycache.VerifiedChainKey, &chain)
	if err != nil {
		return err
	}
	if !found || chain <= 0 {
		chain = 1
	}
	c.commonChain = max(c.commonChain, chain)
	if c.chainSeq != 0 {
		return nil // configured again in this run, as on a reconnection
	}
	var run int64
	if _, err = c.cache.Get(ctx, historycache.VerifiedRunKey, &run); err != nil {
		return err
	}
	run++
	if err = c.cache.Put(ctx, historycache.VerifiedRunKey, run); err != nil {
		return err
	}
	// A run ends a chain at most once per update gap: 1<<32 of them
	// before the next run's chains begin.
	c.chainSeq = run << 32
	c.channelChain = c.chainSeq
	return nil
}

// endChainsWithoutState ends the common box's chain when the updates
// manager has no state to ask its difference from: it starts from
// Telegram's state of now, and what changed before is never told.
func (s *Store) endChainsWithoutState(self int64) {
	cache := s.Cache()
	if cache == nil || self == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, found, err := cache.GetState(ctx, self); err != nil || found {
		return
	}
	c := s.history
	c.mu.Lock()
	c.endChain(0)
	historylog.Printf("chains of every chat ended: no updates state kept; common chain %d", c.commonChain)
	c.mu.Unlock()
	s.keepCommonChain()
}

// endChannelChains ends the chains of the channels, by channel id, whose
// updates were skipped.
func (s *Store) endChannelChains(channels []int64) {
	if len(channels) == 0 {
		return
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range channels {
		c.endChain(peerID(&tg.PeerChannel{ChannelID: ch}))
	}
	historylog.Printf("chains of %d channels ended: their pts raised to their dialogs'", len(channels))
}

// verify records low..high of chat as verified at chain, when chain is
// still the chat's: a chain that ended while the page was asked for tells
// nothing of it.
func (s *Store) verify(ctx context.Context, cache *historycache.Cache, chat int64, low, high int, chain int64) error {
	c := s.history
	c.mu.Lock()
	now, ok := c.chainOf(chat)
	c.mu.Unlock()
	if !ok || now != chain || low > high {
		return nil
	}
	historylog.Printf("verified chat=%d %d..%d chain %d", chat, low, high, chain)
	return cache.AddVerified(ctx, chat, low, high, chain)
}

// verifiedPage tells whether cached, the cache's page of chat next to
// anchor in dir, is whole and verified at chain, to be shown without
// Telegram: it has as many messages as a page, or reaches the chat's first
// message (dir < 0) or its newest (dir > 0), and every ID from it to anchor
// lies in a range verified at chain. A page with fewer messages before
// anchor reaches the chat's start when the range runs down to 1, as one
// read up to the start does (pageSpan): the first message is known only
// once such a page was read in the run (noteFirst), and the page at the
// start was asked of Telegram again after each restart.
func (s *Store) verifiedPage(ctx context.Context, chat int64, cached []model.Message, dir, anchor, limit int, chain int64) bool {
	if len(cached) == 0 || dir == 0 {
		return false
	}
	low, high, whole := s.wholePage(chat, cached, dir, anchor, limit)
	if !whole && dir < 0 {
		low, high, whole = 1, anchor-1, true
	}
	cache := s.Cache()
	if !whole || low > high || cache == nil {
		return false
	}
	ok, err := cache.Verified(ctx, chat, low, high, chain)
	return err == nil && ok
}

// wholePage tells whether cached, the cache's page of chat next to anchor
// in dir, is whole: it has as many messages as a page, or reaches the
// chat's first message known (dir < 0) or its newest (dir > 0); and the
// IDs from it to anchor that it covers.
func (s *Store) wholePage(chat int64, cached []model.Message, dir, anchor, limit int) (low, high int, whole bool) {
	if len(cached) == 0 || dir == 0 {
		return 0, 0, false
	}
	c := s.history
	c.mu.Lock()
	first, top := c.firsts[chat], c.top[chat]
	c.mu.Unlock()
	oldest, newest := int(cached[0].Key.MessageID), int(cached[len(cached)-1].Key.MessageID)
	if dir > 0 {
		return anchor + 1, newest, newest >= anchor+1 && (len(cached) >= limit || top != 0 && newest >= top)
	}
	return oldest, anchor - 1, oldest <= anchor-1 && (len(cached) >= limit || first != 0 && oldest == first)
}
