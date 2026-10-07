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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
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
	picker  pickerCache
	themes  themeCatalogue
	changed func()
	notices atomic.Pointer[func(model.MessageNotice)]
	history *conversation

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
	blocked       blockedState
	bots          botState
	// dialogsLoading: loadDialogs runs.
	dialogsLoading atomic.Bool
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
	return s.loadDialogs(ctx, api, self.ID)
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
		tops := map[int64]int{}
		for _, d := range dialogs.GetDialogs() {
			if d, ok := d.(*tg.Dialog); ok {
				s.history.top[peerID(d.Peer)] = max(s.history.top[peerID(d.Peer)], d.TopMessage)
				tops[peerID(d.Peer)] = d.TopMessage
			}
		}
		s.history.mu.Unlock()
		if err := s.liveFromDialogs(ctx, tops, liveEpoch); err != nil {
			return err
		}
		added := l.add(dialogs)
		chats, folders := l.snapshot(filters.Filters)
		s.publish(func() { s.chats, s.folders = chats, folders })

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
	messages map[[2]int64]tg.MessageClass

	chats   []model.Chat
	entries map[int64]entry
	seen    map[int64]bool
	// pins counts the pinned dialogs seen; they come first, in pin order.
	pins int
}

func newList(self int64) *list {
	return &list{
		self:     self,
		users:    map[int64]*tg.User{},
		groups:   map[int64]*tg.Chat{},
		channels: map[int64]*tg.Channel{},
		titles:   map[int64]string{},
		messages: map[[2]int64]tg.MessageClass{},
		entries:  map[int64]entry{},
		seen:     map[int64]bool{},
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
		}
	}
	for _, m := range page.GetMessages() {
		if peer, ok := messagePeer(m); ok {
			l.messages[[2]int64{peerID(peer), int64(m.GetID())}] = m
		}
	}
	added := 0
	for _, d := range page.GetDialogs() {
		dialog, ok := d.(*tg.Dialog)
		if !ok {
			continue // a folder entry: the archive is not loaded yet
		}
		id := peerID(dialog.Peer)
		if l.seen[id] {
			continue
		}
		l.seen[id] = true
		chat, e := l.chat(dialog)
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
		ID:     id,
		Unread: d.UnreadCount,
		Pinned: d.Pinned,
	}
	chat.Muted = mutedNow(d.NotifySettings)
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
			e.contact = u.Contact || u.Self
			switch {
			case u.Self:
				chat.Kind, chat.Title = model.KindSaved, "Избранное"
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
			chat.Forum = c.Forum
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
		chat.LastMessage, chat.LastTime = preview(m)
		if service, ok := m.(*tg.MessageService); ok {
			chat.LastMessage = l.servicePreview(service, chat)
		}
		if chat.Kind == model.KindGroup {
			chat.LastSender = l.sender(m)
		}
	}
	return chat, e
}

// sender names who wrote m, for the line under a group's title.
func (l *list) sender(m tg.MessageClass) string {
	var from tg.PeerClass
	switch m := m.(type) {
	case *tg.Message:
		if m.Out {
			return "Вы"
		}
		from, _ = m.GetFromID()
	case *tg.MessageService:
		return ""
	}
	if user, ok := from.(*tg.PeerUser); ok {
		if u := l.users[user.UserID]; u != nil {
			return u.FirstName
		}
	}
	return ""
}

func userName(u *tg.User) string {
	if u.Deleted {
		return "Удалённый аккаунт"
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

// preview is the text shown for a chat's last message, and its time.
func preview(m tg.MessageClass) (string, time.Time) {
	switch m := m.(type) {
	case *tg.Message:
		text := strings.Join(strings.Fields(m.Message), " ")
		if rich, ok := m.GetRichMessage(); ok {
			page := convertRich(rich)
			text = strings.Join(strings.Fields(page.Summary().Text), " ")
			if text == "" {
				text = richFallbackName(*page)
			}
		}
		if media := mediaName(m.Media); media != "" {
			if text == "" {
				text = media
			} else {
				text = media + ", " + text
			}
		}
		return text, time.Unix(int64(m.Date), 0)
	case *tg.MessageService:
		return "Служебное сообщение", time.Unix(int64(m.Date), 0)
	}
	return "", time.Time{}
}

func mediaName(media tg.MessageMediaClass) string {
	switch media := media.(type) {
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		return ""
	case *tg.MessageMediaPhoto:
		return "Фото"
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return "Геопозиция"
	case *tg.MessageMediaContact:
		return "Контакт"
	case *tg.MessageMediaPoll:
		return "Опрос"
	case *tg.MessageMediaDice:
		return media.Emoticon
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return "Файл"
		}
		for _, a := range doc.Attributes {
			switch a := a.(type) {
			case *tg.DocumentAttributeSticker:
				return strings.TrimSpace(a.Alt + " Стикер")
			case *tg.DocumentAttributeAudio:
				if a.Voice {
					return "Голосовое сообщение"
				}
				return "Музыка"
			case *tg.DocumentAttributeVideo:
				if a.RoundMessage {
					return "Видеосообщение"
				}
				return "Видео"
			case *tg.DocumentAttributeAnimated:
				return "GIF"
			}
		}
		return "Файл"
	}
	return "Вложение"
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
	entries := make(map[int64]entry, len(l.entries))
	for id, e := range l.entries {
		entries[id] = e
	}
	var folders []model.Folder
	for _, f := range filters {
		if folder, ok := l.folder(f, entries); ok {
			folders = append(folders, folder)
		}
	}
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
