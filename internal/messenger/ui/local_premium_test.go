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
	other := map[int64]string{7: "b"}
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
	if err := prefs.SetLocalPremium(preferences.LocalPremium{Pins: true}); err != nil {
		t.Fatal(err)
	}
	if star(me) {
		t.Fatal("a star with only the pins on")
	}
	if err := prefs.SetLocalPremium(preferences.LocalPremium{Star: true}); err != nil {
		t.Fatal(err)
	}
	// Each account has its own star.
	if !star(me) || !star(7) {
		t.Fatal("no star with what all follow")
	}
	if err := prefs.SetLocalPremiumOf("b", preferences.LocalPremium{}); err != nil {
		t.Fatal(err)
	}
	if !star(me) || star(7) {
		t.Fatal("the star of one account follows another's")
	}
	if err := prefs.SetLocalPremiumOf("b", preferences.LocalPremium{Star: true}); err != nil {
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
	on := model.LocalPremium{GIFs: true}
	p.localPremium = func() model.LocalPremium { return on }
	if got := p.premiumText(l); got != "Local" {
		t.Fatalf("with it: %q", got)
	}
	on = model.LocalPremium{}
	if got := p.premiumText(l); got != "Not subscribed" {
		t.Fatalf("off again: %q", got)
	}
}

// failingStore tells of one failure.
type failingStore struct {
	failure model.SendFailure
	told    bool
}

func (s *failingStore) TakeSendFailure() (model.SendFailure, bool) {
	if s.told {
		return model.SendFailure{}, false
	}
	s.told = true
	return s.failure, true
}

// A failure reaches the toast, in the interface's words where they are known.
func TestSendFailureToast(t *testing.T) {
	l := localization.For("ru")
	var shown []string
	toast := func(s string) { shown = append(shown, s) }
	source := &failingStore{failure: model.SendFailure{Text: "CHAT_WRITE_FORBIDDEN"}}
	tellSendFailure(source, toast, l)
	tellSendFailure(source, toast, l)
	if len(shown) != 1 || shown[0] != l.T("composer.send_failed")+": CHAT_WRITE_FORBIDDEN" {
		t.Fatalf("shown %q", shown)
	}
	shown = nil
	tellSendFailure(&failingStore{failure: model.SendFailure{Text: "x", NoFFprobe: true}}, toast, l)
	if len(shown) != 1 || shown[0] != l.T("composer.send_no_ffprobe") {
		t.Fatalf("shown %q", shown)
	}
	shown = nil
	tellSendFailure(struct{}{}, toast, l)
	if len(shown) != 0 {
		t.Fatalf("a store without it: %q", shown)
	}
}
