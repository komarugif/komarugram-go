// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/pkg/tdata"
)

func TestExpandTDataPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.zip", "a.ZIP", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := expandTDataPaths([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || filepath.Base(paths[0]) != "a.ZIP" || filepath.Base(paths[1]) != "b.zip" {
		t.Fatalf("expanded paths %v", paths)
	}
}

func TestInitialAccountRestoresOnlyLastKnownAccount(t *testing.T) {
	settings, err := preferences.OpenPath(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	windows := newAccountWindows(nil, appwindow.Options{}, nil, nil, nil, settings, nil, nil)
	windows.addAccounts(&account.Account{ID: "first"}, &account.Account{ID: "last"})
	if got := windows.InitialAccountID(); got != "first" {
		t.Fatalf("initial account = %q, want first", got)
	}
	if err := settings.SetLastAccount("last"); err != nil {
		t.Fatal(err)
	}
	if got := windows.InitialAccountID(); got != "last" {
		t.Fatalf("restored account = %q, want last", got)
	}
	if err := settings.SetLastAccount("removed"); err != nil {
		t.Fatal(err)
	}
	if got := windows.InitialAccountID(); got != "first" {
		t.Fatalf("fallback account = %q, want first", got)
	}
}

// testEnvironment points the user's config and cache directories into a
// temporary one.
func testEnvironment(t *testing.T) string {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	// On Windows the user's directories come from APPDATA and LOCALAPPDATA:
	// without these, tests would import accounts into the real profile.
	t.Setenv("APPDATA", filepath.Join(root, "config"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "cache"))
	return root
}

func testArchive(t *testing.T) *account.TData {
	t.Helper()
	key := make([]byte, 256)
	for i := range key {
		key[i] = byte(i * 7)
	}
	archive, err := tdata.CreateZipBytes([]tdata.TDataSession{{AuthKey: hex.EncodeToString(key), UserID: 42, DC: 2}})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := account.ReadTData(archive)
	if err != nil {
		t.Fatal(err)
	}
	return imported
}

type fakeTPM struct{ secret []byte }

func (t *fakeTPM) Probe() error { return nil }
func (t *fakeTPM) Seal(secret, auth []byte) ([]byte, []byte, error) {
	t.secret = append([]byte(nil), secret...)
	return append([]byte(nil), auth...), []byte("device"), nil
}
func (t *fakeTPM) Unseal(public, private, auth []byte) ([]byte, error) {
	return append([]byte(nil), t.secret...), nil
}

// TestStartupOffersProtectionForNewAccounts checks that importing a new
// account asks about protection first, and that starting again with the
// same archive neither asks nor imports anything.
func TestStartupOffersProtectionForNewAccounts(t *testing.T) {
	root := testEnvironment(t)
	protection, err := security.OpenPath(filepath.Join(root, "security.json"), new(fakeTPM))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := preferences.OpenPath(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for run, wantAdded := range []int{1, 0} {
		manager, err := account.NewManager(account.TDesktop(), protection)
		if err != nil {
			t.Fatal(err)
		}
		windows := newAccountWindows(nil, appwindow.Options{}, manager, nil, protection, settings, nil, []*account.TData{testArchive(t)})
		offers := 0
		added, err := windows.start(context.Background(), func(context.Context) error {
			offers++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(added) != wantAdded || offers != wantAdded {
			t.Fatalf("run %d: added %d, offered %d times", run, len(added), offers)
		}
		if again, _ := windows.start(context.Background(), nil); again != nil {
			t.Fatalf("run %d: started twice", run)
		}
		if all := windows.All(); len(all) != 1 || all[0].ID != "42" {
			t.Fatalf("run %d: listed %+v", run, all)
		}
		manager.Close()
	}
}

// TestAccountListWithoutWindows checks that a saved account is shown by its
// name and photo although no window of it has been opened in this process.
func TestAccountListWithoutWindows(t *testing.T) {
	root := testEnvironment(t)
	manager, err := account.NewManager(account.TDesktop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	added, err := manager.ImportTData(context.Background(), testArchive(t))
	if err != nil || len(added) != 1 {
		t.Fatalf("import: %d, %v", len(added), err)
	}
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveCard(added[0], account.Card{FirstName: "Ada", Username: "ada", PhotoID: 1, Photo: photo.Bytes()}); err != nil {
		t.Fatal(err)
	}
	manager.Close()

	restarted, err := account.NewManager(account.TDesktop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	settings, err := preferences.OpenPath(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	windows := newAccountWindows(nil, appwindow.Options{}, restarted, nil, nil, settings, nil, nil)
	if _, err := windows.start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	all := windows.All()
	if len(all) != 1 {
		t.Fatalf("%d accounts listed", len(all))
	}
	got := all[0]
	if got.Name != "Ada" || got.Username != "ada" || got.UserID != 42 || got.Avatar == nil || got.Open {
		t.Fatalf("listed %+v", got)
	}
}
