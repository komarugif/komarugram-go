// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

// The unread filter shrinks during a read. Its manual mark, archived member
// and paginated forum must all be processed, even with Ghost enabled.
func TestFolderReadAndRemove(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	forumPeer := peerRecord{ID: 6, Kind: "channel", Hash: 50}
	forum := forumPeer.key()
	raw := &tg.DialogFilter{ID: 4, NonContacts: true, Groups: true, ExcludeRead: true, Title: tg.TextWithEntities{Text: "Unread"}, ExcludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 4}}}
	filters := &tg.MessagesDialogFilters{Filters: []tg.DialogFilterClass{&tg.DialogFilterDefault{}, raw}}
	s.entries = map[int64]entry{3: {unreadMark: true}}
	s.chats = []model.Chat{{ID: 2, Unread: 5, Muted: true, Archived: true}, {ID: 3}, {ID: 4, Unread: 7}, {ID: forum, Kind: model.KindGroup, Forum: true, Unread: 2, Last: model.Preview{ID: 16}}}
	s.history.peers[2] = peerRecord{ID: 2, Kind: "user"}
	s.history.peers[3] = peerRecord{ID: 3, Kind: "user"}
	s.history.peers[forum] = forumPeer
	var calls []string
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			return filters, nil
		case *tg.MessagesGetPeerDialogsRequest:
			calls = append(calls, "top")
			return &tg.MessagesPeerDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 2}, TopMessage: 12}}}, nil
		case *tg.MessagesReadHistoryRequest:
			calls = append(calls, fmt.Sprintf("read %d to %d", inputPeerID(r.Peer, 1), r.MaxID))
			return &tg.MessagesAffectedMessages{}, nil
		case *tg.MessagesMarkDialogUnreadRequest:
			if r.Unread {
				return nil, errors.New("set unread mark")
			}
			calls = append(calls, fmt.Sprintf("clear %d", inputPeerID(r.Peer.(*tg.InputDialogPeer).Peer, 1)))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesGetForumTopicsRequest:
			if r.OffsetTopic == 0 {
				return &tg.MessagesForumTopics{Count: 2, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 1, Peer: &tg.PeerChannel{ChannelID: 6}, UnreadCount: 1, TopMessage: 10, FromID: &tg.PeerUser{UserID: 1}}}}, nil
			}
			return &tg.MessagesForumTopics{Count: 2, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 9, Peer: &tg.PeerChannel{ChannelID: 6}, UnreadCount: 1, UnreadMentionsCount: 1, TopMessage: 16, FromID: &tg.PeerUser{UserID: 1}}}}, nil
		case *tg.MessagesReadDiscussionRequest:
			calls = append(calls, fmt.Sprintf("topic %d to %d", r.MsgID, r.ReadMaxID))
			return &tg.BoolTrue{}, nil
		case *tg.MessagesReadMentionsRequest:
			calls = append(calls, fmt.Sprintf("mentions %d", r.TopMsgID))
			return &tg.MessagesAffectedHistory{}, nil
		case *tg.MessagesUpdateDialogFilterRequest:
			if r.ID != 4 || r.Filter != nil {
				return nil, fmt.Errorf("unexpected filter mutation: %v", r)
			}
			calls = append(calls, "delete filter")
			return &tg.BoolTrue{}, nil
		default:
			return nil, fmt.Errorf("unexpected RPC %T", in)
		}
	})
	if err := s.loadDialogFilters(ctx, s.history.api, 1); err != nil {
		t.Fatal(err)
	}
	if len(s.UnreadInFolder(4)) != 0 {
		t.Fatal("offered read on an incomplete dialog list")
	}
	s.dialogsComplete.Store(true)
	excludesArchived := *raw
	excludesArchived.ExcludeArchived = true
	filters.Filters[1] = &excludesArchived
	if err := s.loadDialogFilters(ctx, s.history.api, 1); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{0, 4} {
		if archived := s.UnreadInArchivedFolder(id); len(archived) != 1 || archived[0].ID != 2 {
			t.Fatalf("archive targets for folder %d: %+v", id, archived)
		}
	}
	if got := s.UnreadInFolder(4); len(got) != 2 || slices.ContainsFunc(got, func(c model.FolderReadChat) bool { return c.ID == 2 }) {
		t.Fatalf("ordinary folder targets included the archived chat: %+v", got)
	}
	filters.Filters[1] = raw
	if err := s.loadDialogFilters(ctx, s.history.api, 1); err != nil {
		t.Fatal(err)
	}
	targets := s.UnreadInFolder(4)
	if len(targets) != 3 {
		t.Fatalf("targets: %+v", targets)
	}
	if err := s.ReadFolderChats(ctx, targets); err != nil {
		t.Fatal(err)
	}
	if len(s.UnreadInFolder(4)) != 0 || s.FolderDialogs()[2].Unread != 7 {
		t.Fatal("read affected the wrong members")
	}
	f := s.Folders()[1]
	stale := f
	stale.Title = "Other"
	if err := s.DeleteFolder(ctx, stale); !errors.Is(err, model.ErrFolderConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := s.DeleteFolder(ctx, f); err != nil {
		t.Fatal(err)
	}
	if len(s.Folders()) != 1 || len(s.FolderDialogs()) != 4 {
		t.Fatal("deletion removed dialogs or retained folder")
	}
	_, kept, err := cachedDialogFilters(ctx, s.Cache())
	if err != nil || len(kept.Filters) != 1 {
		t.Fatalf("offline folders: %v %v", kept, err)
	}
	if want := []string{"top", "read 2 to 12", "clear 3", "topic 1 to 10", "topic 9 to 16", "mentions 9", "delete filter"}; !slices.Equal(calls, want) {
		t.Fatalf("RPCs: %v", calls)
	}
}

