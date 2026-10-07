// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"komarugram/internal/messenger/model"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s := New(nil)
	if e := s.Configure(context.Background(), "a", filepath.Join(t.TempDir(), "h"), nil); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestUpdateEditDeleteAndLatePage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	chat := int64(5)
	s.history.histories[chat] = &model.History{}
	m := &tg.Message{ID: 17, PeerID: &tg.PeerUser{UserID: 5}, Message: "before", Date: 10}
	old, e := s.ingest(ctx, []tg.MessageClass{m}, false, 0)
	if e != nil {
		t.Fatal(e)
	}
	m.Message = "edited"
	m.EditDate = 11
	if e = s.Handle(ctx, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: m}}}); e != nil {
		t.Fatal(e)
	}
	s.finishPage(chat, 0, old, true, nil, 0)
	h := s.History(chat)
	if len(h.Messages) != 1 || h.Messages[0].Text != "edited" {
		t.Fatalf("late page overwrote edit: %+v", h)
	}
	if e = s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateDeleteMessages{Messages: []int{17}}}); e != nil {
		t.Fatal(e)
	}
	s.finishPage(chat, 0, old, true, nil, 0)
	if len(s.History(chat).Messages) != 0 {
		t.Fatal("late page resurrected deletion")
	}
	if e = s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 17, PeerID: &tg.PeerChannel{ChannelID: 8}, Message: "channel", Date: 20}}}); e != nil {
		t.Fatal(e)
	}
	got, e := s.Cache().Around(ctx, peerID(&tg.PeerChannel{ChannelID: 8}), 0, 10)
	if e != nil || len(got) != 1 {
		t.Fatal("channel ID collision", e)
	}
}
func TestMediaClassificationAttributeOrder(t *testing.T) {
	for _, attrs := range [][]tg.DocumentAttributeClass{{&tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{W: 320, H: 200}}, {&tg.DocumentAttributeVideo{W: 320, H: 200}, &tg.DocumentAttributeAnimated{}}} {
		k, m, _ := documentMedia(&tg.Document{ID: 1, MimeType: "video/mp4", Attributes: attrs})
		if k != model.MessageGIF || m.Width != 320 {
			t.Fatal(k, m)
		}
	}
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Message: "👋 link", Entities: []tg.MessageEntityClass{&tg.MessageEntityTextURL{Offset: 3, Length: 4, URL: "https://telegram.org"}}, ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{Buttons: []tg.KeyboardInlineButton{{Text: "open", Type: &tg.InlineButtonTypeURL{URL: "https://telegram.org"}}, {Text: "action", Type: &tg.InlineButtonTypeCallback{Data: []byte("secret callback")}}}}}}}
	m, _ := convertMessage("a", msg, nil)
	if m.Entities[0].Offset != 3 || m.Buttons[0][0].Kind != "url" || m.Buttons[0][1].URL != "" {
		t.Fatal("entities/readonly keyboard conversion")
	}
}
func TestOfflineMediaAndCachedPages(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 4, MessageID: 10}, Media: &model.MessageMedia{ID: "test"}}
	if e := s.Cache().SaveMessages(ctx, []model.Message{m}); e != nil {
		t.Fatal(e)
	}
	if e := s.Cache().SaveMedia(ctx, "test", []byte("bytes")); e != nil {
		t.Fatal(e)
	}
	b, e := s.Media(ctx, m)
	if e != nil || string(b) != "bytes" {
		t.Fatal("offline media", e)
	}
	s.history.histories[4] = &model.History{Messages: []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: 4, MessageID: 20}}}, LoadingOlder: true}
	s.fetch(4, -1, 20)
	h := s.History(4)
	if h.Err != nil || !h.Offline || len(h.Messages) != 2 || h.Messages[0].Key.MessageID != 10 {
		t.Fatalf("offline history %+v", h)
	}
}
func TestParallelDownloadBound(t *testing.T) {
	b := new(cappedFile)
	if _, e := b.WriteAt([]byte("world"), 5); e != nil {
		t.Fatal(e)
	}
	if _, e := b.WriteAt([]byte("hello"), 0); e != nil {
		t.Fatal(e)
	}
	if string(b.data) != "helloworld" {
		t.Fatal(string(b.data))
	}
	if _, e := b.WriteAt([]byte("x"), MaxMediaBytes); e == nil {
		t.Fatal("size limit not enforced")
	}
}

