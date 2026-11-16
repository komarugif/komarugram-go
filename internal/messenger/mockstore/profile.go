// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"slices"
	"time"

	"komarugram/internal/messenger/model"
)

// PeerProfile implements model.PeerProfileSource: Анна Смирнова shows a
// unique gift's cover with gifts around her avatar, collectible usernames
// and her channel; Новости Go a profile color, and its own username after
// the collectible one it put first; chat 8 has one collectible name alone.
func (s *Store) PeerProfile(_ context.Context, chat int64) (model.PeerProfile, error) {
	var out model.PeerProfile
	if name := usernames[chat]; name != "" {
		out.Usernames = []model.Username{{Name: name}}
	}
	switch chat {
	case 2:
		out.Usernames = append(out.Usernames, model.Username{Name: "anna", Collectible: true}, model.Username{Name: "smirnova", Collectible: true})
		out.About = "Фотографирую рассветы и пишу о них в t.me/gonews. Пишите @gopher_bot или на https://go.dev."
		out.Phone = "79001234567"
		out.Cover = model.ProfileCover{Gift: true, CenterColor: 0x6fae6a, EdgeColor: 0x2f6e43, PatternColor: 0x1c4f2c, TextColor: 0xffffff, Pattern: 1}
		for _, i := range []int{1, 3, 5} {
			out.Gifts = append(out.Gifts, demoGift(chat, i))
		}
		s.mu.Lock()
		for _, c := range s.chats {
			if c.ID == 13 {
				// The post ends with a custom emoji, drawn as the demo's
				// sticker.
				post := c.Last
				post.Text += " 🌅"
				out.Channel = &model.PersonalChannel{Chat: c, Post: post, Date: c.LastTime,
					Entities: []model.Entity{{Kind: "emoji", Offset: 21, Length: 2, DocumentID: 3}}}
			}
		}
		s.mu.Unlock()
	case 4:
		out.Usernames = []model.Username{{Name: "gonews", Collectible: true}, {Name: "golang_news"}}
		out.About = "Новости Go и его экосистемы: релизы, предложения и статьи. Обсуждение — @golang_chat, сайт — https://go.dev."
		out.Cover.Colors = []uint32{0x5a8fd6, 0x82b4ee}
		out.Cover.DarkColors = []uint32{0x2a4f80, 0x3f6aa3}
		out.Cover.Pattern = 2
	case 8:
		// A lone collectible name, which a hint points to.
		out.Usernames = []model.Username{{Name: "material_design", Collectible: true}}
	}
	if songs := s.playlist(chat); len(songs) > 0 {
		out.Music = &songs[0]
	}
	return out, nil
}

// demoPlaylists are the songs on the demo's profiles, by their titles and
// performers: Анна Смирнова's, and the account's own, chat 0 here.
var demoPlaylists = map[int64][][2]string{
	2: {{"Ночной трамвай", "Демо-оркестр"}, {"Рассвет над Байкалом", "Северный ветер"}, {"Утро в горах", "Демо-оркестр"}},
	0: {{"Gopher Blues", "The Goroutines"}, {"Select Without Default", "Channel Five"}},
}

// playlist is the playlist of chat's profile: songs keyed by their places,
// as Telegram's store has them; the account's own as it changed it. The
// demo's all play the demo's song.
func (s *Store) playlist(chat int64) []model.Message {
	var songs []model.MessageMedia
	if chat == s.Me().ID {
		s.mu.Lock()
		songs = slices.Clone(s.ownMusicLocked())
		s.mu.Unlock()
	} else {
		songs = demoSongs(chat)
	}
	var out []model.Message
	for i, media := range songs {
		out = append(out, model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: model.MessageID(-i - 1)}, Kind: model.MessageMusic, Media: &media, Playlist: true, ContentRevision: 1})
	}
	return out
}

