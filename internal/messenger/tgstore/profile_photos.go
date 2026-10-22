// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/tg"
)

// profilePhotoLimit is how many photos of a profile are asked for.
const profilePhotoLimit = 100

// bigAvatarSide is the side of the large picture of a profile photo.
const bigAvatarSide = 640

var (
	_ model.PhotoGallery       = (*Store)(nil)
	_ model.ProfilePhotoSource = (*Store)(nil)
)

// ProfilePhoto implements model.ProfilePhotoSource. It reads only local peer
// metadata: the large picture of the photo the chat shows, with the small
// one the chat list has already loaded as its variant, so that the viewer
// has something to show while the large one comes.
func (s *Store) ProfilePhoto(chat int64) (model.Message, bool) {
	small, ok := s.Avatar(chat)
	if !ok || small.Media == nil {
		return model.Message{}, false
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.refs[small.Media.ID]
	if !ok || ref.Peer == nil {
		return model.Message{}, false
	}
	peer := c.peers[chat]
	big := *small.Media
	big.ID = small.Media.ID + "/big"
	big.Thumbnail = nil
	big.Width, big.Height = bigAvatarSide, bigAvatarSide
	big.Variants = []model.MessageMedia{{ID: small.Media.ID, MIMEType: small.Media.MIMEType, Width: small.Media.Width, Height: small.Media.Height}}
	ref.Big = true
	c.refs[big.ID] = ref
	return model.Message{
		Kind:       model.MessagePhoto,
		Key:        model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.ProfilePhotoID(0)},
		Media:      &big,
		SenderName: peer.Name,
		NoForwards: peer.Rights.NoForwards,
	}, true
}

// ProfilePhotos implements model.ProfilePhotoSource: a user's photos are
// Telegram's list of them, and a group's or channel's are the photo it has
// now and the ones it had, told by the service messages of their changes.
// Pages go as in Telegram Desktop: a user's past the last photo's ID
// (max_id), a group's past the last service message, and the total is
// Telegram's count of them. offset is "index/past/extra": the place of the
// page's first photo, where Telegram goes on, and the photos counted beside
// Telegram's (a group's current photo whose service message is gone).
func (s *Store) ProfilePhotos(ctx context.Context, chat int64, offset string, limit int) (model.GalleryPage, error) {
	var index, extra int
	var past int64
	if offset != "" {
		if _, err := fmt.Sscanf(offset, "%d/%d/%d", &index, &past, &extra); err != nil {
			return model.GalleryPage{}, fmt.Errorf("profile photos offset %q: %w", offset, err)
		}
	}
	limit = min(profilePhotoLimit, max(1, limit))
	ctx, api, peer, _, done, err := s.peerOperation(ctx, chat)
	if err != nil {
		return model.GalleryPage{}, err
	}
	defer done()
	type found struct {
		photo   *tg.Photo
		message int
	}
	var photos []found
	seen := map[int64]bool{}
	add := func(p tg.PhotoClass, message int) {
		if p, ok := p.(*tg.Photo); ok && !seen[p.ID] {
			seen[p.ID] = true
			photos = append(photos, found{p, message})
		}
	}
	var total int
	var more bool
	switch peer.Kind {
	case "user":
		res, e := api.PhotosGetUserPhotos(ctx, &tg.PhotosGetUserPhotosRequest{UserID: peer.inputUser(), MaxID: past, Limit: limit})
		if e != nil {
			return model.GalleryPage{}, e
		}
		list := res.GetPhotos()
		total = len(list)
		for _, p := range list {
			add(p, 0)
			past = p.GetID()
		}
		// photos.photos is the whole list; a slice tells how many there are.
		if slice, ok := res.(*tg.PhotosPhotosSlice); ok {
			total = slice.Count
			more = len(list) > 0 && index+len(list) < total
		}
	case "chat", "channel":
		var current tg.PhotoClass
		if offset == "" {
			if peer.Kind == "chat" {
				full, e := api.MessagesGetFullChat(ctx, peer.ID)
				if e != nil {
					return model.GalleryPage{}, e
				}
				if f, ok := full.FullChat.(*tg.ChatFull); ok {
					current = f.ChatPhoto
				}
			} else {
				full, e := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
				if e != nil {
					return model.GalleryPage{}, e
				}
				if f, ok := full.FullChat.(*tg.ChannelFull); ok {
					current = f.ChatPhoto
				}
			}
			add(current, 0)
		}
		res, e := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: peer.input(), Filter: &tg.InputMessagesFilterChatPhotos{}, OffsetID: int(past), Limit: limit})
		if e != nil {
			return model.GalleryPage{}, e
		}
		total = searchTotal(res)
		var messages []tg.MessageClass
		if modified, ok := res.AsModified(); ok {
			messages = modified.GetMessages()
		}
		_, whole := res.(*tg.MessagesMessages)
		more = !whole && len(messages) >= limit
		told := false
		for _, m := range messages {
			past = int64(m.GetID())
			if service, ok := m.(*tg.MessageService); ok {
				if edit, ok := service.Action.(*tg.MessageActionChatEditPhoto); ok {
					if p, ok := current.(*tg.Photo); ok && edit.Photo.GetID() == p.ID {
						// The current photo was told by its service message.
						photos[0].message, told = m.GetID(), true
					}
					add(edit.Photo, m.GetID())
				}
			}
		}
		if _, ok := current.(*tg.Photo); ok && !told {
			extra = 1
		}
	default:
		return model.GalleryPage{}, errors.New("no photos")
	}
	page := model.GalleryPage{Total: total + extra}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range photos {
		meta := c.rememberProfilePhoto(f.photo, f.message)
		if meta == nil {
			continue
		}
		page.Messages = append(page.Messages, model.Message{
			Kind:       model.MessagePhoto,
			Key:        model.MessageKey{AccountID: c.account, ChatID: chat, MessageID: model.ProfilePhotoID(index + len(page.Messages))},
			Media:      meta,
			Date:       time.Unix(int64(f.photo.Date), 0),
			SenderName: peer.Name,
			NoForwards: peer.Rights.NoForwards,
		})
	}
	if more && past != 0 {
		page.More = true
		page.Next = fmt.Sprintf("%d/%d/%d", index+len(page.Messages), past, extra)
	}
	return page, nil
}

