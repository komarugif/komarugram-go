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
	"komarugram/pkg/video"
)

// sendFiles sends the files of msg as Telegram Desktop does: what can go in
// an album goes in one (messages.sendMultiMedia), the rest one by one, the
// caption on the last message. Each message has an identity of its own, made
// from msg's, so that sending it all again after a failure sends nothing
// twice, and a file cancelled meanwhile (CancelUpload) leaves the rest as they
// go. chat is the one shown: a thread's, which the uploads are told in.
func (s *Store) sendFiles(ctx context.Context, api *tg.Client, chat int64, peer peerRecord, replyTo tg.InputReplyToClass, msg model.OutgoingMessage, outgoing *outgoingEntry) error {
	out := msg.Files
	way := sendfiles.Way{Documents: out.Documents, Group: out.Group, HighQuality: out.HighQuality, SilentAsVideo: out.SilentAsVideo}
	// places are the files' places in the message: what is cancelled leaves
	// the rest where they were, with the identities they had.
	files := make([]sendfiles.File, 0, len(out.Paths))
	places := make([]int, 0, len(out.Paths))
	for i, path := range out.Paths {
		if s.fileCancelled(outgoing, i) {
			continue
		}
		f, err := sendfiles.Inspect(path)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if out.IsAnimated(i) {
			f = f.AsAnimation()
		}
		files, places = append(files, f), append(places, i)
	}
	// captionOf is the caption of the file at a place, and its entities: its
	// message shows it, and a cancelled file gives its own to another.
	captionOf := func(place int) (string, []tg.MessageEntityClass) {
		s.history.mu.Lock()
		defer s.history.mu.Unlock()
		m := outgoing.pending[place].msg
		return m.Text, inputEntities(outgoing.wireEntities(m.Entities))
	}
	identity := func(place int) int64 { return outgoingRandomID(msg.RandomID, place) }
	// The first message sent clears the chat's draft, as the composer sent it.
	clearDraft := s.outgoingClearDraft(outgoing)
	for _, g := range sendfiles.Divide(files, way) {
		at := places[:len(g.Files)]
		places = places[len(g.Files):]
		// An album takes what the server already has, not an upload, until
		// it goes; a file by itself goes as it was uploaded.
		ready := make(map[int]tg.InputMediaClass, len(g.Files))
		var order []int
		for i, f := range g.Files {
			if outgoing.fileDone(at[i]) {
				continue
			}
			media, err := s.prepareFile(ctx, api, chat, peer, f, way, msg.FFmpeg, outgoing, at[i], len(g.Files) > 1)
			if err != nil {
				return err
			}
			if media != nil {
				ready[at[i]] = media
				order = append(order, at[i])
			}
		}
		order = s.claimFiles(outgoing, order)
		clearDraft = s.outgoingClearDraft(outgoing)
		ids := make([]int64, len(order))
		for i, place := range order {
			ids[i] = identity(place)
		}
		switch len(order) {
		case 0:
			continue
		case 1:
			text, ents := captionOf(order[0])
			res, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer.input(), RandomID: ids[0], Message: text, Entities: ents, Media: ready[order[0]], ReplyTo: replyTo, ClearDraft: clearDraft})
			if err != nil {
				return fmt.Errorf("send: %w", err)
			}
			if err := s.acceptOutgoing(ctx, outgoing, res, ids); err != nil {
				return err
			}
			s.draftCleared(chat, &clearDraft, outgoing)
			s.handleOutgoingMedia(ctx, outgoing, res, ids[0], text)
		default:
			items := make([]tg.InputSingleMedia, 0, len(order))
			for i, place := range order {
				text, ents := captionOf(place)
				items = append(items, tg.InputSingleMedia{Media: ready[place], RandomID: ids[i], Message: text, Entities: ents})
			}
			res, err := api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: peer.input(), MultiMedia: items, ReplyTo: replyTo, ClearDraft: clearDraft})
			if err != nil {
				return fmt.Errorf("send: %w", err)
			}
			if err := s.acceptOutgoing(ctx, outgoing, res, ids); err != nil {
				return err
			}
			s.draftCleared(chat, &clearDraft, outgoing)
			_ = s.Handle(ctx, res)
		}
	}
	s.readOnInteract(chat)
	return nil
}

