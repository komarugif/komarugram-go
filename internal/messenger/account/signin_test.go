// SPDX-License-Identifier: Unlicense OR MIT

package account

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// fakeAuth is a server with one account: phone "+1", code "12345" and,
// if password is set, that 2FA password.
type fakeAuth struct {
	authorized bool
	password   string
	sendErr    error
	sent       []string // Phones the code was sent to.
	signIns    int

	// exports are the answers to exportLoginToken in turn, the last one
	// given again; a token of its own without them. An answer that is
	// AuthLoginTokenSuccess authorizes.
	exports []tokenAnswer
	// imported is the answer to importLoginToken, and dcs where it was
	// imported.
	imported tokenAnswer
	dcs      []int
	// acceptEvery tells, as the server does when the token is accepted,
	// after every token given.
	acceptEvery bool
	accepted    chan struct{}
}

type tokenAnswer struct {
	result tg.AuthLoginTokenClass
	err    error
}

// newToken is a token that expires in half a minute.
func newToken(token string) tokenAnswer {
	return tokenAnswer{result: &tg.AuthLoginToken{Token: []byte(token), Expires: int(time.Now().Add(30 * time.Second).Unix())}}
}

func (f *fakeAuth) ExportLoginToken(context.Context) (tg.AuthLoginTokenClass, error) {
	a := newToken("token")
	if len(f.exports) > 0 {
		a = f.exports[0]
		if len(f.exports) > 1 {
			f.exports = f.exports[1:]
		}
	}
	return f.answerToken(a)
}

func (f *fakeAuth) ImportLoginToken(_ context.Context, dc int, _ []byte) (tg.AuthLoginTokenClass, error) {
	f.dcs = append(f.dcs, dc)
	return f.answerToken(f.imported)
}

func (f *fakeAuth) answerToken(a tokenAnswer) (tg.AuthLoginTokenClass, error) {
	switch a.result.(type) {
	case *tg.AuthLoginTokenSuccess:
		f.authorized = true
	case *tg.AuthLoginToken:
		if f.acceptEvery {
			f.LoginTokenAccepted()
			select {
			case f.accepted <- struct{}{}:
			default:
			}
		}
	}
	return a.result, a.err
}

func (f *fakeAuth) LoginTokenAccepted() <-chan struct{} {
	if f.accepted == nil {
		f.accepted = make(chan struct{}, 1)
	}
	return f.accepted
}

// newQRServer is a server that accepts every QR code it gives, the first
// with the token {1, 2, 0xfb}, and answers with exports after it.
func newQRServer(exports ...tokenAnswer) *fakeAuth {
	first := newToken(string([]byte{1, 2, 0xfb}))
	return &fakeAuth{exports: append([]tokenAnswer{first}, exports...), acceptEvery: true}
}

var success = tokenAnswer{result: &tg.AuthLoginTokenSuccess{Authorization: &tg.AuthAuthorization{}}}

func (f *fakeAuth) Status(context.Context) (*auth.Status, error) {
	return &auth.Status{Authorized: f.authorized}, nil
}

func (f *fakeAuth) SendCode(_ context.Context, phone string, _ auth.SendCodeOptions) (tg.AuthSentCodeClass, error) {
	f.sent = append(f.sent, phone)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	if phone != "+1" {
		return nil, tgerr.New(400, "PHONE_NUMBER_INVALID")
	}
	return &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "hash"}, nil
}

func (f *fakeAuth) SignIn(_ context.Context, phone, code, hash string) (*tg.AuthAuthorization, error) {
	f.signIns++
	if hash != "hash" || phone != "+1" {
		return nil, errors.New("wrong request")
	}
	if code != "12345" {
		return nil, tgerr.New(400, "PHONE_CODE_INVALID")
	}
	if f.password != "" {
		return nil, auth.ErrPasswordAuthNeeded
	}
	f.authorized = true
	return &tg.AuthAuthorization{}, nil
}

func (f *fakeAuth) Password(_ context.Context, password string) (*tg.AuthAuthorization, error) {
	if password != f.password {
		return nil, auth.ErrPasswordInvalid
	}
	f.authorized = true
	return &tg.AuthAuthorization{}, nil
}

func (f *fakeAuth) PasswordHint(context.Context) (string, error) { return "the hint", nil }

// step is one answer of a scripted user: the text to type, or an error.
type step struct {
	text string
	err  error
}

// script is a Prompter that answers from lists and records what it was told.
type script struct {
	phones, codes, passwords []step
	// qrs are the QR codes shown in turn: each waits for links, then returns
	// err, or waits for the question to end if err is nil. Without them,
	// the user chooses the phone number at once.
	qrs      []qrStep
	links    []string // The QR codes shown.
	events   []string // "qr", "qr over", "phone", "code" and "password", in order.
	problems []error  // Problems shown, in order.
	info     CodeInfo
	hint     string
}

