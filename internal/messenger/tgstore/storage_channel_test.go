// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func gifItems(ids ...int) []model.PickerItem {
	out := make([]model.PickerItem, len(ids))
	for i, id := range ids {
		out[i] = model.PickerItem{ID: fmt.Sprintf("document/%d", id), DocumentID: int64(id)}
	}
	return out
}

func gifIDs(items []model.PickerItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}

// The list is ordered by use, so that only how many are missing tells which were
// pushed out: those at its end.
func TestEvictedGIFs(t *testing.T) {
	for _, c := range []struct {
		name     string
		old, new []int
		limit    int
		want     []int
	}{
		{"new at the limit", []int{1, 2, 3}, []int{9, 1, 2}, 3, []int{3}},
		{"used, moved up, nothing out", []int{1, 2, 3}, []int{3, 1, 2}, 3, nil},
		{"removed under the limit", []int{1, 2, 3}, []int{1, 3}, 5, nil},
		{"removed at the limit", []int{1, 2, 3}, []int{1, 3}, 3, nil},
		{"two new, one removed", []int{1, 2, 3, 4}, []int{8, 9, 1, 3}, 4, []int{2, 4}},
		{"removed and new with room", []int{1, 2, 3}, []int{9, 1, 3}, 5, nil},
		{"a Premium that ended", []int{1, 2, 3, 4}, []int{1, 2}, 2, []int{3, 4}},
		{"one out of three missing", []int{1, 2, 3, 4}, []int{9, 1}, 4, []int{4}},
		{"first read", nil, []int{1, 2}, 2, nil},
		{"limit not known", []int{1, 2}, []int{9, 1}, 0, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := gifIDs(evictedGIFs(gifItems(c.old...), gifItems(c.new...), c.limit))
			if want := gifIDs(gifItems(c.want...)); !slices.Equal(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

// storageAPI is Telegram for the channel: a list of saved GIFs that sending moves
// up, as the server does and forwarding does not.
type storageAPI struct {
	mu                            sync.Mutex
	list                          []int64
	limit                         int
	messages                      map[int]int64
	next                          int
	forwards                      []int
	sends                         []int64
	flood                         int
	refused                       map[int64]bool
	expired                       map[int64]bool
	files                         int
	index                         []byte
	indexDoc                      int64
	found                         []tg.ChatClass
	dialogs                       []tg.ChatClass
	created                       int
	archived                      bool
	muted                         bool
	deleted                       []int
	unknown []string
	indexReads int
	parts, written                []byte
	edits                         []int
	pinned, pinnedIndex, created2 int
}

func (a *storageAPI) doc(id int64) *tg.Document {
	return &tg.Document{ID: id, AccessHash: id * 10, FileReference: []byte("r"), MimeType: "video/mp4", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}}}
}

func (a *storageAPI) sent(random int64, id int, media tg.MessageMediaClass) *tg.Updates {
	return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: id, RandomID: random}}}
}

