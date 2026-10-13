// SPDX-License-Identifier: Unlicense OR MIT

// Package account keeps the Telegram accounts of the client and connects to
// them one connection at a time.
//
// Accounts come from Telegram Desktop tdata archives or a phone sign-in. The
// list of them is a registry database shared by all (see registry); the
// authorization of each is a session file in a private directory of its own,
// beside its history cache, so several accounts can stay connected at once
// and survive a restart.
//
// Two things end a session on the server, and both are guarded against here:
// connecting under an api_id other than the one the session was created with
// (see Identity), and using one auth key from two connections at once. The
// second is refused within the process and, through a lock file, across
// processes — a second copy of the client, or a test run next to it.
package account

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"komarugram/internal/diagnostics"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/proxy"
	"komarugram/internal/messenger/security"
	"komarugram/pkg/dcpool"
	"komarugram/pkg/tdata"
)

// ErrProxyChanged is what Add returns when the proxy was changed while it was
// still connecting: the sign-in is to be started again, through the new one.
var ErrProxyChanged = errors.New("account: the proxy was changed")

// ErrInUse is returned when an account is already connected, in this process
// or in another one.
var ErrInUse = errors.New("account: already connected elsewhere")

// ErrAccountExists means that the Telegram user is already registered
// locally. One local account deliberately owns one authorization and one
// window.
var ErrAccountExists = errors.New("account: user already registered")

// Account is one authorized Telegram account.
type Account struct {
	// ID is the stable local account id. It is also the namespace for every
	// account-specific cache and setting.
	ID string
	// UserID is the account's user id.
	UserID int64
	// DC is the account's home data center.
	DC int

	keyID   string
	dir     string
	storage *fileStorage
	busy    atomic.Bool
}

func accountID(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

// AuthorizationID returns a short non-secret fingerprint of the auth key.
// It is suitable for detecting an accidentally duplicated session, but it
// cannot be used to authorize as the account.
func (a *Account) AuthorizationID() string { return a.keyID }

// HistoryPath is account-scoped and contains no authorization material.
func (a *Account) HistoryPath() string {
	return filepath.Join(a.dir, "history.db")
}

// Manager holds the accounts of the client.
type Manager struct {
	identity Identity
	security *security.Manager
	// lockDir holds one lock file per auth key.
	lockDir string
	// accountsDir holds one private directory per account.
	accountsDir string
	registry    *registry

	mu       sync.Mutex
	loaded   bool
	accounts []*Account
	// resolver, if set, connects through a proxy instead of directly; it and
	// proxyChanged, which is closed when the proxy is changed, are under proxyMu.
	proxyMu      sync.RWMutex
	resolver     dcs.Resolver
	proxyChanged chan struct{}
	// premiumMedia tells whether files over WebSocket go to the address of
	// Premium accounts; nil for no.
	premiumMedia func() bool
}

// UseProxy makes the connections made from now on go through p; nil is
// connecting directly. The ones made already stay as they are: ProxyChanged
// tells whoever is to make them again.
func (m *Manager) UseProxy(p *proxy.Proxy) error {
	var resolver dcs.Resolver
	if p != nil {
		var err error
		if resolver, err = p.ResolverWith(proxy.Options{PremiumMedia: m.premiumMediaNow}); err != nil {
			return err
		}
	}
	m.proxyMu.Lock()
	m.resolver = resolver
	close(m.proxyChanged)
	m.proxyChanged = make(chan struct{})
	m.proxyMu.Unlock()
	return nil
}

// SetPremiumMedia sets what tells, at each connection over WebSocket, whether
// files are asked for at the address of Premium accounts.
func (m *Manager) SetPremiumMedia(f func() bool) {
	m.proxyMu.Lock()
	m.premiumMedia = f
	m.proxyMu.Unlock()
}

func (m *Manager) premiumMediaNow() bool {
	m.proxyMu.RLock()
	f := m.premiumMedia
	m.proxyMu.RUnlock()
	return f != nil && f()
}

// ProxyChanged returns a channel that is closed when UseProxy is called
// next.
func (m *Manager) ProxyChanged() <-chan struct{} {
	_, changed := m.proxyState()
	return changed
}

// proxyState is the resolver in use, and the channel that is closed when it
// is replaced.
func (m *Manager) proxyState() (dcs.Resolver, <-chan struct{}) {
	m.proxyMu.RLock()
	defer m.proxyMu.RUnlock()
	return m.resolver, m.proxyChanged
}

// NewManager returns a manager of the accounts in the user's config
// directory whose sessions connect as identity. With protection set, its data
// is encrypted whenever local-data protection is enabled, and nothing of it
// can be read before the user unlocks: Load reads it then.
func NewManager(identity Identity, protection *security.Manager) (*Manager, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return newManager(identity, protection, filepath.Join(config, "komarugram-go"), filepath.Join(cache, "komarugram-go", "locks"))
}

// newManager keeps the accounts in dir and the connection locks in lockDir.
func newManager(identity Identity, protection *security.Manager, dir, lockDir string) (*Manager, error) {
	accountsDir := filepath.Join(dir, "accounts")
	for _, d := range []string{lockDir, accountsDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	m := &Manager{
		identity: identity, security: protection, lockDir: lockDir, accountsDir: accountsDir,
		registry: &registry{path: filepath.Join(dir, "accounts.db"), protection: protection},
		proxyChanged: make(chan struct{}),
	}
	if protection != nil {
		protection.SetMigration(m.protectSessions)
		protection.SetUnmigration(m.unprotectSessions)
	}
	return m, nil
}

// Load reads the list of accounts. It fails with security.ErrLocked until
// local-data protection is unlocked; once it has succeeded it does nothing.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked()
}

func (m *Manager) loadLocked() error {
	if m.loaded {
		return nil
	}
	records, err := m.registry.list()
	if err != nil {
		return err
	}
	accounts := make([]*Account, 0, len(records))
	for _, rec := range records {
		dir := filepath.Join(m.accountsDir, rec.ID)
		if _, err := os.Stat(filepath.Join(dir, "session.json")); err != nil {
			// Without its session the account cannot connect; it is listed
			// again once imported or signed in anew.
			log.Printf("account %s: session: %v", rec.ID, err)
			continue
		}
		accounts = append(accounts, m.account(rec, dir))
	}
	m.accounts, m.loaded = accounts, true
	return nil
}

func (m *Manager) account(rec record, dir string) *Account {
	return &Account{
		ID: rec.ID, UserID: rec.UserID, DC: rec.DC, keyID: rec.KeyID, dir: dir,
		storage: m.fileStorage(filepath.Join(dir, "session.json")),
	}
}

// Close closes the registry. The manager opens it again if it is used
// afterwards.
func (m *Manager) Close() error {
	return m.registry.close()
}

// Accounts returns the accounts in the order they were added; none before
// Load.
func (m *Manager) Accounts() []*Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*Account(nil), m.accounts...)
}

