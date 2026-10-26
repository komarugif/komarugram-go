// SPDX-License-Identifier: Unlicense OR MIT

// Package tgstore is a model.Store backed by a Telegram account.
//
// Load reads what the chat list needs: the account's profile, its chat
// folders and its dialogs. Everything it calls only reads: no message is
// marked as read and no online status is set, so the account looks untouched
// to its other sessions. History and live updates are handled by the
// account-scoped conversation runtime in history.go.
package tgstore

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// pageSize is how many dialogs one messages.getDialogs asks for; 100 is the
// most the server hands out.
const pageSize = 100

// pagePause spaces the pages out, as an official client scrolling its list
// would, instead of asking for everything at once.
const pagePause = 300 * time.Millisecond

// Store holds what Load last read. It is safe to use from any goroutine.
type Store struct {
	picker pickerCache
	// watch hands the messages of chats watched to their watchers.
	watch   chatWatch
	themes  themeCatalogue
	changed func()
	notices atomic.Pointer[func(model.MessageNotice)]
	// editLimit is Telegram's edit_time_limit in seconds, 0 until read;
	// editLimitAsked, that it is being read or was.
	editLimit      atomic.Int64
	editLimitAsked atomic.Bool
	history        *conversation
	// emoji gathers the custom emoji to ask Telegram for together.
	emoji emojiBatch
	// activity is what others do in chats now.
	activity activities
	// stories are the stories over the list of chats.
	stories storyState
	// communities are the communities opened.
	communities communityState

	themeRevisions map[int64]uint64
	// appearances are the states of the chats' cached appearances.
	appearances  map[int64]uint8
	mu           sync.RWMutex
	me           model.Profile
	dc           int
	sessionEnded model.SessionEnd
	// connectionErr is what stopped the connection, and reconnect asks for
	// it again: see FailConnection.
	connectionErr error
	reconnect     chan struct{}
	search        searchState
	profiles      profileColors
	freeze        model.Freeze
	// freezeChecked is when a refused request last read the configuration
	// again: see Middleware.
	freezeChecked time.Time
	premium       model.Premium
	folders       []model.Folder
	chats         []model.Chat
	reactions     reactionState
	recent        recentChats
	ghost         ghostState
	presence      presences
	blocked       blockedState
	bots          botState
	// dialogsLoading: loadDialogs runs.
	dialogsLoading atomic.Bool

	// entries is what the folders' rules ask of the chats beyond them, and
	// filters the folders as Telegram tells them: see folderList.
	entries map[int64]entry
	filters []tg.DialogFilterClass
}

// New returns an empty store that calls changed whenever Load has read more.
func New(changed func()) *Store {
	if changed == nil {
		changed = func() {}
	}
	return &Store{changed: changed, history: newConversation(), reconnect: make(chan struct{}, 1)}
}

func (s *Store) Me() model.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.me
}

func (s *Store) Folders() []model.Folder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.folders
}

func (s *Store) Chats() []model.Chat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.chats
}

// Load reads the profile, the folders and every dialog of the main list,
// publishing each part as soon as it has it.
func (s *Store) Load(ctx context.Context, api *tg.Client) error {
	full, err := api.UsersGetFullUser(ctx, &tg.InputUserSelf{})
	if err != nil {
		return fmt.Errorf("tgstore: profile: %w", err)
	}
	var self *tg.User
	for _, u := range full.Users {
		if user, ok := u.(*tg.User); ok && user.Self {
			self = user
		}
	}
	if self == nil {
		return fmt.Errorf("tgstore: profile: the server did not return the account's user")
	}
	s.rememberPeers(full.Users, nil)
	s.publish(func() {
		s.me = profile(self, full.FullUser.About)
		s.me.DC = s.dc
	})
	s.loadAppConfig(ctx, api, self.Premium)
	if err := s.loadDialogs(ctx, api, self.ID); err != nil {
		return err
	}
	s.goLoadStories(ctx, api)
	return nil
}

// goLoadStories asks for the stories over the list in the background.
func (s *Store) goLoadStories(ctx context.Context, api *tg.Client) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	c.wg.Go(func() {
		defer crash.Recover("stories", nil)
		_ = s.loadStories(ctx, api)
	})
}

