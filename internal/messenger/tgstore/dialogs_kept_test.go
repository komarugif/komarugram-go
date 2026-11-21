// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// dialogServer hands out a list of private chats as Telegram does: the
// newest first, 100 at most per page, from after the offset's peer.
type dialogServer struct {
	mu    sync.Mutex
	chats map[int64]*fakeDialog // by user id
	asked int
}

type fakeDialog struct {
	top, date, unread int
	contact           bool
}

func newDialogServer(n int) *dialogServer {
	d := &dialogServer{chats: map[int64]*fakeDialog{}}
	for i := range n {
		d.chats[int64(1000+i)] = &fakeDialog{top: 10, date: 100000 - i}
	}
	return d
}

func (d *dialogServer) order() []int64 {
	ids := make([]int64, 0, len(d.chats))
	for id := range d.chats {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return d.chats[ids[i]].date > d.chats[ids[j]].date })
	return ids
}

func (d *dialogServer) page(ids []int64) (dialogs []tg.DialogClass, msgs []tg.MessageClass, users []tg.UserClass) {
	for _, id := range ids {
		c := d.chats[id]
		dialogs = append(dialogs, &tg.Dialog{Peer: &tg.PeerUser{UserID: id}, TopMessage: c.top, UnreadCount: c.unread, ReadInboxMaxID: c.top - c.unread})
		msgs = append(msgs, &tg.Message{ID: c.top, PeerID: &tg.PeerUser{UserID: id}, Message: "m", Date: c.date})
		users = append(users, &tg.User{ID: id, AccessHash: 1, FirstName: fmt.Sprint("u", id), Contact: c.contact})
	}
	return
}

func (d *dialogServer) invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch r := in.(type) {
	case *tg.MessagesGetDialogFiltersRequest:
		*out.(*tg.MessagesDialogFilters) = tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "Contacts"}, Contacts: true}}}
	case *tg.MessagesGetDialogsRequest:
		if r.FolderID == 1 {
			out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{}
			return nil
		}
		d.asked++
		ids := d.order()
		from := 0
		if p, ok := r.OffsetPeer.(*tg.InputPeerUser); ok {
			for i, id := range ids {
				if id == p.UserID {
					from = i + 1
				}
			}
		}
		to := min(from+min(r.Limit, 100), len(ids))
		dialogs, msgs, users := d.page(ids[from:to])
		if len(ids) <= 100 {
			out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: dialogs, Messages: msgs, Users: users}
		} else {
			out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogsSlice{Count: len(ids), Dialogs: dialogs, Messages: msgs, Users: users}
		}
	case *tg.MessagesGetPeerDialogsRequest:
		var ids []int64
		for _, p := range r.Peers {
			ids = append(ids, p.(*tg.InputDialogPeer).Peer.(*tg.InputPeerUser).UserID)
		}
		dialogs, msgs, users := d.page(ids)
		*out.(*tg.MessagesPeerDialogs) = tg.MessagesPeerDialogs{Dialogs: dialogs, Messages: msgs, Users: users}
	default:
		return fmt.Errorf("unexpected %T", in)
	}
	return nil
}

// load loads the dialogs and returns how many pages it asked for.
func (d *dialogServer) load(t *testing.T, s *Store) int {
	t.Helper()
	d.mu.Lock()
	before := d.asked
	d.mu.Unlock()
	if err := s.loadDialogs(context.Background(), s.history.api, 1); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked - before
}

func chatIDs(chats []model.Chat) []int64 {
	ids := make([]int64, len(chats))
	for i, c := range chats {
		ids[i] = c.ID
	}
	return ids
}

func user(id int64) int64 { return peerID(&tg.PeerUser{UserID: id}) }

