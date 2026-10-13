// SPDX-License-Identifier: Unlicense OR MIT

package account

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/historycache"
	"komarugram/internal/messenger/securedb"
	"komarugram/internal/messenger/security"
)

// ErrBack is what a Prompter returns to leave the code or password step for
// the phone number step.
var ErrBack = errors.New("account: back to the phone number")

// ErrNoAccount means the phone number has no Telegram account. Registering
// one is not supported.
var ErrNoAccount = errors.New("account: no Telegram account for this number")

// ErrCodeUnsupported means the server wants the code delivered in a way this
// client cannot ask for, such as through an e-mail that is yet to be set up.
var ErrCodeUnsupported = errors.New("account: this way of sending the code is not supported")

// ErrNoConnection means Telegram did not answer in time.
var ErrNoConnection = errors.New("account: no answer from Telegram")

// connectTimeout is how long the first request to Telegram may take.
const connectTimeout = 25 * time.Second

// CodeKind is how the server delivered the login code.
type CodeKind int

const (
	CodeOther CodeKind = iota
	// CodeApp is a message in Telegram on another of the user's devices.
	CodeApp
	CodeSMS
	CodeCall
)

// CodeInfo describes a login code that has just been sent.
type CodeInfo struct {
	Kind CodeKind
	// Length is the number of digits, 0 if the server did not say.
	Length int
}

// Prompter asks the user for what signing in needs. Every method may be
// called again after a wrong answer, with problem set to what was wrong; it
// returns as soon as ctx ends. Code and Password may return ErrBack.
type Prompter interface {
	Phone(ctx context.Context, problem error) (string, error)
	Code(ctx context.Context, sent CodeInfo, problem error) (string, error)
	Password(ctx context.Context, hint string, problem error) (string, error)
}

// earlyPhone is a Prompter whose first question, for the phone number, was
// asked when it was made, not when signIn came to it. Connecting to Telegram
// takes seconds, and the user can type the number meanwhile: if they are
// done before the connection is, signIn takes the answer as soon as it is
// there, and the user only waits for what is left.
type earlyPhone struct {
	Prompter
	answer chan phoneAnswer
	// asked is set once the early answer was taken. Phone is called by
	// signIn alone, one call at a time.
	asked bool
}

type phoneAnswer struct {
	phone string
	err   error
}

// askPhoneEarly asks p for the phone number now, and returns the Prompter
// that gives that answer to the first Phone. The question ends with ctx,
// whether it is taken or not.
func askPhoneEarly(ctx context.Context, p Prompter) *earlyPhone {
	e := &earlyPhone{Prompter: p, answer: make(chan phoneAnswer, 1)}
	go func() {
		phone, err := p.Phone(ctx, nil)
		e.answer <- phoneAnswer{phone, err}
	}()
	return e
}

// Phone returns the early answer the first time, and asks p after that: a
// number that was refused, or the user going back to it.
func (e *earlyPhone) Phone(ctx context.Context, problem error) (string, error) {
	if e.asked {
		return e.Prompter.Phone(ctx, problem)
	}
	e.asked = true
	select {
	case a := <-e.answer:
		return a.phone, a.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// authAPI is the part of the gotd client that signing in uses.
type authAPI interface {
	Status(ctx context.Context) (*auth.Status, error)
	SendCode(ctx context.Context, phone string, options auth.SendCodeOptions) (tg.AuthSentCodeClass, error)
	SignIn(ctx context.Context, phone, code, codeHash string) (*tg.AuthAuthorization, error)
	Password(ctx context.Context, password string) (*tg.AuthAuthorization, error)
	// PasswordHint returns the hint the account gave for its 2FA password.
	PasswordHint(ctx context.Context) (string, error)
}

// gotdAuth is authAPI on a gotd client.
type gotdAuth struct {
	*auth.Client
	api *tg.Client
}

func (g gotdAuth) PasswordHint(ctx context.Context) (string, error) {
	settings, err := g.api.AccountGetPassword(ctx)
	if err != nil {
		return "", err
	}
	return settings.Hint, nil
}

// signIn authorizes the client if its session is not authorized yet: it asks
// for the phone number, the code and, if the account has one, the 2FA
// password. A wrong answer is asked for again. Errors that are not the
// user's to fix — the network, the server — end the sign-in.
func signIn(ctx context.Context, client authAPI, p Prompter) error {
	statusCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	status, err := client.Status(statusCtx)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return ErrNoConnection
	}
	if err != nil {
		return fmt.Errorf("account: authorization status: %w", err)
	}
	if status.Authorized {
		return nil
	}

	var problem error
	for {
		phone, err := p.Phone(ctx, problem)
		if err != nil {
			return err
		}
		problem = nil
		err = signInWithPhone(ctx, client, p, phone)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, ErrBack):
		case userError(err):
			problem = err
		default:
			return err
		}
	}
}

