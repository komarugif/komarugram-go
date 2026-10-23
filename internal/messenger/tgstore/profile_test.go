// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

func profileGift(id int64, pinned bool) tg.SavedStarGift {
	doc := &tg.Document{ID: id, AccessHash: 1, FileReference: []byte{1}, Date: 1, MimeType: "application/x-tgsticker", Size: 10, DCID: 2,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Stickerset: &tg.InputStickerSetEmpty{}}}}
	return tg.SavedStarGift{Date: 1, PinnedToTop: pinned, SavedID: id, Gift: &tg.StarGiftUnique{ID: id, Title: "Gift", Slug: "gift", Num: int(id),
		Attributes: []tg.StarGiftAttributeClass{&tg.StarGiftAttributeModel{Name: "Model", Document: doc, Rarity: &tg.StarGiftAttributeRarity{Permille: 10}}}}}
}

// A user's profile: the usernames that are collectible, the cover of the
// gift the emoji status is, the gifts pinned but that one, and the channel
// with its subscribers and its post; offline, the one kept.
func TestPeerProfile(t *testing.T) {
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 5, AccessHash: 1, FirstName: "Svatosh"}}, nil)
	user := &tg.User{ID: 5, AccessHash: 1, FirstName: "Svatosh",
		Usernames: []tg.Username{{Username: "svatosh", Editable: true, Active: true}, {Username: "y9svatosh", Active: true}, {Username: "gone"}},
		EmojiStatus: &tg.EmojiStatusCollectible{CollectibleID: 100, DocumentID: 1, Title: "Gift", Slug: "gift-100", PatternDocumentID: 55,
			CenterColor: 0x6fae6a, EdgeColor: 0x2f6e43, PatternColor: 0x1c4f2c, TextColor: 0xffffff}}
	var asked []string
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch r := in.(type) {
		case *tg.UsersGetFullUserRequest:
			asked = append(asked, "user")
			full := tg.UserFull{ID: 5, Settings: tg.PeerSettings{}, NotifySettings: tg.PeerNotifySettings{}}
			full.SetPersonalChannelID(9)
			full.SetPersonalChannelMessage(77)
			return &tg.UsersUserFull{FullUser: full, Users: []tg.UserClass{user},
				Chats: []tg.ChatClass{&tg.Channel{ID: 9, AccessHash: 3, Title: "Fund", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}}}, nil
		case *tg.ChannelsGetFullChannelRequest:
			asked = append(asked, "channel")
			full := &tg.ChannelFull{ID: 9, ChatPhoto: &tg.PhotoEmpty{}, NotifySettings: tg.PeerNotifySettings{}}
			full.SetParticipantsCount(6)
			return &tg.MessagesChatFull{FullChat: full}, nil
		case *tg.ChannelsGetMessagesRequest:
			asked = append(asked, "post")
			if id, ok := r.ID[0].(*tg.InputMessageID); !ok || id.ID != 77 {
				t.Fatalf("post asked %+v", r.ID)
			}
			return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.Message{ID: 77, PeerID: &tg.PeerChannel{ChannelID: 9}, Message: "Сюда  бабки 💸", Date: 10,
				Entities: []tg.MessageEntityClass{&tg.MessageEntityCustomEmoji{Offset: 12, Length: 2, DocumentID: 8}}}}}, nil
		case *tg.PaymentsGetSavedStarGiftsRequest:
			asked = append(asked, "gifts")
			return &tg.PaymentsSavedStarGifts{Count: 3, Gifts: []tg.SavedStarGift{profileGift(100, true), profileGift(101, true), profileGift(102, false)}}, nil
		}
		t.Fatalf("unexpected request %T", in)
		return nil, nil
	})
	ctx := context.Background()
	check := func(p model.PeerProfile) {
		t.Helper()
		want := []model.Username{{Name: "svatosh"}, {Name: "y9svatosh", Collectible: true}}
		if len(p.Usernames) != 2 || p.Usernames[0] != want[0] || p.Usernames[1] != want[1] {
			t.Fatalf("usernames %+v", p.Usernames)
		}
		if c := p.Cover; !c.Gift || c.Pattern != 55 || c.CenterColor != 0x6fae6a || c.TextColor != 0xffffff {
			t.Fatalf("cover %+v", c)
		}
		if len(p.Gifts) != 1 || p.Gifts[0].Gift == nil || p.Gifts[0].Gift.Number != 101 {
			t.Fatalf("gifts %+v", p.Gifts)
		}
		// The status Telegram says now, for a person with no dialog to bring it.
		if !p.BadgesKnown || p.Badges.EmojiStatus != 1 || p.Badges.User != 5 {
			t.Fatalf("badges %+v known %v", p.Badges, p.BadgesKnown)
		}
		ch := p.Channel
		if ch == nil || ch.Chat.Title != "Fund" || ch.Chat.Kind != model.KindChannel || ch.Chat.Members != 6 || ch.Post.Text != "Сюда  бабки 💸" ||
			len(ch.Entities) != 1 || ch.Entities[0].Kind != "emoji" || ch.Entities[0].DocumentID != 8 {
			t.Fatalf("channel %+v", ch)
		}
	}
	if _, ok := s.CachedPeerProfile(5); ok {
		t.Fatal("a profile cached before it was asked for")
	}
	p, err := s.PeerProfile(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	check(p)
	// What it said of the user is kept as well, so that a chat made of it
	// has the new status, not the one the user was remembered with.
	if got := s.history.peers[peerID(&tg.PeerUser{UserID: 5})].Metadata.Badges.EmojiStatus; got != 1 {
		t.Fatalf("the user is remembered with status %d, want 1", got)
	}
	cached, ok := s.CachedPeerProfile(5)
	if !ok {
		t.Fatal("the profile is not cached")
	}
	check(cached)
	s.history.api = nil
	p, err = s.PeerProfile(ctx, 5)
	if err != nil {
		t.Fatalf("offline: %v", err)
	}
	check(p)
}