// A failed first download can leave an unchanged first page in the cache.
// That page alone must not make later unread dialogs unreachable.
func TestFolderReadAfterInterruptedDialogLoad(t *testing.T) {
	s := testStore(t)
	srv := newDialogServer(120)
	for _, c := range srv.chats {
		c.contact = true
		c.unread = 2
	}
	offline := errors.New("disconnected")
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if req, ok := in.(*tg.MessagesGetDialogsRequest); ok && req.OffsetID != 0 {
			return offline
		}
		return srv.invoke(ctx, in, out)
	}))
	if err := s.loadDialogs(t.Context(), s.history.api, 1); !errors.Is(err, offline) {
		t.Fatal(err)
	}
	if len(s.Chats()) == 0 || len(s.UnreadInFolder(2)) != 0 {
		t.Fatal("partial folder offered for reading")
	}
	s.history.api = tg.NewClient(telegram.InvokeFunc(srv.invoke))
	srv.load(t, s)
	if got := len(s.UnreadInFolder(2)); got != 120 {
		t.Fatalf("resumed read has %d targets", got)
	}
	var complete bool
	if found, err := s.Cache().Get(t.Context(), "dialog-lists-complete", &complete); err != nil || !found || !complete {
		t.Fatal("missing complete offline snapshot", err)
	}
}

func TestFolderReadPartialFailure(t *testing.T) {
	s := testStore(t)
	var ids []int64
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesReadHistoryRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		id := inputPeerID(r.Peer, 1)
		ids = append(ids, id)
		if id == 3 {
			return nil, tgerr.New(420, "FLOOD_WAIT_2")
		}
		return &tg.MessagesAffectedMessages{}, nil
	})
	for _, id := range []int64{2, 3, 4} {
		s.history.peers[id] = peerRecord{ID: id, Kind: "user"}
		s.chats = append(s.chats, model.Chat{ID: id, Last: model.Preview{ID: 8}, Unread: 2})
	}
	s.dialogsComplete.Store(true)
	if err := s.ReadFolderChats(t.Context(), s.UnreadInFolder(0)); err == nil {
		t.Fatal("failed read reported success")
	}
	left := s.UnreadInFolder(0)
	if len(left) != 2 || left[0].ID != 3 || left[1].ID != 4 || !slices.Equal(ids, []int64{2, 3}) {
		t.Fatalf("remaining %v after RPCs %v", left, ids)
	}
	chats, err := cachedDialogs(t.Context(), s.Cache())
	if err != nil || len(chats) != 3 || chats[0].Unread != 0 || chats[1].Unread != 2 {
		t.Fatalf("offline partial read %v %v", chats, err)
	}
}

