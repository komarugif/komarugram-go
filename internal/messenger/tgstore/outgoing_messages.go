// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/video"
)

// outgoingMessages builds the same media and grouping the accepted messages
// will have, before starting any network operation.
func (s *Store) outgoingMessages(ctx context.Context, chat int64, msg model.OutgoingMessage, at time.Time) ([]*pendingMessage, error) {
	me := s.Me()
	base := model.Message{Text: msg.Text, Entities: msg.Entities, SenderID: me.ID, SenderName: me.Name(), Date: at, Outgoing: true, ReplyToMessageID: msg.ReplyTo, Sending: true}
	c := s.history
	c.mu.Lock()
	real, _ := c.threadChat(chat)
	if real != 0 && (real == me.ID || c.peers[real].Rights.Self) {
		base.Outgoing = false
		base.SenderID = 0
		base.SenderName = ""
	}
	c.mu.Unlock()
	var messages []model.Message
	if msg.Item != nil {
		base.Kind, base.Media = msg.Item.Media.Kind, msg.Item.Media.Media
	}
	paths := outgoingPaths(msg)
	if len(paths) > 0 {
		way := sendfiles.Way{Documents: !msg.AsMedia}
		if msg.Files != nil {
			way = sendfiles.Way{Documents: msg.Files.Documents, Group: msg.Files.Group, HighQuality: msg.Files.HighQuality}
		}
		files := make([]sendfiles.File, 0, len(paths))
		wentAsFile := false
		for i, path := range paths {
			f, asFile, err := inspectOutgoing(ctx, path, msg.FFmpeg, way.Documents)
			if err != nil {
				return nil, err
			}
			wentAsFile = wentAsFile || asFile
			if msg.Files.IsAnimated(i) {
				f = f.AsAnimation()
			}
			files = append(files, f)
		}
		if wentAsFile {
			// Told once, and here: this message is built once, however often it
			// is tried.
			s.sendFailure.Store(&model.SendFailure{AsFile: true})
		}
		groups := sendfiles.Divide(files, way)
		cg, cf := sendfiles.CaptionTarget(groups)
		index := 0
		for gi, g := range groups {
			for fi, f := range g.Files {
				m := base
				m.Text, m.Entities = "", nil
				if gi == cg && fi == cf {
					trimmed := (model.RichText{Text: msg.Text, Entities: msg.Entities}).Trimmed()
					m.Text, m.Entities = trimmed.Text, trimmed.Entities
				}
				m.Kind = model.MessageFile
				media := &model.MessageMedia{ID: fmt.Sprintf("outbox/%d/%d", msg.RandomID, index), FileName: f.Name, MIMEType: f.MIME, Size: f.Size, Width: f.Width, Height: f.Height, Duration: f.Duration, Title: f.Title, Performer: f.Performer}
				if !way.Documents {
					switch f.Kind {
					case sendfiles.KindPhoto:
						m.Kind = model.MessagePhoto
					case sendfiles.KindVideo:
						m.Kind = model.MessageVideo
					case sendfiles.KindAnimation:
						m.Kind = model.MessageGIF
					}
					if strings.HasPrefix(f.MIME, "video/") && (m.Kind == model.MessageVideo || m.Kind == model.MessageGIF) {
						s.showOutgoingVideo(ctx, media, f, msg.FFmpeg)
					}
				}
				if f.Kind == sendfiles.KindMusic {
					m.Kind = model.MessageMusic
					// What its button shows until Telegram has it: the file's
					// own cover may be many times larger.
					if thumb, err := sendfiles.DocumentThumbnail(f.Cover); err == nil {
						media.LocalPreview = s.saveOutgoingPreview(ctx, media.ID, thumb)
						if media.LocalPreview == nil {
							media.Preview = thumb
						}
					}
				}
				if msg.Voice != nil {
					m.Kind = model.MessageVoice
					media.Duration = msg.Voice.Duration
					media.Waveform = msg.Voice.Waveform
				}
				if f.Kind == sendfiles.KindPhoto || m.Kind == model.MessageGIF && f.MIME == "image/gif" {
					if photo, err := sendfiles.PreparePhoto(f.Path, way.HighQuality); err == nil {
						if m.Kind != model.MessageFile {
							media.LocalPreview = s.saveOutgoingPreview(ctx, media.ID, photo.JPEG)
						}
						if media.LocalPreview == nil {
							media.Preview = photo.JPEG
						}
					}
				}
				m.Media = media
				// index is the file's place: the groups keep the files' order.
				m.MediaSpoiler = msg.Files.IsSpoiler(index) && spoilerable(f, way)
				if len(g.Files) > 1 {
					m.GroupedID = msg.RandomID + int64(gi)
					if m.GroupedID == 0 {
						m.GroupedID = 1
					}
				}
				messages = append(messages, m)
				index++
			}
		}
	} else {
		if len(msg.Tasks) > 0 {
			base.Text = ""
			base.Todo = &model.Todo{Title: msg.Text}
			for i, text := range msg.Tasks {
				base.Todo.Items = append(base.Todo.Items, model.TodoItem{ID: i + 1, Text: text})
			}
		}
		messages = []model.Message{base}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var pending []*pendingMessage
	for _, m := range messages {
		c.pending.seq++
		m.Key = model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.MessageID(-(pendingIDBase + c.pending.seq))}
		m.ContentRevision = model.Revision(m)
		pending = append(pending, &pendingMessage{chat: chat, seq: c.pending.seq, msg: m})
	}
	return pending, nil
}

