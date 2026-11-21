// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"errors"
	"slices"
	"testing"
)

// Shared folders can only use explicit peers. A pin is also a member and
// occupies a single slot when applying the account's filter limit.
func TestSharedFolderChatActions(t *testing.T) {
	c := Chat{ID: 12, Kind: KindChannel}
	base := Folder{ID: 2, Title: "Shared", Shared: true, Included: []int64{11, 12}, Pinned: []int64{10}}.WithDialogFlags(nil)
	pinned, err := base.WithChat(c, FolderPinChat)
	if err != nil || !slices.Equal(pinned.Pinned, []int64{12, 10}) || !slices.Equal(pinned.Included, []int64{11}) {
		t.Fatalf("pin: %v %v", pinned, err)
	}
	limits := Premium{Limits: []PremiumLimit{{Key: "dialog_filters_chats_limit", Default: 3}}}
	if _, err := CheckFolderSave([]Folder{{}, base}, &base, pinned, limits); err != nil {
		t.Fatalf("pin used an extra slot: %v", err)
	}
	extra, err := pinned.WithChat(Chat{ID: 13, Kind: KindGroup}, FolderAddChat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFolderSave([]Folder{{}, pinned}, &pinned, extra, limits); !errors.Is(err, ErrFolderPeerLimit) {
		t.Fatalf("extra peer: %v", err)
	}
	removed, err := pinned.WithChat(c, FolderRemoveChat)
	if err != nil || removed.Contains(c) || len(removed.Excluded) != 0 || !slices.Equal(removed.Pinned, []int64{10}) {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if !slices.Equal(base.Included, []int64{11, 12}) || !slices.Equal(base.Pinned, []int64{10}) {
		t.Fatal("mutated original folder")
	}
	last, err := removed.WithChat(Chat{ID: 11}, FolderRemoveChat)
	if err != nil || last.CanRemoveChat(Chat{ID: 10}) {
		t.Fatal("offered removal of the last explicit member", err)
	}
}
