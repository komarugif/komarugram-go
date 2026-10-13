// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"komarugram/internal/messenger/security"
)

type testTPM struct{ device []byte }

func (f *testTPM) Probe() error { return nil }
func (f *testTPM) Seal(secret, auth []byte) ([]byte, []byte, error) {
	if f.device == nil {
		f.device = bytes.Repeat([]byte{0x5a}, len(secret))
	}
	private := make([]byte, len(secret))
	for i := range secret {
		private[i] = secret[i] ^ f.device[i]
	}
	return append(append([]byte(nil), auth...), f.device...), private, nil
}
func (f *testTPM) Unseal(public, private, auth []byte) ([]byte, error) {
	if len(public) != len(auth)+len(f.device) || !bytes.Equal(public[:len(auth)], auth) {
		return nil, errors.New("authorization failed")
	}
	secret := make([]byte, len(private))
	for i := range private {
		secret[i] = private[i] ^ f.device[i]
	}
	return secret, nil
}

var (
	bob   = Proxy{Kind: SOCKS5, Host: "10.0.0.1", Port: 1080, User: "bob", Password: "hunter2"}
	local = Proxy{Kind: SOCKS5, Host: "127.0.0.1", Port: 2080}
	mtp   = Proxy{Kind: MTProto, Host: "example.org", Port: 443, Secret: "dddf83f4ddaee1f1171b2e7a6e7403c60d"}
)

func TestStoreAddSelectRemove(t *testing.T) {
	s := NewStore("", nil)
	if _, ok := s.Active(); ok {
		t.Fatal("a proxy is in use before any was added")
	}
	for _, p := range []Proxy{bob, local, mtp} {
		if err := s.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.List(); !slices.Equal(got, []Proxy{bob, local, mtp}) {
		t.Errorf("list: %v", got)
	}
	if p, ok := s.Active(); !ok || p != mtp {
		t.Errorf("the one added last is in use: %v %v", p, ok)
	}
	if err := s.Select(local.ID()); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Active(); p != local {
		t.Errorf("selected %v, in use %v", local, p)
	}
	if err := s.Select("socks5|nowhere|1|"); err == nil {
		t.Error("a proxy that is not in the list was selected")
	}

	// The same server and user is one proxy, whose password is changed.
	renewed := bob
	renewed.Password = "new"
	if err := s.Add(renewed); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 3 || got[0] != renewed {
		t.Errorf("list after a new password: %v", got)
	}

	if err := s.Remove(renewed.ID()); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(DirectID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Active(); ok {
		t.Error("DirectID still has a proxy in use")
	}
	if err := s.Add(Proxy{Kind: SOCKS5, Host: "x", Port: 0}); err == nil {
		t.Error("an invalid proxy was added")
	}
	if got := len(s.List()); got != 2 {
		t.Errorf("%d proxies, want 2", got)
	}
}

func TestStoreRemovingTheOneInUseGoesDirect(t *testing.T) {
	s := NewStore("", nil)
	s.Add(local)
	s.Remove(local.ID())
	if _, ok := s.Active(); ok || s.ActiveID() != DirectID {
		t.Error("a removed proxy is still in use")
	}
}

func TestStoreTelegramWebSocketIsAlwaysThere(t *testing.T) {
	s := NewStore("", nil)
	if err := s.Select(Telegram().ID()); err != nil {
		t.Fatal(err)
	}
	if p, ok := s.Active(); !ok || p != Telegram() {
		t.Errorf("%v %v", p, ok)
	}
	if len(s.List()) != 0 {
		t.Errorf("the built-in one is in the list the user keeps: %v", s.List())
	}
	// Added as a link, it is not kept twice.
	s.Add(Telegram())
	if len(s.List()) != 0 {
		t.Errorf("kept twice: %v", s.List())
	}
}

func TestStoreForce(t *testing.T) {
	s := NewStore("", nil)
	s.Add(local)
	forced := bob
	if err := s.Force(&forced); err != nil {
		t.Fatal(err)
	}
	forced.Host = "changed"
	if p, _ := s.Active(); p != bob || !s.Forced() {
		t.Errorf("forced %v, in use %v", bob, p)
	}
	s.Force(nil)
	if p, _ := s.Active(); p != local || s.Forced() {
		t.Errorf("after letting go: %v", p)
	}
}

func TestStoreNotifies(t *testing.T) {
	s := NewStore("", nil)
	var n atomic.Int32
	stop := s.Subscribe(func() { n.Add(1) })
	s.Add(local)
	s.Select(DirectID)
	s.Select(DirectID) // not a change
	s.Remove(local.ID())
	if got := n.Load(); got != 3 {
		t.Errorf("told %d times, want 3", got)
	}
	stop()
	s.Add(bob)
	if got := n.Load(); got != 3 {
		t.Error("told after the subscription ended")
	}
}

// TestStoreKeepsNothingBeforeAnAccount checks the rule that a list lives in
// memory until the first account: nothing is written for it before Persist.
func TestStoreKeepsNothingBeforeAnAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.json")
	s := NewStore(path, nil)
	s.Add(bob)
	s.Add(local)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file before an account: %v", err)
	}
	if err := s.Persist(); err != nil {
		t.Fatal(err)
	}
	again := NewStore(path, nil)
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if got := again.List(); !slices.Equal(got, []Proxy{bob, local}) {
		t.Errorf("read back %v", got)
	}
	if p, _ := again.Active(); p != local {
		t.Errorf("read back in use %v", p)
	}
	// After it, every change is kept.
	s.Remove(bob.ID())
	third := NewStore(path, nil)
	third.Load()
	if got := third.List(); !slices.Equal(got, []Proxy{local}) {
		t.Errorf("after a removal: %v", got)
	}
	// And a list that came to nothing leaves no file.
	s.Remove(local.ID())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an empty list left a file: %v", err)
	}
}