// outgoingVideoPatience is how long a message being sent waits to tell of its
// video: it does not show until then.
const outgoingVideoPatience = 5 * time.Second

// showOutgoingVideo records the shape, length and first picture of a video.
func (s *Store) showOutgoingVideo(ctx context.Context, media *model.MessageMedia, f sendfiles.File, ffmpeg string) {
	ctx, cancel := context.WithTimeout(ctx, outgoingVideoPatience)
	defer cancel()
	if v, err := videoAttribute(ctx, f.Path, ffmpeg); err == nil {
		media.Width, media.Height = v.W, v.H
		media.Duration = time.Duration(v.Duration * float64(time.Second))
	}
	thumb, err := sendfiles.VideoDocumentThumbnail(ctx, video.ResolveFFmpeg(ffmpeg), f.Path)
	if err == nil {
		media.LocalPreview = s.saveOutgoingPreview(ctx, media.ID, thumb)
		media.Thumbnail = media.LocalPreview
	}
}

func (s *Store) saveOutgoingPreview(ctx context.Context, mediaID string, thumb []byte) *model.MessageMedia {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		return nil
	}
	id := "outbox-thumb/" + strings.TrimPrefix(mediaID, "outbox/")
	if cache := s.Cache(); cache == nil || cache.SaveMedia(ctx, id, thumb) != nil {
		return nil
	}
	return &model.MessageMedia{ID: id, MIMEType: "image/jpeg", Size: int64(len(thumb)), Width: cfg.Width, Height: cfg.Height}
}

// restoreOutgoingPreview upgrades the presentation of older queued files.
func (s *Store) restoreOutgoingPreview(ctx context.Context, media *model.MessageMedia) {
	if media == nil || media.LocalPreview != nil || !strings.HasPrefix(media.ID, "outbox/") {
		return
	}
	if media.Thumbnail != nil {
		media.LocalPreview = media.Thumbnail
	} else if len(media.Preview) > 0 {
		media.LocalPreview = s.saveOutgoingPreview(ctx, media.ID, media.Preview)
	}
}

