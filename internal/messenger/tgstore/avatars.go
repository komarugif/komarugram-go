// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/telegram/thumbnail"
	"github.com/gotd/td/tg"
)

type avatarSnapshot struct {
	photo   int64
	hash    int64
	dc      int
	video   bool
	message model.Message
}

type avatarRecord struct {
	ID       int64
	DC       int
	Video    bool
	Stripped []byte
}

func chatAvatar(photo tg.ChatPhotoClass) *avatarRecord {
	if p, ok := photo.(*tg.ChatPhoto); ok {
		return &avatarRecord{p.PhotoID, p.DCID, p.HasVideo, p.StrippedThumb}
	}
	return nil
}

// Avatar only reads local peer metadata; it never blocks the frame on an RPC.
func (s *Store) Avatar(id int64) (model.Message, bool) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	peer, ok := c.peers[id]
	if !ok || peer.Photo == nil {
		return model.Message{}, false
	}
	p := peer.Photo
	if c.goneAvatars[p.ID] {
		return model.Message{}, false
	}
	if old, ok := c.avatars[id]; ok && old.photo == p.ID && old.hash == peer.Hash && old.dc == p.DC && old.video == p.Video {
		return old.message, true
	}
	key := fmt.Sprintf("avatar/%d/%d", id, p.ID)
	preview, _ := thumbnail.Expand(p.Stripped)
	meta := &model.MessageMedia{ID: key, MIMEType: "image/jpeg", Width: 160, Height: 160, Preview: preview}
	bare := peer
	bare.Photo = nil
	c.refs[key] = fileLocation{ID: p.ID, DC: p.DC, Peer: &bare}
	if p.Video {
		meta.Thumbnail = &model.MessageMedia{ID: key + "/video", MIMEType: "application/x-avatar-video", Width: 160, Height: 160, Preview: preview}
	}
	msg := model.Message{Kind: model.MessagePhoto, Media: meta}
	if c.avatars == nil {
		c.avatars = map[int64]avatarSnapshot{}
	}
	c.avatars[id] = avatarSnapshot{p.ID, peer.Hash, p.DC, p.Video, msg}
	return msg, true
}

// avatarGone asks Telegram for the peer of the avatar of key, whose photo
// Telegram no longer has (FILE_ID_INVALID): changed or removed since the
// peer was read, as one deep in the list, which the dialogs read now no
// longer reach (dialogs_kept.go). Its avatar is then the new one, or none.
// Still the same, the photo is not asked for again this session: it was on
// every show of the avatar.
func (s *Store) avatarGone(ctx context.Context, key string) {
	var id, photoID int64
	if n, _ := fmt.Sscanf(key, "avatar/%d/%d", &id, &photoID); n != 2 || key != fmt.Sprintf("avatar/%d/%d", id, photoID) {
		return
	}
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[id]
	c.mu.Unlock()
	if api != nil && peer.ID != 0 {
		var users []tg.UserClass
		var chats []tg.ChatClass
		var err error
		switch peer.Kind {
		case "user":
			users, err = api.UsersGetUsers(ctx, []tg.InputUserClass{peer.inputUser()})
		case "chat":
			var res tg.MessagesChatsClass
			if res, err = api.MessagesGetChats(ctx, []int64{peer.ID}); err == nil {
				chats = res.GetChats()
			}
		case "channel":
			var res tg.MessagesChatsClass
			if res, err = api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}}); err == nil {
				chats = res.GetChats()
			}
		}
		if err == nil {
			s.rememberPeers(users, chats)
		}
	}
	c.mu.Lock()
	if p := c.peers[id].Photo; p != nil && p.ID == photoID {
		if c.goneAvatars == nil {
			c.goneAvatars = map[int64]bool{}
		}
		c.goneAvatars[photoID] = true
	}
	c.mu.Unlock()
	s.changed()
	s.goPersistDialogs()
}

// AvatarVideo resolves the current profile video lazily. Static pictures remain
// visible while it loads, and no video RPC/download runs with animations off.
func (s *Store) AvatarVideo(parent context.Context, key string) (model.Message, error) {
	return s.avatarVideo(parent, key, false)
}

// avatarVideo is AvatarVideo, asked of Telegram again when fresh, for a file
// reference of now: the one kept expires.
func (s *Store) avatarVideo(parent context.Context, key string, fresh bool) (model.Message, error) {
	var id, photoID int64
	if _, e := fmt.Sscanf(key, "avatar/%d/%d/video", &id, &photoID); e != nil {
		return model.Message{}, e
	}
	c := s.history
	c.mu.Lock()
	peer, ok := c.peers[id]
	api, cache := c.api, c.cache
	c.mu.Unlock()
	if !ok || cache == nil {
		return model.Message{}, errors.New("avatar unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	var message model.Message
	if !fresh {
		if found, e := cache.Get(ctx, key, &message); e != nil || found {
			return message, e
		}
	}
	if api == nil {
		return message, errors.New("avatar video unavailable offline")
	}
	var photo tg.PhotoClass
	switch peer.Kind {
	case "user":
		full, e := api.UsersGetFullUser(ctx, &tg.InputUser{UserID: peer.ID, AccessHash: peer.Hash})
		if e != nil {
			return message, e
		}
		photo = full.FullUser.ProfilePhoto
	case "chat":
		full, e := api.MessagesGetFullChat(ctx, peer.ID)
		if e != nil {
			return message, e
		}
		if f, ok := full.FullChat.(*tg.ChatFull); ok {
			photo = f.ChatPhoto
		}
	case "channel":
		full, e := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash})
		if e != nil {
			return message, e
		}
		if f, ok := full.FullChat.(*tg.ChannelFull); ok {
			photo = f.ChatPhoto
		}
	}
	p, ok := photo.(*tg.Photo)
	if !ok || p.ID != photoID {
		return message, errors.New("avatar photo changed")
	}
	var chosen *tg.VideoSize
	for _, v := range p.VideoSizes {
		if v, ok := v.(*tg.VideoSize); ok && (chosen == nil || v.Size < chosen.Size) {
			chosen = v
		}
	}
	if chosen == nil {
		return message, errors.New("avatar has no video")
	}
	meta := &model.MessageMedia{ID: key + "/" + chosen.Type, MIMEType: "video/mp4", Size: int64(chosen.Size), Width: chosen.W, Height: chosen.H}
	ref := fileLocation{ID: p.ID, Hash: p.AccessHash, Reference: p.FileReference, DC: p.DCID, Photo: true, Thumb: chosen.Type}
	c.mu.Lock()
	c.refs[meta.ID] = ref
	c.mu.Unlock()
	message = model.Message{Kind: model.MessageGIF, Media: meta}
	if e := cache.Put(ctx, "ref/"+meta.ID, ref); e != nil {
		return message, e
	}
	return message, cache.Put(ctx, key, message)
}