// A channel's profile color is its cover, light and dark, by Telegram's
// colors, which are asked for once, with its background emoji for the
// pattern.
func TestPeerProfileColor(t *testing.T) {
	s := testStore(t)
	s.rememberPeers(nil, []tg.ChatClass{&tg.Channel{ID: 9, AccessHash: 3, Title: "Go", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}})
	colors := 0
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.ChannelsGetFullChannelRequest:
			color := &tg.PeerColor{}
			color.SetColor(3)
			color.SetBackgroundEmojiID(77)
			channel := &tg.Channel{ID: 9, AccessHash: 3, Title: "Go", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}
			channel.SetProfileColor(color)
			return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ID: 9, ChatPhoto: &tg.PhotoEmpty{}, NotifySettings: tg.PeerNotifySettings{}}, Chats: []tg.ChatClass{channel}}, nil
		case *tg.HelpGetPeerProfileColorsRequest:
			colors++
			option := tg.HelpPeerColorOption{ColorID: 3}
			option.SetColors(&tg.HelpPeerColorProfileSet{PaletteColors: []int{1}, BgColors: []int{0x5a8fd6, 0x82b4ee}, StoryColors: []int{1}})
			option.SetDarkColors(&tg.HelpPeerColorProfileSet{PaletteColors: []int{1}, BgColors: []int{0x2a4f80}, StoryColors: []int{1}})
			return &tg.HelpPeerColors{Hash: 1, Colors: []tg.HelpPeerColorOption{option}}, nil
		case *tg.PaymentsGetSavedStarGiftsRequest:
			return &tg.PaymentsSavedStarGifts{}, nil
		}
		t.Fatalf("unexpected request %T", in)
		return nil, nil
	})
	chat := peerID(&tg.PeerChannel{ChannelID: 9})
	for range 2 {
		p, err := s.PeerProfile(context.Background(), chat)
		if err != nil {
			t.Fatal(err)
		}
		c := p.Cover
		if c.Gift || len(c.Colors) != 2 || c.Colors[1] != 0x82b4ee || len(c.DarkColors) != 1 || c.DarkColors[0] != 0x2a4f80 || c.Pattern != 77 {
			t.Fatalf("cover %+v", c)
		}
	}
	if colors != 1 {
		t.Fatalf("colors asked %d times", colors)
	}
}
