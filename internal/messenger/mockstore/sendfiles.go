// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"fmt"
	"os"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

// appendFiles adds the files of out to the history h of chat, as messages
// as Telegram would make of them: an album is messages of one group, the
// caption on the one the box puts it on. The photos are made ready as they
// would be sent, so the demo shows what went out. s.mu must be held.
func (s *Store) appendFiles(chat int64, h model.History, first model.MessageID, out model.OutgoingMessage) error {
	way := sendfiles.Way{Documents: out.Files.Documents, Group: out.Files.Group, HighQuality: out.Files.HighQuality}
	files := make([]sendfiles.File, 0, len(out.Files.Paths))
	for _, path := range out.Files.Paths {
		f, err := sendfiles.Inspect(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		files = append(files, f)
	}
	groups := sendfiles.Divide(files, way)
	captioned, captionedFile := sendfiles.CaptionTarget(groups)
	messages := append([]model.Message(nil), h.Messages...)
	id := first
	now := time.Now()
	for gi, g := range groups {
		album := int64(0)
		if len(g.Files) > 1 {
			album = out.RandomID + int64(gi) + 1
		}
		for fi, f := range g.Files {
			m := model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: id}, Outgoing: true, Date: now, SenderID: s.me.ID, ContentRevision: 1, ReplyToMessageID: out.ReplyTo, GroupedID: album}
			if gi == captioned && fi == captionedFile {
				m.Text, m.Entities = out.Text, out.Entities
			}
			media := &model.MessageMedia{ID: fmt.Sprintf("demo/sent/%d/%d", chat, id), FileName: f.Name, MIMEType: f.MIME, Size: f.Size}
			var data []byte
			switch {
			case f.Kind == sendfiles.KindPhoto && !way.Documents:
				photo, err := sendfiles.PreparePhoto(f.Path, way.HighQuality)
				if err != nil {
					return err
				}
				m.Kind = model.MessagePhoto
				data = photo.JPEG
				media.FileName, media.MIMEType, media.Size, media.Width, media.Height = "", "image/jpeg", int64(len(data)), photo.Width, photo.Height
			case f.Kind == sendfiles.KindAnimation && !way.Documents:
				m.Kind = model.MessageGIF
				data, _ = os.ReadFile(f.Path)
			case f.Kind == sendfiles.KindMusic:
				m.Kind = model.MessageMusic
				media.Title, media.Performer, media.Duration = f.Title, f.Performer, f.Duration
				data, _ = os.ReadFile(f.Path)
			default:
				m.Kind = model.MessageFile
			}
			m.Media = media
			if data != nil {
				if s.files == nil {
					s.files = map[string][]byte{}
				}
				s.files[media.ID] = data
			}
			messages = append(messages, m)
			id++
		}
	}
	h.Messages = messages
	h.Revision++
	s.histories[chat] = h
	last := messages[len(messages)-1]
	for i := range s.chats {
		if s.chats[i].ID == chat {
			s.chats[i].Last.Text = last.Text
			s.chats[i].LastTime = last.Date
		}
	}
	return nil
}
