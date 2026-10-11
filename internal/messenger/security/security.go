// SPDX-License-Identifier: Unlicense OR MIT

// Package security owns the device-bound key which protects local messenger
// data. The key is sealed by TPM 2.0 (the keychain on macOS) and can only be
// released after the user supplies the master password; where there is no
// TPM, it is sealed by the master password alone (password.go).
package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	configVersion = 1
	keySize       = 32
	saltSize      = 16

	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
)

var (
	ErrUnavailable  = errors.New("security: TPM 2.0 is unavailable")
	ErrLocked       = errors.New("security: local data is locked")
	ErrUnlockFailed = errors.New("security: wrong password or TPM rejected the key")
	ErrNotProtected = errors.New("security: local-data protection is not enabled")
	ErrPlaintext    = errors.New("security: plaintext data found while protection is enabled")
)

var envelopeMagic = []byte("KTGRM-AEAD\x00\x01")
var disableMarkerAD = []byte("komarugram-go/disable-protection/v1")

// State is a consistent snapshot suitable for the settings and unlock UI.
type State struct {
	// Available reports whether protection can be enabled, or the enabled
	// one unlocked: always, but for a key sealed by a TPM that is gone.
	Available bool
	// Hardware reports whether the key is, or would be, sealed by the TPM
	// (the keychain on macOS) rather than by the password alone.
	Hardware bool
	Enabled  bool
	Unlocked bool
	// Encrypting reports that protection is being turned on: the key is
	// known, and the data are being encrypted with it. Nothing is to be
	// unlocked then, though Unlocked is false until they are.
	Encrypting bool
	Busy       bool
	Problem    string
}

type config struct {
	Version       int    `json:"version"`
	Salt          []byte `json:"salt"`
	ArgonTime     uint32 `json:"argon_time"`
	ArgonMemory   uint32 `json:"argon_memory_kib"`
	ArgonThreads  uint8  `json:"argon_threads"`
	SealedPublic  []byte `json:"sealed_public"`
	SealedPrivate []byte `json:"sealed_private"`
	// Sealer is what sealed the key: "" for the TPM, sealerPassword for
	// the password alone.
	Sealer string `json:"sealer,omitempty"`
}

// TPM is the deliberately small hardware boundary used by Manager. Tests can
// exercise every migration and envelope rule without emulating a TPM.
type TPM interface {
	Probe() error
	Seal(secret, authorization []byte) (public, private []byte, err error)
	Unseal(public, private, authorization []byte) ([]byte, error)
}

// Manager keeps the unsealed key only in process memory. Configuration holds
// only TPM-wrapped material and an Argon2 salt, never a password verifier or a
// key which can be brute-forced away from the original TPM.
type Manager struct {
	mu   sync.Mutex
	path string
	tpm  TPM
	// soft seals the key where the TPM cannot (passwordSealer).
	soft         TPM
	config       *config
	key          []byte
	available    bool
	access       Access
	ready        bool
	disabling    bool
	encrypting   bool
	busy         bool
	problem      string
	migrate      func() error
	unmigrate    func() error
	migrations   map[uint64]func() error
	unmigrations map[uint64]func() error
	subs         map[uint64]func()
	nextID       uint64
	waitChange   chan struct{}
}

// Open loads the device-bound security configuration from the app directory.
func Open() (*Manager, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return OpenPath(filepath.Join(dir, "komarugram-go", "security.json"), defaultTPM())
}

