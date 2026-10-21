// SPDX-License-Identifier: Unlicense OR MIT

package account

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
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

// ErrUsePhone is what Prompter.QR returns when the user would rather sign in
// with the phone number than scan the QR code.
var ErrUsePhone = errors.New("account: sign in with the phone number")

// ErrUseQR is what Prompter.Phone returns when the user would rather scan a
// QR code than give the phone number.
var ErrUseQR = errors.New("account: sign in with a QR code")

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
	// QR shows the QR code of link, a login token for a device where the
	// account is signed in to scan; link is "" while the token is being
	// made. Every link received from more replaces the one shown. It
	// returns when ctx ends, or with ErrUsePhone.
	QR(ctx context.Context, link string, more <-chan string) error
	// Phone may return ErrUseQR.
	Phone(ctx context.Context, problem error) (string, error)
	Code(ctx context.Context, sent CodeInfo, problem error) (string, error)
	Password(ctx context.Context, hint string, problem error) (string, error)
}

// early is a Prompter whose first question, for the QR code, was asked when
// it was made, not when signIn came to it. Connecting to Telegram takes
// seconds: the QR code waits for its token meanwhile, and a user who chooses
// the phone number is asked for it at once, so that one typed before the
// connection is made is taken as soon as it is there, and the user only
// waits for what is left.
type early struct {
	Prompter
	cancelQR context.CancelFunc
	// links passes the links signIn shows to the early question.
	links chan string
	qr    chan error
	phone chan phoneAnswer
	// qrTaken and phoneTaken are set once the early answers were taken;
	// usePhone, once the early QR code was given up for the phone number.
	// They are used by signIn alone, one call at a time.
	qrTaken, phoneTaken, usePhone bool
}

type phoneAnswer struct {
	phone string
	err   error
}

// askEarly shows p's QR code now, waiting for its token, and asks for the
// phone number as soon as the user chooses it. It returns the Prompter that
// gives these answers to the first QR and Phone. The questions end with ctx,
// whether they are taken or not.
func askEarly(ctx context.Context, p Prompter) *early {
	qrCtx, cancel := context.WithCancel(ctx)
	e := &early{
		Prompter: p,
		cancelQR: cancel,
		links:    make(chan string),
		qr:       make(chan error, 1),
		phone:    make(chan phoneAnswer, 1),
	}
	go func() {
		err := p.QR(qrCtx, "", e.links)
		e.qr <- err
		// A QR code given up as signIn ended it asks for nothing more.
		if errors.Is(err, ErrUsePhone) && qrCtx.Err() == nil {
			phone, err := p.Phone(ctx, nil)
			e.phone <- phoneAnswer{phone, err}
		}
	}()
	return e
}

// QR passes the links to the early question the first time, and returns its
// answer; it asks p after that.
func (e *early) QR(ctx context.Context, link string, more <-chan string) error {
	if e.qrTaken {
		return e.Prompter.QR(ctx, link, more)
	}
	e.qrTaken = true
	answered := func(err error) error {
		e.usePhone = errors.Is(err, ErrUsePhone)
		return err
	}
	ended := func() error {
		e.cancelQR()
		<-e.qr
		return ctx.Err()
	}
	if link != "" {
		select {
		case e.links <- link:
		case err := <-e.qr:
			return answered(err)
		case <-ctx.Done():
			return ended()
		}
	}
	for {
		select {
		case link := <-more:
			select {
			case e.links <- link:
			case err := <-e.qr:
				return answered(err)
			case <-ctx.Done():
				return ended()
			}
		case err := <-e.qr:
			return answered(err)
		case <-ctx.Done():
			return ended()
		}
	}
}

