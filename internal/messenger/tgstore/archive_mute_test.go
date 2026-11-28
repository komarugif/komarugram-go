// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func TestArchiveMuteAccountSetting(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	server := tg.GlobalPrivacySettings{KeepArchivedUnmuted: true, KeepArchivedFolders: true, HideReadMarks: true}
	server.SetNoncontactPeersPaidStars(0)
	var reads, writes int
	var reject bool
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch req := in.(type) {
		case *tg.AccountGetGlobalPrivacySettingsRequest:
			reads++
			return &server, nil
		case *tg.AccountSetGlobalPrivacySettingsRequest:
			writes++
			want := server
			want.SetArchiveAndMuteNewNoncontactPeers(!server.ArchiveAndMuteNewNoncontactPeers)
			if !reflect.DeepEqual(req.Settings, want) {
				return nil, fmt.Errorf("settings changed beyond the archive flag: got %+v, want %+v", req.Settings, want)
			}
			if reject {
				return nil, tgerr.New(403, "PREMIUM_ACCOUNT_REQUIRED")
			}
			server = req.Settings
			return &server, nil
		default:
			return nil, fmt.Errorf("unexpected RPC %T", in)
		}
	})
	if s.ArchiveMute().Known {
		t.Fatal("unloaded state reported as known")
	}
	if err := s.SetArchiveMute(ctx, true); !errors.Is(err, model.ErrArchiveMuteRequiresPremium) || reads != 0 || writes != 0 {
		t.Fatalf("non-Premium enable reached Telegram: %d reads, %d writes, %v", reads, writes, err)
	}
	s.me.Premium = true
	if err := s.RefreshArchiveMute(ctx); err != nil || !s.ArchiveMute().Known || s.ArchiveMute().Enabled {
		t.Fatalf("initial refresh: %+v, %v", s.ArchiveMute(), err)
	}
	if err := s.SetArchiveMute(ctx, true); err != nil || !s.ArchiveMute().Enabled {
		t.Fatalf("enabling: %+v, %v", s.ArchiveMute(), err)
	}
	// A second client changes unrelated privacy settings between our writes.
	server.SetHideReadMarks(false)
	server.SetNoncontactPeersPaidStars(42)
	if err := s.SetArchiveMute(ctx, false); err != nil || s.ArchiveMute().Enabled || server.Flags.Has(0) {
		t.Fatalf("disabling: %+v, %v", server, err)
	}
	if err := s.SetArchiveMute(ctx, false); err != nil || writes != 2 {
		t.Fatalf("unchanged flag issued a write: %d writes, %v", writes, err)
	}
	reject = true
	if err := s.SetArchiveMute(ctx, true); !errors.Is(err, model.ErrArchiveMuteRequiresPremium) || s.ArchiveMute().Enabled {
		t.Fatalf("rejected write: %+v, %v", s.ArchiveMute(), err)
	}
	server.SetArchiveAndMuteNewNoncontactPeers(true)
	if err := s.RefreshArchiveMute(ctx); err != nil || !s.ArchiveMute().Enabled {
		t.Fatalf("external change: %+v, %v", s.ArchiveMute(), err)
	}
	s.me.Premium, reject = false, false
	if err := s.SetArchiveMute(ctx, false); err != nil || s.ArchiveMute().Enabled {
		t.Fatalf("disabling after Premium expired: %+v, %v", s.ArchiveMute(), err)
	}
}
