// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"komarugram/internal/messenger/model"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func composerAPI(fn func(bin.Encoder) (bin.Encoder, error)) *tg.Client {
	return tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		result, err := fn(in)
		if err != nil {
			return err
		}
		b := new(bin.Buffer)
		if err = result.Encode(b); err != nil {
			return err
		}
		return out.Decode(b)
	}))
}
func TestSendTextEntitiesAndAcknowledgement(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	sent := false
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesSendMessageRequest:
			sent = true
			if r.RandomID != 123 || r.Message != "hi 🙂" || len(r.Entities) != 1 {
				t.Fatalf("wrong request %+v", r)
			}
			e := r.Entities[0].(*tg.MessageEntityCustomEmoji)
			if e.Offset != 3 || e.Length != 2 || e.DocumentID != 77 {
				t.Fatalf("lost emoji %+v", e)
			}
			return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
		case *tg.MessagesGetMessagesRequest:
			return nil, errors.New("connection lost after acceptance")
		default:
			return nil, fmt.Errorf("unexpected %T", in)
		}
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 123, Text: "hi 🙂", Entities: []model.Entity{{Kind: "emoji", Offset: 3, Length: 2, DocumentID: 77}}}); err != nil {
		t.Fatal("accepted send must not invite retry:", err)
	}
	if !sent {
		t.Fatal("message not sent")
	}
}

func TestSendToSelfWithoutDialog(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 5, Self: true, FirstName: "Me"}}, nil)
	if !s.CanSend(5) {
		t.Fatal("self peer cannot send")
	}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch req := in.(type) {
		case *tg.MessagesSendMessageRequest:
			if _, ok := req.Peer.(*tg.InputPeerSelf); !ok {
				t.Fatalf("saved messages peer: %T", req.Peer)
			}
			return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
		case *tg.MessagesGetMessagesRequest:
			return nil, errors.New("fetch unavailable after acceptance")
		default:
			return nil, fmt.Errorf("unexpected %T", in)
		}
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, Text: "note"}); err != nil {
		t.Fatal(err)
	}
}

// In Saved Messages Telegram does not mark the user's messages as outgoing,
// nor tell who sent them: the message on its way, and the one its
// acknowledgement makes, stand on the side Telegram's will, not on the other
// first, and show no name; in a chat with somebody they are outgoing.
func TestSentMessageSideAsTelegrams(t *testing.T) {
	for _, saved := range []bool{true, false} {
		s := testStore(t)
		s.rememberPeers([]tg.UserClass{&tg.User{ID: 5, Self: true, FirstName: "Me"}, &tg.User{ID: 6, AccessHash: 66, FirstName: "Ann"}}, nil)
		s.mu.Lock()
		s.me = model.Profile{ID: 5, FirstName: "Me"}
		s.mu.Unlock()
		chat := int64(6)
		if saved {
			chat = 5
		}
		s.history.histories[chat] = &model.History{}
		var pending []model.Message
		s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
			if _, ok := in.(*tg.MessagesSendMessageRequest); !ok {
				return nil, fmt.Errorf("unexpected %T", in)
			}
			pending = s.History(chat).Messages
			return &tg.UpdateShortSentMessage{ID: 9, Date: 10, Out: !saved}, nil
		})
		if err := s.Send(context.Background(), chat, model.OutgoingMessage{RandomID: 1, Text: "note"}); err != nil {
			t.Fatal(err)
		}
		// Not outgoing, a message with a name shows it over its text.
		if len(pending) != 1 || !pending[0].Sending || pending[0].Outgoing == saved || saved && pending[0].SenderName != "" {
			t.Fatalf("saved %v: the message on its way %+v", saved, pending)
		}
		got := s.History(chat).Messages
		if len(got) != 1 || got[0].Key.MessageID != 9 || got[0].Outgoing == saved || saved && got[0].SenderName != "" {
			t.Fatalf("saved %v: the message sent %+v", saved, got)
		}
	}
}