// Phone returns the early answer the first time after the early QR code was
// given up for it, and asks p otherwise: a number that was refused, or the
// user going back to it.
func (e *early) Phone(ctx context.Context, problem error) (string, error) {
	if !e.usePhone || e.phoneTaken {
		return e.Prompter.Phone(ctx, problem)
	}
	e.phoneTaken = true
	select {
	case a := <-e.phone:
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
	// ExportLoginToken asks for a login token to show as a QR code, or, once
	// one was accepted, for the authorization.
	ExportLoginToken(ctx context.Context) (tg.AuthLoginTokenClass, error)
	// ImportLoginToken moves the session to the data center dc, where the
	// account is, and imports there the token accepted.
	ImportLoginToken(ctx context.Context, dc int, token []byte) (tg.AuthLoginTokenClass, error)
	// LoginTokenAccepted receives when the server tells, with
	// updateLoginToken, that the token shown was accepted.
	LoginTokenAccepted() <-chan struct{}
}

// gotdAuth is authAPI on a gotd client.
type gotdAuth struct {
	*auth.Client
	api     *tg.Client
	appID   int
	appHash string
	migrate func(ctx context.Context, dc int) error
	// accepted is filled by an update handler (onLoginToken).
	accepted <-chan struct{}
}

func (g gotdAuth) PasswordHint(ctx context.Context) (string, error) {
	settings, err := g.api.AccountGetPassword(ctx)
	if err != nil {
		return "", err
	}
	return settings.Hint, nil
}

func (g gotdAuth) ExportLoginToken(ctx context.Context) (tg.AuthLoginTokenClass, error) {
	return g.api.AuthExportLoginToken(ctx, &tg.AuthExportLoginTokenRequest{APIID: g.appID, APIHash: g.appHash})
}

func (g gotdAuth) ImportLoginToken(ctx context.Context, dc int, token []byte) (tg.AuthLoginTokenClass, error) {
	if err := g.migrate(ctx, dc); err != nil {
		return nil, fmt.Errorf("account: move to data center %d: %w", dc, err)
	}
	return g.api.AuthImportLoginToken(ctx, token)
}

func (g gotdAuth) LoginTokenAccepted() <-chan struct{} { return g.accepted }

// onLoginToken is an update handler that tells of updateLoginToken on the
// channel it returns. The channel keeps one signal for a reader that is busy
// when it comes, as the next token is being asked for.
func onLoginToken() (telegram.UpdateHandler, <-chan struct{}) {
	accepted := make(chan struct{}, 1)
	d := tg.NewUpdateDispatcher()
	d.OnLoginToken(func(context.Context, tg.Entities, *tg.UpdateLoginToken) error {
		select {
		case accepted <- struct{}{}:
		default:
		}
		return nil
	})
	return d, accepted
}

// signIn authorizes the client if its session is not authorized yet: it
// shows QR codes to scan with a device where the account is signed in, as
// Telegram Desktop does, or, if the user chooses it, asks for the phone
// number and the code; then, if the account has one, for the 2FA password.
// A wrong answer is asked for again. Errors that are not the user's to fix —
// the network, the server — end the sign-in.
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
	usePhone := false
	for {
		if !usePhone {
			err := signInWithQR(ctx, client, p)
			switch {
			case err == nil:
				return nil
			case errors.Is(err, ErrUsePhone):
				usePhone = true
			case errors.Is(err, ErrBack):
				// Back from the password: a new QR code.
			default:
				return err
			}
			continue
		}
		phone, err := p.Phone(ctx, problem)
		problem = nil
		if errors.Is(err, ErrUseQR) {
			usePhone = false
			continue
		}
		if err != nil {
			return err
		}
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

// qrQuestion is the QR code being shown: Prompter.QR, run until it is
// answered or stopped.
type qrQuestion struct {
	cancel   context.CancelFunc
	links    chan string
	answered chan error
	// over is set once the answer was received.
	over bool
}

func askQR(ctx context.Context, p Prompter) *qrQuestion {
	ctx, cancel := context.WithCancel(ctx)
	q := &qrQuestion{cancel: cancel, links: make(chan string), answered: make(chan error, 1)}
	go func() { q.answered <- p.QR(ctx, "", q.links) }()
	return q
}

// show replaces the QR code with link. It returns the answer if the
// question ended first.
func (q *qrQuestion) show(ctx context.Context, link string) error {
	select {
	case q.links <- link:
		return nil
	case err := <-q.answered:
		q.over = true
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stop ends the question and waits for it to be over, for the next one.
func (q *qrQuestion) stop() {
	if q.over {
		return
	}
	q.cancel()
	<-q.answered
	q.over = true
}

// tokenLife is how long the QR code of a token that expires at expires is
// shown before a new token is asked for. The server gives tokens about half
// a minute; a time much off that is the computer's clock being wrong, and
// half a minute is what is taken then, not a new token every moment nor a
// code that expired long ago.
func tokenLife(expires int, now time.Time) time.Duration {
	life := time.Unix(int64(expires), 0).Sub(now)
	if life < time.Second || life > 2*time.Minute {
		return 30 * time.Second
	}
	return life
}

// signInWithQR shows QR codes of login tokens until one is accepted on a
// device where the account is signed in (https://core.telegram.org/api/qr-login),
// as Telegram Desktop does (intro_qr.cpp): a token lasts about half a minute
// and is replaced by a new one when it expires; when it is accepted, the
// server sends updateLoginToken, and exporting the token again gives the
// authorization, or the data center of the account to import it on. An
// account with a 2FA password asks for it then. It returns ErrUsePhone when
// the user would rather sign in with the phone number.
func signInWithQR(ctx context.Context, client authAPI, p Prompter) error {
	q := askQR(ctx, p)
	defer q.stop()
	result, err := client.ExportLoginToken(ctx)
	for {
		if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
			q.stop()
			return signInWithPassword(ctx, client, p)
		}
		if err != nil {
			return err
		}
		switch t := result.(type) {
		case *tg.AuthLoginTokenSuccess:
			return nil
		case *tg.AuthLoginTokenMigrateTo:
			result, err = client.ImportLoginToken(ctx, t.DCID, t.Token)
			continue
		case *tg.AuthLoginToken:
			if err := q.show(ctx, "tg://login?token="+base64.URLEncoding.EncodeToString(t.Token)); err != nil {
				return err
			}
			timer := time.NewTimer(tokenLife(t.Expires, time.Now()))
			select {
			case err := <-q.answered:
				timer.Stop()
				q.over = true
				return err
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-client.LoginTokenAccepted():
				timer.Stop()
			case <-timer.C:
			}
			result, err = client.ExportLoginToken(ctx)
		default:
			return fmt.Errorf("account: unexpected answer to exportLoginToken: %T", result)
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
		if err := historycache.ProtectFile(account.HistoryPath(), account.MediaPath(), account.ID, m.security); err != nil {
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

// askingCode is an authAPI that tells when the code is about to be asked
// for, or the 2FA password after a QR code was accepted: from then on the
// sign-in has something to lose.
type askingCode struct {
	authAPI
	asking func()
}

func (a askingCode) SendCode(ctx context.Context, phone string, options auth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
	a.asking()
	return a.authAPI.SendCode(ctx, phone, options)
}

func (a askingCode) ImportLoginToken(ctx context.Context, dc int, token []byte) (tg.AuthLoginTokenClass, error) {
	a.asking()
	return a.authAPI.ImportLoginToken(ctx, dc, token)
}

func (a askingCode) PasswordHint(ctx context.Context) (string, error) {
	a.asking()
	return a.authAPI.PasswordHint(ctx)
}
