package mockstore

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"komarugram/internal/messenger/model"
)

func (s *Store) OpenChat(id int64) {}
func (s *Store) LoadOlder(int64)   {}
func (s *Store) LoadNewer(int64)   {}
func (s *Store) Viewport(id int64) (model.Viewport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.views[id]
	return v, ok
}
func (s *Store) SaveView(v model.Viewport, ls []model.MessageLayout) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.views == nil {
		s.views = map[int64]model.Viewport{}
	}
	s.views[v.ChatID] = v
}
func (s *Store) Layouts(int64, model.RenderEnvironment) []model.MessageLayout { return nil }
func (s *Store) History(chat int64) model.History {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.histories[chat]; ok {
		return h
	}
	if chat == DemoNotesBot && s.isBot(chat) {
		// A bot nobody has written to: its chat is empty, and starts with
		// the Start button.
		h := model.History{Revision: 1}
		if s.histories == nil {
			s.histories = map[int64]model.History{}
		}
		s.histories[chat] = h
		return h
	}
	start := time.Date(2026, 9, 18, 12, 0, 0, 0, time.Local)
	messages := make([]model.Message, 0, 328)
	for i := 1; i <= 320; i++ {
		m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: model.MessageID(i)}, Date: start.Add(time.Duration(i/80)*24*time.Hour + time.Duration(i)*time.Minute), SenderID: 42, SenderName: "Анна", Text: fmt.Sprintf("Сообщение %d. История сохраняет положение при переключении чатов. 👋", i), ContentRevision: 1, Outgoing: i%4 == 0}
		if i%7 == 0 {
			m.Text = strings.Repeat("Длинный абзац для проверки переноса строк и прокрутки. ", 5)
		}
		if i%10 == 0 {
			m.Reactions = []model.Reaction{{Emoji: "❤", Count: 3}, {Emoji: "👍", Count: 1, Chosen: i%20 == 0}}
		}
		if i%35 == 0 {
			n := i / 35
			m.Kind, m.Media, m.Text = model.MessagePhoto, demoPhoto(n), fmt.Sprintf("Фото %d — откройте, чтобы листать все фото чата", n)
		}
		messages = append(messages, m)
	}
	add := func(text string, kind model.MessageKind, media *model.MessageMedia, entities []model.Entity, buttons [][]model.MessageButton) {
		messages = append(messages, model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: model.MessageID(len(messages) + 1)}, Date: start.Add(4*24*time.Hour + time.Duration(len(messages))*time.Minute), SenderID: 42, SenderName: "Демонстрация", Text: text, Kind: kind, Media: media, Entities: entities, Buttons: buttons, ContentRevision: 1})
	}

	rich := "👋 Привет! Жирный, курсив, код и спойлер. Telegram"
	var entities []model.Entity
	for _, pair := range [][2]string{{"bold", "Жирный"}, {"italic", "курсив"}, {"code", "код"}, {"spoiler", "спойлер"}, {"url", "Telegram"}} {
		i := strings.Index(rich, pair[1])
		e := model.Entity{Kind: pair[0], Offset: len(utf16.Encode([]rune(rich[:i]))), Length: len(utf16.Encode([]rune(pair[1])))}
		if e.Kind == "url" {
			e.URL = "https://telegram.org"
		}
		entities = append(entities, e)
	}
	add(rich, model.MessageText, nil, entities, nil)
	messages[len(messages)-1].WebPage = &model.WebPreview{URL: "https://telegram.org", Title: "Telegram", Description: "Telegram Messenger", Photo: &model.MessageMedia{ID: "demo/photo", MIMEType: "image/png", Width: 640, Height: 360}}
	add("", model.MessagePoll, nil, nil, nil)
	messages[len(messages)-1].Poll = &model.Poll{Question: "Какое оформление выберем?", Total: 10, Answers: []model.PollAnswer{{Text: "Светлое", Voters: 6}, {Text: "Тёмное", Voters: 4, Chosen: true}}}

	add("Изображение из локального кеша", model.MessagePhoto, &model.MessageMedia{ID: "demo/photo", MIMEType: "image/png", Width: 640, Height: 360}, nil, nil)
	add("GIF воспроизводится прямо в чате", model.MessageGIF, &model.MessageMedia{ID: "demo/gif", MIMEType: "image/gif", Width: 240, Height: 140}, nil, nil)
	add("", model.MessageSticker, &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}, nil, nil)
	add("", model.MessageSticker, &model.MessageMedia{ID: "demo/webm", MIMEType: "video/webm", Width: 512, Height: 512}, nil, nil)
	add("Видео открывается в отдельном окне", model.MessageVideo, &model.MessageMedia{ID: "demo/video", MIMEType: "video/mp4", Width: 640, Height: 360, Size: 7789765, Thumbnail: &model.MessageMedia{ID: "demo/photo", MIMEType: "image/png", Width: 640, Height: 360}}, nil, nil)
	// Voice messages: one with the waveform Telegram sends, one without,
	// whose waveform the player works out.
	add("", model.MessageVoice, &model.MessageMedia{ID: "demo/voice", MIMEType: "audio/ogg", Size: int64(len(demoVoice)), Duration: 7 * time.Second, Waveform: demoVoiceWaveform}, nil, nil)
	add("", model.MessageVoice, &model.MessageMedia{ID: "demo/voice-bare", MIMEType: "audio/ogg", Size: int64(len(demoVoice)), Duration: 7 * time.Second}, nil, nil)
	messages[len(messages)-1].MediaUnread = true
	// An MP3 voice message, as some bots send, without a waveform.
	add("", model.MessageVoice, &model.MessageMedia{ID: "demo/voice-mp3", MIMEType: "audio/mpeg", Size: int64(len(demoVoiceMP3)), Duration: 5 * time.Second}, nil, nil)
	// Music, which plays in the client as voice messages do.
	add("", model.MessageMusic, &model.MessageMedia{ID: "demo/music", MIMEType: "audio/mpeg", Size: int64(len(demoVoiceMP3)), Duration: 5 * time.Second, Title: "Ночной трамвай", Performer: "Демо-оркестр"}, nil, nil)
	// An M4A voice message, as one sent from a file; its decoder is fetched.
	add("", model.MessageVoice, &model.MessageMedia{ID: "demo/voice-m4a", MIMEType: "audio/mp4", Size: int64(len(demoVoiceM4A)), Duration: 5 * time.Second}, nil, nil)
	add("Кнопки бота: ссылки и обратные вызовы работают.", model.MessageText, nil, nil, [][]model.MessageButton{{{Text: "Telegram", Kind: "url", URL: "https://telegram.org"}, {Text: "Обновить", Kind: "callback", Data: []byte("refresh")}}})
	for i := 0; i < 3; i++ {
		add("Альбом: несколько вложений в одном сообщении", model.MessagePhoto, demoPhoto(len(demoPhotoSizes)-3+i), nil, nil)
		messages[len(messages)-1].GroupedID = 99
	}
	// A lone emoji is drawn large, without a bubble; two stay in one.
	add("🔥", model.MessageText, nil, nil, nil)
	add("❤", model.MessageText, nil, nil, nil)
	messages[len(messages)-1].Outgoing = true
	add("😀😀", model.MessageText, nil, nil, nil)
	add("", model.MessageSticker, &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}, nil, nil)
	messages[len(messages)-1].ReplyToMessageID = messages[len(messages)-2].Key.MessageID
	add("Следующая дата остаётся у верхнего края при прокрутке.", model.MessageText, nil, nil, nil)
	// Service messages: what happened in the chat, worded by the UI.
	service := func(a model.ServiceAction, reply model.MessageID) {
		add("", model.MessageService, nil, nil, nil)
		messages[len(messages)-1].Service = &a
		messages[len(messages)-1].ReplyToMessageID = reply
	}
	service(model.ServiceAction{Kind: model.ServiceAddUser, Peers: []model.ServicePeer{{ID: 7, Name: "Ольга"}, {ID: 8, Name: "Павел"}}}, 0)
	service(model.ServiceAction{Kind: model.ServicePin}, demoPinned[len(demoPinned)-1])
	service(model.ServiceAction{Kind: model.ServiceEditTitle, Title: "Команда разработки"}, 0)
	service(model.ServiceAction{Kind: model.ServiceTTL, Count: 7 * 86400}, 0)
	service(model.ServiceAction{Kind: model.ServicePhoneCall, Count: 754}, 0)
	spoiler := "Спойлер на нескольких строках: " + strings.Repeat("Нажмите здесь — текст откроется волной от места клика. ", 4)
	add(spoiler, model.MessageText, nil, []model.Entity{{Kind: "spoiler", Offset: 0, Length: len(utf16.Encode([]rune(spoiler)))}}, nil)
	add("Выделите часть этого текста и нажмите Ctrl/Cmd+C. Для выборки сообщений проведите по свободному месту рядом с пузырьками. Escape отменяет выделение.", model.MessageText, nil, nil, nil)
	page := RichExample(true)
	summary := page.Summary()
	add(summary.Text, model.MessageText, nil, summary.Entities, nil)
	messages[len(messages)-1].Rich = &page
	add("Статья с мгновенным просмотром: https://telegram.org/blog/instant-view", model.MessageText, nil, []model.Entity{{Kind: "url", Offset: 32, Length: 38, URL: "https://telegram.org/blog/instant-view"}}, nil)
	messages[len(messages)-1].WebPage = &model.WebPreview{URL: "https://telegram.org/blog/instant-view", DisplayURL: "telegram.org/blog/instant-view", Site: "Telegram", Title: "Instant View", Description: "Статьи из интернета открываются мгновенно, прямо в клиенте, даже без подключения.", InstantView: true}
	add("https://www.youtube.com/watch?v=demo", model.MessageText, nil, []model.Entity{{Kind: "url", Offset: 0, Length: 36}}, nil)
	messages[len(messages)-1].WebPage = &model.WebPreview{URL: "https://www.youtube.com/watch?v=demo", Site: "YouTube", Title: "Видео со ссылки", Description: "Telegram хранит видео страницы, и оно играет, как видео сообщения.", VideoKind: model.MessageVideo, Video: &model.MessageMedia{ID: "demo/video", MIMEType: "video/mp4", Width: 640, Height: 360, Size: 7789765, Thumbnail: &model.MessageMedia{ID: "demo/photo", MIMEType: "image/png", Width: 640, Height: 360}}}
	add("", model.MessageFile, &model.MessageMedia{ID: "demo/markdown", FileName: "Пример.md", MIMEType: "text/markdown", Size: int64(len(demoMarkdown))}, nil, nil)
	blocksText, blocksEntities := TextBlocksExample()
	add(blocksText, model.MessageText, nil, blocksEntities, nil)
	entitiesText, entities := EntitiesExample(time.Now())
	add(entitiesText, model.MessageText, nil, entities, nil)
	if s.isChannel(chat) {
		// A channel's posts have no sender and a discussion: the last ones
		// have comments, the others wait for the first one.
		for i := range messages {
			m := &messages[i]
			m.Post, m.Outgoing, m.SenderName, m.SenderID = true, false, "", 0
			m.Views = 1200 + 37*i
			m.CommentsOpen = true
			if i%3 != 0 {
				m.Comments = 1 + i%17
				m.Commenters = []int64{2, 5, 9}[:min(3, m.Comments)]
			}
		}
	}
	if s.isBot(chat) {
		messages = append(messages, demoBotMenu(chat, messages[len(messages)-1].Key.MessageID+1, start.Add(30*24*time.Hour)))
	}
	h := model.History{Messages: messages, Revision: 1}
	if s.histories == nil {
		s.histories = map[int64]model.History{}
	}
	s.histories[chat] = h
	return h
}
func (s *Store) Media(ctx context.Context, m model.Message) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if n, size, ok := parseDemoPhoto(m.Media.ID); ok {
		return demoPhotoJPEG(n, size.X, size.Y)
	}
	s.mu.Lock()
	sent, ok := s.files[m.Media.ID]
	s.mu.Unlock()
	if ok {
		return sent, nil
	}
	switch m.Media.ID {
	case "demo/markdown":
		return []byte(demoMarkdown), nil
	case "demo/voice", "demo/voice-bare":
		return demoVoice, nil
	case "demo/voice-mp3", "demo/music":
		return demoVoiceMP3, nil
	case "demo/voice-m4a":
		return demoVoiceM4A, nil
	case "demo/tgs":
		return os.ReadFile("stickers/sample.tgs")
	case "demo/webm":
		return os.ReadFile("stickers/circle.webm")
	case "demo/video":
		return os.ReadFile("video.mp4")
	case "demo/photo":
		im := image.NewRGBA(image.Rect(0, 0, 640, 360))
		for y := 0; y < 360; y++ {
			for x := 0; x < 640; x++ {
				im.SetRGBA(x, y, color.RGBA{uint8(30 + x/4), uint8(80 + y/3), 150, 255})
			}
		}
		e := png.Encode(&b, im)
		return b.Bytes(), e
	case "demo/gif":
		g := &gif.GIF{}
		palette := color.Palette{color.RGBA{35, 48, 75, 255}, color.RGBA{130, 190, 250, 255}, color.RGBA{245, 184, 120, 255}}
		for n := 0; n < 24; n++ {
			im := image.NewPaletted(image.Rect(0, 0, 240, 140), palette)
			for y := 35; y < 105; y++ {
				for x := n * 8; x < min(n*8+48, 240); x++ {
					im.SetColorIndex(x, y, 1+uint8(n%2))
				}
			}
			g.Image = append(g.Image, im)
			g.Delay = append(g.Delay, 6)
		}
		e := gif.EncodeAll(&b, g)
		return b.Bytes(), e
	}
	return nil, errors.New("demo media unavailable")
}