// reloadDialogs reads the folders and dialogs again, after updates were
// missed (too long): their unread counts and last messages are old. It
// does nothing before Load, or while a load runs.
func (s *Store) reloadDialogs() {
	c := s.history
	c.mu.Lock()
	api, ctx := c.api, c.ctx
	c.mu.Unlock()
	s.mu.RLock()
	self := s.me.ID
	s.mu.RUnlock()
	if api == nil || self == 0 {
		return
	}
	go func() {
		defer crash.Recover("dialogs reload", nil)
		_ = s.loadDialogs(ctx, api, self)
	}()
	// What changed of the stories meanwhile.
	s.goLoadStories(ctx, api)
}

// loadDialogs reads the folders and every dialog of the main list, and
// then the histories open: see resync. One runs at a time.
func (s *Store) loadDialogs(ctx context.Context, api *tg.Client, self int64) error {
	if !s.dialogsLoading.CompareAndSwap(false, true) {
		return nil
	}
	defer s.dialogsLoading.Store(false)
	filters, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return fmt.Errorf("tgstore: folders: %w", err)
	}

	l := newList(self)
	offset := page{peer: &tg.InputPeerEmpty{}}
	for {
		// A gap in the updates while the page is asked for leaves its
		// chats as they are.
		s.history.mu.Lock()
		liveEpoch := s.history.liveEpoch
		s.history.mu.Unlock()
		res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offset.date,
			OffsetID:   offset.id,
			OffsetPeer: offset.peer,
			Limit:      pageSize,
		})
		if err != nil {
			return fmt.Errorf("tgstore: dialogs: %w", err)
		}
		dialogs, ok := res.AsModified()
		if !ok {
			break
		}
		s.rememberPeers(dialogs.GetUsers(), dialogs.GetChats())
		s.history.mu.Lock()
		tops, pts := map[int64]int{}, map[int64]int{}
		for _, d := range dialogs.GetDialogs() {
			if d, ok := d.(*tg.Dialog); ok {
				s.history.top[peerID(d.Peer)] = max(s.history.top[peerID(d.Peer)], d.TopMessage)
				tops[peerID(d.Peer)] = d.TopMessage
				if p, ok := d.Peer.(*tg.PeerChannel); ok {
					if n, ok := d.GetPts(); ok {
						pts[p.ChannelID] = n
					}
				}
			}
		}
		s.history.mu.Unlock()
		if err := s.keepChannelPts(ctx, self, pts); err != nil {
			return err
		}
		if err := s.liveFromDialogs(ctx, tops, liveEpoch); err != nil {
			return err
		}
		read := len(l.chats)
		added := l.add(dialogs)
		page := l.chats[read:]
		// Without them the thumbnails stay blank: the list is shown all the
		// same.
		_ = s.keepPreviewRefs(ctx, l.refs)
		l.refs = nil
		// The pages after one that tells nothing new are of chats that
		// changed no more: see dialogs_kept.go.
		unchanged := false
		s.publish(func() {
			unchanged = s.pageUnchanged(page, l)
			chats, entries := s.withKept(l)
			chats = keepAlbums(chats, s.chats)
			s.chats, s.entries, s.filters = s.keepReads(chats), entries, filters.Filters
			s.folders = folderList(self, filters.Filters, entries)
		})
		if unchanged {
			break
		}

		// A complete list comes as messages.dialogs; only a slice has more,
		// and it says how many dialogs there are in all.
		slice, ok := dialogs.(*tg.MessagesDialogsSlice)
		if !ok || added == 0 || len(l.chats) >= slice.Count {
			break
		}
		next, ok := l.nextPage(dialogs)
		if !ok {
			break
		}
		offset = next
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pagePause):
		}
	}
	if err := s.persistDialogs(ctx); err != nil {
		return err
	}
	s.resync(0)
	return nil
}

// keepChannelPts keeps the channels' pts the dialogs tell (pts, by channel
// id), as Telegram Desktop takes them: a channel the updates manager starts
// when an update of it comes asks for its difference from there, not from
// where it stood when the client last ran. What happened before is in the
// dialogs, and in the pages of the histories opened. Only channels kept
// already are: the manager starts any other from its first update.
func (s *Store) keepChannelPts(ctx context.Context, self int64, pts map[int64]int) error {
	cache := s.Cache()
	if cache == nil || len(pts) == 0 {
		return nil
	}
	return cache.RaiseChannelPts(ctx, self, pts)
}

