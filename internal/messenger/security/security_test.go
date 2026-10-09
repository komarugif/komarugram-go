// SPDX-License-Identifier: Unlicense OR MIT

package security

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeTPM struct {
	mu     sync.Mutex
	device []byte
}

func (f *fakeTPM) Probe() error { return nil }

func (f *fakeTPM) Seal(secret, auth []byte) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.device == nil {
		f.device = bytes.Repeat([]byte{0x5a}, keySize)
	}
	public := append([]byte(nil), auth...)
	public = append(public, f.device...)
	private := make([]byte, len(secret))
	for i := range secret {
		private[i] = secret[i] ^ f.device[i%len(f.device)]
	}
	return public, private, nil
}

func (f *fakeTPM) Unseal(public, private, auth []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(public) != len(auth)+len(f.device) || !equal(public[:len(auth)], auth) || !equal(public[len(auth):], f.device) {
		return nil, errors.New("authorization failed")
	}
	secret := make([]byte, len(private))
	for i := range private {
		secret[i] = private[i] ^ f.device[i%len(f.device)]
	}
	return secret, nil
}

func TestEnableEncryptRestartUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	tpm := new(fakeTPM)
	m, err := OpenPath(path, tpm)
	if err != nil {
		t.Fatal(err)
	}
	migrated := 0
	m.SetMigration(func() error { migrated++; return nil })
	if err := m.Enable(context.Background(), "easy"); err != nil {
		t.Fatal(err)
	}
	if migrated != 1 || !m.State().Enabled || !m.State().Unlocked {
		t.Fatalf("state %+v, migrations %d", m.State(), migrated)
	}
	if err := m.VerifyPassword(context.Background(), "wrong"); !errors.Is(err, ErrUnlockFailed) {
		t.Fatalf("unlocked manager accepted wrong password: %v", err)
	}
	if err := m.VerifyPassword(context.Background(), "easy"); err != nil {
		t.Fatalf("unlocked manager rejected master password: %v", err)
	}
	plain := []byte(`{"auth_key":"secret"}`)
	encrypted, err := m.Encrypt(plain, []byte("session"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("secret")) || !IsEnvelope(encrypted) {
		t.Fatal("session was not encrypted")
	}

	restarted, err := OpenPath(path, tpm)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.State().Enabled || restarted.State().Unlocked {
		t.Fatalf("restart state %+v", restarted.State())
	}
	if err := restarted.Unlock(context.Background(), "wrong"); !errors.Is(err, ErrUnlockFailed) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := restarted.Unlock(context.Background(), "easy"); err != nil {
		t.Fatal(err)
	}
	opened, err := restarted.Decrypt(encrypted, []byte("session"))
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("opened %q, err %v", opened, err)
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := restarted.Decrypt(encrypted, []byte("session")); err == nil {
		t.Fatal("tampered session was accepted")
	}
}

func TestCopiedConfigCannotUnlockOnAnotherTPM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	first := new(fakeTPM)
	m, _ := OpenPath(path, first)
	if err := m.Enable(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "security.json")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(copyPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	second := &fakeTPM{device: bytes.Repeat([]byte{0xa5}, keySize)}
	copied, err := OpenPath(copyPath, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := copied.Unlock(context.Background(), "1"); !errors.Is(err, ErrUnlockFailed) {
		t.Fatalf("copied vault unlocked on another TPM: %v", err)
	}
}

func TestForgedDisableMarkerCannotDecrypt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	tpm := new(fakeTPM)
	m, err := OpenPath(path, tpm)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".disabling", []byte("forged"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenPath(path, tpm)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Unlock(context.Background(), "secret"); err == nil {
		t.Fatal("forged marker accepted")
	}
	if !restarted.Enabled() || restarted.State().Unlocked {
		t.Fatal("forged marker disabled protection")
	}
}

func TestDerivedKeysAreSeparated(t *testing.T) {
	m, _ := OpenPath(filepath.Join(t.TempDir(), "security.json"), new(fakeTPM))
	if err := m.Enable(context.Background(), "password"); err != nil {
		t.Fatal(err)
	}
	a, _ := m.DeriveKey("sqlite/account-a")
	b, _ := m.DeriveKey("sqlite/account-b")
	if bytes.Equal(a, b) {
		t.Fatal("purpose-separated keys are equal")
	}
}

func TestUnlockWaitsForMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	tpm := new(fakeTPM)
	initial, _ := OpenPath(path, tpm)
	if err := initial.Enable(context.Background(), "password"); err != nil {
		t.Fatal(err)
	}

	restarted, _ := OpenPath(path, tpm)
	migrationStarted := make(chan struct{})
	allowMigration := make(chan struct{})
	restarted.SetMigration(func() error {
		close(migrationStarted)
		<-allowMigration
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- restarted.Unlock(context.Background(), "password") }()
	<-migrationStarted
	if restarted.State().Unlocked {
		t.Fatal("manager reported unlocked before plaintext migration finished")
	}
	waited := make(chan error, 1)
	go func() { waited <- restarted.WaitUnlocked(context.Background()) }()
	select {
	case <-waited:
		t.Fatal("account connection passed the migration barrier")
	default:
	}
	close(allowMigration)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
}

// While Enable encrypts the data, the state says so: the key is known,
// and nothing is to be unlocked, though Unlocked is false until it ends.
func TestEnableReportsEncrypting(t *testing.T) {
	m, err := OpenPath(filepath.Join(t.TempDir(), "security.json"), new(fakeTPM))
	if err != nil {
		t.Fatal(err)
	}
	var during State
	m.SetMigration(func() error { during = m.State(); return nil })
	if err := m.Enable(context.Background(), "secret"); err != nil {
		t.Fatal(err)
	}
	if !during.Enabled || during.Unlocked || !during.Encrypting {
		t.Fatalf("state while encrypting: %+v", during)
	}
	if st := m.State(); !st.Unlocked || st.Encrypting {
		t.Fatalf("state after encrypting: %+v", st)
	}
}

// A failed encryption leaves the key unknown, to be unlocked again.
func TestFailedEnableIsNotEncrypting(t *testing.T) {
	m, err := OpenPath(filepath.Join(t.TempDir(), "security.json"), new(fakeTPM))
	if err != nil {
		t.Fatal(err)
	}
	m.SetMigration(func() error { return errors.New("disk full") })
	if err := m.Enable(context.Background(), "secret"); err == nil {
		t.Fatal("Enable succeeded with a failed migration")
	}
	if st := m.State(); st.Encrypting || st.Unlocked {
		t.Fatalf("state after a failed encryption: %+v", st)
	}
}
