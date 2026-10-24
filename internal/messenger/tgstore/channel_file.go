// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"

	"komarugram/pkg/dcpool"
)

// ChannelFile downloads the file attached to a post of a public channel:
// the way Telegram Desktop gets the files it keeps in Telegram's cloud,
// such as its emoji sets, from the channel tdhbcfiles. The channel is only
// read: its name is resolved, the post asked for and its document
// downloaded. Nothing of it is kept in the history. progress, if not nil,
// is told the bytes received and the file's size.
func (s *Store) ChannelFile(ctx context.Context, username string, post int, progress func(done, total int64)) ([]byte, error) {
	c := s.history
	c.mu.Lock()
	api, pool := c.api, c.pool
	c.mu.Unlock()
	if api == nil || pool == nil {
		return nil, errors.New("not connected to Telegram")
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, err
	}
	var channel *tg.InputChannel
	for _, chat := range resolved.Chats {
		if ch, ok := chat.(*tg.Channel); ok {
			channel = &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
			break
		}
	}
	if channel == nil {
		return nil, fmt.Errorf("@%s is not a channel", username)
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: channel, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: post}}})
	if err != nil {
		return nil, err
	}
	messages, ok := res.(interface{ GetMessages() []tg.MessageClass })
	if !ok {
		return nil, fmt.Errorf("@%s/%d: no messages", username, post)
	}
	for _, m := range messages.GetMessages() {
		message, ok := m.(*tg.Message)
		if !ok || message.ID != post {
			continue
		}
		media, ok := message.Media.(*tg.MessageMediaDocument)
		if !ok {
			break
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			break
		}
		if doc.Size <= 0 || doc.Size > MaxMediaBytes {
			return nil, fmt.Errorf("@%s/%d: a file of %d bytes", username, post, doc.Size)
		}
		out := &cappedFile{progress: func(n int64) {
			if progress != nil {
				progress(n, doc.Size)
			}
		}}
		location := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
		if err := downloadFile(ctx, dcpool.DownloadClient(pool, ctx, doc.DCID), location, doc.Size, out, nil); err != nil {
			return nil, err
		}
		if int64(len(out.data)) != doc.Size {
			return nil, fmt.Errorf("@%s/%d: %d bytes of %d", username, post, len(out.data), doc.Size)
		}
		return out.data, nil
	}
	return nil, fmt.Errorf("@%s/%d has no file", username, post)
}
