// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"slices"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/dcpool"
)

// Local Premium can keep its lists in a private channel of the user's, which
// the account's devices share. Each saved GIF has a message there, whose id
// outlives its place in Telegram's list and renews its file reference; a file,
// the channel's pinned message, tells the order of the GIFs Telegram pushed out
// and the list of pinned chats. Telegram's clients see an ordinary channel,
// archived.
//
// Sending a GIF moves it to the top of Telegram's saved GIFs, and at the limit
// pushes the last one out; forwarding it as it is does neither. So a GIF goes to
// the channel forwarded from where it was met, or sent when it is at the top
// anyway, or all together with SendAllGIFs, whose order comes back at its end.

const (
	storageStateKey = "local-premium/storage"
	// storageIndexName is the file of the pinned message.
	storageIndexName = "komarugram-storage.json"
	// storageIndexMost is the most of the file that is read.
	storageIndexMost = 16 << 20
	storageVersion   = 1
)

// storagePace is the pause between messages to the channel: a guess, Telegram
// names no limit. storageDebounce gathers changes before the file is replaced.
var (
	storagePace     = 500 * time.Millisecond
	storageDebounce = 3 * time.Second
)

// storageState is what storageStateKey keeps, guarded by collections' mu.
type storageState struct {
	ChannelID, Hash int64
	Title           string
	// Index is the message of the file, IndexDoc its document last read or
	// written here: another is what changed elsewhere.
	Index    int
	IndexDoc int64
	// Archived is the channel's message of each GIF, by media id; Scanned, the
	// last of its messages looked at for them.
	Archived map[string]int
	Scanned  int
	// Origins is where each saved GIF not in the channel was met, which also
	// works without a channel to renew its reference.
	Origins map[string]model.MessageKey
	// GIFBase, PinBase and SyncedBase are the lists of the file last read or
	// written. A part switched off here writes them back as they were.
	GIFBase, PinBase, SyncedBase []int64
	// GIFsDirty and PinsDirty: this device changed what the file does not tell.
	GIFsDirty, PinsDirty bool
	// Delete are the channel's messages of GIFs the user removed.
	Delete []int
	// Pass is the sending of all GIFs under way.
	Pass *storagePassState
}

// storagePassState goes through Order, Telegram's list when the pass began,
// from its end; Done of them are sent.
type storagePassState struct {
	Order []int64
	Done  int
}

// storageIndex is the file.
type storageIndex struct {
	Version int
	// GIFs are the messages of the GIFs Telegram pushed out, first shown first.
	GIFs []int64
	// Pins is the list of pinned chats, Synced what Telegram had when written.
	Pins, Synced []int64
}

// storageWork is the worker's state, guarded by collections' mu.
type storageWork struct {
	running, wake bool
	attempts      int
	// indexStale and scanStale ask to read the file and the new messages.
	indexStale, scanStale bool
	// looked: this run of the client has looked for a channel by its name.
	looked bool
	// newerIndex: the file comes from a newer client, and is left alone.
	newerIndex bool
	publishAt  time.Time
	pass       model.StoragePass
	// missing: Sync is on, and there is no channel; StorageMissing tells it.
	missing bool
	// remembered is the channel the settings keep, and saveBinding keeps it
	// there: what the cache says is lost with it.
	remembered  model.StorageBinding
	saveBinding func(model.StorageBinding)
	// file downloads a document; tests replace it.
	file func(context.Context, *tg.Document) ([]byte, error)
}

func (st *storageState) bound() bool { return st.ChannelID != 0 }

func (st *storageState) input() *tg.InputChannel {
	return &tg.InputChannel{ChannelID: st.ChannelID, AccessHash: st.Hash}
}

func (st *storageState) peer() *tg.InputPeerChannel {
	return &tg.InputPeerChannel{ChannelID: st.ChannelID, AccessHash: st.Hash}
}

func (st *storageState) key() int64 { return peerID(&tg.PeerChannel{ChannelID: st.ChannelID}) }

// restoreStorage reads what a former run kept. One that cannot be read costs
// the binding, which is found again by the channel's name.
func (s *Store) restoreStorage(ctx context.Context, l *localCollections) {
	if _, err := s.Cache().Get(ctx, storageStateKey, &l.storage); err != nil {
		log.Printf("tgstore: storage channel: %v", err)
		l.storage = storageState{}
	}
	if l.storage.Archived == nil {
		l.storage.Archived = map[string]int{}
	}
	if l.storage.Origins == nil {
		l.storage.Origins = map[string]model.MessageKey{}
	}
	l.work.indexStale, l.work.scanStale = true, true
	if pass := l.storage.Pass; pass != nil {
		l.work.pass = model.StoragePass{Active: true, Done: pass.Done, Total: len(pass.Order)}
	}
}

// StorageMissing implements model.StorageChannels.
func (s *Store) StorageMissing() bool {
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	missing := l.work.missing
	l.work.missing = false
	return missing && !l.storage.bound() && s.local().Sync
}

// storageMissing has whoever shows the settings told there is no channel: what
// Telegram pushes out would otherwise be thought safe, and go with the cache.
func (s *Store) storageMissing() {
	l := &s.collections
	l.mu.Lock()
	l.work.missing = true
	l.mu.Unlock()
	s.changed()
}

// RememberStorage implements model.StorageChannels. A channel the cache has and
// the settings do not goes to the settings.
func (s *Store) RememberStorage(kept model.StorageBinding, save func(model.StorageBinding)) {
	l := &s.collections
	l.mu.Lock()
	l.work.remembered, l.work.saveBinding = kept, save
	st := l.storage
	l.mu.Unlock()
	if st.bound() {
		if now := (model.StorageBinding{ChannelID: st.ChannelID, Hash: st.Hash, Title: st.Title}); now != kept {
			s.keepBinding(now)
		}
		return
	}
	s.wakeStorage()
}

// keepBinding has the settings keep b.
func (s *Store) keepBinding(b model.StorageBinding) {
	l := &s.collections
	l.mu.Lock()
	l.work.remembered = b
	save := l.work.saveBinding
	l.mu.Unlock()
	if save != nil {
		save(b)
	}
}

