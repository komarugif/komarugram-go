// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

var _ model.GIFMaker = (*Store)(nil)

// SaveGIFFile implements model.GIFMaker: the file is uploaded with
// messages.uploadMedia, which sends nothing, as Telegram Desktop uploads a
// GIF (PrepareUploadedDocument): an MP4 video, animated, without
// nosound_video, which would make Telegram keep it as a video, not
// animated, and messages.saveGif refuse it with GIF_ID_INVALID. It is
// saved with messages.saveGif, and remembered as the GIF of source.
func (s *Store) SaveGIFFile(ctx context.Context, source model.Message, path string, width, height int, duration time.Duration) error {
	s.history.mu.Lock()
	api := s.history.api
	s.history.mu.Unlock()
	if api == nil {
		return errNotConnected
	}
	file, err := uploader.NewUploader(api).FromPath(ctx, path)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	media, err := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{
		Peer: &tg.InputPeerSelf{},
		Media: &tg.InputMediaUploadedDocument{
			File: file, MimeType: "video/mp4",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeVideo{SupportsStreaming: true, Duration: duration.Seconds(), W: width, H: height},
				&tg.DocumentAttributeAnimated{},
				&tg.DocumentAttributeFilename{FileName: filepath.Base(path)},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	doc, ok := media.(*tg.MessageMediaDocument)
	if !ok {
		return fmt.Errorf("upload: unexpected %T", media)
	}
	d, ok := doc.Document.(*tg.Document)
	if !ok {
		return errors.New("upload: no document")
	}
	if kind, _, _ := documentMedia(d); kind != model.MessageGIF {
		// saveGif would refuse it, saying only GIF_ID_INVALID.
		return fmt.Errorf("upload: Telegram kept the file as %s, not a GIF: %s", d.MimeType, describeAttributes(d.Attributes))
	}
	// Kept where it is, as a saved GIF's, for SaveGIF to find.
	items := s.savedGIFItems(ctx, []tg.DocumentClass{d})
	if len(items) == 0 {
		return errors.New("upload: no document")
	}
	gif := items[0].Media
	err = s.SaveGIF(ctx, gif, false)
	if err == nil || errors.Is(err, model.ErrSavedGIFLimit) {
		s.rememberGIF(ctx, source, gif)
	}
	return err
}

// madeGIFKey is where the cache keeps the GIF made of the video of media id.
func madeGIFKey(id string) string { return "gif/made/" + id }

// MadeGIF implements model.GIFMaker, by the cache: the same video, by its
// document, wherever it was sent, made the GIF.
func (s *Store) MadeGIF(ctx context.Context, source model.Message) (model.Message, bool) {
	cache := s.Cache()
	if cache == nil || source.Media == nil {
		return model.Message{}, false
	}
	var gif model.Message
	if ok, err := cache.Get(ctx, madeGIFKey(source.Media.ID), &gif); err != nil || !ok || gif.Media == nil {
		return model.Message{}, false
	}
	return gif, true
}

// rememberGIF keeps gif as the GIF made of the video of source, for
// MadeGIF: a video made again saves the same document, which Telegram
// moves first among the saved GIFs, not one more of the same.
func (s *Store) rememberGIF(ctx context.Context, source, gif model.Message) {
	if cache := s.Cache(); cache != nil && source.Media != nil && gif.Media != nil {
		_ = cache.Put(ctx, madeGIFKey(source.Media.ID), model.Message{Kind: gif.Kind, Media: gif.Media})
	}
}

// describeAttributes tells what Telegram keeps of a document, for an error.
func describeAttributes(attrs []tg.DocumentAttributeClass) string {
	var out []string
	for _, a := range attrs {
		switch a := a.(type) {
		case *tg.DocumentAttributeVideo:
			out = append(out, fmt.Sprintf("video %dx%d %.2fs nosound=%v streaming=%v", a.W, a.H, a.Duration, a.Nosound, a.SupportsStreaming))
		case *tg.DocumentAttributeFilename:
			out = append(out, "filename "+a.FileName)
		default:
			out = append(out, strings.TrimPrefix(a.TypeName(), "documentAttribute"))
		}
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// ConvertGIFByBot implements model.GIFMaker: the video is forwarded to
// model.GIFBot, which answers with a message it edits as it goes, then
// sends the GIF. The bot's messages are read as they come in the updates,
// and marked read once done with, as the dialog showed them.
func (s *Store) ConvertGIFByBot(ctx context.Context, m model.Message, progress func(model.GIFProgress)) (model.Message, error) {
	if m.Media == nil {
		return model.Message{}, errors.New("no video")
	}
	if m.Media.Size > model.GIFBotMaxBytes {
		return model.Message{}, model.ErrGIFTooLarge
	}
	bot, err := s.ResolveUsername(ctx, model.GIFBot)
	if err != nil {
		return model.Message{}, err
	}
	// Watched before it is forwarded, which brings the forward itself.
	messages, stop := s.watch.add(bot.ID)
	if err := s.ForwardMessages(ctx, m.Key.ChatID, []model.MessageID{m.Key.MessageID}, bot.ID); err != nil {
		stop()
		return model.Message{}, err
	}
	progress(model.GIFProgress{Share: -1})
	gif, last, err := waitGIFBot(ctx, messages, model.GIFBotPatience, progress)
	if ctx.Err() == nil {
		stop()
		s.readGIFBot(bot.ID, last)
		if err == nil {
			s.rememberGIF(ctx, m, gif)
		}
		return gif, err
	}
	// Cancelled here, the bot stops too, and converts nothing more; what
	// it answers to that is read as well.
	go func() {
		defer crash.Recover("cancel GIF bot", nil)
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if s.sendText(ctx, bot.ID, model.GIFBotCancel) == nil {
			last = max(last, cancelledGIFBot(messages, gifBotCancelWait))
		}
		s.readGIFBot(bot.ID, last)
	}()
	return gif, err
}

// gifBotCancelWait is how long the bot's answer to its cancel is waited for.
var gifBotCancelWait = 10 * time.Second

// cancelledGIFBot reads the bot's messages until it answers its cancel, or
// for wait, and returns the last of them.
func cancelledGIFBot(messages <-chan model.Message, wait time.Duration) model.MessageID {
	timeout := time.After(wait)
	var last model.MessageID
	for {
		select {
		case <-timeout:
			return last
		case m := <-messages:
			if m.Outgoing {
				continue
			}
			last = max(last, m.Key.MessageID)
			if model.GIFBotCancelAnswer(m.Text) {
				return last
			}
		}
	}
}

// readGIFBot marks the bot's messages read up to last: the dialog showed
// them, and the account wrote to the bot, which Ghost's ReadOnInteract
// takes as reading.
func (s *Store) readGIFBot(chat int64, last model.MessageID) {
	if last <= 0 {
		return
	}
	g := s.ghostSettings()
	s.MarkRead(chat, last, !g.SendRead && g.ReadOnInteract)
}

// sendText sends text to chat, as it is, with messages.sendMessage.
func (s *Store) sendText(ctx context.Context, chat int64, text string) error {
	c := s.history
	c.mu.Lock()
	api := c.api
	peer, ok := c.peers[chat]
	c.mu.Unlock()
	if api == nil || !ok {
		return errNotConnected
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	res, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer.input(), Message: text, RandomID: int64(binary.LittleEndian.Uint64(random[:]))})
	if err != nil {
		return err
	}
	return s.Handle(ctx, res)
}

// waitGIFBot reads the messages of the bot's chat, the forward first, until
// the GIF the bot sends after it, telling how far the bot has come; it
// gives up when the bot says nothing for patience. last is the last of the
// bot's messages read.
func waitGIFBot(ctx context.Context, messages <-chan model.Message, patience time.Duration, progress func(model.GIFProgress)) (gif model.Message, last model.MessageID, err error) {
	silence := time.NewTimer(patience)
	defer silence.Stop()
	var forwarded model.MessageID
	for {
		select {
		case <-ctx.Done():
			return model.Message{}, last, ctx.Err()
		case <-silence.C:
			return model.Message{}, last, model.ErrGIFBotSilent
		case m := <-messages:
			if m.Outgoing {
				if forwarded == 0 && m.Media != nil {
					forwarded = m.Key.MessageID
				}
				continue
			}
			// What the bot said before is of other videos.
			if forwarded == 0 || m.Key.MessageID <= forwarded {
				continue
			}
			silence.Reset(patience)
			last = max(last, m.Key.MessageID)
			if m.Media != nil && (m.Kind == model.MessageGIF || m.Kind == model.MessageVideo) {
				if m.Media.Size > model.GIFMaxBytes {
					return model.Message{}, last, model.ErrGIFTooLarge
				}
				return m, last, nil
			}
			p, ok, err := model.ParseGIFBot(m.Text)
			if err != nil {
				return model.Message{}, last, err
			}
			if ok {
				progress(p)
			}
		}
	}
}

// chatWatch hands the messages of chats, new and edited, to those who
// watch them, as the updates bring them.
type chatWatch struct {
	mu   sync.Mutex
	subs map[int64][]chan model.Message
}

// add watches chat until stop is called.
func (w *chatWatch) add(chat int64) (<-chan model.Message, func()) {
	ch := make(chan model.Message, 64)
	w.mu.Lock()
	if w.subs == nil {
		w.subs = map[int64][]chan model.Message{}
	}
	w.subs[chat] = append(w.subs[chat], ch)
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		subs := w.subs[chat]
		for i, c := range subs {
			if c == ch {
				w.subs[chat] = append(subs[:i:i], subs[i+1:]...)
				break
			}
		}
		if len(w.subs[chat]) == 0 {
			delete(w.subs, chat)
		}
	}
}

// deliver hands m to those who watch its chat; one who reads none of it
// misses it, and the updates go on.
func (w *chatWatch) deliver(m model.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ch := range w.subs[m.Key.ChatID] {
		select {
		case ch <- m:
		default:
		}
	}
}
