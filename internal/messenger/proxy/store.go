// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"komarugram/internal/messenger/security"
)

// Protection is what the store asks of local-data protection, which
// *security.Manager is: the list holds the passwords of proxies, and is
// encrypted as the accounts are where protection is on.
type Protection interface {
	Enabled() bool
	Disabling() bool
	Encrypt(plain, associatedData []byte) ([]byte, error)
	Decrypt(envelope, associatedData []byte) ([]byte, error)
	AddMigration(f func() error) func()
	AddUnmigration(f func() error) func()
}

var associatedData = []byte("komarugram-go/proxies/v1")

const storeVersion = 1

// DirectID is the ID of connecting without a proxy, which is what is chosen
// until a proxy is.
const DirectID = ""

// Store is the proxies the user keeps and the one in use. It is safe for use
// from any goroutine.
//
// Until the first account is signed in, the list is kept in memory alone:
// where there is no account, nothing of the client is left on the machine.
// Persist saves it from then on, and what was added before.
type Store struct {
	path       string
	protection Protection

	mu         sync.Mutex
	list       []Proxy
	active     string
	forced     *Proxy
	persistent bool
	subs       map[uint64]func()
	nextSub    uint64
}

// NewStore returns the list kept in path, or in memory alone where path is
// empty. The file is read by Load, which protected data allows only when it
// is unlocked.
func NewStore(path string, protection Protection) *Store {
	s := &Store{path: path, protection: protection, subs: map[uint64]func(){}}
	if protection != nil && path != "" {
		// Not unregistered: the store lives as long as the process.
		protection.AddMigration(s.protect)
		protection.AddUnmigration(s.unprotect)
	}
	return s
}

// DefaultPath is the file of the list, beside the other local data.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "komarugram-go", "proxies.json"), nil
}

type fileData struct {
	Version int     `json:"version"`
	Active  string  `json:"active,omitempty"`
	Proxies []Proxy `json:"proxies"`
}

// Load reads the saved list, if there is one, and makes it persistent: a file
// is there because the list was meant to be kept. What was added before it
// stays, after it. It fails with security.ErrLocked until protected data is
// unlocked.
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.protection != nil && s.protection.Enabled() && !(s.protection.Disabling() && !security.IsEnvelope(data)) {
		if data, err = s.protection.Decrypt(data, associatedData); err != nil {
			return err
		}
	}
	var file fileData
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("proxy list: %w", err)
	}
	if file.Version != storeVersion {
		return fmt.Errorf("proxy list: unsupported version %d", file.Version)
	}
	s.mu.Lock()
	var loaded []Proxy
	for _, p := range file.Proxies {
		// What cannot be used is passed over, not the list with it.
		if p.Validate() == nil {
			loaded = append(loaded, p)
		}
	}
	for _, p := range s.list {
		if i := indexOf(loaded, p.ID()); i >= 0 {
			loaded[i] = p
		} else {
			loaded = append(loaded, p)
		}
	}
	s.list, s.persistent = loaded, true
	if s.active == DirectID {
		s.active = file.Active
	}
	if !s.knownLocked(s.active) {
		s.active = DirectID
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

// Persist keeps the list in its file from now on, and saves it.
func (s *Store) Persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistent = true
	return s.saveLocked()
}

func indexOf(list []Proxy, id string) int {
	return slices.IndexFunc(list, func(p Proxy) bool { return p.ID() == id })
}

// knownLocked reports whether id is one that can be in use.
func (s *Store) knownLocked(id string) bool {
	return id == DirectID || id == Telegram().ID() || indexOf(s.list, id) >= 0
}

// List is the proxies kept, the first added first.
func (s *Store) List() []Proxy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// Active is the proxy in use; false for connecting directly.
func (s *Store) Active() (Proxy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeLocked()
}

func (s *Store) activeLocked() (Proxy, bool) {
	if s.forced != nil {
		return *s.forced, true
	}
	switch s.active {
	case DirectID:
		return Proxy{}, false
	case Telegram().ID():
		return Telegram(), true
	}
	if i := indexOf(s.list, s.active); i >= 0 {
		return s.list[i], true
	}
	return Proxy{}, false
}

