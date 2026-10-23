// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

// sendFiles sends the files of msg as Telegram Desktop does: what can go in
// an album goes in one (messages.sendMultiMedia), the rest one by one, the
// caption on the last message. Each message has an identity of its own, made
// from msg's, so that sending it all again after a failure sends nothing
// twice.
func (s *Store) sendFiles(ctx context.Context, api *tg.Client, chat int64, peer peerRecord, replyTo tg.InputReplyToClass, msg model.OutgoingMessage) error {
	out := msg.Files
	way := sendfiles.Way{Documents: out.Documents, Group: out.Group, HighQuality: out.HighQuality}
	files := make([]sendfiles.File, 0, len(out.Paths))
	for _, path := range out.Paths {
		f, err := sendfiles.Inspect(path)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		files = append(files, f)
	}
	groups := sendfiles.Divide(files, way)
	captioned, captionedFile := sendfiles.CaptionTarget(groups)
	caption := strings.TrimSpace(msg.Text)
	entities := inputEntities(msg.Entities)
	// captionOf is the caption of file i of group g, and its entities.
	captionOf := func(g, i int) (string, []tg.MessageEntityClass) {
		if g == captioned && i == captionedFile {
			return caption, entities
		}
		return "", nil
	}
	sent := 0
	identity := func() int64 {
		id := msg.RandomID + int64(sent)
		sent++
		if id == 0 {
			id = 1
		}
		return id
	}
	for gi, g := range groups {
		if len(g.Files) == 1 {
			media, err := s.uploadFile(ctx, api, g.Files[0], way, msg.FFmpeg)
			if err != nil {
				return err
			}
			text, ents := captionOf(gi, 0)
			res, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer.input(), RandomID: identity(), Message: text, Entities: ents, Media: media, ReplyTo: replyTo})
			if err != nil {
				return fmt.Errorf("send: %w", err)
			}
			_ = s.Handle(ctx, res)
			continue
		}
		items := make([]tg.InputSingleMedia, 0, len(g.Files))
		for i, f := range g.Files {
			uploaded, err := s.uploadFile(ctx, api, f, way, msg.FFmpeg)
			if err != nil {
				return err
			}
			// An album takes what the server already has, not an upload.
			media, err := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: peer.input(), Media: uploaded})
			if err != nil {
				return fmt.Errorf("%s: %w", f.Name, err)
			}
			input, err := inputMedia(media)
			if err != nil {
				return fmt.Errorf("%s: %w", f.Name, err)
			}
			text, ents := captionOf(gi, i)
			items = append(items, tg.InputSingleMedia{Media: input, RandomID: identity(), Message: text, Entities: ents})
		}
		res, err := api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: peer.input(), MultiMedia: items, ReplyTo: replyTo})
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		_ = s.Handle(ctx, res)
	}
	s.readOnInteract(chat)
	return nil
}

// uploadFile uploads f as its kind and the way ask: a photo scaled and
// saved as JPEG, a video or a GIF as media, anything else as a document.
func (s *Store) uploadFile(ctx context.Context, api *tg.Client, f sendfiles.File, way sendfiles.Way, ffmpeg string) (tg.InputMediaClass, error) {
	up := uploader.NewUploader(api)
	asMedia := !way.Documents
	if f.Kind == sendfiles.KindPhoto && asMedia {
		photo, err := sendfiles.PreparePhoto(f.Path, way.HighQuality)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		file, err := up.FromBytes(ctx, strings.TrimSuffix(f.Name, filepath.Ext(f.Name))+".jpg", photo.JPEG)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		return &tg.InputMediaUploadedPhoto{File: file}, nil
	}
	if f.Kind == sendfiles.KindMusic {
		return uploadMusic(ctx, up, f)
	}
	attrs, err := uploadAttributes(ctx, f.Path, f.MIME, asMedia && f.Kind == sendfiles.KindVideo, ffmpeg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	if f.Kind == sendfiles.KindAnimation && asMedia {
		attrs = append(attrs, &tg.DocumentAttributeAnimated{})
	}
	file, err := up.FromPath(ctx, f.Path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	media := asMedia && (f.Kind == sendfiles.KindVideo || f.Kind == sendfiles.KindAnimation)
	return &tg.InputMediaUploadedDocument{File: file, MimeType: f.MIME, ForceFile: !media, Attributes: attrs}, nil
}

// uploadMusic uploads f as a track, as Telegram Desktop sends music: with
// its title, performer and length, whole seconds of it, and its cover as
// the thumbnail.
func uploadMusic(ctx context.Context, up *uploader.Uploader, f sendfiles.File) (tg.InputMediaClass, error) {
	audio := &tg.DocumentAttributeAudio{Duration: int(f.Duration / time.Second), Title: f.Title, Performer: f.Performer}
	media := &tg.InputMediaUploadedDocument{MimeType: f.MIME, Attributes: []tg.DocumentAttributeClass{
		&tg.DocumentAttributeFilename{FileName: f.Name}, audio,
	}}
	if f.Cover != nil {
		// A cover that does not decode leaves the track without one.
		if jpeg, err := sendfiles.DocumentThumbnail(f.Cover); err == nil {
			thumb, err := up.FromBytes(ctx, "thumb.jpg", jpeg)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			media.SetThumb(thumb)
		}
	}
	file, err := up.FromPath(ctx, f.Path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	media.File = file
	return media, nil
}

// inputMedia turns what messages.uploadMedia made of an upload into the
// media an album refers to.
func inputMedia(media tg.MessageMediaClass) (tg.InputMediaClass, error) {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := m.Photo.AsNotEmpty(); ok {
			return &tg.InputMediaPhoto{ID: photo.AsInput()}, nil
		}
	case *tg.MessageMediaDocument:
		if doc, ok := m.Document.AsNotEmpty(); ok {
			return &tg.InputMediaDocument{ID: doc.AsInput()}, nil
		}
	}
	return nil, errors.New("the server did not take the file")
}