// Cards returns the saved cards of the accounts, by account id. With
// local-data protection on and locked it fails with security.ErrLocked.
func (m *Manager) Cards() (map[string]Card, error) {
	records, err := m.registry.list()
	if err != nil {
		return nil, err
	}
	cards := make(map[string]Card, len(records))
	for _, rec := range records {
		cards[rec.ID] = rec.Card
	}
	return cards, nil
}

// SaveCard replaces the saved card of a.
func (m *Manager) SaveCard(a *Account, c Card) error {
	if len(c.Photo) > MaxCardPhoto {
		return fmt.Errorf("account: card photo of %d bytes is too large", len(c.Photo))
	}
	return m.registry.saveCard(a.ID, c)
}

// TData is a Telegram Desktop tdata archive read into memory, whose accounts
// are not added yet.
type TData struct {
	sessions []tdataSession
}

type tdataSession struct {
	record
	data *session.Data
}

// ReadTData reads the accounts of a zipped Telegram Desktop tdata directory.
func ReadTData(archive []byte) (*TData, error) {
	sessions, err := tdata.ReadZipBytes(archive)
	if err != nil {
		return nil, fmt.Errorf("account: read tdata: %w", err)
	}
	if len(sessions) == 0 {
		return nil, errors.New("account: no session in tdata")
	}
	t := new(TData)
	for _, s := range sessions {
		converted, err := fromTData(s)
		if err != nil {
			return nil, err
		}
		t.sessions = append(t.sessions, converted)
	}
	return t, nil
}

// NewInTData returns how many accounts of t ImportTData would add.
func (m *Manager) NewInTData(t *TData) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return 0, err
	}
	n := 0
	for _, s := range t.sessions {
		if m.byKeyLocked(s.KeyID) == nil {
			n++
		}
	}
	return n, nil
}