// OpenPath is Open with explicit dependencies, primarily for tests.
func OpenPath(path string, tpm TPM) (*Manager, error) {
	m := &Manager{path: path, tpm: tpm, soft: passwordSealer{}, ready: true, subs: make(map[uint64]func()), waitChange: make(chan struct{})}
	m.access = checkAccess(tpm)
	m.available = m.access.Kind == AccessReady
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(path + ".disabling")
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("security: read configuration: %w", err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	m.config = &cfg
	m.ready = false
	if _, err := os.Stat(path + ".disabling"); err == nil {
		m.disabling = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return m, nil
}

func validateConfig(c *config) error {
	if c.Sealer != "" && c.Sealer != sealerPassword {
		return errors.New("security: invalid configuration")
	}
	if c.Version != configVersion || len(c.Salt) != saltSize || c.ArgonTime == 0 || c.ArgonMemory < 8*1024 || c.ArgonThreads == 0 || len(c.SealedPublic) == 0 || len(c.SealedPrivate) == 0 {
		return errors.New("security: invalid configuration")
	}
	return nil
}

// SetMigration installs the atomic session-rewrite step which must succeed
// before the manager reports itself unlocked. It is normally set once by the
// account manager during startup.
func (m *Manager) SetMigration(migrate func() error) {
	m.mu.Lock()
	m.migrate = migrate
	m.mu.Unlock()
}

// SetUnmigration installs the account store's inverse migration.
func (m *Manager) SetUnmigration(unmigrate func() error) {
	m.mu.Lock()
	m.unmigrate = unmigrate
	m.mu.Unlock()
}

// Disabling reports that a crash-resumable transition to plaintext is active.
func (m *Manager) Disabling() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disabling
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked()
}

func (m *Manager) stateLocked() State {
	hardware := m.available && m.tpm != nil
	if m.config != nil {
		hardware = m.config.Sealer == ""
	}
	_, usable := m.sealerLocked(m.config)
	return State{
		Available:  usable,
		Hardware:   hardware,
		Enabled:    m.config != nil,
		Unlocked:   m.config == nil || m.ready || (m.disabling && len(m.key) == keySize),
		Encrypting: m.encrypting,
		Busy:       m.busy,
		Problem:    m.problem,
	}
}

// Enable creates and verifies a random root key sealed by the TPM, or by
// the password alone where there is none, saves the public blobs, then rewrites all existing sessions before announcing success.
func (m *Manager) Enable(ctx context.Context, password string) error {
	if password == "" {
		return errors.New("security: empty master password")
	}
	m.mu.Lock()
	if m.config != nil {
		m.mu.Unlock()
		return errors.New("security: protection is already enabled")
	}
	if m.busy {
		m.mu.Unlock()
		return errors.New("security: another operation is in progress")
	}
	sealer, _ := m.sealerLocked(nil)
	m.busy, m.problem = true, ""
	m.signalLocked()
	m.mu.Unlock()
	m.notify()

	err := m.enable(ctx, password, sealer)
	m.finish(err)
	return err
}

func (m *Manager) enable(ctx context.Context, password string, sealer TPM) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A completed plaintext transition leaves its authenticated marker until
	// the next start. Remove it before making a new protected configuration.
	if err := os.Remove(m.path + ".disabling"); err == nil {
		if err := syncConfigDir(m.path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	salt := make([]byte, saltSize)
	root := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	if _, err := io.ReadFull(rand.Reader, root); err != nil {
		return err
	}
	defer clear(root)
	cfg := &config{Version: configVersion, Salt: salt, ArgonTime: argonTime, ArgonMemory: argonMemory, ArgonThreads: argonThreads}
	if _, soft := sealer.(passwordSealer); soft {
		cfg.Sealer, cfg.ArgonTime, cfg.ArgonMemory = sealerPassword, passwordArgonTime, passwordArgonMemory
	}
	auth := deriveAuthorization(password, salt, cfg.ArgonTime, cfg.ArgonMemory, cfg.ArgonThreads)
	defer clear(auth)
	public, private, err := sealer.Seal(root, auth)
	if err != nil {
		return fmt.Errorf("security: seal root key: %w", err)
	}
	check, err := sealer.Unseal(public, private, auth)
	if err != nil || len(check) != keySize || !equal(check, root) {
		clear(check)
		if err == nil {
			err = errors.New("TPM returned another key")
		}
		return fmt.Errorf("security: verify sealed key: %w", err)
	}
	clear(check)
	cfg.SealedPublic, cfg.SealedPrivate = public, private
	if err := writeConfig(m.path, cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.config = cfg
	m.key = append([]byte(nil), root...)
	m.ready = false
	m.encrypting = true
	migrate := m.migrationLocked()
	m.signalLocked()
	m.mu.Unlock()
	if migrate != nil {
		if err := migrate(); err != nil {
			m.mu.Lock()
			clear(m.key)
			m.key = nil
			m.encrypting = false
			m.signalLocked()
			m.mu.Unlock()
			return fmt.Errorf("security: encrypt existing sessions: %w", err)
		}
	}
	m.mu.Lock()
	m.ready = true
	m.encrypting = false
	m.signalLocked()
	m.mu.Unlock()
	return nil
}

// Unlock asks the TPM, or the password alone, to release the root key and completes any interrupted
// plaintext-session migration before waking account connections.
func (m *Manager) Unlock(ctx context.Context, password string) error {
	m.mu.Lock()
	if m.config == nil {
		m.mu.Unlock()
		return nil
	}
	if len(m.key) == keySize {
		m.mu.Unlock()
		return nil
	}
	sealer, usable := m.sealerLocked(m.config)
	if !usable {
		m.mu.Unlock()
		return ErrUnavailable
	}
	if m.busy {
		m.mu.Unlock()
		return errors.New("security: another operation is in progress")
	}
	m.busy, m.problem = true, ""
	cfg := *m.config
	m.signalLocked()
	m.mu.Unlock()
	m.notify()

	err := m.unlock(ctx, password, &cfg, sealer)
	m.finish(err)
	return err
}

// VerifyPassword checks the master password again even when the root key is
// already in memory. Unlock cannot be used for authorization of sensitive
// actions because it deliberately succeeds immediately after the first unlock.
func (m *Manager) VerifyPassword(ctx context.Context, password string) error {
	m.mu.Lock()
	if m.config == nil {
		m.mu.Unlock()
		return ErrNotProtected
	}
	if m.busy {
		m.mu.Unlock()
		return errors.New("security: another operation is in progress")
	}
	sealer, usable := m.sealerLocked(m.config)
	if !usable {
		m.mu.Unlock()
		return ErrUnavailable
	}
	cfg := *m.config
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	auth := deriveAuthorization(password, cfg.Salt, cfg.ArgonTime, cfg.ArgonMemory, cfg.ArgonThreads)
	defer clear(auth)
	root, err := sealer.Unseal(cfg.SealedPublic, cfg.SealedPrivate, auth)
	defer clear(root)
	if err != nil || len(root) != keySize {
		return ErrUnlockFailed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.key) != keySize || !equal(root, m.key) {
		return ErrLocked
	}
	return nil
}

// Disable permanently returns local stores to plaintext after a fresh TPM
// password check. A marker makes interrupted migrations resume on next unlock.
func (m *Manager) Disable(ctx context.Context, password string) error {
	if err := m.VerifyPassword(ctx, password); err != nil {
		return err
	}
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return errors.New("security: another operation is in progress")
	}
	if (!m.ready && !m.disabling) || m.unmigrate == nil {
		m.mu.Unlock()
		return errors.New("security: plaintext migration is unavailable")
	}
	m.busy, m.problem = true, ""
	m.signalLocked()
	m.mu.Unlock()
	m.notify()
	if err := ctx.Err(); err != nil {
		m.finish(err)
		return err
	}
	err := m.writeDisableMarker()
	if err == nil {
		m.mu.Lock()
		m.disabling = true
		m.ready = false // No new account connections during the transition.
		m.signalLocked()
		unmigrate := m.unmigrationLocked()
		m.mu.Unlock()
		err = unmigrate()
	}
	if err == nil {
		err = m.finishDisable()
	}
	m.finish(err)
	return err
}

func (m *Manager) writeDisableMarker() error {
	marker := m.path + ".disabling"
	if data, err := os.ReadFile(marker); err == nil {
		plain, err := m.Decrypt(data, disableMarkerAD)
		if err != nil || string(plain) != "disable" {
			return errors.New("security: invalid disable marker")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := m.Encrypt([]byte("disable"), disableMarkerAD)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(marker), ".disable-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), marker); err != nil {
		return err
	}
	return syncConfigDir(marker)
}

func syncConfigDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (m *Manager) finishDisable() error {
	if err := os.Remove(m.path); err != nil {
		return err
	}
	// Keep the authenticated marker until the next start. If a power failure
	// resurrects security.json, the transition will still be resumed.
	_ = syncConfigDir(m.path)
	m.mu.Lock()
	clear(m.key)
	m.key = nil
	m.config = nil
	m.disabling = false
	m.ready = true
	m.signalLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) unlock(ctx context.Context, password string, cfg *config, sealer TPM) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	auth := deriveAuthorization(password, cfg.Salt, cfg.ArgonTime, cfg.ArgonMemory, cfg.ArgonThreads)
	defer clear(auth)
	root, err := sealer.Unseal(cfg.SealedPublic, cfg.SealedPrivate, auth)
	if err != nil || len(root) != keySize {
		clear(root)
		return ErrUnlockFailed
	}
	m.mu.Lock()
	m.key = append([]byte(nil), root...)
	m.ready = false
	m.signalLocked()
	m.mu.Unlock()
	clear(root)
	if m.Disabling() {
		data, err := os.ReadFile(m.path + ".disabling")
		if err == nil {
			var plain []byte
			plain, err = m.Decrypt(data, disableMarkerAD)
			if err == nil && string(plain) != "disable" {
				err = errors.New("security: invalid disable marker")
			}
		}
		if err != nil {
			m.mu.Lock()
			clear(m.key)
			m.key = nil
			m.signalLocked()
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Lock()
	migrate := m.migrationLocked()
	if m.disabling {
		migrate = m.unmigrationLocked()
	}
	m.mu.Unlock()
	if migrate != nil {
		if err := migrate(); err != nil {
			m.mu.Lock()
			clear(m.key)
			m.key = nil
			m.signalLocked()
			m.mu.Unlock()
			return fmt.Errorf("security: migrate local data: %w", err)
		}
	}
	if m.Disabling() {
		return m.finishDisable()
	}
	m.mu.Lock()
	m.ready = true
	m.signalLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) finish(err error) {
	m.mu.Lock()
	m.busy = false
	if err != nil {
		m.problem = err.Error()
	} else {
		m.problem = ""
	}
	m.signalLocked()
	m.mu.Unlock()
	m.notify()
}

// sealerLocked is what seals the key of cfg, or of the protection Enable
// would make for a nil cfg, and whether it can be used now.
func (m *Manager) sealerLocked(cfg *config) (TPM, bool) {
	if cfg == nil {
		if m.available && m.tpm != nil {
			return m.tpm, true
		}
		return m.soft, true
	}
	if cfg.Sealer == sealerPassword {
		return m.soft, true
	}
	return m.tpm, m.available && m.tpm != nil
}

func deriveAuthorization(password string, salt []byte, time, memory uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(password), salt, time, memory, threads, keySize)
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}

func writeConfig(path string, cfg *config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".security-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Enabled reports whether new persistent secrets must be encrypted.
func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config != nil
}

// IsEnvelope distinguishes protected data from the legacy JSON format.
func IsEnvelope(data []byte) bool {
	return len(data) >= len(envelopeMagic) && equal(data[:len(envelopeMagic)], envelopeMagic)
}

// Encrypt authenticates and encrypts one complete persistent object.
func (m *Manager) Encrypt(plain, associatedData []byte) ([]byte, error) {
	key, err := m.keyCopy()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(envelopeMagic)+len(nonce)+len(plain)+aead.Overhead())
	out = append(out, envelopeMagic...)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plain, associatedData)
	return out, nil
}

// Decrypt verifies and opens a complete persistent object.
func (m *Manager) Decrypt(envelope, associatedData []byte) ([]byte, error) {
	if !IsEnvelope(envelope) {
		return nil, ErrPlaintext
	}
	key, err := m.keyCopy()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	offset := len(envelopeMagic)
	if len(envelope) < offset+aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("security: truncated encrypted data")
	}
	nonce := envelope[offset : offset+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, envelope[offset+aead.NonceSize():], associatedData)
	if err != nil {
		return nil, errors.New("security: encrypted data failed authentication")
	}
	return plain, nil
}