// Deterministic fixtures also exercise profile images and the energy policy.
func (s *Store) Avatar(id int64) (model.Message, bool) {
	meta := &model.MessageMedia{ID: "demo/photo", MIMEType: "image/png", Width: 640, Height: 360}
	if id%3 == 0 {
		meta.Thumbnail = &model.MessageMedia{ID: "demo/gif", MIMEType: "image/gif", Width: 240, Height: 140}
	}
	return model.Message{Kind: model.MessagePhoto, Media: meta}, true
}

func (s *Store) HistorySince(chat int64, revision uint64) (model.History, bool) {
	h := s.History(chat)
	if revision == h.Revision {
		h.Messages = nil
		return h, false
	}
	s.mu.Lock()
	h.Messages = append(slices.Clone(h.Messages), s.streamedDrafts(chat)...)
	s.mu.Unlock()
	return h, true
}

// demoVoice is 7.3 s of a tone that rises and falls as speech does, Opus
// in OGG as Telegram's voice messages; demoVoiceWaveform is its waveform:
//
//	ffmpeg -f lavfi -i "aevalsrc=0.6*sin(2*PI*(180+40*sin(2*PI*0.5*t))*t)*abs(sin(2*PI*0.9*t))*(0.35+0.65*abs(sin(2*PI*2.7*t))):s=48000:d=7.3" \
//	  -ac 1 -c:a libopus -b:a 24k -application voip voice.ogg
//
//go:embed voice.ogg
var demoVoice []byte