// saveStorage keeps the state as it is now; saveMu keeps an older one from being
// written last.
func (s *Store) saveStorage(ctx context.Context) error {
	cache := s.Cache()
	if cache == nil {
		return nil
	}
	l := &s.collections
	l.saveMu.Lock()
	defer l.saveMu.Unlock()
	l.mu.Lock()
	b, err := json.Marshal(l.storage)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	return cache.Put(ctx, storageStateKey, json.RawMessage(b))
}

// wakeStorage has the worker look at what there is to do. One runs at a time,
// and goes again if woken meanwhile.
func (s *Store) wakeStorage() {
	if !s.local().Sync {
		return
	}
	l := &s.collections
	l.mu.Lock()
	// Before the dialogs are read there is nothing to do; dialogsRead wakes it.
	if !l.loaded || !l.ready {
		l.mu.Unlock()
		return
	}
	l.work.wake = true
	start := !l.work.running
	l.work.running = true
	l.mu.Unlock()
	if !start {
		return
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	stopped := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.work.running = false
	}
	if c.closing {
		stopped()
		return
	}
	ctx := c.ctx
	c.wg.Go(func() {
		defer crash.Recover("storage channel", func(*crash.Panic) { stopped() })
		s.runStorage(ctx)
	})
}

func (s *Store) runStorage(ctx context.Context) {
	l := &s.collections
	for {
		l.mu.Lock()
		if !l.work.wake || ctx.Err() != nil {
			l.work.running = false
			l.mu.Unlock()
			return
		}
		l.work.wake = false
		l.mu.Unlock()
		err := s.storageStep(ctx)
		l.mu.Lock()
		if err == nil {
			l.work.attempts = 0
		} else {
			l.work.attempts++
		}
		attempts := l.work.attempts
		l.mu.Unlock()
		if err != nil {
			log.Printf("tgstore: storage channel: %v", err)
			if storageGone(err) {
				s.unbindStorage(ctx)
			} else if delay, retry := outgoingRetry(err, attempts); retry {
				time.AfterFunc(delay, s.wakeStorage)
			} else {
				// Asked no more: looking for the channel has failed for good.
				l.mu.Lock()
				bound := l.storage.bound()
				l.mu.Unlock()
				if !bound {
					s.storageMissing()
				}
			}
		}
	}
}

// storageGone tells that the channel can no longer be used.
func storageGone(err error) bool {
	return tgerr.Is(err, "CHANNEL_INVALID", "CHANNEL_PRIVATE", "CHAT_WRITE_FORBIDDEN", "CHAT_ADMIN_REQUIRED")
}

// unbindStorage forgets the channel. The GIFs kept stay here.
func (s *Store) unbindStorage(ctx context.Context) {
	l := &s.collections
	l.mu.Lock()
	origins := l.storage.Origins
	wasPass := l.storage.Pass != nil
	l.storage = storageState{Archived: map[string]int{}, Origins: origins}
	if wasPass {
		l.work.pass = model.StoragePass{Failed: true}
	}
	l.mu.Unlock()
	s.keepBinding(model.StorageBinding{})
	_ = s.saveStorage(ctx)
	s.storageMissing()
}

// storageStep does what there is to do: the pass alone while it runs, as it
// holds Telegram's list in its order.
func (s *Store) storageStep(ctx context.Context) error {
	l := &s.collections
	c := s.history
	c.mu.Lock()
	api := c.api
	c.mu.Unlock()
	if api == nil || !s.local().Sync {
		return nil
	}
	l.mu.Lock()
	ready, bound, looked := l.ready, l.storage.bound(), l.work.looked
	remembered := l.work.remembered
	pass := l.storage.Pass != nil
	l.work.looked = l.work.looked || ready
	l.mu.Unlock()
	if !ready {
		return nil
	}
	switch {
	case bound:
	case remembered.ChannelID != 0:
		// The cache was lost; the settings tell the channel, of whatever name.
		log.Printf("tgstore: storage channel: %d, from the settings", remembered.ChannelID)
		ch := &tg.Channel{ID: remembered.ChannelID, AccessHash: remembered.Hash, Title: remembered.Title}
		if err := s.bindStorage(ctx, api, ch); err != nil {
			return err
		}
	case looked:
		return nil
	default:
		found, err := s.lookForStorage(ctx, api)
		if err != nil {
			// Asked again when it is retried.
			l.mu.Lock()
			l.work.looked = false
			l.mu.Unlock()
			return err
		}
		if found == nil {
			log.Printf("tgstore: storage channel: none named %q", model.StorageChannelTitle)
			s.storageMissing()
			return nil
		}
		if err := s.bindStorage(ctx, api, found); err != nil {
			return err
		}
	}
	if pass {
		if err := s.runStoragePass(ctx); err != nil {
			return err
		}
	}
	if err := s.scanStorage(ctx, api); err != nil {
		return err
	}
	if err := s.readStorageIndex(ctx, api); err != nil {
		return err
	}
	if err := s.deleteStorageMessages(ctx, api); err != nil {
		return err
	}
	if err := s.archiveGIFs(ctx, api); err != nil {
		return err
	}
	return s.publishStorage(ctx, api)
}

// lookForStorage is the user's channel of the name, the oldest of them: among the
// peers known, which have the list of chats, and in the archive, where it goes and
// whose dialogs are not read otherwise. contacts.search does not find it there.
func (s *Store) lookForStorage(ctx context.Context, api *tg.Client) (*tg.Channel, error) {
	found := s.knownStorage()
	if found != nil {
		return found, nil
	}
	err := s.readArchive(ctx, api, func() bool {
		found = s.knownStorage()
		return found != nil
	})
	return found, err
}

// knownStorage is the user's channel of the name among the peers known.
func (s *Store) knownStorage() *tg.Channel {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	var best *tg.Channel
	for _, p := range c.peers {
		if p.Kind != "channel" || !p.Rights.Creator || !p.Rights.Broadcast || p.Rights.Left || p.Name != model.StorageChannelTitle || p.Username != "" {
			continue
		}
		if best == nil || p.ID < best.ID {
			best = &tg.Channel{ID: p.ID, AccessHash: p.Hash, Title: p.Name}
		}
	}
	return best
}

// storageArchivePages is how many pages of the archive are read at most.
const storageArchivePages = 50