func TestSendChecklistAndRender(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesSendMediaRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		todo, ok := r.Media.(*tg.InputMediaTodo)
		if !ok || len(todo.Todo.List) != 2 || todo.Todo.List[1].ID != 2 || todo.Todo.Title.Text != "Shopping" {
			t.Fatalf("bad checklist %+v", r.Media)
		}
		return &tg.Updates{}, nil
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, Text: "Shopping", Tasks: []string{"Milk", "Bread"}}); err != nil {
		t.Fatal(err)
	}
	msg, _ := convertMessage("a", &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}, Media: &tg.MessageMediaToDo{Todo: tg.TodoList{Title: tg.TextWithEntities{Text: "Shopping"}, List: []tg.TodoItem{{ID: 1, Title: tg.TextWithEntities{Text: "Milk"}}}}, Completions: []tg.TodoCompletion{{ID: 1}}}}, nil)
	if msg.Text != "Shopping\n☑ Milk" {
		t.Fatalf("checklist missing from history %q", msg.Text)
	}
}
func TestPickerSavedFirstAndPagination(t *testing.T) {
	s := testStore(t)
	s.picker.pages = map[model.PickerTab]model.PickerPage{model.PickerStickers: {Packs: []model.PickerPack{{ID: 1, Title: "Cats", Items: []model.PickerItem{{ID: "document/2", DocumentID: 2, Keywords: "cat"}}}}}}
	s.picker.at = map[model.PickerTab]time.Time{model.PickerStickers: time.Now()}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesSearchStickersRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected %T", in)
		}
		if r.Q != "cat" || r.Offset != 60 {
			t.Fatalf("wrong search %+v", r)
		}
		res := &tg.MessagesFoundStickers{Stickers: []tg.DocumentClass{&tg.Document{ID: 1, MimeType: "image/webp"}, &tg.Document{ID: 2, MimeType: "image/webp"}}}
		res.SetNextOffset(120)
		return res, nil
	})
	p, err := s.Picker(context.Background(), model.PickerRequest{Tab: model.PickerStickers, Query: "cat", Offset: "60", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 || p.Items[0].DocumentID != 2 || p.Next != "120" {
		t.Fatalf("bad priority/page %+v", p)
	}
}

func TestPickerIncludesUninstalledFeaturedPacks(t *testing.T) {
	for _, tc := range []struct {
		name string
		tab  model.PickerTab
	}{
		{"stickers", model.PickerStickers},
		{"emoji", model.PickerEmoji},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			installed := tg.StickerSet{ID: 10, AccessHash: 100, Title: "Mine"}
			featured := tg.StickerSet{ID: 20, AccessHash: 200, Title: "Featured", Count: 3, Emojis: tc.tab == model.PickerEmoji}
			cover := &tg.Document{ID: 21, MimeType: "image/webp", Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeSticker{Alt: "🙂", Stickerset: &tg.InputStickerSetID{ID: 20, AccessHash: 200}},
			}}
			calls := 0
			api := composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
				switch in.(type) {
				case *tg.MessagesGetRecentStickersRequest:
					return &tg.MessagesRecentStickers{}, nil
				case *tg.MessagesGetAllStickersRequest, *tg.MessagesGetEmojiStickersRequest:
					return &tg.MessagesAllStickers{Sets: []tg.StickerSet{installed}}, nil
				case *tg.MessagesGetStickerSetRequest:
					return &tg.MessagesStickerSet{Set: installed}, nil
				case *tg.MessagesGetFeaturedStickersRequest:
					if tc.tab != model.PickerStickers {
						t.Fatal("sticker recommendations requested on emoji tab")
					}
					calls++
					return &tg.MessagesFeaturedStickers{Sets: []tg.StickerSetCoveredClass{
						&tg.StickerSetCovered{Set: installed, Cover: cover},
						&tg.StickerSetMultiCovered{Set: featured, Covers: []tg.DocumentClass{cover}},
					}}, nil
				case *tg.MessagesGetFeaturedEmojiStickersRequest:
					if tc.tab != model.PickerEmoji {
						t.Fatal("emoji recommendations requested on sticker tab")
					}
					calls++
					return &tg.MessagesFeaturedStickers{Sets: []tg.StickerSetCoveredClass{
						&tg.StickerSetCovered{Set: installed, Cover: cover},
						&tg.StickerSetFullCovered{Set: featured, Documents: []tg.DocumentClass{cover}},
					}}, nil
				default:
					return nil, fmt.Errorf("unexpected %T", in)
				}
			})
			page, err := s.pickerCatalogue(context.Background(), api, tc.tab)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || len(page.Packs) != 1 || len(page.Featured) != 1 || page.Featured[0].ID != 20 || page.Featured[0].Ref.AccessHash != 200 || len(page.Featured[0].Items) != 1 {
				t.Fatalf("featured packs: %+v, calls=%d", page, calls)
			}
		})
	}
}
func TestGIFSearchWebResultsAndSavedOrder(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesGetSavedGifsRequest:
			return &tg.MessagesSavedGifs{Gifs: []tg.DocumentClass{&tg.Document{ID: 9, MimeType: "image/gif"}, &tg.Document{ID: 4, MimeType: "image/gif"}}}, nil
		case *tg.HelpGetConfigRequest:
			return &tg.Config{GifSearchUsername: "gifprovider", WebfileDCID: 4}, nil
		case *tg.ContactsResolveUsernameRequest:
			if r.Username != "gifprovider" {
				t.Fatal("ignored server GIF provider")
			}
			return &tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: 42}, Users: []tg.UserClass{&tg.User{ID: 42, Bot: true, AccessHash: 55}}}, nil
		case *tg.MessagesGetInlineBotResultsRequest:
			if r.Query != "cat" || r.Offset != "next" {
				t.Fatal("wrong GIF query")
			}
			return &tg.MessagesBotResults{QueryID: 42, NextOffset: "more", Results: []tg.BotInlineResultClass{&tg.BotInlineResult{ID: "one", Type: "gif", Content: &tg.WebDocumentNoProxy{URL: "https://example.com/a.mp4", MimeType: "video/mp4", Size: 20, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 320, H: 180}}}, SendMessage: &tg.BotInlineMessageMediaAuto{Message: ""}}}}, nil
		default:
			return nil, fmt.Errorf("unexpected %T", in)
		}
	})
	p, err := s.Picker(context.Background(), model.PickerRequest{Tab: model.PickerGIF})
	if err != nil || len(p.Items) != 2 || p.Items[0].DocumentID != 9 {
		t.Fatalf("lost saved recency %+v %v", p, err)
	}
	p, err = s.Picker(context.Background(), model.PickerRequest{Tab: model.PickerGIF, Query: "cat", Offset: "next", ChatID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].QueryID != 42 || p.Items[0].Media.Media.Width != 320 || p.Next != "more" {
		t.Fatalf("lost web GIF %+v", p)
	}
}
func TestPickerRecentsPersist(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, e := range []string{"🙂", "👍", "🙂"} {
		if err := s.RememberPicker(ctx, model.PickerEmoji, model.PickerItem{ID: e, Emoji: e}); err != nil {
			t.Fatal(err)
		}
	}
	var recent []model.PickerItem
	ok, err := s.Cache().Get(ctx, "picker/recent/0", &recent)
	if err != nil || !ok || len(recent) != 2 || recent[0].Emoji != "🙂" {
		t.Fatalf("recent ordering %+v %v", recent, err)
	}
}

