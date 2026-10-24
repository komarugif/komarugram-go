// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"fmt"
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
						return f.Match(c)
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