// demoSongs are the songs of demoPlaylists of key, the first with a cover.
func demoSongs(key int64) []model.MessageMedia {
	var out []model.MessageMedia
	for i, song := range demoPlaylists[key] {
		media := model.MessageMedia{ID: "demo/music", MIMEType: "audio/mpeg", Size: int64(len(demoVoiceMP3)), Duration: 5 * time.Second, Title: song[0], Performer: song[1]}
		if i == 0 {
			media.Thumbnail = &model.MessageMedia{ID: "demo/cover", MIMEType: "image/png", Width: 320, Height: 320}
		}
		out = append(out, media)
	}
	return out
}

// ownMusicLocked is the account's playlist, made from the demo's the first
// time; under s.mu.
func (s *Store) ownMusicLocked() []model.MessageMedia {
	if s.music == nil {
		s.music = demoSongs(0)
	}
	return s.music
}

// sameSong reports whether a and b are one song: the demo's songs are
// told apart by their titles and performers, their file being one.
func sameSong(a, b *model.MessageMedia) bool {
	return a != nil && b != nil && a.Title == b.Title && a.Performer == b.Performer
}

// SavedMusic implements model.SavedMusicStore.
func (s *Store) SavedMusic(m model.Message) bool {
	return slices.ContainsFunc(s.playlist(s.Me().ID), func(song model.Message) bool { return sameSong(song.Media, m.Media) })
}

// SaveMusic implements model.SavedMusicStore: the song goes first on the
// account's playlist, as Telegram puts it, moved there when it is on it.
func (s *Store) SaveMusic(_ context.Context, m model.Message) error {
	if m.Media == nil || m.Kind != model.MessageMusic {
		return errors.New("no song to save")
	}
	media := *m.Media
	s.mu.Lock()
	defer s.mu.Unlock()
	songs := slices.DeleteFunc(slices.Clone(s.ownMusicLocked()), func(song model.MessageMedia) bool { return sameSong(&song, &media) })
	s.music = append([]model.MessageMedia{media}, songs...)
	return nil
}

// RemoveMusic implements model.SavedMusicStore.
func (s *Store) RemoveMusic(_ context.Context, m model.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.music = slices.DeleteFunc(slices.Clone(s.ownMusicLocked()), func(song model.MessageMedia) bool { return sameSong(&song, m.Media) })
	return nil
}

// MoveMusic implements model.SavedMusicStore.
func (s *Store) MoveMusic(_ context.Context, m model.Message, after *model.Message) error {
	if m.Media == nil {
		return errors.New("no song to move")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	songs := slices.DeleteFunc(slices.Clone(s.ownMusicLocked()), func(song model.MessageMedia) bool { return sameSong(&song, m.Media) })
	at := 0
	if after != nil {
		at = slices.IndexFunc(songs, func(song model.MessageMedia) bool { return sameSong(&song, after.Media) }) + 1
		if at == 0 {
			return errors.New("no such song to put it after")
		}
	}
	s.music = slices.Insert(songs, at, *m.Media)
	return nil
}

// CachedPeerProfile implements model.PeerProfileSource: the demo knows
// every profile already.
func (s *Store) CachedPeerProfile(chat int64) (model.PeerProfile, bool) {
	p, err := s.PeerProfile(context.Background(), chat)
	return p, err == nil
}

// CustomEmoji is every custom emoji of the demo, as the cover's pattern:
// its sticker.
func (s *Store) CustomEmoji(_ context.Context, id int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}}, nil
}

// CollectibleUsername implements model.PeerProfileSource: every demo
// collectible was bought on one day for 10 TON.
func (s *Store) CollectibleUsername(_ context.Context, username string) (model.CollectibleUsername, error) {
	return model.CollectibleUsername{
		Username:       username,
		Bought:         time.Date(2023, 8, 31, 19, 59, 0, 0, time.Local),
		Currency:       "USD",
		Amount:         1368,
		CryptoCurrency: "TON",
		CryptoAmount:   10_000_000_000,
		URL:            "https://fragment.com/username/" + username,
	}, nil
}
