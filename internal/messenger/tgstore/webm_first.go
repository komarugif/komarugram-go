// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

// Telegram keeps nothing of a WebM but the file: no video's attributes and no
// thumbnail, whatever was sent with it. A getHistory tells only its name, and asking
// for a thumbnail it told of a moment before is FILE_REFERENCE_EXPIRED, however new
// the reference. So what a video shows is made here, of the start of the file, as
// this thumbnail, kept under an ID of its own, and of the same place as the file.

// webmFirstSuffix ends the ID of the first picture made of a WebM.
const webmFirstSuffix = "/first"

// webmShape is what the start of a WebM told of it, kept for when its message
// comes again.
type webmShape struct {
	Width, Height int
	Duration      time.Duration
}

func webmShapeKey(video string) string { return "webm/" + video }

// giveWebMThumbnail makes m, the media of document id, show the picture made here,
// the preview and the shape Telegram told kept meanwhile.
func giveWebMThumbnail(m *model.MessageMedia, id int64) {
	first := &model.MessageMedia{ID: fmt.Sprintf("document/%d%s", id, webmFirstSuffix), MIMEType: "image/jpeg"}
	if told := m.Thumbnail; told != nil {
		first.Width, first.Height, first.Preview = told.Width, told.Height, told.Preview
		if m.Width <= 0 || m.Height <= 0 {
			m.Width, m.Height = told.Width, told.Height
		}
	}
	m.Thumbnail = first
}

// applyWebMShape fills m in with what the start of its file told, once that was read.
func (s *Store) applyWebMShape(ctx context.Context, m *model.MessageMedia) {
	if m == nil || m.Thumbnail == nil || !strings.HasSuffix(m.Thumbnail.ID, webmFirstSuffix) {
		return
	}
	cache := s.Cache()
	if cache == nil {
		return
	}
	var shape webmShape
	if ok, err := cache.Get(ctx, webmShapeKey(m.ID), &shape); err != nil || !ok {
		return
	}
	m.Width, m.Height = shape.Width, shape.Height
	m.Thumbnail.Width, m.Thumbnail.Height = shape.Width, shape.Height
	if shape.Duration > 0 {
		m.Duration = shape.Duration
	}
}

// webmFirstPicture makes the picture of m, a WebM's thumbnail, reading the start of
// the file. Its message is asked for again, for the shape to reach what shows it.
func (s *Store) webmFirstPicture(ctx context.Context, m model.Message) ([]byte, error) {
	c := s.history
	c.mu.Lock()
	ref, ok := c.refs[m.Media.ID]
	cache := c.cache
	c.mu.Unlock()
	if !ok {
		found, err := cache.Get(ctx, "ref/"+m.Media.ID, &ref)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("media location missing")
		}
	}
	if ref.FileSize <= 0 {
		return nil, errors.New("video size unavailable")
	}
	video := strings.TrimSuffix(m.Media.ID, webmFirstSuffix)
	r, size, closeStream, err := s.MediaStream(ctx, model.Message{Key: m.Key, Media: &model.MessageMedia{ID: video, MIMEType: "video/webm", Size: ref.FileSize}})
	if err != nil {
		return nil, err
	}
	defer closeStream()
	picture, meta, err := sendfiles.WebMFirstPicture(ctx, r, size)
	if err != nil {
		return nil, err
	}
	if err := cache.SaveMedia(ctx, m.Media.ID, picture); err != nil {
		return nil, err
	}
	shape := webmShape{Width: meta.Width, Height: meta.Height, Duration: meta.Duration}
	if shape.Width > 0 && shape.Height > 0 {
		if err := cache.Put(ctx, webmShapeKey(video), shape); err != nil {
			return nil, err
		}
		if m.Key.MessageID > 0 {
			if err := s.refreshReference(ctx, m); err != nil {
				// The picture stands: its message shows the shape when it comes again.
				log.Printf("tgstore: webm shape: %v", err)
			}
		}
	}
	return picture, nil
}
