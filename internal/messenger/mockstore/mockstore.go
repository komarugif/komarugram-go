// SPDX-License-Identifier: Unlicense OR MIT

// Package mockstore is a model.Store with fixed demo data, used until the
// client talks to a real server.
package mockstore

import (
	"context"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/miniapp"
)

type Store struct {
	sent map[int64]bool
	// files are what the voice messages sent in the demo hold, by media ID.
	files map[string][]byte
	// themes are the chats' themes set only here; chatThemes, the emoji of
	// the ones set for all in the chat, as Telegram keeps them.
	themes     map[int64]model.ChatTheme
	chatThemes map[int64]string
	// mu guards histories and views: a photo viewer in its own window reads
	// the store from that window's goroutine.
	mu        sync.Mutex
	histories map[int64]model.History
	chats     []model.Chat
	views     map[int64]model.Viewport
	me        model.Profile
	// recent is the search history; query, the search asked for last.
	recent []model.Chat
	query  model.SearchQuery
	// pinnedHidden are the chats whose pinned messages were hidden.
	pinnedHidden map[int64]bool
	// created is when the store was made, which the times of its forum's
	// topics count back from.
	created time.Time
	// miniApp is the bundled Mini App the demo's bots open, served once asked.
	miniApp *miniapp.Demo
	// notices tells of what Receive brings, the received-th message.
	notices  func(model.MessageNotice)
	received int
	// streams are the drafts the demo's bots stream, by chat; changed is
	// told when one goes further.
	streams map[int64]*demoStream
	changed func()
}

// New returns a store with demo chats whose times are relative to now, and
// extra generated chats to try the UI with long lists.
func New(now time.Time, extra int) *Store {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	day := 24 * time.Hour
	chats := []model.Chat{
		{ID: 1, Kind: model.KindSaved, Title: "Избранное", LastMessage: "Список покупок на выходные", LastTime: ago(2 * time.Hour), Pinned: true},
		{ID: 2, Kind: model.KindUser, Title: "Анна Смирнова", LastMessage: "До встречи в субботу!", LastTime: ago(5 * time.Minute), Unread: 2, Badges: model.Badges{Premium: true}},
		{ID: 3, Kind: model.KindGroup, Title: "Команда разработки", LastSender: "Игорь", LastMessage: "Сборка прошла, можно выкатывать", LastTime: ago(20 * time.Minute), Unread: 14, Members: 12},
		{ID: 4, Kind: model.KindChannel, Title: "Новости Go", LastMessage: "Вышел Go 1.27: что нового в стандартной библиотеке", LastTime: ago(3 * time.Hour), Unread: 5, Muted: true, Members: 48210, Badges: model.Badges{Verified: true}},
		{ID: 5, Kind: model.KindUser, Title: "Мама", LastMessage: "Позвони, как освободишься", LastTime: ago(26 * time.Hour)},
		{ID: 6, Kind: model.KindBot, Title: "Погодный бот", LastMessage: "Завтра +18°, без осадков", LastTime: ago(9 * time.Hour), Unread: 1},
		{ID: 7, Kind: model.KindGroup, Title: "Соседи по дому", LastSender: "Ольга", LastMessage: "Кто-нибудь видел рыжего кота?", LastTime: ago(2 * day), Unread: 37, Muted: true, Members: 86},
		{ID: 8, Kind: model.KindChannel, Title: "Material Design", LastMessage: "Expressive motion: новые токены пружинных анимаций", LastTime: ago(3 * day), Members: 15320, Badges: model.Badges{Verified: true}},
		{ID: 9, Kind: model.KindUser, Title: "Дмитрий Козлов", LastMessage: "Скинул ссылку на репозиторий", LastTime: ago(4 * day), Badges: model.Badges{Premium: true}},
		{ID: 10, Kind: model.KindGroup, Title: "Книжный клуб", LastSender: "Вы", LastMessage: "Предлагаю в этот раз фантастику", LastTime: ago(6 * day), Members: 9},
		{ID: 11, Kind: model.KindBot, Title: "Бот заметок", LastMessage: "Напоминание: оплатить интернет", LastTime: ago(8 * day), Badges: model.Badges{Scam: true}},
		{ID: 12, Kind: model.KindUser, Title: "Екатерина", LastMessage: "Спасибо за помощь!", LastTime: ago(12 * day)},
		{ID: 13, Kind: model.KindChannel, Title: "Фото природы", LastMessage: "Рассвет над Байкалом", LastTime: ago(15 * day), Muted: true, Members: 3021},
		{ID: 14, Kind: model.KindGroup, Title: "Одногруппники", LastSender: "Павел", LastMessage: "Встреча выпускников 20 сентября", LastTime: ago(20 * day), Members: 27},
		{ID: 15, Kind: model.KindUser, Title: "Сергей Иванов", LastMessage: "Ок", LastTime: ago(40 * day), Badges: model.Badges{Fake: true}},
		{ID: DemoForum, Kind: model.KindGroup, Forum: true, Title: "Клуб Go: обсуждения", LastSender: "Игорь", LastMessage: "Релиз в пятницу, кто берёт заметки?", LastTime: ago(35 * time.Minute), Unread: 9, Members: 240},
	}
	chats = append(chats, generate(now, extra, int64(len(chats)+1))...)
	model.Renumbered(chats)
	return &Store{chats: chats, created: now,
		// A chat of the demo has a theme set for all in it, as from Telegram.
		chatThemes: map[int64]string{2: "🌷"}, me: model.Profile{
			ID:        5000000001,
			DC:        2,
			FirstName: "Алексей",
			LastName:  "Петров",
			Username:  "alexey_petrov",
			Phone:     "+7 900 000-00-00",
			Bio:       "Пишу интерфейсы на Go и Gio.",
			Badges:    model.Badges{Premium: true},
		}}
}