func (a *storageAPI) client() *tg.Client {
	return composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch r := in.(type) {
		case *tg.MessagesGetSavedGifsRequest:
			gifs := &tg.MessagesSavedGifs{Hash: 7}
			for _, id := range a.list {
				gifs.Gifs = append(gifs.Gifs, a.doc(id))
			}
			return gifs, nil
		case *tg.MessagesSendMediaRequest:
			if _, ok := r.Media.(*tg.InputMediaUploadedDocument); ok {
				a.next++
				a.pinnedIndex, a.written = a.next, a.parts
				a.parts = nil
				a.created2++
				return a.sent(r.RandomID, a.next, nil), nil
			}
			media, ok := r.Media.(*tg.InputMediaDocument)
			if !ok {
				return nil, fmt.Errorf("sent %T", r.Media)
			}
			if a.flood > 0 {
				a.flood--
				return nil, tgerr.New(420, "FLOOD_WAIT_0")
			}
			doc := media.ID.(*tg.InputDocument)
			if a.expired[doc.ID] {
				delete(a.expired, doc.ID)
				return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			if a.refused[doc.ID] {
				return nil, tgerr.New(400, "MEDIA_EMPTY")
			}
			a.next++
			a.messages[a.next] = doc.ID
			a.sends = append(a.sends, doc.ID)
			a.list = append([]int64{doc.ID}, slices.DeleteFunc(a.list, func(id int64) bool { return id == doc.ID })...)
			if len(a.list) > a.limit {
				a.list = a.list[:a.limit]
			}
			return a.sent(r.RandomID, a.next, nil), nil
		case *tg.UploadSaveFilePartRequest:
			a.parts = append(a.parts, r.Bytes...)
			return &tg.BoolTrue{}, nil
		case *tg.MessagesEditMessageRequest:
			a.edits = append(a.edits, r.ID)
			a.written, a.parts = a.parts, nil
			return &tg.Updates{}, nil
		case *tg.MessagesUpdatePinnedMessageRequest:
			a.pinned = r.ID
			return &tg.Updates{}, nil
		case *tg.MessagesForwardMessagesRequest:
			a.next++
			a.forwards = append(a.forwards, r.ID[0])
			return a.sent(r.RandomID[0], a.next, nil), nil
		case *tg.MessagesSearchRequest:
			out := &tg.MessagesChannelMessages{}
			for id, doc := range a.messages {
				if id > r.MinID && (r.OffsetID == 0 || id < r.OffsetID) {
					out.Messages = append(out.Messages, &tg.Message{ID: id, Media: &tg.MessageMediaDocument{Document: a.doc(doc)}, PeerID: &tg.PeerChannel{ChannelID: 50}})
				}
			}
			return out, nil
		case *tg.ChannelsGetFullChannelRequest:
			a.indexReads++
			return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ChatPhoto: &tg.PhotoEmpty{}, ID: 50, PinnedMsgID: map[bool]int{true: 1000}[a.index != nil]}}, nil
		case *tg.ChannelsGetMessagesRequest:
			out := &tg.MessagesChannelMessages{}
			for _, raw := range r.ID {
				id := raw.(*tg.InputMessageID).ID
				switch doc, ok := a.messages[id]; {
				case id == 1000 && a.index != nil:
					out.Messages = append(out.Messages, &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 50}, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: a.indexDoc, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: storageIndexName}}}}})
				case ok:
					out.Messages = append(out.Messages, &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 50}, Media: &tg.MessageMediaDocument{Document: a.doc(doc)}})
				default:
					out.Messages = append(out.Messages, &tg.MessageEmpty{ID: id})
				}
			}
			return out, nil
		case *tg.ContactsSearchRequest:
			return &tg.ContactsFound{Chats: a.found}, nil
		case *tg.ChannelsCreateChannelRequest:
			a.created++
			return &tg.Updates{Chats: []tg.ChatClass{&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 50, AccessHash: 5, Title: r.Title, Creator: true, Broadcast: true}}}, nil
		case *tg.FoldersEditPeerFoldersRequest:
			a.archived = true
			return &tg.Updates{}, nil
		case *tg.AccountUpdateNotifySettingsRequest:
			a.muted = true
			return &tg.BoolTrue{}, nil
		case *tg.MessagesGetDialogsRequest:
			return &tg.MessagesDialogs{Chats: a.dialogs}, nil
		case *tg.ChannelsDeleteMessagesRequest:
			a.deleted = append(a.deleted, r.ID...)
			return &tg.MessagesAffectedMessages{}, nil
		}
		a.unknown = append(a.unknown, fmt.Sprintf("%T", in))
		return nil, fmt.Errorf("unexpected %T", in)
	})
}

func newStorageAPI(limit int, list ...int64) *storageAPI {
	return &storageAPI{list: list, limit: limit, messages: map[int]int64{}, refused: map[int64]bool{}, expired: map[int64]bool{}}
}