// keepReads keeps in chats, the dialogs read so far, the reads this client
// told Telegram since: a dialog read before Telegram had the read, its
// ReadInbox older than it, counts the messages read since as unread. The
// pages of a long load, each with every dialog read until then, brought a
// chat read meanwhile its unread count back, and the read, told already,
// was not told again to take it away. The caller holds s.mu.
func (s *Store) keepReads(chats []model.Chat) []model.Chat {
	now := make(map[int64]model.Chat, len(s.chats))
	for _, c := range s.chats {
		now[c.ID] = c
	}
	s.ghost.mu.Lock()
	defer s.ghost.mu.Unlock()
	for i, c := range chats {
		told := s.ghost.read[c.ID]
		if told <= c.ReadInbox {
			continue
		}
		chats[i].ReadInbox = told
		if kept, ok := now[c.ID]; ok {
			chats[i].Unread = kept.Unread
		}
	}
	return chats
}

func (s *Store) publish(update func()) {
	s.mu.Lock()
	update()
	s.mu.Unlock()
	s.changed()
}

func profile(u *tg.User, about string) model.Profile {
	p := model.Profile{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username, Bio: about,
		Badges: userBadges(u, time.Now())}
	if u.Phone != "" {
		p.Phone = "+" + u.Phone
	}
	return p
}

// peerID is the chat id the model uses: tdlib's, which keeps users, basic
// groups and channels apart although their own ids overlap.
func peerID(p tg.PeerClass) int64 {
	var id constant.TDLibPeerID
	switch p := p.(type) {
	case *tg.PeerUser:
		id.User(p.UserID)
	case *tg.PeerChat:
		id.Chat(p.ChatID)
	case *tg.PeerChannel:
		id.Channel(p.ChannelID)
	}
	return int64(id)
}

// inputPeerID is peerID for the peers a folder names.
func inputPeerID(p tg.InputPeerClass, self int64) int64 {
	var id constant.TDLibPeerID
	switch p := p.(type) {
	case *tg.InputPeerSelf:
		id.User(self)
	case *tg.InputPeerUser:
		id.User(p.UserID)
	case *tg.InputPeerChat:
		id.Chat(p.ChatID)
	case *tg.InputPeerChannel:
		id.Channel(p.ChannelID)
	case *tg.InputPeerUserFromMessage:
		id.User(p.UserID)
	case *tg.InputPeerChannelFromMessage:
		id.Channel(p.ChannelID)
	}
	return int64(id)
}

// entry is what the list knows of a chat beyond model.Chat: what folder
// rules ask about.
type entry struct {
	contact    bool
	unreadMark bool
}

// list gathers dialogs page by page, with the users, chats and messages the
// pages refer to.
type list struct {
	self     int64
	users    map[int64]*tg.User
	groups   map[int64]*tg.Chat
	channels map[int64]*tg.Channel
	titles   map[int64]string // forbidden chats and channels
	// communities are the communities the pages name.
	communities map[int64]*tg.Community
	messages    map[[2]int64]tg.MessageClass

	chats   []model.Chat
	entries map[int64]entry
	seen    map[int64]bool
	// refs are where the thumbnails of the chats' last messages download
	// from, by their media, until the store keeps them.
	refs map[string]fileLocation
	// pins counts the pinned dialogs seen; they come first, in pin order.
	pins int
}

func newList(self int64) *list {
	return &list{
		self:        self,
		users:       map[int64]*tg.User{},
		groups:      map[int64]*tg.Chat{},
		channels:    map[int64]*tg.Channel{},
		titles:      map[int64]string{},
		communities: map[int64]*tg.Community{},
		messages:    map[[2]int64]tg.MessageClass{},
		entries:     map[int64]entry{},
		seen:        map[int64]bool{},
	}
}