// signInWithPhone sends the code to the phone and checks what the user types.
func signInWithPhone(ctx context.Context, client authAPI, p Prompter, phone string) error {
	sentCode, err := client.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return err
	}
	var sent *tg.AuthSentCode
	switch s := sentCode.(type) {
	case *tg.AuthSentCode:
		sent = s
	case *tg.AuthSentCodeSuccess:
		// The server took the device as already verified.
		if _, ok := s.Authorization.(*tg.AuthAuthorization); ok {
			return nil
		}
		return ErrNoAccount
	default:
		return fmt.Errorf("account: unexpected answer to sendCode: %T", sentCode)
	}
	info, err := codeInfo(sent)
	if err != nil {
		return err
	}

	var problem error
	for {
		code, err := p.Code(ctx, info, problem)
		if err != nil {
			return err
		}
		problem = nil
		_, err = client.SignIn(ctx, phone, code, sent.PhoneCodeHash)
		var signUp *auth.SignUpRequired
		switch {
		case err == nil:
			return nil
		case errors.Is(err, auth.ErrPasswordAuthNeeded):
			return signInWithPassword(ctx, client, p)
		case errors.As(err, &signUp):
			return ErrNoAccount
		case tgerr.Is(err, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"):
			problem = err
		default:
			return err
		}
	}
}

func signInWithPassword(ctx context.Context, client authAPI, p Prompter) error {
	hint, err := client.PasswordHint(ctx)
	if err != nil {
		return fmt.Errorf("account: password settings: %w", err)
	}
	var problem error
	for {
		password, err := p.Password(ctx, hint, problem)
		if err != nil {
			return err
		}
		problem = nil
		_, err = client.Password(ctx, password)
		switch {
		case err == nil:
			return nil
		case userError(err):
			problem = err
		default:
			return err
		}
	}
}

// codeInfo tells how the code was delivered.
func codeInfo(s *tg.AuthSentCode) (CodeInfo, error) {
	switch t := s.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		return CodeInfo{Kind: CodeApp, Length: t.Length}, nil
	case *tg.AuthSentCodeTypeSMS:
		return CodeInfo{Kind: CodeSMS, Length: t.Length}, nil
	case *tg.AuthSentCodeTypeCall:
		return CodeInfo{Kind: CodeCall, Length: t.Length}, nil
	case *tg.AuthSentCodeTypeSetUpEmailRequired:
		return CodeInfo{}, ErrCodeUnsupported
	}
	return CodeInfo{}, nil
}

// userError reports whether err is about what the user typed, or about
// trying too often: something to show and ask again, not to give up on.
func userError(err error) bool {
	if errors.Is(err, ErrNoAccount) || errors.Is(err, ErrCodeUnsupported) || errors.Is(err, auth.ErrPasswordInvalid) {
		return true
	}
	if _, ok := tgerr.AsFloodWait(err); ok {
		return true
	}
	return tgerr.Is(err,
		"PHONE_NUMBER_INVALID", "PHONE_NUMBER_BANNED", "PHONE_NUMBER_FLOOD",
		"PHONE_CODE_EXPIRED", "PHONE_CODE_INVALID", "PHONE_PASSWORD_FLOOD",
		"API_ID_INVALID", "API_ID_PUBLISHED_FLOOD",
	)
}