// Premium implements model.PremiumSource: the demo account has Premium,
// with Telegram Desktop's limits.
func (s *Store) Premium() model.Premium {
	return model.PremiumFromConfig(s.Me().Premium, map[string]any{"premium_purchase_blocked": false, "premium_bot_username": "PremiumBot"})
}

func (s *Store) Me() model.Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.me
}

// EditProfile implements model.ProfileEditor. The demo keeps the edit until
// the window closes; "durov" stands for a username someone else has.
func (s *Store) EditProfile(ctx context.Context, e model.ProfileEdit) error {
	if err := e.ValidateWith(s.Premium().Limit("about_length_limit")); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(300 * time.Millisecond): // As long as a round trip.
	}
	if strings.EqualFold(e.Username, "durov") {
		return model.ErrUsernameTaken
	}
	s.mu.Lock()
	s.me.FirstName, s.me.LastName, s.me.Username, s.me.Bio = e.FirstName, e.LastName, e.Username, e.Bio
	s.mu.Unlock()
	return nil
}

func (s *Store) Folders() []model.Folder {
	return []model.Folder{
		{ID: 1, Title: "Личные", Kinds: []model.ChatKind{model.KindUser, model.KindSaved}},
		{ID: 2, Title: "Группы", Kinds: []model.ChatKind{model.KindGroup}},
		{ID: 3, Title: "Каналы", Kinds: []model.ChatKind{model.KindChannel}},
		{ID: 4, Title: "Боты", Kinds: []model.ChatKind{model.KindBot}},
	}
}

func (s *Store) Chats() []model.Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats
}

// PinChat implements model.ChatListActions with the limit of the demo
// account's Premium.
func (s *Store) PinChat(ctx context.Context, chat int64, pin bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond): // As long as a round trip.
	}
	limit := s.Premium().Limit("dialogs_pinned_limit")
	s.mu.Lock()
	defer s.mu.Unlock()
	pinned := false
	for _, c := range s.chats {
		if c.ID == chat {
			pinned = c.Pinned
		}
	}
	if pin && !pinned && model.PinnedCount(s.chats) >= limit {
		return model.ErrPinnedTooMuch
	}
	s.chats = model.WithPinned(s.chats, chat, pin)
	return nil
}

// MarkChatRead implements model.ChatListActions.
func (s *Store) MarkChatRead(chat int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := append([]model.Chat(nil), s.chats...)
	for i := range chats {
		if chats[i].ID == chat {
			chats[i].Unread = 0
		}
	}
	s.chats = chats
}

var (
	maleNames   = []string{"Андрей", "Иван", "Никита", "Артём", "Михаил", "Роман"}
	femaleNames = []string{"Мария", "Ольга", "Светлана", "Юлия", "Татьяна", "Алина"}
	firstNames  = append(append([]string{}, maleNames...), femaleNames...)
	// Male surnames; the female form adds "а".
	lastNames  = []string{"Соколов", "Волков", "Морозов", "Лебедев", "Попов", "Новиков", "Фёдоров", "Кузнецов", "Орлов", "Зайцев"}
	groupNames = []string{"Проект", "Чат", "Клуб", "Команда", "Сообщество", "Курс"}
	topics     = []string{"велосипедистов", "по Go", "выходного дня", "фотографов", "любителей кофе", "поддержки", "путешественников", "по дизайну"}
	messages   = []string{"Привет! Как дела?", "Скинь, пожалуйста, файл", "Созвонимся завтра?", "Готово, посмотри", "Спасибо!", "Увидимся вечером", "Новый выпуск уже доступен", "Кто со мной?"}
)

// generate returns n chats with ids from firstID, deterministic for n.
func generate(now time.Time, n int, firstID int64) []model.Chat {
	rng := rand.New(rand.NewPCG(1, uint64(n)))
	pick := func(list []string) string { return list[rng.IntN(len(list))] }
	chats := make([]model.Chat, n)
	for i := range chats {
		c := model.Chat{
			ID:          firstID + int64(i),
			LastMessage: pick(messages),
			// Older and older, a few minutes apart on average.
			LastTime: now.Add(-time.Duration(i+1) * time.Duration(1+rng.IntN(10)) * time.Minute),
		}
		switch kind := rng.IntN(10); {
		case kind < 5:
			c.Kind = model.KindUser
			if rng.IntN(2) == 0 {
				c.Title = pick(maleNames) + " " + pick(lastNames)
			} else {
				c.Title = pick(femaleNames) + " " + pick(lastNames) + "а"
			}
		case kind < 8:
			c.Kind = model.KindGroup
			c.Title = pick(groupNames) + " " + pick(topics)
			c.LastSender = pick(firstNames)
			c.Members = 3 + rng.IntN(500)
		case kind < 9:
			c.Kind = model.KindChannel
			c.Title = "Канал " + pick(topics)
			c.Members = 100 + rng.IntN(100000)
		default:
			c.Kind = model.KindBot
			c.Title = "Бот " + pick(topics)
		}
		if rng.IntN(4) == 0 {
			c.Unread = 1 + rng.IntN(120)
			c.Muted = rng.IntN(3) == 0
		}
		chats[i] = c
	}
	return chats
}
