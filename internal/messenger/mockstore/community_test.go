// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// The demo has a community shown as one chat and one shown apart, which has
// no dialog, as Telegram's, each with the chats of the demo's list it links
// and some the account is not in; a community is shown apart, and as one
// chat, when asked.
func TestDemoCommunities(t *testing.T) {
	s := New(time.Now(), 0)
	collapsed := func(id int64) (bool, bool) {
		for _, c := range s.Chats() {
			if c.ID == id && c.Kind == model.KindCommunity {
				return c.Collapsed, true
			}
		}
		return false, false
	}
	if v, ok := collapsed(DemoCommunity); !ok || !v {
		t.Fatalf("the demo's community: listed %v, shown as one chat %v", ok, v)
	}
	if _, ok := collapsed(DemoPhotoCommunity); ok {
		t.Fatal("the community shown apart has a dialog")
	}
	if s.LinkedCommunity(900021) != DemoPhotoCommunity || s.Community(DemoPhotoCommunity).Title != "Фотоклуб" {
		t.Fatal("the community shown apart is not known by its chats")
	}
	sections := s.Community(DemoCommunity).Sections()
	if got := [4]int{len(sections[0]), len(sections[1]), len(sections[2]), len(sections[3])}; got != [4]int{2, 3, 1, 1} {
		t.Fatalf("the community has %v joined, to view, to ask to join, hidden", got)
	}
	if err := s.SetCommunityCollapsed(context.Background(), DemoCommunity, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := collapsed(DemoCommunity); ok {
		t.Fatal("the community shown apart kept its dialog")
	}
	if err := s.SetCommunityCollapsed(context.Background(), DemoPhotoCommunity, true); err != nil {
		t.Fatal(err)
	}
	if v, ok := collapsed(DemoPhotoCommunity); !ok || !v {
		t.Fatal("the community shown as one chat has no dialog")
	}
}
