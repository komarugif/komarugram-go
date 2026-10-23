// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"

	"komarugram/internal/messenger/model"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// registrationPointsKey keeps, in the cache's kv, what the user was asked
// about the points and what was sent.
const registrationPointsKey = "registration-points"

// registrationPointsState is what registrationPointsKey keeps: the users
// whose points were sent, those asked about, and whether the user asked
// not to be asked again.
type registrationPointsState struct {
	Sent, Asked []int64
	Never       bool
}

// RegistrationPoints implements model.RegistrationPointSource: the private
// chats whose last message is "joined Telegram", as the chat list has them,
// and those whose cached history begins with it. Nothing is asked of
// Telegram.
func (s *Store) RegistrationPoints(ctx context.Context) ([]model.RegistrationPoint, int, bool, error) {
	cache := s.registrationCache()
	if cache == nil {
		return nil, 0, false, errors.New("no history cache")
	}
	var state registrationPointsState
	if _, err := cache.Get(ctx, registrationPointsKey, &state); err != nil {
		return nil, 0, false, err
	}
	found := map[int64]model.RegistrationPoint{}
	for _, c := range s.Chats() {
		if p, ok := chatSignUp(c); ok {
			found[p.User] = p
		}
	}
	cached, err := cache.ContactSignUps(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	for _, p := range cached {
		if _, ok := found[p.User]; !ok {
			found[p.User] = p
		}
	}
	var points []model.RegistrationPoint
	unasked := 0
	for _, p := range found {
		if slices.Contains(state.Sent, p.User) {
			continue
		}
		points = append(points, p)
		if !slices.Contains(state.Asked, p.User) {
			unasked++
		}
	}
	return points, unasked, state.Never, nil
}

// chatSignUp is the point of a private chat whose last message is "joined
// Telegram": its user, on the message's day.
func chatSignUp(c model.Chat) (model.RegistrationPoint, bool) {
	m := c.Last.Service
	if c.Kind != model.KindUser || c.ID <= 0 || m == nil || m.Service == nil || m.Service.Kind != model.ServiceContactSignUp || m.Date.IsZero() {
		return model.RegistrationPoint{}, false
	}
	return model.RegistrationPoint{User: c.ID, Date: m.Date}, true
}

// SendRegistrationPoints implements model.RegistrationPointSource: the
// points go as a file named after the day, which cmd/registration-table reads,
// to the channel's direct messages, which take a message from anyone.
func (s *Store) SendRegistrationPoints(ctx context.Context, points []model.RegistrationPoint) error {
	c := s.history
	c.mu.Lock()
	api := c.api
	c.mu.Unlock()
	if api == nil {
		return errNotConnected
	}
	cache := s.registrationCache()
	if cache == nil {
		return errors.New("no history cache")
	}
	// Sent already, from another window answered first, they are not sent
	// again.
	var state registrationPointsState
	if _, err := cache.Get(ctx, registrationPointsKey, &state); err != nil {
		return err
	}
	points = slices.DeleteFunc(slices.Clone(points), func(p model.RegistrationPoint) bool { return slices.Contains(state.Sent, p.User) })
	if len(points) == 0 {
		return nil
	}
	var file bytes.Buffer
	now := time.Now()
	if err := model.WriteRegistrationPoints(&file, points, now); err != nil {
		return err
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: model.RegistrationPointsRecipient})
	if err != nil {
		return err
	}
	channel, ok := resolved.Peer.(*tg.PeerChannel)
	if !ok {
		return fmt.Errorf("@%s is not a channel", model.RegistrationPointsRecipient)
	}
	s.rememberPeers(resolved.Users, resolved.Chats)
	direct, err := s.DirectMessages(ctx, peerID(channel))
	if err != nil {
		return err
	}
	s.history.mu.Lock()
	to := s.history.peers[direct.ID].input()
	s.history.mu.Unlock()
	name := model.RegistrationPointsName(now)
	uploaded, err := uploader.NewUploader(api).FromBytes(ctx, name, file.Bytes())
	if err != nil {
		return err
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	_, err = api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     to,
		RandomID: int64(binary.LittleEndian.Uint64(random[:])),
		Media: &tg.InputMediaUploadedDocument{File: uploaded, MimeType: "text/plain", ForceFile: true,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}}},
	})
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return s.rememberRegistrationPoints(ctx, points, true, false)
}

// PostponeRegistrationPoints implements model.RegistrationPointSource.
func (s *Store) PostponeRegistrationPoints(ctx context.Context, points []model.RegistrationPoint, never bool) error {
	return s.rememberRegistrationPoints(ctx, points, false, never)
}

// rememberRegistrationPoints adds points to those asked about, and to those
// sent with sent.
func (s *Store) rememberRegistrationPoints(ctx context.Context, points []model.RegistrationPoint, sent, never bool) error {
	cache := s.registrationCache()
	if cache == nil {
		return errors.New("no history cache")
	}
	var state registrationPointsState
	if _, err := cache.Get(ctx, registrationPointsKey, &state); err != nil {
		return err
	}
	for _, p := range points {
		if !slices.Contains(state.Asked, p.User) {
			state.Asked = append(state.Asked, p.User)
		}
		if sent && !slices.Contains(state.Sent, p.User) {
			state.Sent = append(state.Sent, p.User)
		}
	}
	state.Never = state.Never || never
	return cache.Put(ctx, registrationPointsKey, state)
}

func (s *Store) registrationCache() registrationCache {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		return nil
	}
	return c.cache
}

// registrationCache is what of the history cache the points need.
type registrationCache interface {
	Get(ctx context.Context, key string, value any) (bool, error)
	Put(ctx context.Context, key string, value any) error
	ContactSignUps(ctx context.Context) ([]model.RegistrationPoint, error)
}