// Folders and their membership used to disappear on an offline restart.
func TestDialogFoldersOffline(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "h")
	open := func() *Store {
		s := New(nil)
		if err := s.Configure(ctx, "a", path, nil); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	srv := newDialogServer(2)
	srv.chats[1001].contact = true
	shared := &tg.DialogFilterChatlist{
		ID: 3, Title: tg.TextWithEntities{Text: "Shared"}, HasMyInvites: true,
		PinnedPeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 42, AccessHash: 99}, &tg.InputPeerSelf{}},
	}
	shared.SetColor(0)
	response := tg.MessagesDialogFilters{TagsEnabled: true, Filters: []tg.DialogFilterClass{
		shared, &tg.DialogFilterDefault{},
		&tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "Contacts"}, Contacts: true},
	}}
	api := tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if r, ok := in.(*tg.MessagesGetDialogsRequest); ok && r.FolderID == 1 {
			archive := newDialogServer(0)
			archive.chats[74] = &fakeDialog{top: 7, date: 4000, contact: true}
			dialogs, msgs, users := archive.page([]int64{74})
			out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: dialogs, Messages: msgs, Users: users}
			return nil
		}
		if _, ok := in.(*tg.MessagesGetDialogFiltersRequest); ok {
			*out.(*tg.MessagesDialogFilters) = response
			return nil
		}
		return srv.invoke(ctx, in, out)
	}))
	s.history.api = api
	srv.load(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	offline := open()
	defer offline.Close()
	folders := offline.Folders()
	if len(folders) != 3 || folders[0].ID != 3 || folders[1].ID != 0 || folders[2].Title != "Contacts" {
		t.Fatalf("folders after offline restart: %v", folders)
	}
	chats := offline.Chats()
	if len(chats) != 2 || folders[2].Contains(chats[0]) || !folders[2].Contains(chats[1]) {
		t.Fatal("offline folder lost its contact rule")
	}
	members := folders[2].Chats(offline.FolderDialogs())
	if len(members) != 2 || !slices.ContainsFunc(members, func(c model.Chat) bool { return c.ID == user(74) && c.Archived && !folders[1].Contains(c) }) {
		t.Fatalf("archived contact after restart: %v", chatIDs(members))
	}
	if !folders[0].Contains(model.Chat{ID: user(1)}) {
		t.Fatal("offline folder lost its Saved Messages pin")
	}
	if !offline.filters.TagsEnabled || len(offline.filters.Filters) != 3 {
		t.Fatal("incomplete cached filter response")
	}
	if _, ok := offline.filters.Filters[1].(*tg.DialogFilterDefault); !ok {
		t.Fatal("lost the position of All chats")
	}
	if got := offline.filters.Filters[0]; !reflect.DeepEqual(got, shared) {
		t.Fatalf("shared folder after restart: %#v, want %#v", got, shared)
	}
	// Another session removes all folders. An empty response replaces the
	// previous snapshot, including the tag switch, even without dialog pages.
	response = tg.MessagesDialogFilters{}
	offline.Remember(model.Profile{ID: 1})
	offline.history.api = api
	if err := offline.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateDialogFilters{}}); err != nil {
		t.Fatal(err)
	}
	if len(offline.Folders()) != 1 || offline.Folders()[0].ID != 0 || offline.filters.TagsEnabled {
		t.Fatal("did not apply empty server snapshot")
	}
	_, cached, err := cachedDialogFilters(ctx, offline.Cache())
	if err != nil || cached.TagsEnabled || len(cached.Filters) != 0 {
		t.Fatalf("stale filters in the cache: %v, %v", cached, err)
	}
}

func TestFolderSettingsSave(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	original := &tg.DialogFilterChatlist{ID: 2, Title: tg.TextWithEntities{Text: "Shared"}, PinnedPeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 42, AccessHash: 91}}}
	filters := tg.MessagesDialogFilters{TagsEnabled: true, Filters: []tg.DialogFilterClass{original}}
	var writes int
	api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch r := in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			*out.(*tg.MessagesDialogFilters) = filters
		case *tg.MessagesUpdateDialogFilterRequest:
			f, ok := r.Filter.(*tg.DialogFilter)
			if !ok || r.ID != 3 || f.ID != 3 || !f.Groups || !f.NonContacts || !f.Bots || f.Broadcasts || f.Contacts || f.Title.Text != "Study" {
				return fmt.Errorf("unexpected creation: %v", r)
			}
			filters.Filters = append(slices.Clone(filters.Filters), f)
			writes++
			out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesUpdateDialogFiltersOrderRequest:
			if !slices.Equal(r.Order, []int{0, 3, 2}) {
				return fmt.Errorf("unexpected order: %v", r.Order)
			}
			writes++
			out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		default:
			return fmt.Errorf("unexpected %T", in)
		}
		return nil
	}))
	s.history.api = api
	if err := s.loadDialogFilters(ctx, api, 1); err != nil {
		t.Fatal(err)
	}
	types := model.FolderTypes{NonContacts: true, Groups: true, Bots: true}
	if err := s.SaveFolder(ctx, nil, model.Folder{ID: 3, Title: "  Study  ", FolderTypes: types}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFolder(ctx, nil, model.Folder{ID: 3, Title: "Study", FolderTypes: types}); err != nil {
		t.Fatal(err)
	}
	before := []int64{0, 2, 3}
	after := []int64{0, 3, 2}
	if err := s.ReorderFolders(ctx, before, []int64{2, 0, 3}); err != model.ErrFolderOrder {
		t.Fatalf("moving All chats: %v", err)
	}
	if err := s.ReorderFolders(ctx, []int64{0, 2}, after); err != model.ErrFolderOrder {
		t.Fatalf("stale order: %v", err)
	}
	if err := s.ReorderFolders(ctx, before, after); err != nil {
		t.Fatal(err)
	}
	if writes != 2 || !slices.Equal(after, model.FolderIDs(s.Folders())) {
		t.Fatal("folder writes or resulting order differ")
	}
	_, cached, err := cachedDialogFilters(ctx, s.Cache())
	if err != nil || !cached.TagsEnabled || len(cached.Filters) != 3 || !reflect.DeepEqual(cached.Filters[2], original) {
		t.Fatalf("cached ordered folders: %v, %v", cached, err)
	}
}