// readArchive has the peers of the archive known, page by page, until enough.
func (s *Store) readArchive(ctx context.Context, api *tg.Client, enough func() bool) error {
	l := newList(0)
	offset := page{peer: &tg.InputPeerEmpty{}}
	for range storageArchivePages {
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{FolderID: 1, OffsetDate: offset.date, OffsetID: offset.id, OffsetPeer: offset.peer, Limit: pageSize})
		if err != nil {
			return err
		}
		dialogs, ok := res.AsModified()
		if !ok {
			return nil
		}
		s.rememberPeers(dialogs.GetUsers(), dialogs.GetChats())
		if enough != nil && enough() {
			return nil
		}
		added := l.add(dialogs)
		slice, ok := dialogs.(*tg.MessagesDialogsSlice)
		if !ok || added == 0 || len(l.chats) >= slice.Count {
			return nil
		}
		if offset, ok = l.nextPage(dialogs); !ok {
			return nil
		}
		if err := sleepStorage(ctx, pagePause); err != nil {
			return err
		}
	}
	return nil
}

// bindStorage makes ch the channel, and keeps it out of the way in the archive.
func (s *Store) bindStorage(ctx context.Context, api *tg.Client, ch *tg.Channel) error {
	s.tidyStorageChannel(ctx, api, &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash})
	l := &s.collections
	l.mu.Lock()
	origins := l.storage.Origins
	l.storage = storageState{ChannelID: ch.ID, Hash: ch.AccessHash, Title: ch.Title, Archived: map[string]int{}, Origins: origins, GIFsDirty: true, PinsDirty: true}
	l.work.indexStale, l.work.scanStale = true, true
	l.mu.Unlock()
	s.keepBinding(model.StorageBinding{ChannelID: ch.ID, Hash: ch.AccessHash, Title: ch.Title})
	return s.saveStorage(ctx)
}

// tidyStorageChannel archives and mutes the channel: muted, a new message does
// not take it out of the archive. A failure leaves it where it is, and works.
func (s *Store) tidyStorageChannel(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel) {
	res, err := api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{Peer: peer, FolderID: 1}})
	if err == nil {
		_ = s.Handle(ctx, res)
	} else {
		log.Printf("tgstore: storage channel: archive: %v", err)
	}
	settings := tg.InputPeerNotifySettings{}
	settings.SetMuteUntil(math.MaxInt32)
	if _, err := api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{Peer: &tg.InputNotifyPeer{Peer: peer}, Settings: settings}); err != nil {
		log.Printf("tgstore: storage channel: mute: %v", err)
	}
}

// FindStorageChannel implements model.StorageChannels.
func (s *Store) FindStorageChannel(ctx context.Context) (model.StorageFound, error) {
	api, err := s.storageAPI(ctx)
	if err != nil {
		return model.StorageFound{}, err
	}
	l := &s.collections
	l.mu.Lock()
	bound := l.storage.bound()
	remembered := l.work.remembered
	l.work.looked = true
	l.mu.Unlock()
	if !bound {
		var found *tg.Channel
		if remembered.ChannelID != 0 {
			found = &tg.Channel{ID: remembered.ChannelID, AccessHash: remembered.Hash, Title: remembered.Title}
		} else if found, err = s.lookForStorage(ctx, api); err != nil {
			return model.StorageFound{}, err
		}
		if found == nil {
			candidates, err := s.storageCandidates(ctx, api)
			return model.StorageFound{Candidates: candidates}, err
		}
		if err := s.bindStorage(ctx, api, found); err != nil {
			return model.StorageFound{}, err
		}
	}
	return s.storageFound(ctx, api)
}

// BindStorageChannel implements model.StorageChannels.
func (s *Store) BindStorageChannel(ctx context.Context, id int64) (model.StorageFound, error) {
	api, err := s.storageAPI(ctx)
	if err != nil {
		return model.StorageFound{}, err
	}
	var ch *tg.Channel
	if id == 0 {
		res, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{Broadcast: true, Title: model.StorageChannelTitle,
			About: "Saved GIFs and pinned chats of KomaruGram. Please do not delete its messages."})
		if err != nil {
			return model.StorageFound{}, err
		}
		_ = s.Handle(ctx, res)
		if told, ok := res.(interface{ GetChats() []tg.ChatClass }); ok {
			for _, raw := range told.GetChats() {
				if c, ok := raw.(*tg.Channel); ok {
					ch = c
					break
				}
			}
		}
		if ch == nil {
			return model.StorageFound{}, errors.New("the new channel was not told")
		}
	} else {
		c := s.history
		c.mu.Lock()
		p := c.peers[id]
		c.mu.Unlock()
		if p.Kind != "channel" || !p.Rights.Creator || !p.Rights.Broadcast {
			return model.StorageFound{}, errors.New("not a channel of the user's")
		}
		ch = &tg.Channel{ID: p.ID, AccessHash: p.Hash, Title: p.Name}
	}
	if err := s.bindStorage(ctx, api, ch); err != nil {
		return model.StorageFound{}, err
	}
	return s.storageFound(ctx, api)
}

func (s *Store) storageAPI(ctx context.Context) (*tg.Client, error) {
	if err := s.restoreLocalCollections(ctx); err != nil {
		return nil, err
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		return nil, errNotConnected
	}
	return c.api, nil
}

