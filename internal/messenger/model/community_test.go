// SPDX-License-Identifier: Unlicense OR MIT

package model

import "testing"

// A community's chats are those that name it, not the community itself; it
// is shown apart without its row, its chats as they were, and as one chat
// with it.
func TestCommunityMembers(t *testing.T) {
	chats := []Chat{
		{ID: 1, Community: 9},
		{ID: 2},
		{ID: 9, Kind: KindCommunity, Collapsed: true, Community: 9},
		{ID: 3, Community: 9},
	}
	got := CommunityMembers(chats, 9)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("members %+v", got)
	}
	apart := WithCommunityShown(chats, Chat{ID: 9}, false)
	if len(apart) != 3 || CommunityMembers(apart, 9)[0].ID != 1 || len(chats) != 4 {
		t.Fatalf("shown apart: %+v, before %+v", apart, chats)
	}
	// As one chat again, it has its row back; once is enough.
	one := WithCommunityShown(WithCommunityShown(apart, Chat{ID: 9, Title: "Go"}, true), Chat{ID: 9}, true)
	if len(one) != 4 || one[3].Kind != KindCommunity || !one[3].Collapsed || one[3].Title != "Go" {
		t.Fatalf("shown as one chat: %+v", one)
	}
}