// storageStore is a store bound to nothing yet, connected to api.
func storageStore(t *testing.T, api *storageAPI) *Store {
	t.Helper()
	pace, debounce := storagePace, storageDebounce
	storagePace, storageDebounce = 0, 0
	t.Cleanup(func() { storagePace, storageDebounce = pace, debounce })
	s := testStore(t)
	s.history.api = api.client()
	s.SetLocalPremium(model.AllLocalPremium)
	return s
}

func waitStorage(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestStorageFindsChannelByItsName(t *testing.T) {
	api := newStorageAPI(200)
	api.found = []tg.ChatClass{
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 70, AccessHash: 7, Title: "Something else", Creator: true, Broadcast: true},
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 40, AccessHash: 6, Title: model.StorageChannelTitle, Broadcast: true},
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 51, AccessHash: 6, Title: model.StorageChannelTitle, Creator: true, Broadcast: true},
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 50, AccessHash: 5, Title: model.StorageChannelTitle, Creator: true, Broadcast: true},
	}
	s := storageStore(t, api)
	found, err := s.FindStorageChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !found.Bound || s.collections.storage.ChannelID != 50 {
		t.Fatalf("%+v, channel %d", found, s.collections.storage.ChannelID)
	}
	if !api.archived || !api.muted || api.created != 0 {
		t.Fatalf("archived %v, muted %v, created %d", api.archived, api.muted, api.created)
	}
}

func TestStorageOffersOwnPrivateChannels(t *testing.T) {
	api := newStorageAPI(200)
	api.dialogs = []tg.ChatClass{
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 70, AccessHash: 7, Title: "Mine", Creator: true, Broadcast: true},
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 71, AccessHash: 7, Title: "Not mine", Broadcast: true},
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 72, AccessHash: 7, Title: "Public", Creator: true, Broadcast: true, Username: "public"},
		&tg.Channel{Photo: &tg.ChatPhotoEmpty{}, ID: 73, AccessHash: 7, Title: "Group", Creator: true, Megagroup: true},
	}
	s := storageStore(t, api)
	found, err := s.FindStorageChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if found.Bound || len(found.Candidates) != 1 || found.Candidates[0].Title != "Mine" {
		t.Fatalf("%+v", found)
	}
	if s.collections.storage.bound() {
		t.Fatal("bound without being asked")
	}
	bound, err := s.BindStorageChannel(context.Background(), found.Candidates[0].ID)
	if err != nil || !bound.Bound || s.collections.storage.ChannelID != 70 {
		t.Fatalf("%+v, %v", bound, err)
	}
	if _, err := s.BindStorageChannel(context.Background(), peerID(&tg.PeerChannel{ChannelID: 71})); err == nil {
		t.Fatal("bound a channel of another's")
	}
}

// The whole point of the pass: the list ends in the order it had, and each GIF has
// a message. Flood waits and a stale reference hold it up, and one refused is
// passed over.
func TestStoragePassKeepsTheOrder(t *testing.T) {
	api := newStorageAPI(5, 1, 2, 3, 4, 5)
	api.flood = 2
	api.expired[3] = true
	s := storageStore(t, api)
	ctx := context.Background()
	found, err := s.BindStorageChannel(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if found.SendAll != 5 || api.created != 1 {
		t.Fatalf("%+v, created %d", found, api.created)
	}
	if err := s.SendAllGIFs(ctx); err != nil {
		t.Fatal(err)
	}
	s.dialogsRead()
	waitStorage(t, "the pass", func() bool { return !s.StoragePassState().Active })
	api.mu.Lock()
	defer api.mu.Unlock()
	if !slices.Equal(api.list, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("the list ended as %v", api.list)
	}
	if !slices.Equal(api.sends[:5], []int64{5, 4, 3, 2, 1}) {
		t.Fatalf("sent %v", api.sends)
	}
	l := &s.collections
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.storage.Archived) != 5 {
		t.Fatalf("archived %v", l.storage.Archived)
	}
}