// storageCandidates are the private channels of the user's: those known, and
// those of the archive, whose dialogs are not read otherwise.
func (s *Store) storageCandidates(ctx context.Context, api *tg.Client) ([]model.StorageChannel, error) {
	if err := s.readArchive(ctx, api, nil); err != nil {
		return nil, err
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []model.StorageChannel
	for id, p := range c.peers {
		if p.Kind == "channel" && p.Rights.Creator && p.Rights.Broadcast && !p.Rights.Left && p.Username == "" {
			out = append(out, model.StorageChannel{ID: id, Title: p.Name})
		}
	}
	slices.SortFunc(out, func(a, b model.StorageChannel) int {
		return cmp.Or(cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// storageFound reads the channel, and tells whether all GIFs can go there: only
// to one without any, as the pass is to be done once.
func (s *Store) storageFound(ctx context.Context, api *tg.Client) (model.StorageFound, error) {
	if err := s.scanStorage(ctx, api); err != nil {
		return model.StorageFound{}, err
	}
	l := &s.collections
	l.mu.Lock()
	found := model.StorageFound{Bound: true, Title: l.storage.Title}
	empty := len(l.storage.Archived) == 0 && l.storage.Pass == nil && s.local().GIFs
	l.mu.Unlock()
	s.wakeStorage()
	if !empty {
		return found, nil
	}
	docs, err := s.savedGIFDocuments(ctx, api)
	if err != nil {
		return found, err
	}
	found.SendAll = len(docs)
	return found, nil
}

func (s *Store) savedGIFDocuments(ctx context.Context, api *tg.Client) ([]*tg.Document, error) {
	res, err := api.MessagesGetSavedGifs(ctx, 0)
	if err != nil {
		return nil, err
	}
	gifs, ok := res.(*tg.MessagesSavedGifs)
	if !ok {
		return nil, errors.New("no saved GIFs told")
	}
	var out []*tg.Document
	for _, raw := range gifs.Gifs {
		if d, ok := raw.(*tg.Document); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// SendAllGIFs implements model.StorageChannels.
func (s *Store) SendAllGIFs(ctx context.Context) error {
	api, err := s.storageAPI(ctx)
	if err != nil {
		return err
	}
	docs, err := s.savedGIFDocuments(ctx, api)
	if err != nil {
		return err
	}
	order := make([]int64, len(docs))
	for i, d := range docs {
		order[i] = d.ID
	}
	l := &s.collections
	l.mu.Lock()
	if !l.storage.bound() || l.storage.Pass != nil {
		l.mu.Unlock()
		return nil
	}
	l.storage.Pass = &storagePassState{Order: order}
	l.work.pass = model.StoragePass{Active: true, Total: len(order)}
	l.mu.Unlock()
	if err := s.saveStorage(ctx); err != nil {
		return err
	}
	s.changed()
	s.wakeStorage()
	return nil
}

// StoragePassState implements model.StorageChannels.
func (s *Store) StoragePassState() model.StoragePass {
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	pass := l.work.pass
	// Nothing runs it now: no dialog waits for it.
	pass.Active = pass.Active && s.local().Sync
	l.work.pass.Failed = false
	return pass
}

// storageRandomID is the same for a GIF in the channel, so that a pass that
// starts again after a stop sends it once.
func storageRandomID(channel, doc int64) int64 {
	h := fnv.New64a()
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:], uint64(channel))
	binary.LittleEndian.PutUint64(b[8:], uint64(doc))
	_, _ = h.Write(b[:])
	return int64(h.Sum64())
}

// sleepStorage waits for d, or returns the context's error.
func sleepStorage(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runStoragePass sends the GIFs from the last to the first. Each goes to the
// top of Telegram's list, so that the list ends as it began, and a GIF pushed
// out meanwhile by another device, the last ones, is in the channel already. It
// waits out a lost connection and flood waits, and a GIF Telegram refuses is
// passed over: the dialog over it cannot be closed.
func (s *Store) runStoragePass(ctx context.Context) error {
	l := &s.collections
	docs := map[int64]*tg.Document{}
	attempts := 0
	refresh := true
	for {
		c := s.history
		c.mu.Lock()
		api := c.api
		c.mu.Unlock()
		retry := func(err error) (bool, error) {
			delay, again := outgoingRetry(err, attempts)
			if storageGone(err) || ctx.Err() != nil {
				return false, err
			}
			if !again {
				return false, nil
			}
			attempts++
			return true, sleepStorage(ctx, delay)
		}
		if api == nil {
			if err := sleepStorage(ctx, 2*time.Second); err != nil {
				return err
			}
			continue
		}
		if refresh {
			list, err := s.savedGIFDocuments(ctx, api)
			if err != nil {
				if again, e := retry(err); e != nil {
					return e
				} else if again {
					continue
				}
				return err
			}
			clear(docs)
			for _, d := range list {
				docs[d.ID] = d
			}
			refresh = false
			attempts = 0
		}
		l.mu.Lock()
		pass := l.storage.Pass
		if pass == nil || pass.Done >= len(pass.Order) {
			l.storage.Pass = nil
			l.work.pass = model.StoragePass{}
			l.work.scanStale = true
			l.mu.Unlock()
			if err := s.saveStorage(ctx); err != nil {
				return err
			}
			s.picker.expire(model.PickerGIF)
			s.changed()
			return nil
		}
		id := pass.Order[len(pass.Order)-1-pass.Done]
		channel, peer := l.storage.ChannelID, l.storage.peer()
		l.mu.Unlock()
		// One gone from the list meanwhile (removed, or pushed out) would come
		// back: it is passed over.
		if d := docs[id]; d != nil {
			random := storageRandomID(channel, d.ID)
			res, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer, RandomID: random, Silent: true,
				Media: &tg.InputMediaDocument{ID: &tg.InputDocument{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}}})
			switch {
			case tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_EMPTY", "FILE_REFERENCE_INVALID") && attempts == 0:
				refresh = true
				attempts = 1
				continue
			case tgerr.Is(err, "RANDOM_ID_DUPLICATE"):
				// Sent before a stop: the scan finds it.
			case err != nil:
				again, e := retry(err)
				if e != nil {
					return e
				}
				if again {
					continue
				}
				log.Printf("tgstore: storage channel: GIF %d passed over: %v", id, err)
			default:
				l.mu.Lock()
				if msg := sentID(res, random); msg != 0 {
					l.storage.Archived[fmt.Sprintf("document/%d", id)] = msg
				}
				l.mu.Unlock()
				_ = s.Handle(ctx, res)
			}
		}
		attempts = 0
		l.mu.Lock()
		if l.storage.Pass != nil {
			l.storage.Pass.Done++
			l.work.pass.Done = l.storage.Pass.Done
		}
		l.mu.Unlock()
		if err := s.saveStorage(ctx); err != nil {
			return err
		}
		s.changed()
		if err := sleepStorage(ctx, storagePace); err != nil {
			return err
		}
	}
}

// scanStorage looks for the GIFs among the channel's messages new since the last
// time: others may have been forwarded there by another device.
func (s *Store) scanStorage(ctx context.Context, api *tg.Client) error {
	l := &s.collections
	l.mu.Lock()
	stale, bound := l.work.scanStale, l.storage.bound()
	since, peer := l.storage.Scanned, l.storage.peer()
	l.work.scanStale = false
	l.mu.Unlock()
	if !stale || !bound {
		return nil
	}
	found := map[string]int{}
	offset, newest := 0, since
	for {
		res, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: peer, Filter: &tg.InputMessagesFilterGif{}, OffsetID: offset, MinID: since, Limit: 100})
		if err != nil {
			l.mu.Lock()
			l.work.scanStale = true
			l.mu.Unlock()
			return err
		}
		modified, ok := res.AsModified()
		if !ok {
			break
		}
		messages := modified.GetMessages()
		if len(messages) == 0 {
			break
		}
		for _, raw := range messages {
			offset = raw.GetID()
			newest = max(newest, offset)
			if d := storageDocument(raw); d != nil {
				found[fmt.Sprintf("document/%d", d.ID)] = raw.GetID()
			}
		}
		if len(messages) < 100 {
			break
		}
		if err := sleepStorage(ctx, storagePace); err != nil {
			return err
		}
	}
	l.mu.Lock()
	for id, msg := range found {
		l.archivedLocked(id, msg)
	}
	l.storage.Scanned = newest
	l.mu.Unlock()
	return s.saveStorage(ctx)
}

// archivedLocked notes that id has the message msg in the channel. One Telegram
// pushed out that had none then is not in the file yet, and would be lost with
// the cache.
func (l *localCollections) archivedLocked(id string, msg int) {
	if _, had := l.storage.Archived[id]; !had && slices.ContainsFunc(l.gifs, func(item model.PickerItem) bool { return item.ID == id }) {
		l.storage.GIFsDirty = true
	}
	l.storage.Archived[id] = msg
	delete(l.storage.Origins, id)
}

func storageDocument(raw tg.MessageClass) *tg.Document {
	m, ok := raw.(*tg.Message)
	if !ok {
		return nil
	}
	media, ok := m.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil
	}
	d, _ := media.Document.(*tg.Document)
	return d
}

func storageFileOf(d *tg.Document) bool {
	for _, a := range d.Attributes {
		if name, ok := a.(*tg.DocumentAttributeFilename); ok {
			return name.FileName == storageIndexName
		}
	}
	return false
}

// readStorageIndex takes in the file when another device changed it.
func (s *Store) readStorageIndex(ctx context.Context, api *tg.Client) error {
	l := &s.collections
	l.mu.Lock()
	stale, bound := l.work.indexStale, l.storage.bound()
	index, known, input := l.storage.Index, l.storage.IndexDoc, l.storage.input()
	l.work.indexStale = false
	l.mu.Unlock()
	if !stale || !bound {
		return nil
	}
	again := func(err error) error {
		l.mu.Lock()
		l.work.indexStale = true
		l.mu.Unlock()
		return err
	}
	if index == 0 {
		full, err := api.ChannelsGetFullChannel(ctx, input)
		if err != nil {
			return again(err)
		}
		if ch, ok := full.FullChat.(*tg.ChannelFull); ok {
			index = ch.PinnedMsgID
		}
		if index == 0 {
			return nil
		}
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: input, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: index}}})
	if err != nil {
		return again(err)
	}
	var doc *tg.Document
	if modified, ok := res.AsModified(); ok {
		for _, raw := range modified.GetMessages() {
			if raw.GetID() == index {
				doc = storageDocument(raw)
			}
		}
	}
	if doc == nil || !storageFileOf(doc) {
		// Deleted, or a pinned message of the user's own: the next write
		// makes a file.
		l.mu.Lock()
		l.storage.Index, l.storage.IndexDoc = 0, 0
		l.storage.GIFsDirty, l.storage.PinsDirty = true, true
		l.mu.Unlock()
		return s.saveStorage(ctx)
	}
	if doc.ID == known {
		return nil
	}
	b, err := s.storageFile(ctx, doc)
	if err != nil {
		return again(err)
	}
	var file storageIndex
	if err := json.Unmarshal(b, &file); err != nil {
		log.Printf("tgstore: storage channel: file: %v", err)
		return nil
	}
	l.mu.Lock()
	l.work.newerIndex = file.Version > storageVersion
	l.storage.Index, l.storage.IndexDoc = index, doc.ID
	newer := l.work.newerIndex
	l.mu.Unlock()
	if newer {
		log.Printf("tgstore: storage channel: file of version %d", file.Version)
		return nil
	}
	parts := s.local()
	if parts.GIFs {
		if err := s.applyIndexGIFs(ctx, api, file.GIFs); err != nil {
			return again(err)
		}
	}
	if parts.Pins {
		s.applyIndexPins(ctx, file)
	}
	l.mu.Lock()
	l.storage.GIFBase, l.storage.PinBase, l.storage.SyncedBase = slices.Clone(file.GIFs), slices.Clone(file.Pins), slices.Clone(file.Synced)
	l.mu.Unlock()
	if err := s.saveStorage(ctx); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *Store) storageFile(ctx context.Context, d *tg.Document) ([]byte, error) {
	l := &s.collections
	l.mu.Lock()
	file := l.work.file
	l.mu.Unlock()
	if file != nil {
		return file(ctx, d)
	}
	if d.Size <= 0 || d.Size > storageIndexMost {
		return nil, fmt.Errorf("a file of %d bytes", d.Size)
	}
	c := s.history
	c.mu.Lock()
	pool := c.pool
	c.mu.Unlock()
	if pool == nil {
		return nil, errNotConnected
	}
	out := &cappedFile{progress: func(int64) {}}
	location := &tg.InputDocumentFileLocation{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}
	if err := downloadFile(ctx, dcpool.DownloadClient(pool, ctx, d.DCID), location, d.Size, out, nil); err != nil {
		return nil, err
	}
	return out.data, nil
}