func TestStoreLoadKeepsWhatWasAddedBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.json")
	saved := NewStore(path, nil)
	saved.Persist()
	saved.Add(bob)

	s := NewStore(path, nil)
	s.Add(local)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); !slices.Equal(got, []Proxy{bob, local}) {
		t.Errorf("list: %v", got)
	}
	if p, _ := s.Active(); p != local {
		t.Errorf("what the user chose here was given up for what was saved: %v", p)
	}
}

func TestStoreSkipsWhatCannotBeUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.json")
	file := `{"version":1,"active":"gone","proxies":[` +
		`{"kind":"socks5","host":"10.0.0.1","port":1080},` +
		`{"kind":"ftp","host":"x","port":1},` +
		`{"kind":"socks5","host":"y","port":0}]}`
	os.WriteFile(path, []byte(file), 0o600)
	s := NewStore(path, nil)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 1 || got[0].Host != "10.0.0.1" {
		t.Errorf("list: %v", got)
	}
	if s.ActiveID() != DirectID {
		t.Errorf("a proxy that is not in the list is in use: %q", s.ActiveID())
	}
	os.WriteFile(path, []byte(`{"version":9}`), 0o600)
	if err := NewStore(path, nil).Load(); err == nil {
		t.Error("a list of a version unknown was read")
	}
}

// TestStoreIsProtectedWithTheAccounts checks that the passwords of proxies
// are not on the disk in plain text once protection is on, whether it was
// turned on before the list was saved or after, and that turning it off
// brings the list back.
func TestStoreIsProtectedWithTheAccounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")
	m, err := security.OpenPath(filepath.Join(dir, "security.json"), &testTPM{})
	if err != nil {
		t.Fatal(err)
	}
	// What the accounts add to a migration is nothing here.
	m.SetMigration(func() error { return nil })
	m.SetUnmigration(func() error { return nil })
	plain := func() bool {
		data, err := os.ReadFile(path)
		return err == nil && bytes.Contains(data, []byte(bob.Password))
	}

	s := NewStore(path, m)
	s.Persist()
	s.Add(bob)
	if !plain() {
		t.Fatal("the list is not saved as it is where there is no protection")
	}
	// Turned on later: the file is rewritten.
	if err := m.Enable(context.Background(), "master"); err != nil {
		t.Fatal(err)
	}
	if plain() {
		t.Fatal("the password is in the file in plain text after protection was turned on")
	}
	data, _ := os.ReadFile(path)
	if !security.IsEnvelope(data) {
		t.Fatal("the file is not an envelope")
	}
	// And what is saved from then on.
	s.Add(local)
	if plain() {
		t.Fatal("the password is in the file in plain text after a change")
	}
	again := NewStore(path, m)
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if got := again.List(); !slices.Equal(got, []Proxy{bob, local}) {
		t.Errorf("read back %v", got)
	}
	// And back.
	if err := m.Disable(context.Background(), "master"); err != nil {
		t.Fatal(err)
	}
	if !plain() {
		t.Error("the list was not brought back when protection was turned off")
	}
}