// keepOutgoingPreview attaches the still from the acknowledged outbox file.
// History refreshes retain it while the message refers to the same media.
func (s *Store) keepOutgoingPreview(ctx context.Context, cache *historycache.Cache, m model.Message, own bool) {
	switch m.Kind {
	case model.MessagePhoto, model.MessageVideo, model.MessageGIF, model.MessageMusic:
	default:
		return
	}
	if m.Media == nil {
		return
	}
	var local *model.MessageMedia
	if cache != nil && own {
		old, ok, err := cache.Message(ctx, m.Key.ChatID, int(m.Key.MessageID))
		if err == nil && ok && old.Media != nil {
			if old.Media.ID != m.Media.ID {
				return
			}
			if old.Media.LocalPreview != nil {
				local = old.Media
			}
		}
	}
	if local == nil {
		c := s.history
		c.mu.Lock()
		for _, p := range c.pending.list {
			if p.id != m.Key.MessageID || p.id <= 0 {
				continue
			}
			if chat, _ := c.threadChat(p.chat); chat == m.Key.ChatID {
				local = p.msg.Media
				break
			}
		}
		c.mu.Unlock()
	}
	if local == nil || local.LocalPreview == nil {
		return
	}
	m.Media.LocalPreview = local.LocalPreview
	if m.Kind == model.MessageVideo || m.Kind == model.MessageGIF {
		// Some documents come back without attributes, notably WebM.
		if m.Media.Width <= 0 || m.Media.Height <= 0 {
			m.Media.Width, m.Media.Height = local.Width, local.Height
		}
		if m.Media.Duration <= 0 {
			m.Media.Duration = local.Duration
		}
	}
}

// acceptOutgoing records an acknowledgement before applying updates or sending
// the next file. Retrying a partially delivered batch skips accepted groups.
func (s *Store) acceptOutgoing(ctx context.Context, e *outgoingEntry, res tg.UpdatesClass, randomIDs []int64) error {
	if len(e.pending) == 0 {
		return fmt.Errorf("outbox: missing pending message")
	}
	q := &s.outbox
	confirmed := make([]int, len(e.pending))
	for _, random := range randomIDs {
		id := sentID(res, random)
		index := 0
		for i := range e.pending {
			if outgoingRandomID(e.Request.RandomID, i) == random {
				index = i
				break
			}
		}
		q.mu.Lock()
		e.Done[random] = id
		q.mu.Unlock()
		q.uploads.remove(e.pending[index].msg.Key)
		if id > 0 {
			if e.LocalEmoji {
				q.mu.Lock()
				m := e.Messages[index]
				q.mu.Unlock()
				if err := s.keepLocalEmoji(ctx, e.Peer.key(), id, m.Text, e.localEmojiEntities(m.Entities)); err != nil {
					return err
				}
			}
			confirmed[index] = id
		}
	}
	c := s.history
	c.mu.Lock()
	for index, id := range confirmed {
		if id > 0 {
			c.sentPending(e.pending[index], id)
		}
	}
	c.mu.Unlock()
	q.mu.Lock()
	defer q.mu.Unlock()
	return s.Cache().PutOutgoing(ctx, e.Request.RandomID, e, nil)
}

// IDs form a stable sequence that skips zero, including a batch crossing it.
func outgoingRandomID(base int64, index int) int64 {
	id := base + int64(index)
	if base < 0 && uint64(index) >= uint64(-base) {
		id++
	}
	return id
}

// sendMedia's short acknowledgement needs the same immediate publication as a
// text acknowledgement; reading it back is not a condition of successful send.
func (s *Store) handleOutgoingMedia(ctx context.Context, e *outgoingEntry, res tg.UpdatesClass, random int64, text string) {
	if short, ok := res.(*tg.UpdateShortSentMessage); ok {
		m := &tg.Message{ID: short.ID, Out: short.Out, PeerID: outgoingPeer(e.Peer), Message: strings.TrimSpace(text), Date: short.Date, Media: short.Media, Entities: short.Entities}
		if e.Request.ReplyTo != 0 {
			m.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: int(e.Request.ReplyTo)}
		}
		if messages, err := s.ingest(ctx, []tg.MessageClass{m}, true, 0); err == nil {
			for _, m := range messages {
				s.mergeUpdate(m)
			}
		}
		s.changed()
		return
	}
	_ = s.Handle(ctx, res)
}

func outgoingPeer(p peerRecord) tg.PeerClass {
	switch p.Kind {
	case "user":
		return &tg.PeerUser{UserID: p.ID}
	case "chat":
		return &tg.PeerChat{ChatID: p.ID}
	default:
		return &tg.PeerChannel{ChannelID: p.ID}
	}
}