type qrStep struct {
	links int
	err   error
}

func (s *script) QR(ctx context.Context, link string, more <-chan string) error {
	s.events = append(s.events, "qr")
	defer func() { s.events = append(s.events, "qr over") }()
	if len(s.qrs) == 0 {
		return ErrUsePhone
	}
	q := s.qrs[0]
	s.qrs = s.qrs[1:]
	if link != "" {
		s.links = append(s.links, link)
		q.links--
	}
	for ; q.links > 0; q.links-- {
		select {
		case link := <-more:
			s.links = append(s.links, link)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if q.err != nil {
		return q.err
	}
	<-ctx.Done()
	return ctx.Err()
}

func pop(list *[]step) (string, error) {
	s := (*list)[0]
	*list = (*list)[1:]
	return s.text, s.err
}

func (s *script) Phone(_ context.Context, problem error) (string, error) {
	s.events = append(s.events, "phone")
	s.problems = append(s.problems, problem)
	return pop(&s.phones)
}

func (s *script) Code(_ context.Context, info CodeInfo, problem error) (string, error) {
	s.events = append(s.events, "code")
	s.info = info
	s.problems = append(s.problems, problem)
	return pop(&s.codes)
}

func (s *script) Password(_ context.Context, hint string, problem error) (string, error) {
	s.events = append(s.events, "password")
	s.hint = hint
	s.problems = append(s.problems, problem)
	return pop(&s.passwords)
}

func TestSignInAuthorizedAsksNothing(t *testing.T) {
	server := &fakeAuth{authorized: true}
	if err := signIn(context.Background(), server, &script{}); err != nil {
		t.Fatal(err)
	}
	if len(server.sent) != 0 {
		t.Errorf("code sent to %v for an authorized session", server.sent)
	}
}

func TestSignInPhoneAndCode(t *testing.T) {
	server := &fakeAuth{}
	p := &script{phones: []step{{text: "+1"}}, codes: []step{{text: "12345"}}}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	if !server.authorized {
		t.Error("not authorized")
	}
	if p.info != (CodeInfo{Kind: CodeApp, Length: 5}) {
		t.Errorf("code info %+v", p.info)
	}
}

func TestSignInWrongPhoneAndCodeAreAskedAgain(t *testing.T) {
	server := &fakeAuth{}
	p := &script{
		phones: []step{{text: "+2"}, {text: "+1"}},
		codes:  []step{{text: "00000"}, {text: "12345"}},
	}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	if !server.authorized {
		t.Error("not authorized")
	}
	// Phone, phone again after the invalid number, code, code again.
	if len(p.problems) != 4 || p.problems[0] != nil || !tgerr.Is(p.problems[1], "PHONE_NUMBER_INVALID") ||
		p.problems[2] != nil || !tgerr.Is(p.problems[3], "PHONE_CODE_INVALID") {
		t.Errorf("problems shown: %v", p.problems)
	}
	if len(server.sent) != 2 {
		t.Errorf("the code was sent %d times, want once per valid phone attempt (2 phones)", len(server.sent))
	}
}

func TestSignInPassword(t *testing.T) {
	server := &fakeAuth{password: "secret"}
	p := &script{
		phones:    []step{{text: "+1"}},
		codes:     []step{{text: "12345"}},
		passwords: []step{{text: "guess"}, {text: "secret"}},
	}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	if !server.authorized {
		t.Error("not authorized")
	}
	if p.hint != "the hint" {
		t.Errorf("hint %q", p.hint)
	}
	if last := p.problems[len(p.problems)-1]; !errors.Is(last, auth.ErrPasswordInvalid) {
		t.Errorf("second password prompt showed %v, want the invalid password", last)
	}
}

func TestSignInBackGoesToPhone(t *testing.T) {
	server := &fakeAuth{}
	p := &script{
		phones: []step{{text: "+1"}, {text: "+1"}},
		codes:  []step{{err: ErrBack}, {text: "12345"}},
	}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	if len(server.sent) != 2 {
		t.Errorf("code sent %d times, want a new one after going back", len(server.sent))
	}
	for _, problem := range p.problems {
		if problem != nil {
			t.Errorf("going back showed the problem %v", problem)
		}
	}
}

func TestSignInServerFailureEndsIt(t *testing.T) {
	boom := errors.New("network is down")
	server := &fakeAuth{sendErr: boom}
	p := &script{phones: []step{{text: "+1"}}}
	if err := signIn(context.Background(), server, p); !errors.Is(err, boom) {
		t.Errorf("error %v, want %v", err, boom)
	}
}

func TestSignInFloodWaitIsShown(t *testing.T) {
	server := &fakeAuth{}
	server.sendErr = tgerr.New(420, "FLOOD_WAIT_30")
	p := &script{phones: []step{{text: "+1"}, {err: context.Canceled}}}
	if err := signIn(context.Background(), server, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(p.problems) != 2 || p.problems[1] == nil {
		t.Errorf("problems %v, want the flood wait shown at the phone step", p.problems)
	}
}

func TestSignInContextEndsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &script{phones: []step{{err: ctx.Err()}}}
	if err := signIn(ctx, &fakeAuth{}, p); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want canceled", err)
	}
}

// askedScript is a script that tells when it is asked for the phone number.
type askedScript struct {
	*script
	asked chan struct{}
}

func (s *askedScript) Phone(ctx context.Context, problem error) (string, error) {
	select {
	case s.asked <- struct{}{}:
	default:
	}
	return s.script.Phone(ctx, problem)
}

// slowServer is a server that answers Status only once opened: the
// connection is still being made till then.
type slowServer struct {
	*fakeAuth
	open chan struct{}
}

func (s *slowServer) Status(ctx context.Context) (*auth.Status, error) {
	select {
	case <-s.open:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.fakeAuth.Status(ctx)
}

func TestEarlyPhoneIsAskedBeforeTheConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &askedScript{script: &script{phones: []step{{text: "+1"}}, codes: []step{{text: "12345"}}}, asked: make(chan struct{}, 1)}
	server := &slowServer{fakeAuth: &fakeAuth{}, open: make(chan struct{})}
	early := askEarly(ctx, p)

	// The connection is not there, and the number is asked for.
	select {
	case <-p.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the phone number is not asked for before the connection is made")
	}
	done := make(chan error, 1)
	go func() { done <- signIn(ctx, server, early) }()
	select {
	case err := <-done:
		t.Fatalf("signed in without a connection: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// Once it is, the answer is used, and the user is not asked again.
	close(server.open)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !server.authorized {
		t.Error("not authorized")
	}
	if len(p.problems) != 2 {
		t.Errorf("asked %d times, want the number once and the code once", len(p.problems))
	}
}

func TestEarlyPhoneIsAskedAgainAfterARefusal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &askedScript{script: &script{phones: []step{{text: "+2"}, {text: "+1"}}, codes: []step{{text: "12345"}}}, asked: make(chan struct{}, 2)}
	server := &fakeAuth{}
	if err := signIn(ctx, server, askEarly(ctx, p)); err != nil {
		t.Fatal(err)
	}
	if len(p.problems) != 3 || p.problems[0] != nil || !tgerr.Is(p.problems[1], "PHONE_NUMBER_INVALID") {
		t.Errorf("problems %v, want the refusal shown at the second asking of the number", p.problems)
	}
}

func TestEarlyPhoneEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &askedScript{script: &script{phones: []step{{err: context.Canceled}}}, asked: make(chan struct{}, 1)}
	early := askEarly(ctx, p)
	<-p.asked
	cancel()
	if err := early.QR(ctx, "", nil); !errors.Is(err, ErrUsePhone) {
		t.Fatalf("QR: %v, want the phone number chosen", err)
	}
	if _, err := early.Phone(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want canceled", err)
	}
}

// TestEarlyQRShowsTheLinks checks that the QR code shown before the
// connection is made gets the links of the sign-in, and that it is over
// when the sign-in ends it: the next question is not asked beside it.
func TestEarlyQRShowsTheLinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &script{qrs: []qrStep{{links: 2}}}
	early := askEarly(ctx, p)
	qrCtx, stop := context.WithCancel(ctx)
	more := make(chan string)
	done := make(chan error, 1)
	go func() { done <- early.QR(qrCtx, "tg://login?token=a", more) }()
	more <- "tg://login?token=b"
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("QR: %v, want canceled", err)
	}
	want := []string{"qr", "qr over"}
	if fmt.Sprint(p.events) != fmt.Sprint(want) {
		t.Errorf("events %v, want %v", p.events, want)
	}
	if fmt.Sprint(p.links) != "[tg://login?token=a tg://login?token=b]" {
		t.Errorf("links shown %v", p.links)
	}
}