// applyIndexGIFs makes the file's list the GIFs kept here, merged with what this
// device did not tell yet. Those without a message in the channel stay after them.
func (s *Store) applyIndexGIFs(ctx context.Context, api *tg.Client, theirs []int64) error {
	l := &s.collections
	l.mu.Lock()
	byMessage := make(map[int64]string, len(l.storage.Archived))
	for id, msg := range l.storage.Archived {
		byMessage[int64(msg)] = id
	}
	items := make(map[string]model.PickerItem, len(l.gifs))
	var mine []int64
	for _, item := range l.gifs {
		items[item.ID] = item
		if msg, ok := l.storage.Archived[item.ID]; ok {
			mine = append(mine, int64(msg))
		}
	}
	order := slices.Clone(theirs)
	if l.storage.GIFsDirty {
		order = mergePins(mine, l.storage.GIFBase, theirs)
	}
	var missing []tg.InputMessageClass
	for _, msg := range order {
		if _, ok := items[byMessage[msg]]; !ok {
			missing = append(missing, &tg.InputMessageID{ID: int(msg)})
		}
	}
	input := l.storage.input()
	l.mu.Unlock()
	fetched := map[int64]model.PickerItem{}
	gone := false
	for start := 0; start < len(missing); start += 100 {
		part := missing[start:min(start+100, len(missing))]
		res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: input, ID: part})
		if err != nil {
			return err
		}
		modified, ok := res.AsModified()
		if !ok {
			continue
		}
		for _, raw := range modified.GetMessages() {
			d := storageDocument(raw)
			if d == nil {
				gone = true
				continue
			}
			if made := s.savedGIFItems(ctx, []tg.DocumentClass{d}); len(made) == 1 {
				fetched[int64(raw.GetID())] = made[0]
			}
		}
	}
	listed := map[string]bool{}
	for _, item := range s.serverGIFs(ctx) {
		listed[item.ID] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var tail []model.PickerItem
	kept := map[string]bool{}
	for _, msg := range order {
		item, ok := fetched[msg]
		if ok {
			l.storage.Archived[item.ID] = int(msg)
		} else if item, ok = items[byMessage[msg]]; !ok {
			gone = true
			continue
		}
		kept[item.ID] = true
		if !listed[item.ID] {
			tail = append(tail, item)
		}
	}
	for _, item := range l.gifs {
		if _, inChannel := l.storage.Archived[item.ID]; !inChannel && !kept[item.ID] && !listed[item.ID] {
			tail = append(tail, item)
		}
	}
	l.gifs = tail
	l.storage.GIFsDirty = l.storage.GIFsDirty || gone
	return s.Cache().Put(ctx, localGIFsKey, localGIFs{tail})
}