func TestVideoAttachmentAttributes(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	if _, err := os.Stat("../../../video.mp4"); err != nil {
		t.Skip("video fixture unavailable")
	}
	attrs, err := uploadAttributes(context.Background(), "../../../video.mp4", "video/mp4", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != 2 {
		t.Fatal("video attachment lacks metadata")
	}
	video, ok := attrs[1].(*tg.DocumentAttributeVideo)
	if !ok || video.W <= 0 || video.H <= 0 || video.Duration <= 0 || !video.SupportsStreaming {
		t.Fatalf("bad video attributes %+v", attrs)
	}
}

// A reply names its message in the request, and the message the short
// acknowledgement publishes quotes it before the server's copy arrives.
func TestSendReply(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[5] = &model.History{}
	replied := func(r tg.InputReplyToClass) int {
		if r, ok := r.(*tg.InputReplyToMessage); ok {
			return r.ReplyToMsgID
		}
		return 0
	}
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.MessagesSendMessageRequest:
			if replied(r.ReplyTo) != 7 {
				t.Fatalf("text reply: %+v", r.ReplyTo)
			}
			return &tg.UpdateShortSentMessage{ID: 9, Date: 10}, nil
		case *tg.MessagesSendMediaRequest:
			if replied(r.ReplyTo) != 8 {
				t.Fatalf("sticker reply: %+v", r.ReplyTo)
			}
			return &tg.Updates{}, nil
		case *tg.MessagesGetMessagesRequest:
			return nil, errors.New("fetch unavailable after acceptance")
		default:
			return nil, fmt.Errorf("unexpected %T", in)
		}
	})
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, Text: "yes", ReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	h := s.History(5)
	if len(h.Messages) != 1 || h.Messages[0].ReplyToMessageID != 7 {
		t.Fatalf("acknowledged reply lost its quote: %+v", h.Messages)
	}
	s.history.refs["document/3"] = fileLocation{ID: 3}
	if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 2, Item: &model.PickerItem{ID: "document/3"}, ReplyTo: 8}); err != nil {
		t.Fatal(err)
	}
}