func TestSignInQR(t *testing.T) {
	server := newQRServer(success)
	p := &script{qrs: []qrStep{{links: 1}}}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	if !server.authorized {
		t.Error("not authorized")
	}
	// The token in base64 for URLs, as Telegram Desktop writes it.
	if fmt.Sprint(p.links) != "[tg://login?token=AQL7]" {
		t.Errorf("links shown %v", p.links)
	}
	if len(server.sent) != 0 {
		t.Errorf("code sent to %v", server.sent)
	}
}

func TestSignInQRMovesToTheAccountsDC(t *testing.T) {
	server := newQRServer(tokenAnswer{result: &tg.AuthLoginTokenMigrateTo{DCID: 4, Token: []byte("t")}})
	server.imported = success
	p := &script{qrs: []qrStep{{links: 1}}}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	if !server.authorized || fmt.Sprint(server.dcs) != "[4]" {
		t.Errorf("authorized %v, imported on %v; want on DC 4", server.authorized, server.dcs)
	}
}

func TestSignInQRThenPassword(t *testing.T) {
	server := newQRServer(tokenAnswer{err: tgerr.New(401, "SESSION_PASSWORD_NEEDED")})
	server.password = "secret"
	told := 0
	p := &script{qrs: []qrStep{{links: 1}}, passwords: []step{{text: "secret"}}}
	if err := signIn(context.Background(), askingCode{server, func() { told++ }}, p); err != nil {
		t.Fatal(err)
	}
	if !server.authorized {
		t.Error("not authorized")
	}
	// The QR code is over before the password is asked for.
	want := []string{"qr", "qr over", "password"}
	if fmt.Sprint(p.events) != fmt.Sprint(want) {
		t.Errorf("events %v, want %v", p.events, want)
	}
	if told != 1 {
		t.Errorf("told %d times before the password, want once", told)
	}
}