// applyIndexPins takes the file's list of pins. What Telegram changed since it
// was written counts, and what this device did not tell yet stands.
func (s *Store) applyIndexPins(ctx context.Context, file storageIndex) {
	if file.Pins == nil {
		return
	}
	s.mu.RLock()
	server := model.PinnedOrder(s.chats)
	s.mu.RUnlock()
	merged := mergePins(file.Pins, file.Synced, server)
	l := &s.collections
	l.mu.Lock()
	if l.pins != nil && l.storage.PinsDirty {
		merged = mergePins(l.pins.Order, l.storage.PinBase, merged)
	}
	l.pins = &localPins{Order: merged, Synced: server}
	l.mu.Unlock()
	if err := s.saveLocalPins(ctx); err != nil {
		log.Printf("tgstore: storage channel: pins: %v", err)
	}
	s.syncPins()
}

// deleteStorageMessages deletes the messages of the GIFs the user removed.
func (s *Store) deleteStorageMessages(ctx context.Context, api *tg.Client) error {
	l := &s.collections
	l.mu.Lock()
	ids, input := l.storage.Delete, l.storage.input()
	l.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	if _, err := api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: input, ID: ids}); err != nil {
		return err
	}
	l.mu.Lock()
	l.storage.Delete = slices.DeleteFunc(l.storage.Delete, func(id int) bool { return slices.Contains(ids, id) })
	l.mu.Unlock()
	return s.saveStorage(ctx)
}

// serverGIFs is Telegram's list as kept here.
func (s *Store) serverGIFs(ctx context.Context) []model.PickerItem {
	page, _, _ := s.keptCatalogue(ctx, model.PickerGIF)
	return page.Items
}

// archiveGIFs forwards to the channel the saved GIFs with a known message, which
// leaves Telegram's list as it is, and sends the one at its top, which moves
// nothing.
func (s *Store) archiveGIFs(ctx context.Context, api *tg.Client) error {
	if !s.local().GIFs {
		return nil
	}
	list := s.serverGIFs(ctx)
	l := &s.collections
	l.mu.Lock()
	peer := l.storage.peer()
	kept := make([]string, 0, len(l.storage.Origins))
	for id := range l.storage.Origins {
		if _, done := l.storage.Archived[id]; !done {
			kept = append(kept, id)
		}
	}
	slices.Sort(kept)
	l.mu.Unlock()
	listed := map[string]bool{}
	for _, item := range list {
		listed[item.ID] = true
	}
	for _, id := range kept {
		l.mu.Lock()
		origin := l.storage.Origins[id]
		tail := slices.ContainsFunc(l.gifs, func(item model.PickerItem) bool { return item.ID == id })
		l.mu.Unlock()
		if !listed[id] && !tail {
			continue
		}
		c := s.history
		c.mu.Lock()
		source := c.peers[origin.ChatID]
		c.mu.Unlock()
		if source.ID == 0 {
			continue
		}
		random := newRandomID()
		res, err := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{FromPeer: source.input(), ToPeer: peer, ID: []int{int(origin.MessageID)}, RandomID: []int64{random}, Silent: true})
		switch {
		case tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED", "MESSAGE_ID_INVALID", "PEER_ID_INVALID", "CHANNEL_PRIVATE", "CHANNEL_INVALID", "MESSAGE_AUTHOR_REQUIRED"):
			// It is the source that refused: another message of it may do.
			log.Printf("tgstore: storage channel: GIF %s not forwarded: %v", id, err)
			l.mu.Lock()
			delete(l.storage.Origins, id)
			l.mu.Unlock()
			continue
		case err != nil:
			return err
		}
		l.mu.Lock()
		if msg := sentID(res, random); msg != 0 {
			l.storage.Archived[id] = msg
			if tail {
				l.storage.GIFsDirty = true
			}
		} else {
			l.work.scanStale = true
		}
		delete(l.storage.Origins, id)
		l.mu.Unlock()
		_ = s.Handle(ctx, res)
		if err := s.saveStorage(ctx); err != nil {
			return err
		}
		if err := sleepStorage(ctx, storagePace); err != nil {
			return err
		}
	}
	if len(list) == 0 {
		return nil
	}
	top := list[0]
	l.mu.Lock()
	_, done := l.storage.Archived[top.ID]
	_, known := l.storage.Origins[top.ID]
	l.mu.Unlock()
	if done || known || top.Media.Media == nil {
		return nil
	}
	// Not the pass's id for it: told a duplicate, the pass would leave this GIF
	// behind those it raised.
	random := newRandomID()
	var sent tg.UpdatesClass
	_, err := s.pickerDocument(ctx, top.Media, func(api *tg.Client, doc *tg.InputDocument) error {
		var err error
		sent, err = api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer, RandomID: random, Silent: true, Media: &tg.InputMediaDocument{ID: doc}})
		return err
	})
	if err != nil {
		if _, retry := outgoingRetry(err, 0); retry || storageGone(err) {
			return err
		}
		// This GIF alone: the file is still to be written.
		log.Printf("tgstore: storage channel: GIF %s not sent: %v", top.ID, err)
		return nil
	}
	l.mu.Lock()
	if msg := sentID(sent, random); msg != 0 {
		l.storage.Archived[top.ID] = msg
	} else {
		l.work.scanStale = true
	}
	l.mu.Unlock()
	_ = s.Handle(ctx, sent)
	return s.saveStorage(ctx)
}

