package ui

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp"
	"gio-mw/exp/appearance"
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/security"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// renderTPM is a TPM for screenshots: it works, or fails as access says.
type renderTPM struct {
	access security.Access
	secret []byte
}

func (t *renderTPM) Probe() error {
	if t.access.Kind != security.AccessReady {
		return errors.New("no access")
	}
	return nil
}
func (t *renderTPM) Access(error) security.Access { return t.access }
func (t *renderTPM) Seal(secret, auth []byte) ([]byte, []byte, error) {
	t.secret = append([]byte(nil), secret...)
	return auth, []byte("device"), nil
}
func (t *renderTPM) Unseal(_, _, _ []byte) ([]byte, error) { return t.secret, nil }

func renderPNG(t *testing.T, path string, size image.Point, frames int, draw func(gtx layout.Context)) {
	t.Helper()
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	for i := 0; i < frames; i++ {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		exp.Background(gtx)
		draw(gtx)
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestRenderAccountScreens draws the screens around adding and leaving an
// account and saves them, for looking at them:
//
//	ACCOUNTS_PNG_DIR=/some/dir go test ./internal/messenger/ui -run RenderAccountScreens
func TestRenderAccountScreens(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	l := localization.For("ru")
	size := image.Pt(900, 820)
	for name, access := range map[string]security.Access{
		"protect-ready":    {Kind: security.AccessReady},
		"protect-no-group": {Kind: security.AccessNoGroup, Device: "/dev/tpmrm0", Group: "tss"},
		"protect-missing":  {Kind: security.AccessMissing},
		// As on Haiku, which has no TPM driver at all.
		"protect-failed": {Kind: security.AccessFailed, Detail: "open /dev/tpmrm0: no such file"},
	} {
		protection, err := security.OpenPath(filepath.Join(t.TempDir(), "security.json"), &renderTPM{access: access})
		if err != nil {
			t.Fatal(err)
		}
		signIn := login.New(nil)
		ctx, cancel := context.WithCancel(context.Background())
		go signIn.OfferProtection(ctx)
		for signIn.State().Step != login.StepProtect {
			time.Sleep(time.Millisecond)
		}
		page := newLoginPage(signIn, newSecurityView(protection, func() {}), nil)
		renderPNG(t, filepath.Join(dir, name+".png"), size, 2, func(gtx layout.Context) {
			page.Update(gtx, localization.For("ru"))
			page.Layout(gtx, l, false)
		})
		cancel()
	}

	// The unlock screen of a protected, locked start.
	configPath := filepath.Join(t.TempDir(), "security.json")
	tpm := &renderTPM{}
	protection, err := security.OpenPath(configPath, tpm)
	if err != nil {
		t.Fatal(err)
	}
	if err := protection.Enable(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	locked, err := security.OpenPath(configPath, tpm)
	if err != nil {
		t.Fatal(err)
	}
	view := newSecurityView(locked, func() {})
	renderPNG(t, filepath.Join(dir, "unlock.png"), size, 1, func(gtx layout.Context) {
		view.UnlockLayout(gtx, l)
	})
	// And of one protected by the password alone.
	noTPM := &renderTPM{access: security.Access{Kind: security.AccessFailed}}
	passwordPath := filepath.Join(t.TempDir(), "security.json")
	byPassword, err := security.OpenPath(passwordPath, noTPM)
	if err != nil {
		t.Fatal(err)
	}
	if err := byPassword.Enable(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if byPassword, err = security.OpenPath(passwordPath, noTPM); err != nil {
		t.Fatal(err)
	}
	passwordView := newSecurityView(byPassword, func() {})
	renderPNG(t, filepath.Join(dir, "unlock-password.png"), size, 1, func(gtx layout.Context) {
		passwordView.UnlockLayout(gtx, l)
	})

	// The profile as it is, covered by visual privacy, and being edited.
	store := mockstore.New(time.Now(), 0)
	for name, state := range map[string]struct{ private, editing bool }{
		"profile":         {},
		"profile-private": {private: true},
		"profile-edit":    {editing: true},
	} {
		page := newProfilePage(store)
		if state.editing {
			me := store.Me()
			page.editing = true
			page.first.editor.SetText(me.FirstName)
			page.last.editor.SetText(me.LastName)
			page.username.editor.SetText(me.Username)
			page.bio.editor.SetText(me.Bio)
		}
		renderPNG(t, filepath.Join(dir, name+".png"), size, 1, func(gtx layout.Context) {
			page.Layout(gtx, store.Me(), l, avatar, state.private, false)
		})
	}

	// The privacy section with visual privacy on.
	privacy := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
		func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	privacy.private = func() bool { return true }
	privacy.setPrivate = func(bool) {}
	privacy.section = settingsPrivacy
	renderPNG(t, filepath.Join(dir, "privacy.png"), size, 2, func(gtx layout.Context) {
		privacy.Update(gtx, themeAuto, "ru")
		privacy.Layout(gtx, themeAuto, appearance.Light, false, l)
	})

	// Premium: the section with and without a subscription, the chat list
	// and the profile with the star.
	// Without media, custom emoji do not load: the star stands for a status.
	badges := (&App{}).badges
	for name, active := range map[string]bool{"premium-on": true, "premium-off": false} {
		page := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
			func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
		page.premium = fixedPremium(model.PremiumFromConfig(active, map[string]any{"premium_purchase_blocked": false, "premium_bot_username": "PremiumBot"}))
		page.section = settingsPremium
		renderPNG(t, filepath.Join(dir, name+".png"), size, 2, func(gtx layout.Context) {
			page.Update(gtx, themeAuto, "ru")
			page.Layout(gtx, themeAuto, appearance.Light, false, l)
		})
	}
	list := newChatList()
	list.badges = badges
	renderPNG(t, filepath.Join(dir, "chats-premium.png"), image.Pt(420, 1100), 2, func(gtx layout.Context) {
		list.Layout(gtx, section{kind: sectionAll}, store.Folders(), store.Chats(), 0, false, l)
	})
	profile := newProfilePage(store)
	profile.badges = badges
	renderPNG(t, filepath.Join(dir, "profile-premium.png"), image.Pt(900, 320), 1, func(gtx layout.Context) {
		profile.Layout(gtx, store.Me(), l, avatar, false, false)
	})

	// The marks themselves, large, to compare with Telegram's; and each
	// kind beside a name.
	renderPNG(t, filepath.Join(dir, "badge-shapes.png"), image.Pt(480, 200), 1, func(gtx layout.Context) {
		offset(gtx, image.Pt(20, 20), func(gtx layout.Context) layout.Dimensions { return layoutPremiumStar(gtx, 128) })
		offset(gtx, image.Pt(220, 20), func(gtx layout.Context) layout.Dimensions { return layoutVerified(gtx, 128) })
	})
	renderPNG(t, filepath.Join(dir, "badge-names.png"), image.Pt(420, 220), 1, func(gtx layout.Context) {
		for i, b := range []model.Badges{{Premium: true}, {Verified: true}, {Verified: true, Premium: true}, {Scam: true}, {Fake: true, Premium: true}} {
			offset(gtx, image.Pt(16, 12+i*40), func(gtx layout.Context) layout.Dimensions {
				before, after := badges(b, 18, false)
				return withBadges(gtx, before, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, "Имя Фамилия", token.TypestyleTitleMediumEmphasized, scheme(gtx).Surface.OnColor, 1)
				}, after)
			})
		}
	})

	// Settings asking to confirm logging out.
	accounts := staticAccounts{{ID: "1", UserID: 1, Name: "Ада Лавлейс", Username: "ada"}}
	p := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, accounts,
		func() string { return "1" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
	p.images = new(imageOps)
	p.confirmingLogOut = true
	renderPNG(t, filepath.Join(dir, "logout.png"), size, 1, func(gtx layout.Context) {
		p.Layout(gtx, themeAuto, appearance.Light, false, l)
	})
}

type fixedPremium model.Premium

func (p fixedPremium) Premium() model.Premium { return model.Premium(p) }