// DeriveKey returns a purpose-separated key for another local store, such as
// one account's SQLite history. Callers must clear it after use.
func (m *Manager) DeriveKey(purpose string) ([]byte, error) {
	root, err := m.keyCopy()
	if err != nil {
		return nil, err
	}
	defer clear(root)
	reader := hkdf.New(sha256.New, root, nil, []byte("komarugram-go/local-key/v1/"+purpose))
	key := make([]byte, keySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func (m *Manager) keyCopy() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config == nil {
		return nil, ErrNotProtected
	}
	if len(m.key) != keySize {
		return nil, ErrLocked
	}
	return append([]byte(nil), m.key...), nil
}

// WaitUnlocked blocks account connections while protected data is locked.
func (m *Manager) WaitUnlocked(ctx context.Context) error {
	for {
		m.mu.Lock()
		if m.config == nil || m.ready {
			m.mu.Unlock()
			return nil
		}
		changed := m.waitChange
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *Manager) signalLocked() {
	close(m.waitChange)
	m.waitChange = make(chan struct{})
}

// Subscribe invokes callback after security state changes.
func (m *Manager) Subscribe(callback func()) func() {
	if callback == nil {
		return func() {}
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.subs[id] = callback
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

func (m *Manager) notify() {
	m.mu.Lock()
	callbacks := make([]func(), 0, len(m.subs))
	for _, callback := range m.subs {
		callbacks = append(callbacks, callback)
	}
	m.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

// AddMigration registers a live cache's protection transition.
func (m *Manager) AddMigration(f func() error) func() {
	m.mu.Lock()
	if m.migrations == nil {
		m.migrations = make(map[uint64]func() error)
	}
	id := m.nextID
	m.nextID++
	m.migrations[id] = f
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.migrations, id); m.mu.Unlock() }
}

func (m *Manager) AddUnmigration(f func() error) func() {
	m.mu.Lock()
	if m.unmigrations == nil {
		m.unmigrations = make(map[uint64]func() error)
	}
	id := m.nextID
	m.nextID++
	m.unmigrations[id] = f
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.unmigrations, id); m.mu.Unlock() }
}

func (m *Manager) unmigrationLocked() func() error {
	fs := make([]func() error, 0, len(m.unmigrations)+1)
	for _, f := range m.unmigrations {
		fs = append(fs, f)
	}
	if m.unmigrate != nil {
		fs = append(fs, m.unmigrate)
	}
	return func() error {
		for _, f := range fs {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	}
}
func (m *Manager) migrationLocked() func() error {
	fs := make([]func() error, 0, len(m.migrations)+1)
	// Close active plaintext databases before the account migration purges inactive files.
	for _, f := range m.migrations {
		fs = append(fs, f)
	}
	if m.migrate != nil {
		fs = append(fs, m.migrate)
	}
	return func() error {
		for _, f := range fs {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	}
}