// Preserve fields outside the editor and peers missing from the dialog cache.
func TestFolderEditorRoundTrip(t *testing.T) {
	ctx := t.Context()
	s := testStore(t)
	pin := &tg.InputPeerChannel{ChannelID: 1234, AccessHash: 775}
	title := tg.TextWithEntities{Text: "Work", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 4}}}
	ordinary := &tg.DialogFilter{ID: 7, Title: title, TitleNoanimate: true, Bots: true, Emoticon: "💡", PinnedPeers: []tg.InputPeerClass{pin}}
	ordinary.SetColor(0)
	shared := &tg.DialogFilterChatlist{ID: 8, HasMyInvites: true, Title: title, PinnedPeers: []tg.InputPeerClass{pin}}
	shared.SetColor(0)
	filters := tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{ordinary, shared}, TagsEnabled: true}
	var writes int
	api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch r := in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			*out.(*tg.MessagesDialogFilters) = filters
		case *tg.MessagesUpdateDialogFilterRequest:
			var buf bin.Buffer
			if err := r.Filter.Encode(&buf); err != nil {
				return err
			}
			encoded, err := tg.DecodeDialogFilter(&buf)
			if err != nil {
				return err
			}
			writes++
			switch f := encoded.(type) {
			case *tg.DialogFilter:
				if color, set := f.GetColor(); !set || color != 0 || !f.TitleNoanimate || !reflect.DeepEqual(f.Title, title) || f.Bots || !f.ExcludeArchived || f.Emoticon != "" || !reflect.DeepEqual(f.PinnedPeers, []tg.InputPeerClass{pin}) || !reflect.DeepEqual(f.IncludePeers, []tg.InputPeerClass{&tg.InputPeerUser{UserID: 19, AccessHash: 72}}) {
					return fmt.Errorf("bad edited filter %v", f)
				}
				filters.Filters[0] = encoded
				return context.DeadlineExceeded
			case *tg.DialogFilterChatlist:
				if color, set := f.GetColor(); !set || color != 0 || !f.HasMyInvites || f.Title.Text != "New" || len(f.Title.Entities) != 0 || !reflect.DeepEqual(f.PinnedPeers, []tg.InputPeerClass{pin}) {
					return fmt.Errorf("bad shared folder %v", f)
				}
				filters.Filters[1] = encoded
			default:
				return fmt.Errorf("unexpected filter %T", f)
			}
			out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		default:
			return fmt.Errorf("unexpected RPC %T", in)
		}
		return nil
	}))
	s.history.api = api
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 19, AccessHash: 72}}, nil)
	if err := s.loadDialogFilters(ctx, api, 1); err != nil {
		t.Fatal(err)
	}
	base := s.Folders()[1]
	f := base
	f.Bots = false
	f.ExcludeArchived = true
	f.Emoticon = ""
	f.Included = []int64{19}
	if err := s.SaveFolder(ctx, &base, f); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := s.SaveFolder(ctx, &base, f); err != nil {
		t.Fatal(err)
	}
	stale := base
	stale.Title = "Stale"
	if err := s.SaveFolder(ctx, &base, stale); err != model.ErrFolderConflict {
		t.Fatalf("stale draft: %v", err)
	}
	base = s.Folders()[2]
	f = base
	f.Title = "New"
	if err := s.SaveFolder(ctx, &base, f); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Fatalf("%d writes", writes)
	}
}

