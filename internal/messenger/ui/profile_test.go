// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// newOwnProfile is the account's profile page on store as the client makes
// it: the info of others' profiles, attached, drawing avatars as squares.
func newOwnProfile(t *testing.T, store *mockstore.Store, invalidate func()) *profilePage {
	t.Helper()
	p := newProfilePage(store)
	p.premium = store
	info := newChatInfo(store, new(imageOps), invalidate)
	info.drawAvatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
	}
	p.attach(info)
	t.Cleanup(info.Destroy)
	return p
}

// editProfile opens the form, types into it with the keyboard and saves it
// with Enter from the last field.
func editProfile(t *testing.T, username string) (*profilePage, *mockstore.Store) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	changed := make(chan struct{}, 1)
	p := newOwnProfile(t, store, func() {})
	focusBio := false
	h := &focusHarness{draw: func(gtx layout.Context) {
		if focusBio {
			p.bio.Focus(gtx)
			focusBio = false
		}
		p.Update(gtx, store.Me(), func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
		p.Layout(gtx, store.Me(), localization.For("ru"), false, false)
	}}
	h.frame()
	// What a click on the pencil does.
	p.editing = true
	p.first.editor.SetText("Ада")
	p.last.editor.SetText("")
	p.username.editor.SetText(username)
	p.bio.editor.SetText("Первая программистка")
	focusBio = true
	h.frame()
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame()
	if !p.saving {
		t.Fatalf("Enter in the last field did not save; problem %v", p.problem)
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("the save did not finish")
	}
	h.frame()
	return p, store
}

func TestProfileEditSaves(t *testing.T) {
	p, store := editProfile(t, "ada_lovelace")
	if p.editing || p.problem != nil {
		t.Fatalf("after saving: editing %v, problem %v", p.editing, p.problem)
	}
	me := store.Me()
	if me.Name() != "Ада" || me.Username != "ada_lovelace" || me.Bio != "Первая программистка" {
		t.Fatalf("saved profile %+v", me)
	}
}

func TestProfileEditShowsTakenUsername(t *testing.T) {
	p, store := editProfile(t, "durov")
	if !p.editing || !errors.Is(p.problem, model.ErrUsernameTaken) {
		t.Fatalf("after a refused save: editing %v, problem %v", p.editing, p.problem)
	}
	if store.Me().Username == "durov" {
		t.Fatal("a refused username was kept")
	}
}

func TestAccountSubtitleHidesIdentifiers(t *testing.T) {
	l := localization.For("ru")
	a := model.AccountInfo{ID: "1", Username: "ada", Phone: "+100"}
	if got := accountSubtitle(a, "1", false, l); got != "@ada · "+l.T("settings.current") {
		t.Errorf("open subtitle %q", got)
	}
	if got := accountSubtitle(a, "1", true, l); got != l.T("settings.current") {
		t.Errorf("private subtitle %q", got)
	}
}

func TestLimitValue(t *testing.T) {
	l := localization.For("ru")
	for value, want := range map[int]string{4000: "2 ГБ", 8000: "4 ГБ", 3000: "1,5 ГБ"} {
		if got := limitValue("upload_max_fileparts", value, l); got != want {
			t.Errorf("%d parts: %q, want %q", value, got, want)
		}
	}
	if got := limitValue("dialog_filters_limit", 30, l); got != "30" {
		t.Errorf("folders %q", got)
	}
}

func TestPremiumRaisesBioLimit(t *testing.T) {
	store := mockstore.New(time.Now(), 0) // The demo account has Premium.
	p := newProfilePage(store)
	p.premium = store
	if p.bioLimit() != 140 {
		t.Fatalf("bio limit with Premium %d", p.bioLimit())
	}
	long := model.ProfileEdit{FirstName: "A", Bio: strings.Repeat("я", 100)}
	if err := long.ValidateWith(p.bioLimit()); err != nil {
		t.Fatalf("a 100-character bio refused with Premium: %v", err)
	}
	if err := long.Validate(); !errors.Is(err, model.ErrBioTooLong) {
		t.Fatalf("a 100-character bio without Premium: %v", err)
	}
}
