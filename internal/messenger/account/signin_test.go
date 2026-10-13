// SPDX-License-Identifier: Unlicense OR MIT

package account

import (
	"context"
	"errors"
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
}

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
	problems                 []error // Problems shown, in order.
	info                     CodeInfo
	hint                     string
}

func pop(list *[]step) (string, error) {
	s := (*list)[0]
	*list = (*list)[1:]
	return s.text, s.err
}

func (s *script) Phone(_ context.Context, problem error) (string, error) {
	s.problems = append(s.problems, problem)
	return pop(&s.phones)
}

func (s *script) Code(_ context.Context, info CodeInfo, problem error) (string, error) {
	s.info = info
	s.problems = append(s.problems, problem)
	return pop(&s.codes)
}

func (s *script) Password(_ context.Context, hint string, problem error) (string, error) {
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
	early := askPhoneEarly(ctx, p)

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
	if err := signIn(ctx, server, askPhoneEarly(ctx, p)); err != nil {
		t.Fatal(err)
	}
	if len(p.problems) != 3 || p.problems[0] != nil || !tgerr.Is(p.problems[1], "PHONE_NUMBER_INVALID") {
		t.Errorf("problems %v, want the refusal shown at the second asking of the number", p.problems)
	}
}

func TestEarlyPhoneEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &askedScript{script: &script{phones: []step{{err: context.Canceled}}}, asked: make(chan struct{}, 1)}
	early := askPhoneEarly(ctx, p)
	cancel()
	if _, err := early.Phone(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want canceled", err)
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
