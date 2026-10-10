// SPDX-License-Identifier: Unlicense OR MIT

package security

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// missingTPM is a TPM that is not there.
type missingTPM struct{}

func (missingTPM) Probe() error { return errors.New("no TPM") }
func (missingTPM) Access(error) Access {
	return Access{Kind: AccessMissing}
}
func (missingTPM) Seal([]byte, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("no TPM")
}
func (missingTPM) Unseal([]byte, []byte, []byte) ([]byte, error) {
	return nil, errors.New("no TPM")
}

// TestPasswordProtectionWithoutTPM checks that protection is offered and
// works without a TPM: sealed by the password, with the costlier Argon2id,
// locked after a restart, and opened by the password only.
func TestPasswordProtectionWithoutTPM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	m, err := OpenPath(path, missingTPM{})
	if err != nil {
		t.Fatal(err)
	}
	if st := m.State(); !st.Available || st.Hardware {
		t.Fatalf("state without a TPM: %+v", st)
	}
	if err := m.Enable(context.Background(), "a long passphrase"); err != nil {
		t.Fatal(err)
	}
	var cfg config
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Sealer != sealerPassword || cfg.ArgonTime != passwordArgonTime || cfg.ArgonMemory != passwordArgonMemory {
		t.Fatalf("configuration: sealer %q, Argon2id t=%d m=%d", cfg.Sealer, cfg.ArgonTime, cfg.ArgonMemory)
	}
	plain := []byte(`{"auth_key":"secret"}`)
	encrypted, err := m.Encrypt(plain, []byte("session"))
	if err != nil || bytes.Contains(encrypted, []byte("secret")) {
		t.Fatalf("not encrypted: %v", err)
	}

	restarted, err := OpenPath(path, missingTPM{})
	if err != nil {
		t.Fatal(err)
	}
	if st := restarted.State(); !st.Available || st.Hardware || !st.Enabled || st.Unlocked {
		t.Fatalf("restart state %+v", st)
	}
	if err := restarted.Unlock(context.Background(), "a long passphrase."); !errors.Is(err, ErrUnlockFailed) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := restarted.Unlock(context.Background(), "a long passphrase"); err != nil {
		t.Fatal(err)
	}
	if opened, err := restarted.Decrypt(encrypted, []byte("session")); err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("opened %q, %v", opened, err)
	}
	if err := restarted.VerifyPassword(context.Background(), "wrong"); !errors.Is(err, ErrUnlockFailed) {
		t.Fatalf("verify, wrong password: %v", err)
	}
}

// TestPasswordProtectionStaysWithATPMAdded checks that a key sealed by the
// password still opens once a TPM is there, and that a TPM, when there is
// one, is what new protection uses.
func TestPasswordProtectionStaysWithATPMAdded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	m, _ := OpenPath(path, missingTPM{})
	if err := m.Enable(context.Background(), "pass"); err != nil {
		t.Fatal(err)
	}
	withTPM, err := OpenPath(path, new(fakeTPM))
	if err != nil {
		t.Fatal(err)
	}
	if st := withTPM.State(); st.Hardware {
		t.Fatalf("a password-sealed key reported as the TPM's: %+v", st)
	}
	if err := withTPM.Unlock(context.Background(), "pass"); err != nil {
		t.Fatal(err)
	}

	fresh, _ := OpenPath(filepath.Join(t.TempDir(), "security.json"), new(fakeTPM))
	if st := fresh.State(); !st.Available || !st.Hardware {
		t.Fatalf("state with a TPM: %+v", st)
	}
}

// TestTPMProtectionWithoutTheTPM checks that a key the TPM sealed, as every
// configuration written before the password sealer was, cannot be opened
// once the TPM is gone, whatever the password.
func TestTPMProtectionWithoutTheTPM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	m, _ := OpenPath(path, new(fakeTPM))
	if err := m.Enable(context.Background(), "pass"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if bytes.Contains(b, []byte(`"sealer"`)) {
		t.Fatalf("a TPM configuration names a sealer: %s", b)
	}
	gone, err := OpenPath(path, missingTPM{})
	if err != nil {
		t.Fatal(err)
	}
	if st := gone.State(); st.Available || !st.Hardware {
		t.Fatalf("state with the TPM gone: %+v", st)
	}
	if err := gone.Unlock(context.Background(), "pass"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unlock with the TPM gone: %v", err)
	}
}

// TestPasswordSealerRejectsAnotherKey checks the sealer alone: what one
// authorization sealed, another does not open.
func TestPasswordSealerRejectsAnotherKey(t *testing.T) {
	var s passwordSealer
	secret := bytes.Repeat([]byte{7}, keySize)
	public, private, err := s.Seal(secret, bytes.Repeat([]byte{1}, keySize))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(private, secret) {
		t.Fatal("the secret is in the sealed key")
	}
	if _, err := s.Unseal(public, private, bytes.Repeat([]byte{2}, keySize)); err == nil {
		t.Fatal("opened with another authorization")
	}
	got, err := s.Unseal(public, private, bytes.Repeat([]byte{1}, keySize))
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("unsealed %x, %v", got, err)
	}
}