// add takes in one page and returns how many chats it added.
func (l *list) add(page tg.ModifiedMessagesDialogs) int {
	for _, u := range page.GetUsers() {
		if user, ok := u.(*tg.User); ok {
			l.users[user.ID] = user
		}
	}
	for _, c := range page.GetChats() {
		switch c := c.(type) {
		case *tg.Chat:
			l.groups[c.ID] = c
		case *tg.ChatForbidden:
			l.titles[peerID(&tg.PeerChat{ChatID: c.ID})] = c.Title
		case *tg.Channel:
			l.channels[c.ID] = c
		case *tg.ChannelForbidden:
			l.titles[peerID(&tg.PeerChannel{ChannelID: c.ID})] = c.Title
		case *tg.Community:
			l.communities[c.ID] = c
		case *tg.CommunityForbidden:
			l.titles[peerID(&tg.PeerChannel{ChannelID: c.ID})] = c.Title
		}
	}
	for _, m := range page.GetMessages() {
		if peer, ok := messagePeer(m); ok {
			l.messages[[2]int64{peerID(peer), int64(m.GetID())}] = m
		}
	}
	added := 0
	for _, d := range page.GetDialogs() {
		var chat model.Chat
		var e entry
		switch d := d.(type) {
		case *tg.Dialog:
			chat, e = l.chat(d)
		case *tg.DialogCommunity:
			chat = l.community(d)
		default:
			continue // a folder entry: the archive is not loaded yet
		}
		id := chat.ID
		if l.seen[id] {
			continue
		}
		l.seen[id] = true
		l.chats = append(l.chats, chat)
		l.entries[id] = e
		added++
	}
	return added
}

func messagePeer(m tg.MessageClass) (tg.PeerClass, bool) {
	switch m := m.(type) {
	case *tg.Message:
		return m.PeerID, true
	case *tg.MessageService:
		return m.PeerID, true
	}
	return nil, false
}

// chat turns a dialog into the model's chat.
func (l *list) chat(d *tg.Dialog) (model.Chat, entry) {
	id := peerID(d.Peer)
	chat := model.Chat{
		ID:         id,
		Unread:     d.UnreadCount,
		Pinned:     d.Pinned,
		ReadOutbox: model.MessageID(d.ReadOutboxMaxID),
		ReadInbox:  model.MessageID(d.ReadInboxMaxID),
	}
	chat.Muted = mutedNow(d.NotifySettings)
	chat.Draft = draftOf(d.Draft)
	if d.Pinned {
		l.pins++
		chat.PinRank = l.pins
	}
	e := entry{unreadMark: d.UnreadMark}

	switch p := d.Peer.(type) {
	case *tg.PeerUser:
		chat.Kind = model.KindUser
		if u := l.users[p.UserID]; u != nil {
			chat.Title = userName(u)
			chat.Badges = userBadges(u, time.Now())
			chat.Community = linkedCommunity(u.GetLinkedCommunityID())
			e.contact = u.Contact || u.Self
			switch {
			case u.Self:
				chat.Kind, chat.Title = model.KindSaved, ""
				chat.Badges = model.Badges{}
			case u.Bot:
				chat.Kind = model.KindBot
			}
		}
	case *tg.PeerChat:
		chat.Kind = model.KindGroup
		if g := l.groups[p.ChatID]; g != nil {
			chat.Title, chat.Members = g.Title, g.ParticipantsCount
		}
	case *tg.PeerChannel:
		chat.Kind = model.KindGroup
		if c := l.channels[p.ChannelID]; c != nil {
			chat.Title = c.Title
			chat.Badges = channelBadges(c, time.Now())
			chat.Forum, chat.DirectMessages = c.Forum, c.Monoforum
			chat.Community = linkedCommunity(c.GetLinkedCommunityID())
			if c.Broadcast {
				chat.Kind = model.KindChannel
			}
			chat.Members, _ = c.GetParticipantsCount()
		}
	}
	if chat.Title == "" {
		chat.Title = l.titles[id]
	}

	if m, ok := l.messages[[2]int64{id, int64(d.TopMessage)}]; ok {
		chat.Last, chat.LastTime = preview(m)
		if service, ok := m.(*tg.MessageService); ok {
			chat.Last = model.Preview{Service: l.serviceMessage(service)}
		}
		chat.Last.ID = model.MessageID(d.TopMessage)
		if msg, ok := m.(*tg.Message); ok {
			chat.Last.Sent = msg.Out
			l.previewContent(&chat.Last, msg)
		}
		if chat.Kind == model.KindGroup {
			chat.Last.Sender, chat.Last.Outgoing = l.sender(m)
		}
	}
	return chat, e
}