// ImportTData adds the accounts of t. An auth key already known is skipped;
// another auth key of a user already known is ErrAccountExists.
func (m *Manager) ImportTData(ctx context.Context, t *TData) ([]*Account, error) {
	if m.security != nil && m.security.Disabling() {
		return nil, errors.New("account: local data migration is in progress")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadLocked(); err != nil {
		return nil, err
	}
	var added []*Account
	for _, s := range t.sessions {
		if m.byKeyLocked(s.KeyID) != nil {
			continue
		}
		pending, err := m.pendingDir(".import-")
		if err != nil {
			return added, err
		}
		err = (&session.Loader{Storage: m.fileStorage(filepath.Join(pending, "session.json"))}).Save(ctx, s.data)
		var a *Account
		if err == nil {
			a, err = m.commitLocked(pending, s.record)
		}
		os.RemoveAll(pending)
		if err != nil {
			return added, err
		}
		added = append(added, a)
	}
	return added, nil
}

func (m *Manager) byKeyLocked(keyID string) *Account {
	for _, a := range m.accounts {
		if a.keyID == keyID {
			return a
		}
	}
	return nil
}

func (m *Manager) hasUser(userID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

// pendingDir creates a private directory for an account being added.
func (m *Manager) pendingDir(prefix string) (string, error) {
	pending, err := os.MkdirTemp(m.accountsDir, prefix)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(pending, 0o700); err != nil {
		os.RemoveAll(pending)
		return "", err
	}
	return pending, nil
}

// commitLocked installs pending, which holds the session of rec, as the
// directory of a new account and lists it. m.mu must be held.
func (m *Manager) commitLocked(pending string, rec record) (*Account, error) {
	for _, a := range m.accounts {
		if a.UserID == rec.UserID {
			return nil, fmt.Errorf("%w: %s", ErrAccountExists, rec.ID)
		}
	}
	dir := filepath.Join(m.accountsDir, rec.ID)
	// A directory left by an account that was never listed — a crash while
	// adding it — holds nothing anyone can use.
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.Rename(pending, dir); err != nil {
		return nil, err
	}
	if err := m.registry.insert(rec); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	a := m.account(rec, dir)
	m.accounts = append(m.accounts, a)
	return a, nil
}

// fromTData turns a Telegram Desktop session into a gotd one. A session is the
// auth key and the data center it belongs to; everything else — the server
// salt, the configuration — the client learns again on connecting.
func fromTData(s tdata.TDataSession) (tdataSession, error) {
	key, err := hex.DecodeString(s.AuthKey)
	if err != nil || len(key) != 256 {
		return tdataSession{}, errors.New("account: tdata holds a malformed auth key")
	}
	addr, err := dcAddress(int(s.DC))
	if err != nil {
		return tdataSession{}, err
	}
	// The key id is the low 64 bits of the key's SHA-1 (MTProto 2.0).
	sum := sha1.Sum(key)
	keyID := sum[12:20]
	return tdataSession{
		record: record{ID: accountID(int64(s.UserID)), UserID: int64(s.UserID), DC: int(s.DC), KeyID: hex.EncodeToString(keyID)},
		data:   &session.Data{DC: int(s.DC), Addr: addr, AuthKey: key, AuthKeyID: keyID},
	}, nil
}

func authKeyID(key []byte) string {
	sum := sha1.Sum(key)
	return hex.EncodeToString(sum[12:20])
}

// dcAddress is the address of a production data center, from gotd's list.
func dcAddress(dc int) (string, error) {
	for _, option := range dcs.Prod().Options {
		if option.ID == dc && !option.Ipv6 && !option.MediaOnly && !option.CDN {
			return fmt.Sprintf("%s:%d", option.IPAddress, option.Port), nil
		}
	}
	return "", fmt.Errorf("account: unknown data center %d", dc)
}

// Run connects to the account and calls f with its API; the connection lasts
// until f returns. Only one Run of an account may be active at a time, in any
// process: another fails at once with ErrInUse.
func (m *Manager) Run(ctx context.Context, a *Account, f func(ctx context.Context, api *tg.Client) error) error {
	return m.RunClient(ctx, a, nil, func(ctx context.Context, c *telegram.Client) error { return f(ctx, c.API()) })
}

// RunClient keeps update handling and all DC pools on the same authorization.
// Middlewares see every request after the diagnostics.
func (m *Manager) RunClient(ctx context.Context, a *Account, handler telegram.UpdateHandler, f func(context.Context, *telegram.Client) error, middlewares ...telegram.Middleware) error {
	if !a.busy.CompareAndSwap(false, true) {
		return ErrInUse
	}
	defer a.busy.Store(false)

	unlock, err := lockFile(filepath.Join(m.lockDir, a.keyID+".lock"))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInUse, err)
	}
	defer unlock()

	identity := m.identity.withTimeZone(time.Now())
	resolver, _ := m.proxyState()
	client := telegram.NewClient(identity.APIID, identity.APIHash, telegram.Options{
		DC:             a.DC,
		SessionStorage: a.storage,
		Device:         identity.Device,
		Resolver:       resolver,
		UpdateHandler:  handler,
		Middlewares:    append(diagnostics.Middleware(a.ID, a.DC), middlewares...),
		OnTransfer:     dcpool.OnTransfer,
	})
	return client.Run(ctx, func(ctx context.Context) error {
		return f(ctx, client)
	})
}