// publishStorage replaces the file when this device changed what it tells, once
// the changes have settled and Telegram's pins have caught up.
func (s *Store) publishStorage(ctx context.Context, api *tg.Client) error {
	l := &s.collections
	l.mu.Lock()
	dirty := l.storage.GIFsDirty || l.storage.PinsDirty
	if !dirty || l.work.newerIndex || !l.storage.bound() {
		l.mu.Unlock()
		return nil
	}
	parts := s.local()
	if wait := time.Until(l.work.publishAt); wait > 0 || parts.Pins && (l.running || l.dirty || l.pins == nil) {
		l.mu.Unlock()
		if wait <= 0 {
			wait = storageDebounce
		}
		time.AfterFunc(wait, s.wakeStorage)
		return nil
	}
	file := storageIndex{Version: storageVersion, GIFs: l.storage.GIFBase, Pins: l.storage.PinBase, Synced: l.storage.SyncedBase}
	if parts.Pins {
		file.Pins, file.Synced = l.pins.Order, l.pins.Synced
	}
	if parts.GIFs {
		file.GIFs = nil
		for _, item := range l.gifs {
			if msg, ok := l.storage.Archived[item.ID]; ok {
				file.GIFs = append(file.GIFs, int64(msg))
			}
		}
	}
	index, peer := l.storage.Index, l.storage.peer()
	gifs, pins := l.storage.GIFsDirty, l.storage.PinsDirty
	l.storage.GIFsDirty, l.storage.PinsDirty = false, false
	l.mu.Unlock()
	fail := func(err error) error {
		l.mu.Lock()
		l.storage.GIFsDirty = l.storage.GIFsDirty || gifs
		l.storage.PinsDirty = l.storage.PinsDirty || pins
		l.mu.Unlock()
		return err
	}
	b, err := json.Marshal(file)
	if err != nil {
		return fail(err)
	}
	uploaded, err := uploader.NewUploader(api).FromBytes(ctx, storageIndexName, b)
	if err != nil {
		return fail(err)
	}
	media := &tg.InputMediaUploadedDocument{File: uploaded, MimeType: "application/json", ForceFile: true,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: storageIndexName}}}
	var res tg.UpdatesClass
	if index != 0 {
		res, err = api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: peer, ID: index, Media: media})
		if tgerr.Is(err, "MESSAGE_ID_INVALID") {
			index, err = 0, nil
		}
	}
	if err != nil {
		if tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
			err = nil
		}
		if err != nil {
			return fail(err)
		}
	}
	if index == 0 {
		random := newRandomID()
		res, err = api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer, RandomID: random, Silent: true, Media: media})
		if err != nil {
			return fail(err)
		}
		index = sentID(res, random)
		if index == 0 {
			return fail(errors.New("the file's message was not told"))
		}
		pinned, err := api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{Silent: true, Peer: peer, ID: index})
		if err != nil {
			return fail(err)
		}
		_ = s.Handle(ctx, pinned)
	}
	l.mu.Lock()
	l.storage.Index = index
	if doc := storageSentDocument(res, index); doc != 0 {
		l.storage.IndexDoc = doc
	}
	l.storage.GIFBase, l.storage.PinBase, l.storage.SyncedBase = file.GIFs, file.Pins, file.Synced
	l.mu.Unlock()
	if res != nil {
		_ = s.Handle(ctx, res)
	}
	return s.saveStorage(ctx)
}

// storageSentDocument is the document of the message id that res tells.
func storageSentDocument(res tg.UpdatesClass, id int) int64 {
	var all []tg.UpdateClass
	switch u := res.(type) {
	case *tg.Updates:
		all = u.Updates
	case *tg.UpdatesCombined:
		all = u.Updates
	}
	for _, raw := range all {
		var m tg.MessageClass
		switch u := raw.(type) {
		case *tg.UpdateNewChannelMessage:
			m = u.Message
		case *tg.UpdateEditChannelMessage:
			m = u.Message
		}
		if m != nil && m.GetID() == id {
			if d := storageDocument(m); d != nil {
				return d.ID
			}
		}
	}
	return 0
}

// storageResumed has the channel looked at anew when a part that uses it is
// switched on: what the file told while it was off was not taken in.
func (s *Store) storageResumed() {
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	l.storage.IndexDoc = 0
	l.work.indexStale, l.work.scanStale = true, true
}

// storageEdited has the file read again when m is its message.
func (s *Store) storageEdited(m tg.MessageClass) {
	message, ok := m.(*tg.Message)
	if !ok {
		return
	}
	peer, ok := message.PeerID.(*tg.PeerChannel)
	if !ok {
		return
	}
	l := &s.collections
	l.mu.Lock()
	mine := peer.ChannelID == l.storage.ChannelID && message.ID == l.storage.Index
	if mine {
		l.work.indexStale = true
	}
	l.mu.Unlock()
	if mine {
		s.wakeStorage()
	}
}