// community turns the dialog of a community into the model's chat: it has
// no messages of its own, the list shows those of its chats.
func (l *list) community(d *tg.DialogCommunity) model.Chat {
	chat := model.Chat{
		ID:     peerID(&tg.PeerChannel{ChannelID: d.CommunityID}),
		Kind:   model.KindCommunity,
		Pinned: d.Pinned,
		Muted:  mutedNow(d.NotifySettings),
		Title:  l.titles[peerID(&tg.PeerChannel{ChannelID: d.CommunityID})],
	}
	if c := l.communities[d.CommunityID]; c != nil {
		chat.Title, chat.Collapsed = c.Title, c.CollapsedInDialogs
	}
	if d.Pinned {
		l.pins++
		chat.PinRank = l.pins
	}
	return chat
}

// linkedCommunity is the chat id of the community a user or channel is
// linked to, 0 for none.
func linkedCommunity(id int64, ok bool) int64 {
	if !ok || id == 0 {
		return 0
	}
	return peerID(&tg.PeerChannel{ChannelID: id})
}

// sender names who wrote m, for the line under a group's title, or says
// the account did.
func (l *list) sender(m tg.MessageClass) (name string, outgoing bool) {
	var from tg.PeerClass
	switch m := m.(type) {
	case *tg.Message:
		if m.Out {
			return "", true
		}
		from, _ = m.GetFromID()
	case *tg.MessageService:
		return "", false
	}
	if user, ok := from.(*tg.PeerUser); ok {
		if u := l.users[user.UserID]; u != nil {
			return u.FirstName, false
		}
	}
	return "", false
}