func TestMessageLinks(t *testing.T) {
	s := testStore(t)
	s.rememberPeers(nil, []tg.ChatClass{
		&tg.Channel{ID: 10, Title: "Public", Username: "news", Broadcast: true},
		&tg.Channel{ID: 11, Title: "Collectible", Usernames: []tg.Username{{Username: "old"}, {Username: "shown", Active: true}}},
		&tg.Channel{ID: 12, Title: "Private"},
		&tg.Chat{ID: 13, Title: "Basic group"},
	})
	for _, c := range []struct {
		chat   int64
		link   string
		public bool
		ok     bool
	}{
		{-1000000000010, "https://t.me/news/5", true, true},
		{-1000000000011, "https://t.me/shown/5", true, true},
		{-1000000000012, "https://t.me/c/12/5", false, true},
		{-13, "", false, false},
	} {
		link, public, ok := s.MessageLink(c.chat, 5)
		if link != c.link || public != c.public || ok != c.ok {
			t.Errorf("chat %d: %q %v %v", c.chat, link, public, ok)
		}
	}
}

// TestPickerItemsLocateThumbnails checks that a GIF of the picker can fetch
// its thumbnail, which it shows until played, and not only its stripped
// preview.
func TestPickerItemsLocateThumbnails(t *testing.T) {
	s := testStore(t)
	doc := &tg.Document{ID: 7, AccessHash: 8, FileReference: []byte{1}, DCID: 2, MimeType: "video/mp4",
		Thumbs: []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{1, 40, 40}},
			&tg.PhotoSize{Type: "m", W: 320, H: 180, Size: 9000},
		},
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{W: 640, H: 360}},
	}
	items := s.pickerItems(context.Background(), []tg.DocumentClass{doc})
	if len(items) != 1 || items[0].Media.Media.Thumbnail == nil {
		t.Fatalf("items %+v have no thumbnail", items)
	}
	thumb := items[0].Media.Media.Thumbnail
	if ref, ok := s.history.refs[thumb.ID]; !ok || ref.Thumb != "m" || ref.ID != 7 {
		t.Fatalf("thumbnail %s located as %+v, %v", thumb.ID, ref, ok)
	}
}

// TestStickerSetCacheFindsByNameAndID checks that a set fetched by its short
// name is found again by it and by its ID.
func TestStickerSetCacheFindsByNameAndID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	set := model.StickerSet{Ref: model.StickerSetRef{Type: "id", ID: 42, AccessHash: 7}, Title: "Cats", Count: 1,
		Items: []model.PickerItem{{ID: "document/1", Emoji: "🐈"}}}
	s.cacheStickerSet(ctx, model.StickerSetRef{Type: "short_name", ShortName: "Cats"}, set)
	for _, ref := range []model.StickerSetRef{{Type: "short_name", ShortName: "cats"}, {Type: "id", ID: 42}} {
		got, ok := s.CachedStickerSet(ctx, ref)
		if !ok || got.Title != "Cats" || len(got.Items) != 1 || got.Items[0].ID != "document/1" {
			t.Fatalf("%+v found %v: %+v", ref, ok, got)
		}
	}
	if _, ok := s.CachedStickerSet(ctx, model.StickerSetRef{Type: "dice", Emoticon: "🎲"}); ok {
		t.Fatal("a dice set was cached")
	}
}

