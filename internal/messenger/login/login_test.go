// SPDX-License-Identifier: Unlicense OR MIT

package login

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/account"
)

// waitFor waits until the login is in a state that ok accepts.
func waitFor(t *testing.T, l *Login, what string, ok func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := l.State(); ok(s) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s, state %+v", what, l.State())
	return State{}
}

func atStep(step Step) func(State) bool {
	return func(s State) bool { return s.Step == step && !s.Busy }
}

func TestAnswersReachThePrompter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(nil)

	type result struct {
		phone, code, password string
		err                   error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		r.err = l.Run(ctx, func(ctx context.Context, p account.Prompter) error {
			var err error
			if r.phone, err = p.Phone(ctx, nil); err != nil {
				return err
			}
			if r.code, err = p.Code(ctx, account.CodeInfo{Kind: account.CodeSMS, Length: 5}, nil); err != nil {
				return err
			}
			if r.password, err = p.Password(ctx, "hint", nil); err != nil {
				return err
			}
			l.Finish()
			return nil
		})
		done <- r
	}()

	waitFor(t, l, "phone", atStep(StepPhone))
	l.Submit("+7 900")
	s := waitFor(t, l, "code", atStep(StepCode))
	if s.CodeLength != 5 || s.Delivery != account.CodeSMS {
		t.Errorf("code step %+v", s)
	}
	l.Submit("12345")
	s = waitFor(t, l, "password", atStep(StepPassword))
	if s.Hint != "hint" {
		t.Errorf("password step %+v", s)
	}
	l.Submit("secret")
	waitFor(t, l, "done", atStep(StepDone))

	r := <-done
	if r.err != nil || r.phone != "+7900" || r.code != "12345" || r.password != "secret" {
		t.Errorf("prompter got %+v", r)
	}
}

func TestBackAndProblems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(nil)
	errs := make(chan error, 1)
	go func() {
		errs <- l.Run(ctx, func(ctx context.Context, p account.Prompter) error {
			if _, err := p.Phone(ctx, nil); err != nil {
				return err
			}
			_, err := p.Code(ctx, account.CodeInfo{}, tgerr.New(400, "PHONE_CODE_INVALID"))
			if !errors.Is(err, account.ErrBack) {
				return errors.New("code step did not end with ErrBack")
			}
			_, err = p.Phone(ctx, nil)
			return err
		})
	}()

	waitFor(t, l, "phone", atStep(StepPhone))
	l.Submit("+1")
	s := waitFor(t, l, "code", atStep(StepCode))
	if !tgerr.Is(s.Problem, "PHONE_CODE_INVALID") {
		t.Errorf("problem %v", s.Problem)
	}
	l.Back()
	s = waitFor(t, l, "phone again", atStep(StepPhone))
	if s.Problem != nil {
		t.Errorf("phone step after back shows %v", s.Problem)
	}
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Errorf("Run: %v, want canceled", err)
	}
}

func TestSubmitOutOfTurnIsIgnored(t *testing.T) {
	l := New(nil)
	l.Submit("early") // Nothing is awaited yet.
	l.Back()
	if s := l.State(); s.Step != StepStarting || s.Busy {
		t.Errorf("state %+v after answers nobody asked for", s)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 1)
	go func() {
		text, _ := l.Phone(ctx, nil)
		got <- text
	}()
	waitFor(t, l, "phone", atStep(StepPhone))
	l.Back() // Not at the code step: no effect.
	l.Submit("1")
	l.Submit("2") // The question is answered already.
	if text := <-got; text != "+1" {
		t.Errorf("phone %q, want +1", text)
	}
}

func TestFailureAndRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(nil)
	attempts := 0
	done := make(chan error, 1)
	go func() {
		done <- l.Run(ctx, func(ctx context.Context, p account.Prompter) error {
			attempts++
			if attempts == 1 {
				return errors.New("no network")
			}
			l.Finish()
			return nil
		})
	}()

	s := waitFor(t, l, "failure", atStep(StepFailed))
	if s.Problem == nil || !strings.Contains(s.Problem.Error(), "no network") {
		t.Errorf("problem %v", s.Problem)
	}
	l.Retry()
	waitFor(t, l, "done", atStep(StepDone))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Errorf("%d attempts, want 2", attempts)
	}
	l.Retry() // Not failed: no effect.
	if l.State().Step != StepDone {
		t.Errorf("Retry left StepDone")
	}
}

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"+7 (900) 123-45-67": "+79001234567",
		"79001234567":        "+79001234567",
		"  +1 202 555 0100 ": "+12025550100",
		"":                   "",
		"abc":                "",
	} {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProtectionOfferWaitsForContinue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(nil)
	done := make(chan error, 1)
	go func() { done <- l.OfferProtection(ctx) }()
	waitFor(t, l, "offer", atStep(StepProtect))
	l.Submit("+1 000") // Not an answer to this step.
	select {
	case err := <-done:
		t.Fatalf("offer ended by a phone answer: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	l.Continue()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The offer to install ends with the installed program, or with nothing
// when declined; the other steps' answers do not end it.
func TestInstallOffer(t *testing.T) {
	for _, exe := range []string{`C:\Users\u\AppData\Local\Programs\KomaruGram\KomaruGram.exe`, ""} {
		l := New(nil)
		type result struct {
			exe string
			err error
		}
		done := make(chan result, 1)
		go func() {
			exe, err := l.OfferInstall(context.Background())
			done <- result{exe, err}
		}()
		waitFor(t, l, "offer", atStep(StepInstall))
		l.Submit("+1 000")
		select {
		case r := <-done:
			t.Fatalf("offer ended by a phone answer: %+v", r)
		case <-time.After(20 * time.Millisecond):
		}
		if exe != "" {
			l.Installed(exe)
		} else {
			l.Continue()
		}
		if r := <-done; r.err != nil || r.exe != exe {
			t.Fatalf("offer: %+v, want %q", r, exe)
		}
	}
}

// TestProxyChangeConnectsAgainAtOnce checks that a connection that was ended
// for a proxy chosen is made again without the user asking, and is not shown
// as a failure.
func TestProxyChangeConnectsAgainAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(nil)
	attempts := 0
	done := make(chan error, 1)
	go func() {
		done <- l.Run(ctx, func(ctx context.Context, p account.Prompter) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf("connecting: %w", account.ErrProxyChanged)
			}
			l.Finish()
			return nil
		})
	}()
	waitFor(t, l, "done", atStep(StepDone))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Errorf("%d attempts, want 2", attempts)
	}
}