// Add signs in a new account and lists it, with a card of its name.
func (m *Manager) Add(ctx context.Context, p Prompter) (*Account, error) {
	if m.security != nil && m.security.Disabling() {
		return nil, errors.New("account: local data migration is in progress")
	}
	if err := m.Load(); err != nil {
		return nil, err
	}
	pending, err := m.pendingDir(".signin-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(pending)
	storage := m.fileStorage(filepath.Join(pending, "session.json"))

	identity := m.identity.withTimeZone(time.Now())
	resolver, proxyChanged := m.proxyState()
	client := telegram.NewClient(identity.APIID, identity.APIHash, telegram.Options{
		SessionStorage: storage,
		Middlewares:    diagnostics.Middleware("sign-in", 0),
		Device:         identity.Device,
		Resolver:       resolver,
	})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	var codeAsked, proxyWasChanged atomic.Bool
	timer := time.AfterFunc(connectTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer timer.Stop()
	// A proxy chosen before the code was asked for is what the sign-in is to be
	// through: it starts again, as nothing was done that it would lose. The code
	// is the server's answer to the connection it was asked on.
	go func() {
		select {
		case <-proxyChanged:
			if !codeAsked.Load() {
				proxyWasChanged.Store(true)
				cancel()
			}
		case <-runCtx.Done():
		}
	}()
	// The phone number is asked for while the connection is made.
	early := askPhoneEarly(runCtx, p)
	var self *tg.User
	err = client.Run(runCtx, func(ctx context.Context) error {
		timer.Stop()
		if err := signIn(ctx, askingCode{gotdAuth{client.Auth(), client.API()}, func() { codeAsked.Store(true) }}, early); err != nil {
			return err
		}
		var err error
		if self, err = client.Self(ctx); err != nil {
			return err
		}
		if m.hasUser(self.ID) {
			// The account is here already under another key. Ending the new
			// authorization keeps a stray session out of the account's
			// device list; failing to is not worth more than a log line.
			if _, err := client.API().AuthLogOut(ctx); err != nil {
				log.Printf("account: end duplicate sign-in: %v", err)
			}
			return fmt.Errorf("%w: %s", ErrAccountExists, accountID(self.ID))
		}
		return nil
	})
	if proxyWasChanged.Load() && ctx.Err() == nil {
		return nil, ErrProxyChanged
	}
	if timedOut.Load() && ctx.Err() == nil {
		return nil, ErrNoConnection
	}
	if err != nil {
		return nil, err
	}
	if self == nil {
		return nil, errors.New("account: sign-in returned no user")
	}
	data, err := (&session.Loader{Storage: storage}).Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("account: load new session: %w", err)
	}
	keyID := hex.EncodeToString(data.AuthKeyID)
	if keyID == "" {
		keyID = authKeyID(data.AuthKey)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commitLocked(pending, record{ID: accountID(self.ID), UserID: self.ID, DC: data.DC, KeyID: keyID, Card: userCard(self)})
}

// LogOut ends the authorization of a on the server. It connects for that,
// so a must not be connected already.
func (m *Manager) LogOut(ctx context.Context, a *Account) error {
	return m.Run(ctx, a, func(ctx context.Context, api *tg.Client) error {
		_, err := api.AuthLogOut(ctx)
		return err
	})
}

// Remove deletes a from this computer: its entry in the registry, its
// session and its history. It does not end the authorization on the server
// (see LogOut). A connected account is ErrInUse.
func (m *Manager) Remove(a *Account) error {
	if !a.busy.CompareAndSwap(false, true) {
		return ErrInUse
	}
	defer a.busy.Store(false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.registry.remove(a.ID); err != nil {
		return err
	}
	for i, other := range m.accounts {
		if other == a {
			m.accounts = append(m.accounts[:i:i], m.accounts[i+1:]...)
			break
		}
	}
	os.Remove(filepath.Join(m.lockDir, a.keyID+".lock"))
	if err := os.RemoveAll(a.dir); err != nil {
		return fmt.Errorf("account %s: remove local data: %w", a.ID, err)
	}
	return nil
}
