// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// With Local Premium on, the accounts signed in here show the Premium star
// to themselves, and nobody else does; without it, nobody does.
func TestLocalPremiumMarksOwnAccounts(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	prefs := preferences.Memory()
	a := &App{store: store, preferences: prefs}
	me := store.Me().ID
	other := map[int64]bool{7: true}
	a.ownUsers.Store(&other)
	star := func(user int64) bool {
		_, after := a.badges(model.Badges{User: user}, "", 16, false)
		return after != nil
	}
	for _, user := range []int64{me, 7, 8, 0} {
		if star(user) {
			t.Fatalf("user %d has a star with Local Premium off", user)
		}
	}
	if err := prefs.SetLocalPremium(true); err != nil {
		t.Fatal(err)
	}
	if !star(me) || !star(7) {
		t.Fatal("an own account has no star with Local Premium on")
	}
	if star(8) || star(0) {
		t.Fatal("a stranger has a star")
	}
	// A verified name keeps its check mark alone, as with the real star.
	if _, after := a.badges(model.Badges{User: me, Verified: true}, "", 16, false); after == nil {
		t.Fatal("a verified own account lost its mark")
	}
}

// The Premium section says the star is local, and the list of the settings
// tells it apart from a subscription.
func TestSettingsTellLocalPremium(t *testing.T) {
	p := &settingsPage{premium: plainPremium{}}
	l := localization.For("en")
	if got := p.premiumText(l); got != "Not subscribed" {
		t.Fatalf("without it: %q", got)
	}
	on := true
	p.localPremium = func() bool { return on }
	if got := p.premiumText(l); got != "Local" {
		t.Fatalf("with it: %q", got)
	}
	on = false
	if got := p.premiumText(l); got != "Not subscribed" {
		t.Fatalf("off again: %q", got)
	}
}
