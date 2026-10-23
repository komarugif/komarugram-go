// SPDX-License-Identifier: Unlicense OR MIT

package account

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/proxy"
	"komarugram/internal/messenger/security"
	"komarugram/pkg/tdata"
)

type fakeSecurityTPM struct{ secret []byte }

func (t *fakeSecurityTPM) Probe() error { return nil }
func (t *fakeSecurityTPM) Seal(secret, auth []byte) ([]byte, []byte, error) {
	t.secret = append([]byte(nil), secret...)
	return append([]byte(nil), auth...), []byte("same-device"), nil
}
func (t *fakeSecurityTPM) Unseal(public, private, auth []byte) ([]byte, error) {
	if !bytes.Equal(public, auth) || string(private) != "same-device" {
		return nil, errors.New("authorization failed")
	}
	return append([]byte(nil), t.secret...), nil
}

func TestTZOffset(t *testing.T) {
	for in, want := range map[int]int{
		0:                0,
		3 * 3600:         3 * 3600,
		5*3600 + 1800:    5*3600 + 1800, // India
		5*3600 + 2700:    5*3600 + 2700, // Nepal
		-(3*3600 + 1800): -(3*3600 + 1800),
		3*3600 + 449:     3 * 3600,
		3*3600 + 450:     3*3600 + 900,
		14 * 3600:        14 * 3600,
		-12 * 3600:       -12 * 3600,
		-13 * 3600:       11 * 3600,
	} {
		if got := tzOffset(in); got != want {
			t.Errorf("tzOffset(%d) = %d, want %d", in, got, want)
		}
	}
}

// fakeSession is a tdata session with a made-up key.
func fakeSession() tdata.TDataSession {
	key := make([]byte, 256)
	for i := range key {
		key[i] = byte(i * 7)
	}
	return tdata.TDataSession{AuthKey: hex.EncodeToString(key), UserID: 42, DC: 2}
}

func anotherFakeSession() tdata.TDataSession {
	s := fakeSession()
	s.UserID = 84
	key, _ := hex.DecodeString(s.AuthKey)
	key[0] ^= 0xff
	s.AuthKey = hex.EncodeToString(key)
	return s
}