func TestHistorySinceInvalidatesOnEditsAndDeletes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.history.histories[5] = &model.History{}
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}, Message: "before"}
	if e := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: msg}}); e != nil {
		t.Fatal(e)
	}
	first, changed := s.HistorySince(5, 0)
	if !changed || len(first.Messages) != 1 {
		t.Fatal("initial snapshot")
	}
	unchanged, changed := s.HistorySince(5, first.Revision)
	if changed || unchanged.Messages != nil {
		t.Fatal("copied unchanged history")
	}
	msg.Message = "after"
	if e := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateEditMessage{Message: msg}}); e != nil {
		t.Fatal(e)
	}
	edited, changed := s.HistorySince(5, first.Revision)
	if !changed || edited.Messages[0].Text != "after" || first.Messages[0].Text != "before" {
		t.Fatal("edit/snapshot isolation")
	}
	if e := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateDeleteMessages{Messages: []int{1}}}); e != nil {
		t.Fatal(e)
	}
	deleted, changed := s.HistorySince(5, edited.Revision)
	if !changed || len(deleted.Messages) != 0 {
		t.Fatal("stale deleted snapshot")
	}
}
func BenchmarkUnchangedHistory(b *testing.B) {
	s := New(nil)
	messages := make([]model.Message, 10000)
	s.history.histories[5] = &model.History{Messages: messages, Revision: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, changed := s.HistorySince(5, 1)
		if changed {
			b.Fatal("unexpected mutation")
		}
	}
}

func chatPreview(t *testing.T, s *Store, id int64) model.Chat {
	t.Helper()
	for _, c := range s.Chats() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("chat %d missing", id)
	return model.Chat{}
}

func TestDeletedLastMessageReplacesDialogPreview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	send := func(id int, peer tg.PeerClass, text string) {
		t.Helper()
		var u tg.UpdateClass = &tg.UpdateNewMessage{Message: &tg.Message{ID: id, PeerID: peer, Message: text, Date: 100 + id}}
		if _, ok := peer.(*tg.PeerChannel); ok {
			u = &tg.UpdateNewChannelMessage{Message: &tg.Message{ID: id, PeerID: peer, Message: text, Date: 100 + id}}
		}
		if e := s.Handle(ctx, &tg.UpdateShort{Update: u}); e != nil {
			t.Fatal(e)
		}
	}
	remove := func(u tg.UpdateClass) {
		t.Helper()
		if e := s.Handle(ctx, &tg.UpdateShort{Update: u}); e != nil {
			t.Fatal(e)
		}
	}
	loaded, cached, emptied := int64(5), int64(6), int64(7)
	channel := &tg.PeerChannel{ChannelID: 8}
	s.history.histories[loaded] = &model.History{}
	send(1, &tg.PeerUser{UserID: loaded}, "first")
	send(2, &tg.PeerUser{UserID: loaded}, "second")
	send(3, &tg.PeerUser{UserID: cached}, "older")
	send(4, &tg.PeerUser{UserID: cached}, "kept  in\ncache")
	send(5, &tg.PeerUser{UserID: cached}, "secret")
	send(6, &tg.PeerUser{UserID: emptied}, "only")
	send(1, channel, "channel first")
	send(2, channel, "channel second")

	// Not the last message: the preview stays.
	remove(&tg.UpdateDeleteMessages{Messages: []int{3}})
	if got := chatPreview(t, s, cached).LastMessage; got != "secret" {
		t.Fatalf("preview changed by an older deletion: %q", got)
	}
	remove(&tg.UpdateDeleteMessages{Messages: []int{2, 5, 6}})
	if got := chatPreview(t, s, loaded).LastMessage; got != "first" || s.history.top[loaded] != 1 {
		t.Fatalf("loaded chat preview %q, top %d", got, s.history.top[loaded])
	}
	if got := chatPreview(t, s, cached).LastMessage; got != "kept in cache" {
		t.Fatalf("cached chat preview %q", got)
	}
	if got := chatPreview(t, s, emptied).LastMessage; got != "" {
		t.Fatalf("deleted text left in the list: %q", got)
	}
	if s.history.top[cached] != 4 {
		t.Fatalf("top message %d", s.history.top[cached])
	}
	remove(&tg.UpdateDeleteChannelMessages{ChannelID: 8, Messages: []int{2}})
	if got := chatPreview(t, s, peerID(channel)).LastMessage; got != "channel first" {
		t.Fatalf("channel preview %q", got)
	}
	// Private chats keep IDs from the shared sequence; a channel deletion must not touch them.
	if got := chatPreview(t, s, cached).LastMessage; got != "kept in cache" {
		t.Fatalf("channel deletion changed a private chat: %q", got)
	}
}