// markStorageChanged tells that the file no longer says what is so here.
func (s *Store) markStorageChanged(gifs, pins bool) {
	l := &s.collections
	l.mu.Lock()
	if !l.storage.bound() {
		l.mu.Unlock()
		return
	}
	l.storage.GIFsDirty = l.storage.GIFsDirty || gifs
	l.storage.PinsDirty = l.storage.PinsDirty || pins
	l.work.publishAt = time.Now().Add(storageDebounce)
	l.mu.Unlock()
	time.AfterFunc(storageDebounce, s.wakeStorage)
}

// gifsMet notes where the saved GIFs among msgs are: a message of the channel
// is the GIF's place there, one elsewhere what it can be forwarded from.
func (s *Store) gifsMet(ctx context.Context, msgs []model.Message) {
	if !s.local().GIFs {
		return
	}
	var gifs []model.Message
	for _, m := range msgs {
		if m.Kind == model.MessageGIF && m.Media != nil && m.Key.MessageID > 0 && !m.Sending {
			gifs = append(gifs, m)
		}
	}
	if len(gifs) == 0 {
		return
	}
	s.history.mu.Lock()
	refused := map[int64]bool{}
	for _, m := range gifs {
		refused[m.Key.ChatID] = s.history.peers[m.Key.ChatID].Rights.NoForwards
	}
	s.history.mu.Unlock()
	list := s.serverGIFs(ctx)
	changed := false
	l := &s.collections
	l.mu.Lock()
	if !l.loaded {
		l.mu.Unlock()
		return
	}
	for _, m := range gifs {
		id := m.Media.ID
		saved := func(item model.PickerItem) bool { return item.ID == id }
		switch {
		case l.storage.bound() && m.Key.ChatID == l.storage.key():
			if l.storage.Archived[id] != int(m.Key.MessageID) {
				l.archivedLocked(id, int(m.Key.MessageID))
				changed = true
			}
		case m.NoForwards || refused[m.Key.ChatID]:
		case !slices.ContainsFunc(list, saved) && !slices.ContainsFunc(l.gifs, saved):
		default:
			if _, done := l.storage.Archived[id]; !done && l.storage.Origins[id] != m.Key {
				l.storage.Origins[id] = m.Key
				changed = true
			}
		}
	}
	l.mu.Unlock()
	if changed {
		_ = s.saveStorage(ctx)
		s.wakeStorage()
	}
}

// renewKeptGIF renews the reference of a GIF Telegram's list no longer has, by
// the channel's message of it or the message it was met in. It reports whether
// there is such a message.
func (s *Store) renewKeptGIF(ctx context.Context, api *tg.Client, doc int64) (bool, error) {
	id := fmt.Sprintf("document/%d", doc)
	l := &s.collections
	l.mu.Lock()
	msg, archived := l.storage.Archived[id]
	origin, met := l.storage.Origins[id]
	input := l.storage.input()
	l.mu.Unlock()
	var res tg.MessagesMessagesClass
	var err error
	switch {
	case archived:
		res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: input, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: msg}}})
	case met:
		c := s.history
		c.mu.Lock()
		peer := c.peers[origin.ChatID]
		c.mu.Unlock()
		ids := []tg.InputMessageClass{&tg.InputMessageID{ID: int(origin.MessageID)}}
		if peer.Kind == "channel" {
			res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, ID: ids})
		} else {
			res, err = api.MessagesGetMessages(ctx, ids)
		}
	default:
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if modified, ok := res.AsModified(); ok {
		for _, raw := range modified.GetMessages() {
			if d := storageDocument(raw); d != nil && d.ID == doc {
				s.savedGIFItems(ctx, []tg.DocumentClass{d})
				return true, nil
			}
		}
	}
	return true, fmt.Errorf("the message of GIF %d is gone", doc)
}

// forgetStorageGIF drops the channel's message of a GIF the user removed.
func (s *Store) forgetStorageGIF(id string) {
	l := &s.collections
	l.mu.Lock()
	msg, ok := l.storage.Archived[id]
	if ok {
		delete(l.storage.Archived, id)
		l.storage.Delete = append(l.storage.Delete, msg)
	}
	delete(l.storage.Origins, id)
	l.mu.Unlock()
	if ok {
		s.markStorageChanged(true, false)
	}
}

// evictedGIFs tells which of old Telegram pushed out, going to list new. Those
// missing from new were removed or pushed out, and only how many tell which: the
// list has limit places, and what it took of them for what is new came from its
// end. It may name one removed for one pushed out when both happened at once.
func evictedGIFs(old, new []model.PickerItem, limit int) []model.PickerItem {
	if limit <= 0 {
		return nil
	}
	has := func(items []model.PickerItem, id string) bool {
		return slices.ContainsFunc(items, func(item model.PickerItem) bool { return item.ID == id })
	}
	added := 0
	for _, item := range new {
		if !has(old, item.ID) {
			added++
		}
	}
	var missing []model.PickerItem
	for _, item := range old {
		if !has(new, item.ID) {
			missing = append(missing, item)
		}
	}
	k := min(max(0, len(old)+added-limit), len(missing))
	return slices.Clone(missing[len(missing)-k:])
}

// gifsFetched keeps what Telegram's new list pushed out, whichever device saved
// what pushed it, and lets go of what is on the list again.
func (s *Store) gifsFetched(ctx context.Context, old, fresh []model.PickerItem) {
	if !s.local().GIFs {
		return
	}
	pushed := evictedGIFs(old, fresh, model.SavedGIFLimit(s.Premium()))
	l := &s.collections
	l.mu.Lock()
	if !l.loaded {
		l.mu.Unlock()
		return
	}
	tail := model.PreferSaved(pushed, l.gifs)
	tail = slices.DeleteFunc(tail, func(item model.PickerItem) bool {
		return slices.ContainsFunc(fresh, func(listed model.PickerItem) bool { return listed.ID == item.ID })
	})
	same := slices.EqualFunc(tail, l.gifs, func(a, b model.PickerItem) bool { return a.ID == b.ID })
	l.gifs = tail
	l.work.indexStale = true
	l.mu.Unlock()
	if !same {
		if err := s.Cache().Put(ctx, localGIFsKey, localGIFs{tail}); err != nil {
			log.Printf("tgstore: local GIFs: %v", err)
		}
		s.markStorageChanged(true, false)
		s.changed()
	}
	s.wakeStorage()
}