// fileStorage keeps a gotd session in a file. The auth key in it is as good
// as the account, so the file is private to the user and is replaced whole:
// a crash while saving leaves the old session, not half of a new one.
type fileStorage struct {
	path       string
	protection *security.Manager
	mu         sync.Mutex
}

var sessionAssociatedData = []byte("komarugram-go/telegram-session/v1")

func (f *fileStorage) LoadSession(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if f.protection == nil || !f.protection.Enabled() {
		return data, nil
	}
	if f.protection.Disabling() && !security.IsEnvelope(data) {
		return data, nil
	}
	return f.protection.Decrypt(data, sessionAssociatedData)
}

func (f *fileStorage) StoreSession(_ context.Context, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.protection != nil && f.protection.Enabled() && !f.protection.Disabling() {
		var err error
		data, err = f.protection.Encrypt(data, sessionAssociatedData)
		if err != nil {
			return err
		}
	}
	return f.writeLocked(data)
}

func (f *fileStorage) writeLocked(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), "session-*.tmp")
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
		err = os.Rename(tmp.Name(), f.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// protect rewrites a legacy plaintext session as a verified AEAD envelope.
// The mutex serializes migration with gotd writes already in flight.
func (f *fileStorage) protect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if security.IsEnvelope(data) {
		_, err := f.protection.Decrypt(data, sessionAssociatedData)
		return err
	}
	encrypted, err := f.protection.Encrypt(data, sessionAssociatedData)
	if err != nil {
		return err
	}
	return f.writeLocked(encrypted)
}

func (f *fileStorage) unprotect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !security.IsEnvelope(data) {
		return nil
	}
	plain, err := f.protection.Decrypt(data, sessionAssociatedData)
	if err != nil {
		return err
	}
	defer clear(plain)
	return f.writeLocked(plain)
}

func (m *Manager) fileStorage(path string) *fileStorage {
	return &fileStorage{path: path, protection: m.security}
}

// protectSessions is the migration of local-data protection: it runs when
// protection is enabled, and again on every unlock to finish one that was
// interrupted. The key is available by then, so the registry is moved into
// its encrypted form first and read from it, then every session file is
// rewritten as an envelope and every plaintext history cache dropped.
func (m *Manager) protectSessions() error {
	if err := m.registry.protect(); err != nil {
		return err
	}
	if err := m.Load(); err != nil {
		return err
	}
	for _, account := range m.Accounts() {
		if err := historycache.ProtectFile(account.HistoryPath(), account.ID, m.security); err != nil {
			return err
		}
		if err := account.storage.protect(); err != nil {
			return fmt.Errorf("account %s: %w", account.ID, err)
		}
	}
	return nil
}

// unprotectSessions completes the reverse transition before security.json is
// removed. Completed plaintext files are authoritative on retry.
func (m *Manager) unprotectSessions() error {
	if err := m.registry.unprotect(); err != nil {
		return err
	}
	if err := m.Load(); err != nil {
		return err
	}
	for _, account := range m.Accounts() {
		if err := account.storage.unprotect(); err != nil {
			return fmt.Errorf("account %s: %w", account.ID, err)
		}
		path := account.HistoryPath()
		plain := path + ".plain"
		if _, err := os.Stat(path + ".secure"); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := os.Stat(plain); errors.Is(err, os.ErrNotExist) {
			db, err := securedb.Open(m.security, path+".secure", account.ID)
			if err != nil {
				return err
			}
			err = securedb.CopyPlain(db, plain)
			if closeErr := db.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return fmt.Errorf("account %s: history: %w", account.ID, err)
			}
		} else if err != nil {
			return err
		}
	}
	for _, account := range m.Accounts() {
		if err := removeDatabase(account.HistoryPath() + ".secure"); err != nil {
			return err
		}
	}
	return removeDatabase(m.registry.securePath())
}

// askingCode is an authAPI that tells when the code is about to be asked for.
type askingCode struct {
	authAPI
	asking func()
}

func (a askingCode) SendCode(ctx context.Context, phone string, options auth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
	a.asking()
	return a.authAPI.SendCode(ctx, phone, options)
}