func TestFromTData(t *testing.T) {
	s := fakeSession()
	converted, err := fromTData(s)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := hex.DecodeString(s.AuthKey)
	sum := sha1.Sum(key)
	if converted.data.DC != 2 || converted.DC != 2 || converted.UserID != 42 || converted.ID != "42" {
		t.Errorf("dc %d/%d, user %d, id %q", converted.data.DC, converted.DC, converted.UserID, converted.ID)
	}
	if hex.EncodeToString(converted.data.AuthKeyID) != hex.EncodeToString(sum[12:20]) || converted.KeyID != hex.EncodeToString(sum[12:20]) {
		t.Error("auth key id is not the low 64 bits of the key's SHA-1")
	}
	if converted.data.Addr == "" {
		t.Error("no address for DC 2")
	}

	for name, bad := range map[string]tdata.TDataSession{
		"short key":  {AuthKey: "abcd", DC: 2},
		"not hex":    {AuthKey: "zz", DC: 2},
		"unknown dc": {AuthKey: s.AuthKey, DC: 9},
	} {
		if _, err := fromTData(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// testManager returns a manager keeping everything under root.
func testManager(t *testing.T, root string, protection *security.Manager) *Manager {
	t.Helper()
	m, err := newManager(TDesktop(), protection, filepath.Join(root, "config"), filepath.Join(root, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func readTData(t *testing.T, sessions ...tdata.TDataSession) *TData {
	t.Helper()
	archive, err := tdata.CreateZipBytes(sessions)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ReadTData(archive)
	if err != nil {
		t.Fatal(err)
	}
	return imported
}

func TestImportTData(t *testing.T) {
	ctx := context.Background()
	m := testManager(t, t.TempDir(), nil)
	archive := readTData(t, fakeSession())
	if n, err := m.NewInTData(archive); n != 1 || err != nil {
		t.Fatalf("new in archive: %d, %v", n, err)
	}
	added, err := m.ImportTData(ctx, archive)
	if err != nil || len(added) != 1 {
		t.Fatalf("added %d, err %v", len(added), err)
	}
	if n, _ := m.NewInTData(archive); n != 0 {
		t.Errorf("%d accounts still new after import", n)
	}
	if again, _ := m.ImportTData(ctx, archive); len(again) != 0 || len(m.Accounts()) != 1 {
		t.Error("the same key was imported twice")
	}
	if _, err := ReadTData([]byte("not a zip")); err == nil {
		t.Error("garbage accepted as tdata")
	}
}

func TestDecryptLocalDataAndReencrypt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := filepath.Join(root, "config", "security.json")
	tpm := new(fakeSecurityTPM)
	protection, err := security.OpenPath(config, tpm)
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, root, protection)
	added, err := m.ImportTData(ctx, readTData(t, fakeSession()))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := historycache.Open(added[0].HistoryPath(), added[0].ID, protection)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.Put(ctx, "cursor", map[string]int{"offset": 12}); err != nil {
		t.Fatal(err)
	}
	if err := protection.Enable(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := protection.Disable(ctx, "wrong"); !errors.Is(err, security.ErrUnlockFailed) {
		t.Fatalf("wrong password: %v", err)
	}
	if !protection.Enabled() {
		t.Fatal("wrong password disabled protection")
	}
	if err := protection.Disable(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if protection.Enabled() {
		t.Fatal("protection still enabled")
	}
	if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("security config remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "accounts.db.plain")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "accounts.db.secure")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("encrypted registry remains: %v", err)
	}
	if _, err := os.Stat(added[0].HistoryPath() + ".plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(added[0].HistoryPath() + ".secure"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("encrypted history remains: %v", err)
	}
	raw, err := os.ReadFile(added[0].storage.path)
	if err != nil || security.IsEnvelope(raw) {
		t.Fatalf("session not plaintext: %v", err)
	}
	var cursor map[string]int
	if found, err := cache.Get(ctx, "cursor", &cursor); err != nil || !found || cursor["offset"] != 12 {
		t.Fatalf("history lost: %v, %v, %v", found, cursor, err)
	}
	if err := protection.Enable(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	if !protection.Enabled() {
		t.Fatal("reencryption failed")
	}
}

func TestInterruptedDecryptionResumesOnUnlock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := filepath.Join(root, "config", "security.json")
	tpm := new(fakeSecurityTPM)
	protection, err := security.OpenPath(config, tpm)
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, root, protection)
	if _, err := m.ImportTData(ctx, readTData(t, fakeSession())); err != nil {
		t.Fatal(err)
	}
	if err := protection.Enable(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	protection.SetUnmigration(func() error {
		if err := m.unprotectSessions(); err != nil {
			return err
		}
		return errors.New("interrupted after converting files")
	})
	if err := protection.Disable(ctx, "secret"); err == nil {
		t.Fatal("transition did not fail")
	}
	if _, err := os.Stat(config + ".disabling"); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := security.OpenPath(config, tpm)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := testManager(t, root, restarted)
	if err := restarted.Unlock(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if restarted.Enabled() {
		t.Fatal("interrupted transition not finished")
	}
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Accounts()) != 1 {
		t.Fatal("account lost on resume")
	}
}

func TestAccountsPersistAndReload(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	m := testManager(t, root, nil)
	added, err := m.ImportTData(ctx, readTData(t, fakeSession(), anotherFakeSession()))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || added[0].AuthorizationID() == added[1].AuthorizationID() {
		t.Fatalf("imported authorizations are not distinct: %d accounts", len(added))
	}
	ada := Card{FirstName: "Ada", LastName: "Lovelace", Username: "ada", Phone: "+100", PhotoID: 7, Photo: []byte{0xff, 0xd8, 0, 1}}
	if err := m.SaveCard(added[0], ada); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCard(added[0], Card{Photo: make([]byte, MaxCardPhoto+1)}); err == nil {
		t.Error("oversized photo saved")
	}
	m.Close()
	info, err := os.Stat(filepath.Join(root, "config", "accounts.db.plain"))
	if err != nil {
		t.Fatal(err)
	}
	if notOwnerOnly(info) {
		t.Errorf("registry mode %v, want 0600", info.Mode().Perm())
	}

	reloaded := testManager(t, root, nil)
	if len(reloaded.Accounts()) != 0 {
		t.Error("accounts listed before Load")
	}
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	accounts := reloaded.Accounts()
	if len(accounts) != 2 || accounts[0].ID != added[0].ID || accounts[1].ID != added[1].ID {
		t.Fatalf("reloaded accounts are not the imported ones in order: %+v", accounts)
	}
	for _, a := range accounts {
		if _, err := (&session.Loader{Storage: a.storage}).Load(ctx); err != nil {
			t.Errorf("account %s session: %v", a.ID, err)
		}
		info, err := os.Stat(a.storage.path)
		if err != nil {
			t.Fatal(err)
		}
		if notOwnerOnly(info) {
			t.Errorf("account %s session mode %v, want 0600", a.ID, info.Mode().Perm())
		}
	}
	cards, err := reloaded.Cards()
	if err != nil {
		t.Fatal(err)
	}
	got := cards[added[0].ID]
	if got.Name() != "Ada Lovelace" || got.Username != "ada" || got.Phone != "+100" || got.PhotoID != 7 || !bytes.Equal(got.Photo, ada.Photo) {
		t.Fatalf("reloaded card %+v", got)
	}
}

func TestRemoveAccount(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	m := testManager(t, root, nil)
	added, err := m.ImportTData(ctx, readTData(t, fakeSession(), anotherFakeSession()))
	if err != nil {
		t.Fatal(err)
	}
	gone := added[0]
	gone.busy.Store(true)
	if err := m.Remove(gone); !errors.Is(err, ErrInUse) {
		t.Fatalf("removed a connected account: %v", err)
	}
	gone.busy.Store(false)
	// Its media, in the cache directory.
	if err := os.MkdirAll(filepath.Join(gone.MediaPath(), "ab"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gone.MediaPath(), root) || strings.HasPrefix(gone.MediaPath(), gone.dir) {
		t.Fatalf("media in %s", gone.MediaPath())
	}
	if err := m.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("account directory left behind: %v", err)
	}
	if _, err := os.Stat(gone.MediaPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("media left behind: %v", err)
	}
	if accounts := m.Accounts(); len(accounts) != 1 || accounts[0] != added[1] {
		t.Fatalf("accounts after removal: %+v", accounts)
	}
	reloaded := testManager(t, root, nil)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if accounts := reloaded.Accounts(); len(accounts) != 1 || accounts[0].ID != added[1].ID {
		t.Fatalf("accounts after restart: %+v", accounts)
	}
	// The removed account can be imported again.
	if again, err := reloaded.ImportTData(ctx, readTData(t, fakeSession())); err != nil || len(again) != 1 {
		t.Fatalf("import after removal: %d, %v", len(again), err)
	}
}

func TestProtectionEncryptsEverything(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tpm := new(fakeSecurityTPM)
	protection, err := security.OpenPath(filepath.Join(root, "security.json"), tpm)
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, root, protection)
	added, err := m.ImportTData(ctx, readTData(t, fakeSession()))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCard(added[0], Card{FirstName: "Visible", Photo: []byte("photo-marker")}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(added[0].storage.path)
	if err != nil || security.IsEnvelope(before) {
		t.Fatalf("unexpected initial session, err %v", err)
	}
	if err := protection.Enable(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(added[0].storage.path)
	if err != nil {
		t.Fatal(err)
	}
	if !security.IsEnvelope(raw) || bytes.Contains(raw, []byte("auth_key")) {
		t.Fatal("session was not migrated to an encrypted envelope")
	}
	if _, err := os.Stat(filepath.Join(root, "config", "accounts.db.plain")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plaintext registry left behind: %v", err)
	}
	if err := m.SaveCard(added[0], Card{FirstName: "Hidden", Photo: []byte("photo-marker")}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	registry, err := os.ReadFile(filepath.Join(root, "config", "accounts.db.secure"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"SQLite format 3", "Hidden", "photo-marker", added[0].keyID} {
		if bytes.Contains(registry, []byte(marker)) {
			t.Fatalf("encrypted registry contains %q", marker)
		}
	}

	restartedProtection, err := security.OpenPath(filepath.Join(root, "security.json"), tpm)
	if err != nil {
		t.Fatal(err)
	}
	restarted := testManager(t, root, restartedProtection)
	if err := restarted.Load(); !errors.Is(err, security.ErrLocked) {
		t.Fatalf("registry read while locked: %v", err)
	}
	if err := restartedProtection.Unlock(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	// Unlocking runs the migration, which has already read the registry.
	accounts := restarted.Accounts()
	if len(accounts) != 1 || accounts[0].ID != added[0].ID {
		t.Fatalf("accounts after unlock: %+v", accounts)
	}
	if _, err := (&session.Loader{Storage: accounts[0].storage}).Load(ctx); err != nil {
		t.Fatalf("unlocked session read: %v", err)
	}
	if cards, err := restarted.Cards(); err != nil || cards[added[0].ID].FirstName != "Hidden" {
		t.Fatalf("unlocked cards %+v, %v", cards, err)
	}
}

// TestRegistryMigrationResumes checks both crash points of the move into the
// encrypted registry: before the encrypted copy is complete, and after it
// but before the plaintext one is removed.
func TestRegistryMigrationResumes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	protection, err := security.OpenPath(filepath.Join(root, "security.json"), new(fakeSecurityTPM))
	if err != nil {
		t.Fatal(err)
	}
	r := &registry{path: filepath.Join(root, "accounts.db")}
	if err := r.insert(record{ID: "1", UserID: 1, DC: 2, KeyID: "k", Card: Card{FirstName: "Kept"}}); err != nil {
		t.Fatal(err)
	}
	r.close()
	if err := protection.Enable(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	// An unfinished copy from an earlier attempt is discarded.
	if err := os.WriteFile(r.securePath()+".tmp", []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.protection = protection
	records, err := r.list()
	if err != nil || len(records) != 1 || records[0].Card.FirstName != "Kept" || records[0].KeyID != "k" {
		t.Fatalf("resumed migration: %+v, %v", records, err)
	}
	r.close()
	if _, err := os.Stat(r.securePath() + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary copy left behind: %v", err)
	}

	// A completed copy wins over a plaintext file left beside it.
	if err := os.WriteFile(r.plainPath(), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if records, err = r.list(); err != nil || len(records) != 1 {
		t.Fatalf("after completed copy: %+v, %v", records, err)
	}
	r.close()
	if _, err := os.Stat(r.plainPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale plaintext registry left behind: %v", err)
	}
}

func TestRefusesAnotherKeyForSameUser(t *testing.T) {
	m := testManager(t, t.TempDir(), nil)
	first := fakeSession()
	second := anotherFakeSession()
	second.UserID = first.UserID
	if _, err := m.ImportTData(context.Background(), readTData(t, first, second)); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("same user with another key: %v", err)
	}
	if len(m.Accounts()) != 1 {
		t.Fatalf("%d accounts after the refused key", len(m.Accounts()))
	}
}

// TestSingleConnection checks both guards without connecting anywhere: a
// second Run in this process, and a lock file already held by another.
func TestSingleConnection(t *testing.T) {
	ctx := context.Background()
	m := testManager(t, t.TempDir(), nil)
	added, err := m.ImportTData(ctx, readTData(t, fakeSession()))
	if err != nil {
		t.Fatal(err)
	}
	a := added[0]

	a.busy.Store(true)
	if err := m.Run(ctx, a, nil); !errors.Is(err, ErrInUse) {
		t.Errorf("second run in the process: %v", err)
	}
	a.busy.Store(false)

	unlock, err := lockFile(filepath.Join(m.lockDir, a.keyID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := lockFile(filepath.Join(m.lockDir, a.keyID+".lock")); err == nil {
		t.Error("the lock was taken twice")
	}
	err = m.Run(ctx, a, func(context.Context, *tg.Client) error {
		t.Error("connected while the lock was held")
		return nil
	})
	if !errors.Is(err, ErrInUse) {
		t.Errorf("run while locked: %v", err)
	}
	if a.busy.Load() {
		t.Error("a refused run left the account busy")
	}
}

// TestRegistryUpgradesFromFirstSchema opens a registry written before the
// Premium column existed.
func TestRegistryUpgradesFromFirstSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.db")
	db, err := sql.Open("sqlite3", path+".plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE accounts(
		id TEXT PRIMARY KEY, user_id INTEGER NOT NULL UNIQUE, dc INTEGER NOT NULL, key_id TEXT NOT NULL UNIQUE,
		position INTEGER NOT NULL, first_name TEXT NOT NULL DEFAULT '', last_name TEXT NOT NULL DEFAULT '',
		username TEXT NOT NULL DEFAULT '', phone TEXT NOT NULL DEFAULT '', photo_id INTEGER NOT NULL DEFAULT 0,
		photo BLOB, added INTEGER NOT NULL, updated INTEGER NOT NULL DEFAULT 0) WITHOUT ROWID;
		PRAGMA user_version = 1;
		INSERT INTO accounts(id, user_id, dc, key_id, position, first_name, added) VALUES('42', 42, 2, 'k', 1, 'Ada', 0);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r := &registry{path: path}
	defer r.close()
	records, err := r.list()
	if err != nil || len(records) != 1 || records[0].Card.FirstName != "Ada" || records[0].Card.Premium {
		t.Fatalf("upgraded registry: %+v, %v", records, err)
	}
	if err := r.saveCard("42", Card{FirstName: "Ada", Premium: true}); err != nil {
		t.Fatal(err)
	}
	if records, _ = r.list(); !records[0].Card.Premium {
		t.Fatal("Premium was not saved")
	}
}

// notOwnerOnly reports whether others than the file's owner may read it, as
// far as its mode tells. On Windows it tells nothing (Go reports 0666 for a
// writable file): the ACL of the user's profile, where the files are, keeps
// them.
func notOwnerOnly(info os.FileInfo) bool {
	return runtime.GOOS != "windows" && info.Mode().Perm() != 0o600
}

// TestUseProxyTellsOfTheChange checks that a manager has the resolver of the
// proxy set, direct again for nil, and that whoever waits for a change is
// told once for each, and is not told of a proxy that cannot be used.
func TestUseProxyTellsOfTheChange(t *testing.T) {
	m := testManager(t, t.TempDir(), nil)
	if r, _ := m.proxyState(); r != nil {
		t.Fatal("a proxy before any was set")
	}
	changed := m.ProxyChanged()
	select {
	case <-changed:
		t.Fatal("told of a change that did not happen")
	default:
	}
	socks := proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1", Port: 2080}
	if err := m.UseProxy(&socks); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("not told of the proxy")
	}
	r, next := m.proxyState()
	if r == nil {
		t.Fatal("no resolver for the proxy")
	}
	select {
	case <-next:
		t.Fatal("the next change was told already")
	default:
	}
	if err := m.UseProxy(&proxy.Proxy{Kind: proxy.SOCKS5, Host: "127.0.0.1"}); err == nil {
		t.Error("a proxy without a port was used")
	}
	select {
	case <-next:
		t.Error("told of a proxy that was refused")
	default:
	}
	if err := m.UseProxy(nil); err != nil {
		t.Fatal(err)
	}
	if r, _ := m.proxyState(); r != nil {
		t.Error("the proxy stayed after nil")
	}
	<-next
}

// TestPremiumMediaIsAskedAtEachConnection checks that the manager reads what
// tells the Premium address when it is asked, so that a switch in the settings
// takes effect without the resolver being made again.
func TestPremiumMediaIsAskedAtEachConnection(t *testing.T) {
	m := testManager(t, t.TempDir(), nil)
	if m.premiumMediaNow() {
		t.Fatal("on before it was set")
	}
	on := false
	m.SetPremiumMedia(func() bool { return on })
	if m.premiumMediaNow() {
		t.Error("on while the setting is off")
	}
	on = true
	if !m.premiumMediaNow() {
		t.Error("off after the setting was turned on")
	}
}