// prepareFile uploads the file at place of outgoing, for an album as what the
// server already has. It is nil for a file cancelled, before or meanwhile.
func (s *Store) prepareFile(ctx context.Context, api *tg.Client, chat int64, peer peerRecord, f sendfiles.File, way sendfiles.Way, ffmpeg string, outgoing *outgoingEntry, place int, album bool) (tg.InputMediaClass, error) {
	fileCtx, end, ok := s.startFile(ctx, outgoing, place)
	if !ok {
		return nil, nil
	}
	defer end()
	media, err := s.uploadFile(fileCtx, api, chat, f, way, ffmpeg, outgoing, place)
	if doc, ok := media.(*tg.InputMediaUploadedDocument); ok && album && f.Kind == sendfiles.KindVideo && !doc.ForceFile {
		// Stays a video, as Telegram Desktop asks (PrepareUploadedDocument): one
		// without sound may otherwise come back a GIF, which no album takes, and
		// sendMultiMedia fails with MEDIA_EMPTY.
		doc.NosoundVideo = true
	}
	if err == nil && album {
		var server tg.MessageMediaClass
		server, err = api.MessagesUploadMedia(fileCtx, &tg.MessagesUploadMediaRequest{Peer: peer.input(), Media: media})
		if err == nil {
			media, err = inputMedia(server)
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	if err != nil {
		if s.fileCancelled(outgoing, place) {
			return nil, nil
		}
		return nil, err
	}
	return media, nil
}

// uploadFile uploads f to send to chat as its kind and the way ask: a photo
// scaled and saved as JPEG, a video or a GIF as media, anything else as a
// document. chat is told meanwhile that the account sends it.
func (s *Store) uploadFile(ctx context.Context, api *tg.Client, chat int64, f sendfiles.File, way sendfiles.Way, ffmpeg string, outgoing *outgoingEntry, place int) (tg.InputMediaClass, error) {
	up := s.fileUploader(api, chat, uploadAction(f, way), outgoing, place)
	return s.uploadMedia(ctx, api, f, way, ffmpeg, up)
}

// uploadAction is what a chat is told the account does as f goes the way asked.
func uploadAction(f sendfiles.File, way sendfiles.Way) model.ChatAction {
	switch {
	case !way.Documents && f.Kind == sendfiles.KindPhoto:
		return model.ActionUploadPhoto
	case !way.Documents && (f.Kind == sendfiles.KindVideo || f.Kind == sendfiles.KindAnimation):
		return model.ActionUploadVideo
	}
	return model.ActionUploadFile
}

// uploadMedia uploads f with up, as its kind and the way ask.
func (s *Store) uploadMedia(ctx context.Context, api *tg.Client, f sendfiles.File, way sendfiles.Way, ffmpeg string, up *uploader.Uploader) (tg.InputMediaClass, error) {
	asMedia := !way.Documents
	// A thumbnail goes apart: its bytes are none of the file's progress.
	thumbs := uploader.NewUploader(api)
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
		return uploadMusic(ctx, up, thumbs, f)
	}
	// A GIF that is a video, as Telegram keeps them, tells its size and length too.
	videoFile := strings.HasPrefix(f.MIME, "video/") && (f.Kind == sendfiles.KindVideo || f.Kind == sendfiles.KindAnimation)
	attrs, err := uploadAttributes(ctx, f.Path, f.MIME, asMedia && videoFile, ffmpeg)
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
	doc := &tg.InputMediaUploadedDocument{File: file, MimeType: f.MIME, ForceFile: !media, Attributes: attrs}
	if way.SilentAsVideo && media && f.Kind == sendfiles.KindVideo {
		// One that cannot be probed goes as it would: only a video known to
		// have no sound may say so.
		if probed, err := video.ProbeMedia(ctx, f.Path, ffmpeg); err == nil && !probed.Sound {
			doc.NosoundVideo = true
		}
	}
	if videoFile {
		// A video sent as a file shows its picture too, as in Telegram
		// Desktop. One that gives no picture goes without.
		thumbCtx, cancel := context.WithTimeout(ctx, videoThumbnailPatience)
		jpeg, err := sendfiles.VideoDocumentThumbnail(thumbCtx, video.ResolveFFmpeg(ffmpeg), f.Path)
		cancel()
		if err == nil {
			thumb, err := thumbs.FromBytes(ctx, "thumb.jpg", jpeg)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			doc.SetThumb(thumb)
		}
	}
	return doc, nil
}

// videoThumbnailPatience is how long FFmpeg may look for a video's first
// picture.
const videoThumbnailPatience = 30 * time.Second

// uploadMusic uploads f as a track, as Telegram Desktop sends music: with
// its title, performer and length, whole seconds of it, and its cover as
// the thumbnail.
func uploadMusic(ctx context.Context, up, thumbs *uploader.Uploader, f sendfiles.File) (tg.InputMediaClass, error) {
	audio := &tg.DocumentAttributeAudio{Duration: int(f.Duration / time.Second), Title: f.Title, Performer: f.Performer}
	media := &tg.InputMediaUploadedDocument{MimeType: f.MIME, Attributes: []tg.DocumentAttributeClass{
		&tg.DocumentAttributeFilename{FileName: f.Name}, audio,
	}}
	if f.Cover != nil {
		// A cover that does not decode leaves the track without one.
		if jpeg, err := sendfiles.DocumentThumbnail(f.Cover); err == nil {
			thumb, err := thumbs.FromBytes(ctx, "thumb.jpg", jpeg)
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

// uploadProgress tells chat, as an upload goes on, that the account sends
// it, as Telegram Desktop does: the action, and how much is done.
type uploadProgress struct {
	s      *Store
	chat   int64
	action model.ChatAction
	// file tells the rest of how far it went.
	file func(done, total int64)
}

func (p uploadProgress) Chunk(_ context.Context, state uploader.ProgressState) error {
	percent := 0
	if state.Total > 0 {
		percent = int(state.Uploaded * 100 / state.Total)
	}
	p.s.sendAction(p.chat, p.action, percent)
	p.file(state.Uploaded, state.Total)
	return nil
}

// fileUploader is an uploader of the file at place of outgoing to send to
// chat, which tells chat of action as it goes on, from now, and the table of
// uploads how far it went.
func (s *Store) fileUploader(api *tg.Client, chat int64, action model.ChatAction, outgoing *outgoingEntry, place int) *uploader.Uploader {
	s.sendAction(chat, action, 0)
	key := outgoing.pending[place].msg.Key
	return uploader.NewUploader(api).WithProgress(uploadProgress{s, chat, action, func(done, total int64) {
		s.outbox.uploads.advance(key, done, total)
		outgoing.alive()
	}})
}

// draftCleared forgets the draft of chat when
// *clear: a message sent cleared it. Then nothing more is to clear.
func (s *Store) draftCleared(chat int64, clear *bool, outgoing *outgoingEntry) {
	if *clear && s.outgoingClearDraft(outgoing) {
		s.setDraft(chat, nil)
		*clear = false
	}
}