func TestLivePreviewNamesMedia(t *testing.T) {
	chat := model.Chat{Kind: model.KindGroup}
	setPreview(&chat, model.Message{Kind: model.MessagePhoto, SenderName: "Анна"})
	if chat.LastMessage != "Фото" || chat.LastSender != "Анна" {
		t.Fatalf("%+v", chat)
	}
	setPreview(&chat, model.Message{Kind: model.MessageVideo, Text: "подпись", Outgoing: true})
	if chat.LastMessage != "Видео, подпись" || chat.LastSender != "Вы" {
		t.Fatalf("%+v", chat)
	}
	private := model.Chat{Kind: model.KindUser}
	setPreview(&private, model.Message{Text: "hi", SenderName: "Анна"})
	if private.LastSender != "" {
		t.Fatal("sender shown outside a group")
	}
}

func TestPhotoVariantsHaveOwnLocations(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	photo := &tg.Photo{ID: 77, AccessHash: 1, DCID: 2, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{1, 40, 40}},
		&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 20000},
		&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{5000, 90000, 160000}},
		&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 70000},
	}}
	msgs, e := s.ingest(ctx, []tg.MessageClass{&tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}, Media: &tg.MessageMediaPhoto{Photo: photo}}}, false, 0)
	if e != nil || len(msgs) != 1 {
		t.Fatal(e)
	}
	media := msgs[0].Media
	if media.ID != "photo/77/y" || media.Width != 1280 || media.Size != 160000 || len(media.Preview) == 0 {
		t.Fatalf("largest size: %+v", media)
	}
	if len(media.Variants) != 2 || media.Variants[0].ID != "photo/77/m" || media.Variants[1].ID != "photo/77/x" {
		t.Fatalf("variants: %+v", media.Variants)
	}
	if got := media.Variant(400, 300).ID; got != "photo/77/x" {
		t.Fatalf("tile of 400×300 picks %s", got)
	}
	for id, thumb := range map[string]string{"photo/77/m": "m", "photo/77/x": "x", "photo/77/y": "y"} {
		var ref fileLocation
		if ok, e := s.Cache().Get(ctx, "ref/"+id, &ref); e != nil || !ok || ref.Thumb != thumb || ref.ID != 77 || !ref.Photo {
			t.Fatalf("location of %s: %+v %v %v", id, ref, ok, e)
		}
	}
}

// TestEditChangesRevision checks that an edit changes the message's
// ContentRevision, which is what makes the history lay it out again.
func TestEditChangesRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := &tg.Message{ID: 3, PeerID: &tg.PeerUser{UserID: 5}, Message: "before", Date: 10}
	first, err := s.ingest(ctx, []tg.MessageClass{m}, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Message, m.EditDate = "after", 11
	second, err := s.ingest(ctx, []tg.MessageClass{m}, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ContentRevision == 0 || first[0].ContentRevision == second[0].ContentRevision {
		t.Errorf("revisions %d and %d", first[0].ContentRevision, second[0].ContentRevision)
	}
}