// inputUser is the user peer stands for, the account itself as Telegram
// asks it to be told.
func (p peerRecord) inputUser() tg.InputUserClass {
	if p.Rights.Self {
		return &tg.InputUserSelf{}
	}
	return &tg.InputUser{UserID: p.ID, AccessHash: p.Hash}
}

// rememberProfilePhoto keeps where the sizes of photo p of a profile are,
// and, for a group's, the service message that tells it, by which its file
// reference is renewed. c.mu is held.
func (c *conversation) rememberProfilePhoto(p *tg.Photo, message int) *model.MessageMedia {
	meta, largest := photoMedia(p)
	if largest == "" {
		return nil
	}
	ref := fileLocation{ID: p.ID, Hash: p.AccessHash, Reference: p.FileReference, DC: p.DCID, Photo: true, Thumb: largest, ProfileMessage: message}
	c.refs[meta.ID] = ref
	for _, v := range meta.Variants {
		ref.Thumb = v.ID[strings.LastIndexByte(v.ID, '/')+1:]
		c.refs[v.ID] = ref
	}
	return meta
}

// refreshProfilePhoto renews the file reference of one photo of a profile,
// as Telegram Desktop does: a user's by asking for the one photo of that ID,
// a group's by its service message, or its current photo by its full info.
func (s *Store) refreshProfilePhoto(ctx context.Context, api *tg.Client, peer peerRecord, ref fileLocation) error {
	var photo tg.PhotoClass
	switch {
	case ref.Peer != nil || !ref.Photo:
		return errors.New("profile photo has no file reference")
	case peer.Kind == "user":
		res, err := api.PhotosGetUserPhotos(ctx, &tg.PhotosGetUserPhotosRequest{UserID: peer.inputUser(), Offset: -1, MaxID: ref.ID, Limit: 1})
		if err != nil {
			return err
		}
		for _, p := range res.GetPhotos() {
			if p.GetID() == ref.ID {
				photo = p
			}
		}
	case ref.ProfileMessage != 0:
		ids := []tg.InputMessageClass{&tg.InputMessageID{ID: ref.ProfileMessage}}
		var res tg.MessagesMessagesClass
		var err error
		if peer.Kind == "channel" {
			res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, ID: ids})
		} else {
			res, err = api.MessagesGetMessages(ctx, ids)
		}
		if err != nil {
			return err
		}
		if modified, ok := res.AsModified(); ok {
			for _, m := range modified.GetMessages() {
				if service, ok := m.(*tg.MessageService); ok {
					if edit, ok := service.Action.(*tg.MessageActionChatEditPhoto); ok && edit.Photo.GetID() == ref.ID {
						photo = edit.Photo
					}
				}
			}
		}
	case peer.Kind == "chat":
		full, err := api.MessagesGetFullChat(ctx, peer.ID)
		if err != nil {
			return err
		}
		if f, ok := full.FullChat.(*tg.ChatFull); ok && f.ChatPhoto.GetID() == ref.ID {
			photo = f.ChatPhoto
		}
	case peer.Kind == "channel":
		full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
		if err != nil {
			return err
		}
		if f, ok := full.FullChat.(*tg.ChannelFull); ok && f.ChatPhoto.GetID() == ref.ID {
			photo = f.ChatPhoto
		}
	}
	p, ok := photo.(*tg.Photo)
	if !ok {
		return errors.New("profile photo no longer available")
	}
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rememberProfilePhoto(p, ref.ProfileMessage)
	return nil
}