// A recording, or an audio file chosen for want of an FFmpeg, is sent as a
// voice message: with its type, duration and waveform, not as a file.
func TestSendVoice(t *testing.T) {
	for name, mime := range map[string]string{"voice.ogg": "audio/ogg", "chosen.mp3": "audio/mpeg", "chosen.m4a": "audio/mp4"} {
		s := testStore(t)
		s.history.peers[5] = peerRecord{Kind: "user", ID: 5}
		path := t.TempDir() + "/" + name
		if err := os.WriteFile(path, []byte("voice"), 0o600); err != nil {
			t.Fatal(err)
		}
		var sent *tg.InputMediaUploadedDocument
		s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
			switch r := in.(type) {
			case *tg.UploadSaveFilePartRequest:
				return &tg.BoolTrue{}, nil
			case *tg.MessagesSendMediaRequest:
				sent, _ = r.Media.(*tg.InputMediaUploadedDocument)
				return &tg.Updates{}, nil
			default:
				return nil, fmt.Errorf("unexpected %T", in)
			}
		})
		waveform := []byte{1, 2, 3}
		if err := s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1, Path: path, Voice: &model.VoiceNote{Duration: 2600 * time.Millisecond, Waveform: waveform}}); err != nil {
			t.Fatal(err)
		}
		if sent == nil || sent.MimeType != mime || sent.ForceFile || len(sent.Attributes) != 1 {
			t.Fatalf("%s: sent %+v", name, sent)
		}
		audio, ok := sent.Attributes[0].(*tg.DocumentAttributeAudio)
		if !ok || !audio.Voice || audio.Duration != 3 || string(audio.Waveform) != string(waveform) {
			t.Fatalf("%s: attribute %+v", name, sent.Attributes[0])
		}
	}
}

// A video sent as media is inspected by the ffprobe beside the FFmpeg the
// user set, not only by one on PATH.
func TestVideoAttributesUseChosenFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake programs are shell scripts")
	}
	dir := t.TempDir()
	for name, script := range map[string]string{
		"ffmpeg":  "#!/bin/sh\nexit 0\n",
		"ffprobe": "#!/bin/sh\necho '{\"streams\":[{\"width\":320,\"height\":240,\"duration\":\"1.5\"}]}'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(clip, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	attrs, err := uploadAttributes(context.Background(), clip, "video/mp4", true, filepath.Join(dir, "ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := attrs[len(attrs)-1].(*tg.DocumentAttributeVideo); !ok || v.W != 320 || v.H != 240 {
		t.Fatalf("attributes %+v", attrs)
	}
}

// A search keeps the rest of the page, so that the list does not change as it
// ends, and leaves the emoji of the picker to the client: Telegram is not
// asked for the keywords.
func TestPickerSearchKeepsThePage(t *testing.T) {
	s := testStore(t)
	s.picker.pages = map[model.PickerTab]model.PickerPage{model.PickerEmoji: {
		Recent:   []model.PickerItem{{ID: "emoji/🅰", Emoji: "🅰"}},
		Packs:    []model.PickerPack{{ID: 1, Title: "Mine"}},
		Featured: []model.PickerPack{{ID: 2, Title: "Featured"}},
	}}
	s.picker.at = map[model.PickerTab]time.Time{model.PickerEmoji: time.Now()}
	keywords := 0
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.MessagesSearchStickersRequest:
			return &tg.MessagesFoundStickers{Stickers: []tg.DocumentClass{&tg.Document{ID: 1, MimeType: "image/webp"}}}, nil
		case *tg.MessagesGetEmojiKeywordsRequest:
			keywords++
		}
		return nil, fmt.Errorf("unexpected %T", in)
	})
	p, err := s.Picker(context.Background(), model.PickerRequest{Tab: model.PickerEmoji, Query: "cat", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Recent) != 1 || len(p.Packs) != 1 || len(p.Featured) != 1 || len(p.Items) != 1 {
		t.Fatalf("the page of a search: %d recent, %d packs, %d featured, %d found", len(p.Recent), len(p.Packs), len(p.Featured), len(p.Items))
	}
	if keywords != 0 {
		t.Fatal("Telegram was asked for the emoji keywords")
	}
}