// Stopped halfway, a pass goes on from where it was, and still ends in order.
func TestStoragePassGoesOnAfterAStop(t *testing.T) {
	api := newStorageAPI(4, 4, 1, 2, 3)
	s := storageStore(t, api)
	ctx := context.Background()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	// The list was 1 2 3 4; 4 has gone.
	l.storage.Pass = &storagePassState{Order: []int64{1, 2, 3, 4}, Done: 1}
	l.work.pass = model.StoragePass{Active: true, Done: 1, Total: 4}
	l.mu.Unlock()
	if err := s.saveStorage(ctx); err != nil {
		t.Fatal(err)
	}
	s.dialogsRead()
	waitStorage(t, "the pass", func() bool { return !s.StoragePassState().Active })
	api.mu.Lock()
	defer api.mu.Unlock()
	if !slices.Equal(api.list, []int64{1, 2, 3, 4}) || !slices.Equal(api.sends[:3], []int64{3, 2, 1}) {
		t.Fatalf("list %v, sent %v", api.list, api.sends)
	}
}

// A GIF gone from the list meanwhile is passed over: sent, it would come back and
// push another out.
func TestStoragePassLeavesWhatLeftTheList(t *testing.T) {
	api := newStorageAPI(3, 1, 3)
	s := storageStore(t, api)
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.storage.Pass = &storagePassState{Order: []int64{1, 2, 3}}
	l.work.pass = model.StoragePass{Active: true, Total: 3}
	l.mu.Unlock()
	s.dialogsRead()
	waitStorage(t, "the pass", func() bool { return !s.StoragePassState().Active })
	api.mu.Lock()
	defer api.mu.Unlock()
	if slices.Contains(api.sends, 2) || !slices.Equal(api.list, []int64{1, 3}) {
		t.Fatalf("list %v, sent %v", api.list, api.sends)
	}
}

// A GIF met in a chat goes to the channel forwarded, which leaves the list as it
// is; one that protects its content, or is not saved, does not.
func TestStorageForwardsGIFsMetInChats(t *testing.T) {
	api := newStorageAPI(5, 1, 2)
	s := storageStore(t, api)
	ctx := context.Background()
	s.picker.mu.Lock()
	s.picker.init()
	s.picker.pages[model.PickerGIF] = model.PickerPage{Items: gifItems(1, 2)}
	s.picker.mu.Unlock()
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.peers[6] = peerRecord{Kind: "channel", ID: 6, Rights: peerRights{NoForwards: true}}
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.storage.Archived["document/1"] = 900 // the top: nothing to send
	l.ready = true
	l.work.looked = true
	l.mu.Unlock()
	gif := func(chat int64, id, doc int, protected bool) model.Message {
		return model.Message{Kind: model.MessageGIF, NoForwards: protected, Key: model.MessageKey{ChatID: chat, MessageID: model.MessageID(id)}, Media: &model.MessageMedia{ID: fmt.Sprintf("document/%d", doc)}}
	}
	s.gifsMet(ctx, []model.Message{
		gif(5, 11, 2, true),  // protects its content
		gif(6, 12, 2, false), // so does its chat
		gif(5, 13, 3, false), // not saved
	})
	time.Sleep(50 * time.Millisecond)
	api.mu.Lock()
	if len(api.forwards) != 0 {
		api.mu.Unlock()
		t.Fatalf("forwarded %v", api.forwards)
	}
	api.mu.Unlock()
	s.gifsMet(ctx, []model.Message{gif(5, 14, 2, false)})
	waitStorage(t, "the forward", func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return len(api.forwards) == 1
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.forwards[0] != 14 || len(api.sends) != 0 || !slices.Equal(api.list, []int64{1, 2}) {
		t.Fatalf("forwarded %v, sent %v, list %v", api.forwards, api.sends, api.list)
	}
}

// The list's top goes to the channel sent: it is at the top already.
func TestStorageSendsTheTopGIF(t *testing.T) {
	api := newStorageAPI(5, 1, 2, 3)
	s := storageStore(t, api)
	ctx := context.Background()
	if err := s.Cache().Put(ctx, "ref/document/1", fileLocation{ID: 1, Hash: 10, Reference: []byte("r"), SavedGIF: true}); err != nil {
		t.Fatal(err)
	}
	s.picker.mu.Lock()
	s.picker.init()
	items := gifItems(1, 2, 3)
	items[0].Media = model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: items[0].ID}}
	s.picker.pages[model.PickerGIF] = model.PickerPage{Items: items}
	s.picker.mu.Unlock()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.work.looked = true
	l.mu.Unlock()
	s.dialogsRead()
	waitStorage(t, "the send", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.storage.Archived["document/1"] != 0
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if !slices.Equal(api.sends, []int64{1}) || !slices.Equal(api.list, []int64{1, 2, 3}) {
		t.Fatalf("sent %v, list %v", api.sends, api.list)
	}
}

