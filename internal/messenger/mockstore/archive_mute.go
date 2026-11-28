// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"

	"komarugram/internal/messenger/model"
)

func (s *Store) ArchiveMute() model.ArchiveMute {
	s.mu.Lock()
	defer s.mu.Unlock()
	return model.ArchiveMute{Known: true, Enabled: s.archiveMute, CanEnable: s.me.Premium}
}

func (s *Store) RefreshArchiveMute(ctx context.Context) error { return ctx.Err() }

func (s *Store) SetArchiveMute(ctx context.Context, on bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if on && !s.me.Premium {
		s.mu.Unlock()
		return model.ErrArchiveMuteRequiresPremium
	}
	s.archiveMute = on
	changed := s.changed
	s.mu.Unlock()
	if changed != nil {
		changed()
	}
	return nil
}