// Exercise the exact filter sent for a manually unread contact. Its pin and
// exceptions share one server limit, and unrelated peer hashes must survive.
func TestChatRowFilterWrites(t *testing.T) {
	ctx := t.Context()
	s := testStore(t)
	peer := &tg.InputPeerUser{UserID: 2, AccessHash: 72}
	other := &tg.InputPeerChannel{ChannelID: 41, AccessHash: 99}
	title := tg.TextWithEntities{Text: " Work ", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 1, Length: 4}}}
	original := &tg.DialogFilter{ID: 2, Title: title, Contacts: true, ExcludeRead: true, PinnedPeers: []tg.InputPeerClass{other}, ExcludePeers: []tg.InputPeerClass{peer}, TitleNoanimate: true}
	original.SetColor(0)
	filters := tg.MessagesDialogFilters{TagsEnabled: true, Filters: []tg.DialogFilterClass{original}}
	s.entries = map[int64]entry{2: {contact: true, unreadMark: true}}
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 2, AccessHash: 72}}, nil)
	writes := 0
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			return &filters, nil
		case *tg.MessagesUpdateDialogFilterRequest:
			f, ok := r.Filter.(*tg.DialogFilter)
			if !ok || r.ID != 2 || f.ID != 2 || !reflect.DeepEqual(f.Title, title) || !f.TitleNoanimate || !f.Contacts || !f.ExcludeRead || len(f.PinnedPeers) == 0 || !reflect.DeepEqual(f.PinnedPeers[len(f.PinnedPeers)-1], other) {
				return nil, fmt.Errorf("unexpected folder update %v", r.Filter)
			}
			if color, set := f.GetColor(); !set || color != 0 {
				return nil, errors.New("lost folder color")
			}
			filters.Filters[0] = f
			writes++
			return &tg.BoolTrue{}, nil
		default:
			return nil, fmt.Errorf("unexpected RPC %T", in)
		}
	})
	if err := s.loadDialogFilters(ctx, s.history.api, 1); err != nil {
		t.Fatal(err)
	}
	chat := model.Chat{ID: 2, Kind: model.KindUser}
	change := func(action model.FolderChatAction) {
		t.Helper()
		before := s.Folders()[1]
		draft, err := before.WithChat(chat, action)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SaveFolder(ctx, &before, draft); err != nil {
			t.Fatal(err)
		}
		if err = s.SaveFolder(ctx, &before, draft); err != nil {
			t.Fatal("retry", err)
		}
	}
	change(model.FolderAddChat)
	f := s.Folders()[1]
	if !f.Contains(chat) || len(f.Included)+len(f.Excluded) != 0 {
		t.Fatal("failed to restore automatic membership")
	}
	change(model.FolderPinChat)
	if actual := filters.Filters[0].(*tg.DialogFilter); !reflect.DeepEqual(actual.PinnedPeers, []tg.InputPeerClass{peer, other}) || len(actual.IncludePeers) != 0 {
		t.Fatal("incorrect pinned peers", actual)
	}
	change(model.FolderUnpinChat)
	if !slices.Equal(s.Folders()[1].Included, []int64{2}) {
		t.Fatal("unpin lost explicit membership")
	}
	change(model.FolderRemoveChat)
	f = s.Folders()[1]
	if f.Contains(chat) || len(f.Included) != 0 || !slices.Equal(f.Excluded, []int64{2}) || writes != 4 {
		t.Fatalf("after removal: %v, writes %d", f, writes)
	}
	_, cached, err := cachedDialogFilters(ctx, s.Cache())
	if err != nil || !cached.TagsEnabled || !reflect.DeepEqual(cached.Filters[0].(*tg.DialogFilter).ExcludePeers, []tg.InputPeerClass{peer}) {
		t.Fatal("lost folder edit in cache", err)
	}
}
