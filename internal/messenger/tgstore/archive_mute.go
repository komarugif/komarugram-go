// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func (s *Store) ArchiveMute() model.ArchiveMute {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.archiveMute
	state.CanEnable = s.me.Premium
	return state
}

func (s *Store) RefreshArchiveMute(ctx context.Context) error {
	s.archiveMuteMu.Lock()
	defer s.archiveMuteMu.Unlock()
	_, _, err := s.readArchiveMute(ctx)
	return err
}

func (s *Store) SetArchiveMute(ctx context.Context, on bool) error {
	s.archiveMuteMu.Lock()
	defer s.archiveMuteMu.Unlock()
	if on && !s.ArchiveMute().CanEnable {
		return model.ErrArchiveMuteRequiresPremium
	}
	api, settings, err := s.readArchiveMute(ctx)
	if err != nil || settings.ArchiveAndMuteNewNoncontactPeers == on {
		return err
	}
	// This RPC replaces the whole privacy object, including unrelated settings.
	settings.SetArchiveAndMuteNewNoncontactPeers(on)
	saved, err := api.AccountSetGlobalPrivacySettings(ctx, *settings)
	if tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED") {
		return model.ErrArchiveMuteRequiresPremium
	}
	if err != nil {
		return err
	}
	s.applyArchiveMute(saved.ArchiveAndMuteNewNoncontactPeers)
	if saved.ArchiveAndMuteNewNoncontactPeers != on {
		return errors.New("Telegram did not apply the archive and mute setting")
	}
	return nil
}

// readArchiveMute takes the latest settings before each write, so fields
// changed in another client are carried forward. The caller holds archiveMuteMu.
func (s *Store) readArchiveMute(ctx context.Context) (*tg.Client, *tg.GlobalPrivacySettings, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	h := s.history
	h.mu.Lock()
	api, closing := h.api, h.closing
	h.mu.Unlock()
	if api == nil || closing {
		return nil, nil, errNotConnected
	}
	settings, err := api.AccountGetGlobalPrivacySettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.applyArchiveMute(settings.ArchiveAndMuteNewNoncontactPeers)
	return api, settings, nil
}

func (s *Store) applyArchiveMute(on bool) {
	s.mu.Lock()
	changed := !s.archiveMute.Known || s.archiveMute.Enabled != on
	s.archiveMute.Known, s.archiveMute.Enabled = true, on
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}
