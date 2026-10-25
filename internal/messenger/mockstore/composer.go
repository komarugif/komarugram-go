package mockstore

import (
	"context"
	"errors"
	"fmt"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/voice"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// demoSavedGIF starts the media IDs of the GIFs of the demo's picker, each
// its own, all drawn as the demo's GIF.
const demoSavedGIF = "demo/gif/"

var (
	_ model.SavedGIFStore      = (*Store)(nil)
	_ model.RecentStickerStore = (*Store)(nil)
)

// demoGIF is a GIF of the picker of media id, w wide.
func demoGIF(id string, w int) model.PickerItem {
	return model.PickerItem{ID: id, Media: model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: id, MIMEType: "image/gif", Width: w, Height: 160}}}
}

// savedGIFs are the demo's saved GIFs; s.mu is held.
func (s *Store) savedGIFs() []model.PickerItem {
	if !s.gifsKept {
		s.gifsKept = true
		for i := 0; i < 12; i++ {
			s.gifs = append(s.gifs, demoGIF(fmt.Sprintf("%s%d", demoSavedGIF, i), 180+i%4*70))
		}
	}
	return s.gifs
}

// SavedGIF implements model.SavedGIFStore.
func (s *Store) SavedGIF(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.savedGIFs(), func(item model.PickerItem) bool { return item.ID == id })
}

// SaveGIF implements model.SavedGIFStore in memory, with the limit of the
// demo's account.
func (s *Store) SaveGIF(ctx context.Context, m model.Message, unsave bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Media == nil {
		return errors.New("no GIF to save")
	}
	limit := model.SavedGIFLimit(s.Premium())
	s.mu.Lock()
	defer s.mu.Unlock()
	gifs := model.WithoutItem(s.savedGIFs(), m.Media.ID)
	if !unsave {
		gifs = append([]model.PickerItem{model.PickerItemOf(m)}, gifs...)
	}
	s.gifs = gifs
	if limit > 0 && len(s.gifs) > limit {
		s.gifs = s.gifs[:limit]
		return model.ErrSavedGIFLimit
	}
	return nil
}

// RemoveRecentSticker implements model.RecentStickerStore in memory.
func (s *Store) RemoveRecentSticker(ctx context.Context, item model.PickerItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recentGone == nil {
		s.recentGone = map[string]bool{}
	}
	s.recentGone[item.ID] = true
	return nil
}

func (s *Store) Picker(ctx context.Context, r model.PickerRequest) (model.PickerPage, error) {
	if err := ctx.Err(); err != nil {
		return model.PickerPage{}, err
	}
	sticker := func(id, emoji string) model.PickerItem {
		return model.PickerItem{ID: id, Emoji: emoji, Media: model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: id, MIMEType: "application/x-tgsticker", Width: 512, Height: 512}}}
	}
	page := model.PickerPage{}
	if r.Tab == model.PickerGIF {
		if r.Query != "" {
			// What a search finds: GIFs not saved, which can be.
			for i := 0; i < 6; i++ {
				item := demoGIF(fmt.Sprintf("%sfound/%d", demoSavedGIF, i), 140+i%3*60)
				// Of a document, as the GIFs Telegram's bot finds are.
				item.DocumentID = int64(1000 + i)
				page.Items = append(page.Items, item)
			}
			return page, nil
		}
		s.mu.Lock()
		page.Items = slices.Clone(s.savedGIFs())
		s.mu.Unlock()
		return page, nil
	}
	if r.Tab == model.PickerEmoji {
		return page, nil
	}
	for n := 1; n <= 12; n++ {
		p := model.PickerPack{ID: int64(n), Title: fmt.Sprintf("Demo %d", n)}
		for i := 0; i < 20; i++ {
			item := sticker("demo/tgs", "🙂")
			item.ID = fmt.Sprintf("demo/sticker/%d/%d", n, i)
			p.Items = append(p.Items, item)
		}
		page.Packs = append(page.Packs, p)
	}
	s.mu.Lock()
	for _, item := range page.Packs[0].Items[:5] {
		if !s.recentGone[item.ID] {
			page.Recent = append(page.Recent, item)
		}
	}
	s.mu.Unlock()
	if r.Query != "" {
		for _, p := range page.Packs {
			if strings.Contains(strings.ToLower(p.Title), strings.ToLower(r.Query)) {
				page.Items = append(page.Items, p.Items...)
			}
		}
	}
	return page, nil
}
func (s *Store) Send(ctx context.Context, chat int64, out model.OutgoingMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := out.Validate(); err != nil {
		return err
	}
	s.History(chat)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent == nil {
		s.sent = map[int64]bool{}
	}
	if s.sent[out.RandomID] {
		return nil
	}
	s.sent[out.RandomID] = true
	h := s.histories[chat]
	id := model.MessageID(1)
	if len(h.Messages) > 0 {
		id = h.Messages[len(h.Messages)-1].Key.MessageID + 1
	}
	if out.Files != nil {
		err := s.appendFiles(chat, h, id, out)
		if err != nil {
			// A try that failed may be made again.
			delete(s.sent, out.RandomID)
		}
		return err
	}
	m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: id}, Text: out.Text, Entities: out.Entities, Outgoing: true, Date: time.Now(), SenderID: s.me.ID, ContentRevision: 1, ReplyToMessageID: out.ReplyTo}
	if out.Item != nil {
		m.Kind = out.Item.Media.Kind
		m.Media = out.Item.Media.Media
	}
	if out.Voice != nil {
		// A voice message, as Telegram returns one: no name, its duration.
		m.Kind = model.MessageVoice
		m.Media = &model.MessageMedia{ID: fmt.Sprintf("demo/sent/%d/%d", chat, id), MIMEType: "audio/ogg", Duration: out.Voice.Duration, Waveform: out.Voice.Waveform}
		if mime := voice.FileMIME(out.Path); mime != "" {
			m.Media.MIMEType = mime
		}
		// The file is gone once sent: the demo keeps what it held, to play.
		if data, err := os.ReadFile(out.Path); err == nil {
			if s.files == nil {
				s.files = map[string][]byte{}
			}
			s.files[m.Media.ID] = data
			m.Media.Size = int64(len(data))
		}
	} else if out.Path != "" {
		m.Text = filepath.Base(out.Path)
	}
	for _, task := range out.Tasks {
		m.Text += "\n☐ " + task
	}
	if !s.isBot(chat) {
		// It goes as a message to Telegram would: on its way, then taken,
		// then read (see deliver).
		m.Sending = true
		go s.deliver(chat, id)
	}
	h.Messages = append(append([]model.Message(nil), h.Messages...), m)
	if s.isBot(chat) && m.Text == "/stream" {
		// The bot streams its answer as it writes it.
		s.startStream(chat)
	} else if s.isBot(chat) {
		if reply, ok := botReply(chat, id+1, m.Text, time.Now()); ok {
			h.Messages = append(h.Messages, reply)
			m = reply
		}
	}
	h.Revision++
	s.histories[chat] = h
	for i := range s.chats {
		if s.chats[i].ID == chat {
			s.chats[i].Last.Text = m.Text
			s.chats[i].LastTime = m.Date
		}
	}
	return nil
}