// ActiveID is the ID of the proxy in use, DirectID for none.
func (s *Store) ActiveID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.activeLocked(); ok {
		return p.ID()
	}
	return DirectID
}

// Add keeps a proxy and makes it the one in use. A proxy of the same server
// and user, which is the same one, takes the place of the old.
func (s *Store) Add(p Proxy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	if i := indexOf(s.list, p.ID()); i >= 0 {
		s.list[i] = p
	} else if p.ID() != Telegram().ID() {
		s.list = append(s.list, p)
	}
	s.active = p.ID()
	err := s.saveLocked()
	s.mu.Unlock()
	s.notify()
	return err
}

// Remove forgets the proxy; if it was in use, the connection is direct.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	i := indexOf(s.list, id)
	if i < 0 {
		s.mu.Unlock()
		return nil
	}
	s.list = slices.Delete(s.list, i, i+1)
	if s.active == id {
		s.active = DirectID
	}
	err := s.saveLocked()
	s.mu.Unlock()
	s.notify()
	return err
}

// Select puts the proxy in use; DirectID is no proxy.
func (s *Store) Select(id string) error {
	s.mu.Lock()
	if !s.knownLocked(id) {
		s.mu.Unlock()
		return errors.New("proxy: not in the list")
	}
	changed := s.active != id
	s.active = id
	var err error
	if changed {
		err = s.saveLocked()
	}
	s.mu.Unlock()
	if changed {
		s.notify()
	}
	return err
}

// Force puts a proxy in use that the user did not choose in the list, and
// that the list cannot change: one named on the command line or in the
// environment. It is not saved. A nil proxy lets the list rule again.
func (s *Store) Force(p *Proxy) error {
	if p != nil {
		if err := p.Validate(); err != nil {
			return err
		}
		c := *p
		p = &c
	}
	s.mu.Lock()
	s.forced = p
	s.mu.Unlock()
	s.notify()
	return nil
}

// Forced reports whether the proxy in use is one that Force put there.
func (s *Store) Forced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forced != nil
}

// Subscribe calls callback, from any goroutine, whenever the list or the proxy
// in use changes. It returns the function that ends the calls.
func (s *Store) Subscribe(callback func()) func() {
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = callback
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

func (s *Store) notify() {
	s.mu.Lock()
	callbacks := make([]func(), 0, len(s.subs))
	for _, f := range s.subs {
		callbacks = append(callbacks, f)
	}
	s.mu.Unlock()
	for _, f := range callbacks {
		f()
	}
}

// saveLocked writes the list where it is kept, if it is kept.
func (s *Store) saveLocked() error {
	if s.path == "" || !s.persistent {
		return nil
	}
	if len(s.list) == 0 && s.active == DirectID {
		// Nothing to keep: no file is left for it.
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(fileData{Version: storeVersion, Active: s.active, Proxies: s.list})
	if err != nil {
		return err
	}
	if s.protection != nil && s.protection.Enabled() && !s.protection.Disabling() {
		if data, err = s.protection.Encrypt(data, associatedData); err != nil {
			return err
		}
	}
	return writeFile(s.path, data)
}

// protect is the migration of local-data protection: the file is rewritten
// as an encrypted one, after the list is read from it.
func (s *Store) protect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || security.IsEnvelope(data) {
		return err
	}
	encrypted, err := s.protection.Encrypt(data, associatedData)
	if err != nil {
		return err
	}
	return writeFile(s.path, encrypted)
}

func (s *Store) unprotect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !security.IsEnvelope(data) {
		return err
	}
	plain, err := s.protection.Decrypt(data, associatedData)
	if err != nil {
		return err
	}
	defer clear(plain)
	return writeFile(s.path, plain)
}

// writeFile replaces the file whole, private to the user: a crash while
// saving leaves the old list, not half of the new.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "proxies-*.tmp")
	if err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