// A load of the dialogs stops at the first page that tells nothing new:
// the pages after it are of chats that changed no more, kept from the load
// before. Every page of a long list was asked for on every start.
func TestDialogsStopAtAnUnchangedPage(t *testing.T) {
	s := testStore(t)
	srv := newDialogServer(250)
	s.history.api = tg.NewClient(telegram.InvokeFunc(srv.invoke))
	if n := srv.load(t, s); n != 3 {
		t.Fatalf("the first load asked for %d pages, want all 3", n)
	}
	first := chatIDs(s.Chats())
	if len(first) != 250 {
		t.Fatalf("%d chats, want 250", len(first))
	}
	if n := srv.load(t, s); n != 1 {
		t.Fatalf("nothing changed, and the load asked for %d pages, want 1", n)
	}
	if got := chatIDs(s.Chats()); fmt.Sprint(got) != fmt.Sprint(first) {
		t.Fatal("the list changed with nothing new")
	}
	// A chat of the third page has a new message: it comes first, and the
	// second page tells nothing new.
	srv.mu.Lock()
	srv.chats[1230].top, srv.chats[1230].date, srv.chats[1230].unread = 11, 200000, 1
	srv.mu.Unlock()
	if n := srv.load(t, s); n != 2 {
		t.Fatalf("one chat moved up, and the load asked for %d pages, want 2", n)
	}
	got := s.Chats()
	if len(got) != 250 || got[0].ID != user(1230) || got[0].Unread != 1 {
		t.Fatalf("%d chats, the first %d with %d unread", len(got), got[0].ID, got[0].Unread)
	}
	if n := len(slices.DeleteFunc(chatIDs(got), func(id int64) bool { return id != user(1230) })); n != 1 {
		t.Fatalf("the chat that moved up is in the list %d times", n)
	}
}

// Chats Telegram no longer hands out, older than the oldest it did, stay
// in the list; a chat newer than that it did not hand out is gone.
func TestDialogsKeepTheOldestChats(t *testing.T) {
	s := testStore(t)
	srv := newDialogServer(150)
	s.history.api = tg.NewClient(telegram.InvokeFunc(srv.invoke))
	srv.load(t, s)
	srv.mu.Lock()
	for id := int64(1120); id < 1150; id++ {
		delete(srv.chats, id) // too old to be handed out
	}
	delete(srv.chats, 1005) // deleted
	for _, c := range srv.chats {
		c.unread = 1 // every page tells something new
	}
	srv.mu.Unlock()
	srv.load(t, s)
	got := map[int64]bool{}
	for _, c := range s.Chats() {
		got[c.ID] = true
	}
	if len(got) != 149 || got[user(1005)] || !got[user(1149)] {
		t.Fatalf("%d chats, the deleted %t, the oldest %t; want 149, the oldest kept", len(got), got[user(1005)], got[user(1149)])
	}
}

// What a stopped load did not read again is kept from the load before, as
// the folders' rules ask: a contact deep in the list is in the contacts'
// folder. A chat archived elsewhere leaves the list, and one brought back
// from the archive is asked for, as the load may stop before its place.
func TestDialogsKeptFoldersAndArchive(t *testing.T) {
	s := testStore(t)
	srv := newDialogServer(250)
	srv.chats[1240].contact = true
	s.history.api = tg.NewClient(telegram.InvokeFunc(srv.invoke))
	srv.load(t, s)
	srv.load(t, s)
	contacts := func(id int64) bool {
		for _, f := range s.Folders() {
			if f.ID == 2 {
				for _, c := range s.Chats() {
					if c.ID == id {
						return f.Contains(c)
					}
				}
			}
		}
		return false
	}
	if !contacts(user(1240)) {
		t.Fatal("a contact deep in the list left the contacts' folder")
	}
	// Kept for the next start too.
	var kept map[int64]keptEntry
	if _, err := s.Cache().Get(context.Background(), "dialog-entries", &kept); err != nil || !readEntries(kept)[user(1240)].contact {
		t.Fatalf("the cache keeps %v (%v)", kept[user(1240)], err)
	}

	s.applyFolderPeers(&tg.UpdateFolderPeers{FolderPeers: []tg.FolderPeer{{Peer: &tg.PeerUser{UserID: 1240}, FolderID: 1}}})
	if slices.Contains(chatIDs(s.Chats()), user(1240)) {
		t.Fatal("a chat archived stays in the list")
	}
	s.applyFolderPeers(&tg.UpdateFolderPeers{FolderPeers: []tg.FolderPeer{{Peer: &tg.PeerUser{UserID: 1240}, FolderID: 0}}})
	deadline := time.Now().Add(3 * time.Second)
	for !slices.Contains(chatIDs(s.Chats()), user(1240)) {
		if time.Now().After(deadline) {
			t.Fatal("a chat brought back from the archive is not in the list")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !contacts(user(1240)) {
		t.Fatal("the chat brought back is not in the contacts' folder")
	}
}