// What another device saved pushes GIFs out with no saveGif here: the fetched list
// tells them, and sending one again brings it out of those kept.
func TestLocalPremiumKeepsGIFsPushedOutElsewhere(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	ctx := context.Background()
	limit := model.SavedGIFLimit(s.Premium())
	ids := make([]int, limit)
	for i := range ids {
		ids[i] = 100 + i
	}
	old := gifItems(ids...)
	s.gifsFetched(ctx, old, append(gifItems(9), old[:limit-1]...))
	last := old[limit-1].ID
	if !s.localGIF(last) {
		t.Fatal("the GIF pushed out is not kept")
	}
	s.gifsFetched(ctx, append(gifItems(9), old[:limit-1]...), append(gifItems(ids[limit-1]), old[:limit-1]...))
	if s.localGIF(last) || !s.localGIF("document/9") {
		t.Fatalf("after coming back: %v, the one it pushed out: %v", s.localGIF(last), s.localGIF("document/9"))
	}
	s.SetLocalPremium(model.LocalPremium{})
	s.gifsFetched(ctx, old, gifItems(1))
	if s.localGIF(old[0].ID) {
		t.Fatal("kept without Local Premium")
	}
}

// A GIF kept here renews its reference by its message in the channel: Telegram's
// list no longer has it.
func TestKeptGIFRenewsItsReferenceByChannel(t *testing.T) {
	api := newStorageAPI(5, 1)
	api.messages[77] = 42
	s := storageStore(t, api)
	ctx := context.Background()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash = 50, 5
	l.storage.Archived["document/42"] = 77
	l.gifs = gifItems(42)
	l.mu.Unlock()
	s.history.refs["document/42"] = fileLocation{ID: 42, Hash: 420, Reference: []byte("old"), SavedGIF: true}
	err := s.refreshReference(ctx, model.Message{Media: &model.MessageMedia{ID: "document/42"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(s.history.refs["document/42"].Reference); got != "r" {
		t.Fatalf("reference %q", got)
	}
}

// The file of another device comes in: GIFs by their messages, in its order, and
// pins merged with what Telegram did since.
func TestStorageTakesInTheFile(t *testing.T) {
	api := newStorageAPI(5, 1)
	api.messages[71], api.messages[72] = 41, 42
	file, err := json.Marshal(storageIndex{Version: storageVersion, GIFs: []int64{72, 71, 73}, Pins: []int64{5, 6, 7}, Synced: []int64{5, 6}})
	if err != nil {
		t.Fatal(err)
	}
	api.index, api.indexDoc = file, 1234
	s := storageStore(t, api)
	ctx := context.Background()
	c := func(id int64) model.Chat { return model.Chat{ID: id} }
	five := c(5)
	five.Pinned = true
	s.mu.Lock()
	// Telegram has lost 6 since the file was written: unpinned elsewhere.
	s.chats = []model.Chat{five, c(6), c(7)}
	s.mu.Unlock()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.storage.Archived["document/1"] = 900
	l.work.looked, l.ready = true, true
	l.work.file = func(context.Context, *tg.Document) ([]byte, error) { return file, nil }
	l.mu.Unlock()
	api.client()
	if err := s.readStorageIndex(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if got := gifIDs(l.gifs); !slices.Equal(got, []string{"document/42", "document/41"}) {
		t.Fatalf("GIFs %v", got)
	}
	if !l.storage.GIFsDirty {
		t.Fatal("a message gone from the channel is to be taken off the file")
	}
	if !slices.Equal(l.pins.Order, []int64{5, 7}) {
		t.Fatalf("pins %v", l.pins.Order)
	}
	if l.storage.IndexDoc != 1234 || l.storage.Index != 1000 {
		t.Fatalf("index %d, doc %d", l.storage.Index, l.storage.IndexDoc)
	}
}

// A file of a newer client is left as it is.
func TestStorageLeavesANewerFile(t *testing.T) {
	api := newStorageAPI(5)
	api.index, api.indexDoc = []byte(`{"Version":99,"Pins":[5]}`), 1
	s := storageStore(t, api)
	ctx := context.Background()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash = 50, 5
	l.storage.GIFsDirty = true
	l.pins = &localPins{Order: []int64{8}}
	l.work.file = func(context.Context, *tg.Document) ([]byte, error) { return api.index, nil }
	l.mu.Unlock()
	if err := s.readStorageIndex(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	if err := s.publishStorage(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	if !l.storage.GIFsDirty || !slices.Equal(l.pins.Order, []int64{8}) {
		t.Fatalf("%+v, %v", l.storage, l.pins.Order)
	}
}

// Removed by the user, a GIF's message leaves the channel too.
func TestStorageDeletesRemovedGIF(t *testing.T) {
	api := newStorageAPI(5, 1)
	s := storageStore(t, api)
	ctx := context.Background()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.storage.Archived["document/42"] = 77
	l.gifs = gifItems(42)
	l.mu.Unlock()
	gif := model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "document/42"}}
	if err := s.SaveGIF(ctx, gif, true); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteStorageMessages(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.deleted, []int{77}) || s.localGIF("document/42") {
		t.Fatalf("deleted %v", api.deleted)
	}
}

// The channel gone, the GIFs stay, and a pass under way tells of it.
func TestStorageChannelGone(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.AllLocalPremium)
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID = 50
	l.storage.Pass = &storagePassState{Order: []int64{1}}
	l.work.pass = model.StoragePass{Active: true, Total: 1}
	l.gifs = gifItems(42)
	l.mu.Unlock()
	s.unbindStorage(context.Background())
	if pass := s.StoragePassState(); pass.Active || !pass.Failed {
		t.Fatalf("%+v", pass)
	}
	if s.StoragePassState().Failed {
		t.Fatal("told twice")
	}
	if !s.localGIF("document/42") {
		t.Fatal("the GIFs went with the channel")
	}
}

// The first write makes the file, pinned; a later one replaces it in that message,
// and what it tells is what another device takes in.
func TestStorageWritesTheFile(t *testing.T) {
	api := newStorageAPI(5, 1)
	s := storageStore(t, api)
	ctx := context.Background()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.storage.Archived["document/42"], l.storage.Archived["document/43"] = 77, 78
	l.gifs = append(gifItems(43, 42), gifItems(44)...) // 44 has no message
	l.pins = &localPins{Order: []int64{5, 6}, Synced: []int64{5}}
	l.storage.GIFsDirty = true
	l.mu.Unlock()
	if err := s.publishStorage(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	var file storageIndex
	if err := json.Unmarshal(api.written, &file); err != nil {
		t.Fatalf("%q: %v", api.written, err)
	}
	if !slices.Equal(file.GIFs, []int64{78, 77}) || !slices.Equal(file.Pins, []int64{5, 6}) || !slices.Equal(file.Synced, []int64{5}) || file.Version != storageVersion {
		t.Fatalf("%+v", file)
	}
	if api.pinned == 0 || api.pinned != l.storage.Index || l.storage.GIFsDirty {
		t.Fatalf("pinned %d, index %d, dirty %v", api.pinned, l.storage.Index, l.storage.GIFsDirty)
	}
	if err := s.publishStorage(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	if len(api.edits) != 0 {
		t.Fatal("written with nothing changed")
	}
	s.markStorageChanged(false, true)
	if err := s.publishStorage(ctx, s.history.api); err != nil {
		t.Fatal(err)
	}
	if len(api.edits) != 1 || api.edits[0] != l.storage.Index || api.created2 != 1 {
		t.Fatalf("edited %v, %d files made", api.edits, api.created2)
	}
}

// Each part acts alone: the pins do not turn on the GIFs, nor they the pins, and
// without Sync nothing reaches the channel.
func TestLocalPremiumPartsAreSeparate(t *testing.T) {
	s := testStore(t)
	s.SetLocalPremium(model.LocalPremium{Pins: true})
	if !s.LocalPinsEnabled() {
		t.Fatal("the pins are off")
	}
	s.collections.gifs = gifItems(42)
	if s.localGIF("document/42") {
		t.Fatal("GIFs kept with only the pins on")
	}
	s.SetLocalPremium(model.LocalPremium{GIFs: true})
	if s.LocalPinsEnabled() || !s.localGIF("document/42") {
		t.Fatal("the GIFs did not change alone")
	}
	api := newStorageAPI(5, 1)
	s.history.api = api.client()
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash = 50, 5
	l.storage.Archived["document/1"] = 7
	l.ready = true
	l.mu.Unlock()
	s.wakeStorage()
	if err := s.storageStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.next != 0 || len(api.unknown) != 0 || api.indexReads != 0 {
		t.Fatalf("the channel was used without Sync: %d, %v", api.next, api.unknown)
	}
}

// With its part off, a device writes back what the file said of it: another
// device's pins do not go because this one keeps only GIFs.
func TestStorageKeepsThePartSwitchedOff(t *testing.T) {
	api := newStorageAPI(5, 1)
	s := storageStore(t, api)
	s.SetLocalPremium(model.LocalPremium{GIFs: true, Sync: true})
	l := &s.collections
	l.mu.Lock()
	l.storage.ChannelID, l.storage.Hash, l.storage.Title = 50, 5, "x"
	l.storage.PinBase, l.storage.SyncedBase = []int64{5, 6}, []int64{5}
	l.storage.Archived["document/42"] = 77
	l.gifs = gifItems(42)
	l.storage.GIFsDirty = true
	l.mu.Unlock()
	if err := s.publishStorage(context.Background(), s.history.api); err != nil {
		t.Fatal(err)
	}
	var file storageIndex
	if err := json.Unmarshal(api.written, &file); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(file.Pins, []int64{5, 6}) || !slices.Equal(file.Synced, []int64{5}) || !slices.Equal(file.GIFs, []int64{77}) {
		t.Fatalf("%+v", file)
	}
	// With the GIFs off and the pins on, the other way round.
	s.SetLocalPremium(model.LocalPremium{Pins: true, Sync: true})
	l.mu.Lock()
	l.pins = &localPins{Order: []int64{9}, Synced: []int64{9}}
	l.storage.PinsDirty = true
	l.gifs = nil
	l.mu.Unlock()
	if err := s.publishStorage(context.Background(), s.history.api); err != nil {
		t.Fatal(err)
	}
	file = storageIndex{}
	if err := json.Unmarshal(api.written, &file); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(file.Pins, []int64{9}) || !slices.Equal(file.GIFs, []int64{77}) {
		t.Fatalf("%+v", file)
	}
}