// userName is a user's name, empty for a deleted account: the interface
// names it as a chat's title.
func userName(u *tg.User) string {
	if u.Deleted {
		return ""
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

// personName is a user's name where it goes into what the store keeps of
// messages, reactions and the like, a deleted account's in the
// interface's language when it was kept.
func personName(u *tg.User) string {
	if u.Deleted {
		return localization.For(interfaceLanguage()).T("chat.deleted_account")
	}
	return userName(u)
}

// preview is what the chat list shows of m, and its time.
func preview(m tg.MessageClass) (model.Preview, time.Time) {
	switch m := m.(type) {
	case *tg.Message:
		p := model.Preview{Text: strings.Join(strings.Fields(m.Message), " ")}
		if rich, ok := m.GetRichMessage(); ok {
			page := convertRich(rich)
			p.Text = strings.Join(strings.Fields(page.Summary().Text), " ")
			if p.Text == "" {
				p.Media = richFallbackKey(*page)
			}
		}
		if media, emoji := mediaKey(m.Media); media != "" || emoji != "" {
			p.Media, p.Emoji = media, emoji
		}
		return p, time.Unix(int64(m.Date), 0)
	case *tg.MessageService:
		return model.Preview{Media: "preview.service"}, time.Unix(int64(m.Date), 0)
	}
	return model.Preview{}, time.Time{}
}

// mediaKey is the key of the text naming what a message carries, and an
// emoji shown before it (a sticker's) or alone (a dice's).
func mediaKey(media tg.MessageMediaClass) (key, emoji string) {
	switch media := media.(type) {
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		return "", ""
	case *tg.MessageMediaPhoto:
		return "preview.photo", ""
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return "preview.location", ""
	case *tg.MessageMediaContact:
		return "preview.contact", ""
	case *tg.MessageMediaPoll:
		return "preview.poll", ""
	case *tg.MessageMediaDice:
		return "", media.Emoticon
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return "preview.file", ""
		}
		// Named by the kind the history shows it as.
		kind, meta, _ := documentMedia(doc)
		switch kind {
		case model.MessageSticker:
			return "preview.sticker", meta.Emoji
		case model.MessageVoice:
			return "preview.voice", ""
		case model.MessageMusic:
			return "preview.music", ""
		case model.MessageRound:
			return "preview.round", ""
		case model.MessageVideo:
			return "preview.video", ""
		case model.MessageGIF:
			return "preview.gif", ""
		}
		return "preview.file", ""
	}
	return "preview.attachment", ""
}

// page is where the next messages.getDialogs starts.
type page struct {
	date, id int
	peer     tg.InputPeerClass
}

// nextPage continues after the last dialog of the page just read.
func (l *list) nextPage(dialogs tg.ModifiedMessagesDialogs) (page, bool) {
	all := dialogs.GetDialogs()
	for i := len(all) - 1; i >= 0; i-- {
		d, ok := all[i].(*tg.Dialog)
		if !ok {
			continue
		}
		m, ok := l.messages[[2]int64{peerID(d.Peer), int64(d.TopMessage)}]
		if !ok {
			continue
		}
		peer, ok := l.inputPeer(d.Peer)
		if !ok {
			continue
		}
		var date int
		switch m := m.(type) {
		case *tg.Message:
			date = m.Date
		case *tg.MessageService:
			date = m.Date
		}
		return page{date: date, id: d.TopMessage, peer: peer}, true
	}
	return page{}, false
}

func (l *list) inputPeer(p tg.PeerClass) (tg.InputPeerClass, bool) {
	switch p := p.(type) {
	case *tg.PeerUser:
		if u := l.users[p.UserID]; u != nil {
			return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, true
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, true
	case *tg.PeerChannel:
		if c := l.channels[p.ChannelID]; c != nil {
			return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}, true
		}
	}
	return nil, false
}

// snapshot returns the chats read so far and the folders over them. The
// slices are fresh: the store hands them to the UI as they are.
func (l *list) snapshot(filters []tg.DialogFilterClass) ([]model.Chat, []model.Folder) {
	chats := append([]model.Chat(nil), l.chats...)
	folders := folderList(l.self, filters, maps.Clone(l.entries))
	return chats, folders
}

// folder turns a Telegram folder into the model's, with its rules as Match.
// The rules are Telegram's own: the chats a folder names are always in it and
// the ones it excludes never are; the rest are in it by kind, unless muted,
// read or archived chats are left out.
func (l *list) folder(f tg.DialogFilterClass, entries map[int64]entry) (model.Folder, bool) {
	ids := func(peers []tg.InputPeerClass) map[int64]bool {
		set := make(map[int64]bool, len(peers))
		for _, p := range peers {
			set[inputPeerID(p, l.self)] = true
		}
		return set
	}
	switch f := f.(type) {
	case *tg.DialogFilter:
		included := ids(append(append([]tg.InputPeerClass(nil), f.PinnedPeers...), f.IncludePeers...))
		excluded := ids(f.ExcludePeers)
		var kinds []model.ChatKind
		if f.Contacts || f.NonContacts {
			kinds = append(kinds, model.KindUser)
		}
		if f.Groups {
			kinds = append(kinds, model.KindGroup)
		}
		if f.Broadcasts {
			kinds = append(kinds, model.KindChannel)
		}
		if f.Bots {
			kinds = append(kinds, model.KindBot)
		}
		return model.Folder{
			ID:    int64(f.ID),
			Title: f.Title.Text,
			Kinds: kinds,
			Match: func(c model.Chat) bool {
				if included[c.ID] {
					return true
				}
				if excluded[c.ID] {
					return false
				}
				e := entries[c.ID]
				if f.ExcludeMuted && c.Muted {
					return false
				}
				if f.ExcludeRead && c.Unread == 0 && !e.unreadMark {
					return false
				}
				// Only the main list is loaded, so ExcludeArchived holds.
				switch c.Kind {
				case model.KindUser, model.KindSaved:
					return (f.Contacts && e.contact) || (f.NonContacts && !e.contact)
				case model.KindBot:
					return f.Bots
				case model.KindGroup:
					return f.Groups
				case model.KindChannel:
					return f.Broadcasts
				}
				return false
			},
		}, true
	case *tg.DialogFilterChatlist:
		included := ids(append(append([]tg.InputPeerClass(nil), f.PinnedPeers...), f.IncludePeers...))
		return model.Folder{
			ID:    int64(f.ID),
			Title: f.Title.Text,
			Match: func(c model.Chat) bool { return included[c.ID] },
		}, true
	}
	return model.Folder{}, false // the "All chats" entry
}

// liveFromDialogs marks live the chats whose newest span reaches the newest
// message the dialog list names (tops), read at liveEpoch: the messages
// that come after it come as updates. See conversation.live.
func (s *Store) liveFromDialogs(ctx context.Context, tops map[int64]int, liveEpoch uint64) error {
	c := s.history
	c.mu.Lock()
	cache := c.cache
	c.mu.Unlock()
	if cache == nil {
		return nil
	}
	for chat, top := range tops {
		span, ok, _, err := cache.SpanOf(ctx, chat, 0)
		if err != nil {
			return err
		}
		if ok && span.High >= top {
			c.mu.Lock()
			c.startLive(chat, liveEpoch)
			c.mu.Unlock()
		}
	}
	return nil
}