func TestSignInBackFromPasswordGoesToQR(t *testing.T) {
	server := newQRServer(tokenAnswer{err: tgerr.New(401, "SESSION_PASSWORD_NEEDED")}, newToken("again"), success)
	server.password = "secret"
	p := &script{qrs: []qrStep{{links: 1}, {links: 1}}, passwords: []step{{err: ErrBack}}}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	want := []string{"qr", "qr over", "password", "qr", "qr over"}
	if fmt.Sprint(p.events) != fmt.Sprint(want) || !server.authorized {
		t.Errorf("events %v, authorized %v; want %v", p.events, server.authorized, want)
	}
}

func TestSignInPhoneAndQRBothWays(t *testing.T) {
	server := newQRServer(newToken("again"), success)
	p := &script{qrs: []qrStep{{err: ErrUsePhone}, {links: 1}}, phones: []step{{err: ErrUseQR}}}
	if err := signIn(context.Background(), server, p); err != nil {
		t.Fatal(err)
	}
	want := []string{"qr", "qr over", "phone", "qr", "qr over"}
	if fmt.Sprint(p.events) != fmt.Sprint(want) || !server.authorized {
		t.Errorf("events %v, authorized %v; want %v", p.events, server.authorized, want)
	}
}

func TestSignInQRServerFailureEndsIt(t *testing.T) {
	boom := errors.New("network is down")
	server := &fakeAuth{exports: []tokenAnswer{{err: boom}}}
	p := &script{qrs: []qrStep{{links: 1}}}
	if err := signIn(context.Background(), server, p); !errors.Is(err, boom) {
		t.Errorf("error %v, want %v", err, boom)
	}
}

func TestTokenLife(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, c := range []struct {
		expires int
		want    time.Duration
	}{
		{1030, 30 * time.Second},
		{1005, 5 * time.Second},
		// The clock is wrong: half a minute, not a new token every
		// moment nor one that expired long ago.
		{999, 30 * time.Second},
		{5000, 30 * time.Second},
	} {
		if got := tokenLife(c.expires, now); got != c.want {
			t.Errorf("token expiring at %d: shown for %v, want %v", c.expires, got, c.want)
		}
	}
}

func TestFileStorage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "session.json")
	s := &fileStorage{path: path}
	if _, err := s.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("load from nothing: %v, want ErrNotFound", err)
	}
	for _, data := range []string{"first", "second, longer than the first"} {
		if err := s.StoreSession(ctx, []byte(data)); err != nil {
			t.Fatal(err)
		}
		got, err := s.LoadSession(ctx)
		if err != nil || string(got) != data {
			t.Fatalf("load: %q, %v; want %q", got, err, data)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d files left in the session directory, want only the session", len(entries))
	}
}

// TestAskingCodeTellsBeforeTheCodeIsAskedFor checks the moment after which a
// change of proxy no longer starts the sign-in again: when the code is asked
// for, and not at the phone number, which loses nothing.
func TestAskingCodeTellsBeforeTheCodeIsAskedFor(t *testing.T) {
	server := &fakeAuth{}
	var told []int
	asked := askingCode{server, func() { told = append(told, len(server.sent)) }}
	user := &script{phones: []step{{text: "+1"}}, codes: []step{{text: "12345"}}}
	if err := signIn(context.Background(), asked, user); err != nil {
		t.Fatal(err)
	}
	// Told once, with the code not yet sent.
	if len(told) != 1 || told[0] != 0 || len(server.sent) != 1 {
		t.Errorf("told with %v codes sent beforehand, %d sent in all", told, len(server.sent))
	}
}
