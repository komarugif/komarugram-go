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
		files := make([]sendfiles.File, 0, len(paths))
		for _, path := range paths {
			f, err := sendfiles.Inspect(path)
			if err != nil {
				return nil, err
			}
			files = append(files, f)
		}
		way := sendfiles.Way{Documents: !msg.AsMedia}
		if msg.Files != nil {
			way = sendfiles.Way{Documents: msg.Files.Documents, Group: msg.Files.Group, HighQuality: msg.Files.HighQuality}
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
						s.showOutgoingVideo(ctx, media, msg.RandomID, index, f, msg.FFmpeg)
					case sendfiles.KindAnimation:
						m.Kind = model.MessageGIF
					}
				}
				if f.Kind == sendfiles.KindMusic {
					m.Kind = model.MessageMusic
					// What its button shows until Telegram has it: the file's
					// own cover may be many times larger.
					if thumb, err := sendfiles.DocumentThumbnail(f.Cover); err == nil {
						media.Preview = thumb
					}
				}
				if msg.Voice != nil {
					m.Kind = model.MessageVoice
					media.Duration = msg.Voice.Duration
					media.Waveform = msg.Voice.Waveform
				}
				if f.Kind == sendfiles.KindPhoto {
					if photo, err := sendfiles.PreparePhoto(f.Path, false); err == nil {
						media.Preview = photo.JPEG
					}
				}
				m.Media = media
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

// showOutgoingVideo gives media, file index of the message randomID, what its
// video shows while it is sent: its shape and length, and its first picture,
// kept as Telegram's thumbnails are. What FFmpeg does not tell leaves it out:
// sending says so when it matters.
func (s *Store) showOutgoingVideo(ctx context.Context, media *model.MessageMedia, randomID int64, index int, f sendfiles.File, ffmpeg string) {
	ctx, cancel := context.WithTimeout(ctx, outgoingVideoPatience)
	defer cancel()
	if attrs, err := uploadAttributes(ctx, f.Path, f.MIME, true, ffmpeg); err == nil {
		for _, a := range attrs {
			if v, ok := a.(*tg.DocumentAttributeVideo); ok {
				media.Width, media.Height = v.W, v.H
				media.Duration = time.Duration(v.Duration * float64(time.Second))
			}
		}
	}
	thumb, err := sendfiles.VideoDocumentThumbnail(ctx, video.ResolveFFmpeg(ffmpeg), f.Path)
	if err != nil {
		return
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		return
	}
	id := fmt.Sprintf("outbox-thumb/%d/%d", randomID, index)
	if cache := s.Cache(); cache == nil || cache.SaveMedia(ctx, id, thumb) != nil {
		return
	}
	media.Thumbnail = &model.MessageMedia{ID: id, MIMEType: "image/jpeg", Size: int64(len(thumb)), Width: cfg.Width, Height: cfg.Height}
}

// acceptOutgoing records an acknowledgement before applying updates or sending
// the next file. Retrying a partially delivered batch skips accepted groups.
func (s *Store) acceptOutgoing(ctx context.Context, e *outgoingEntry, res tg.UpdatesClass, randomIDs []int64) error {
	if len(e.pending) == 0 {
		return fmt.Errorf("outbox: missing pending message")
	}
	for _, random := range randomIDs {
		id := sentID(res, random)
		e.Done[random] = id
		index := 0
		for i := range e.pending {
			if outgoingRandomID(e.Request.RandomID, i) == random {
				index = i
				break
			}
		}
		if id > 0 {
			if e.LocalEmoji {
				m := e.Messages[index]
				if err := s.keepLocalEmoji(ctx, e.Peer.key(), id, m.Text, e.localEmojiEntities(m.Entities)); err != nil {
					return err
				}
			}
			s.history.mu.Lock()
			s.history.sentPending(e.pending[index], id)
			s.history.mu.Unlock()
		}
	}
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