// demoVoiceMP3 is 4.6 s of another such tone, MP3 at 44.1 kHz:
//
//	ffmpeg -f lavfi -i "aevalsrc=0.6*sin(2*PI*(160+30*sin(2*PI*0.4*t))*t)*abs(sin(2*PI*1.1*t))*(0.3+0.7*abs(sin(2*PI*2.1*t))):s=44100:d=4.6" \
//	  -ac 1 -c:a libmp3lame -b:a 32k voice.mp3
//
//go:embed voice.mp3
var demoVoiceMP3 []byte

// demoVoiceM4A is 5.2 s of a third such tone, AAC-LC in M4A at 44.1 kHz:
//
//	ffmpeg -f lavfi -i "aevalsrc=0.6*sin(2*PI*(200+50*sin(2*PI*0.3*t))*t)*abs(sin(2*PI*0.8*t))*(0.3+0.7*abs(sin(2*PI*1.9*t))):s=44100:d=5.2" \
//	  -ac 1 -c:a aac -b:a 48k voice.m4a
//
//go:embed voice.m4a
var demoVoiceM4A []byte

var demoVoiceWaveform = []byte{0x2b, 0x4e, 0xdf, 0x63, 0x2c, 0x4f, 0xea, 0x7e, 0xd9, 0x9, 0xe6, 0x6c, 0x1e, 0x65, 0x72, 0xf2, 0x7a, 0x1e, 0x61, 0x89, 0xb2, 0xff, 0xfa, 0x82, 0x92, 0xd2, 0x7b, 0x19, 0x4d, 0x89, 0xda, 0x67, 0xf9, 0x8e, 0x94, 0xde, 0x47, 0x99, 0x18, 0x94, 0xbe, 0xcb, 0x48, 0xa2, 0xe4, 0xde, 0x2e, 0xa6, 0x64, 0xf4, 0x5e, 0xca, 0xf3, 0x64, 0xf6, 0x3a, 0x1a, 0x13, 0xa5, 0xf7, 0x52, 0xaa, 0x6}
