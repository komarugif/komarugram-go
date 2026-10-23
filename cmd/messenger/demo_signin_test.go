// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/login"
)

// waitStep waits until the sign-in waits for an answer at step.
func waitStep(t *testing.T, l *login.Login, step login.Step) login.State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := l.State(); s.Step == step && !s.Busy {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for step %d, state %+v", step, l.State())
	return login.State{}
}

// waitQR waits until the sign-in shows the demo's QR code.
func waitQR(t *testing.T, l *login.Login) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := l.State(); s.Step == login.StepQR && !s.Busy && s.QRLink == demoQRLink {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for the QR code, state %+v", l.State())
}

func TestDemoSignInGoesThroughEveryStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := login.New(nil)
	done := make(chan error, 1)
	go func() { done <- demoSignIn(ctx, l, 0) }()

	// The offers come first, and each waits to be answered.
	waitStep(t, l, login.StepInstall)
	l.Continue()
	waitStep(t, l, login.StepProtect)
	l.Continue()

	// The QR code comes first, and is given up for the number, and the
	// number for the QR code, and the code again for the number.
	waitQR(t, l)
	l.UsePhone()
	waitStep(t, l, login.StepPhone)
	l.UseQR()
	waitQR(t, l)
	l.UsePhone()

	// A number that is too short is refused.
	waitStep(t, l, login.StepPhone)
	l.Submit("+7 90")
	s := waitStep(t, l, login.StepPhone)
	if !tgerr.Is(s.Problem, "PHONE_NUMBER_INVALID") {
		t.Fatalf("short number: problem %v", s.Problem)
	}
	l.Submit("+7 900 123-45-67")

	// So is a wrong code, and the code can be given up for the number.
	s = waitStep(t, l, login.StepCode)
	if s.Phone != "+79001234567" || s.Delivery != account.CodeApp || s.CodeLength != len(demoCode) {
		t.Fatalf("code step %+v", s)
	}
	l.Submit("00000")
	s = waitStep(t, l, login.StepCode)
	if !tgerr.Is(s.Problem, "PHONE_CODE_INVALID") {
		t.Fatalf("wrong code: problem %v", s.Problem)
	}
	l.Back()
	waitStep(t, l, login.StepPhone)
	l.Submit("+7 900 123-45-67")
	waitStep(t, l, login.StepCode)
	l.Submit(demoCode)

	// The account has a cloud password, with its hint.
	s = waitStep(t, l, login.StepPassword)
	if s.Hint != demoPasswordHint {
		t.Fatalf("password step %+v", s)
	}
	l.Submit("wrong")
	s = waitStep(t, l, login.StepPassword)
	if !errors.Is(s.Problem, auth.ErrPasswordInvalid) {
		t.Fatalf("wrong password: problem %v", s.Problem)
	}
	l.Submit(demoPassword)

	waitStep(t, l, login.StepDone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDemoSignInEndsWithItsWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := login.New(nil)
	done := make(chan error, 1)
	go func() { done <- demoSignIn(ctx, l, 0) }()
	waitStep(t, l, login.StepInstall)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in goes on without its window")
	}
}

// TestDemoProtection checks that the demo's protection can be turned on and
// off, as the settings do, and leaves nothing behind.
func TestDemoProtection(t *testing.T) {
	manager, remove, err := demoProtection()
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	if manager.Enabled() {
		t.Fatal("protection is on before it is asked for")
	}
	if !manager.State().Available {
		t.Fatal("the password alone cannot protect the demo")
	}
	ctx := context.Background()
	if err := manager.Enable(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if s := manager.State(); !s.Enabled || !s.Unlocked {
		t.Fatalf("after Enable: %+v", s)
	}
	if err := manager.Disable(ctx, "secret"); err != nil {
		t.Fatal(err)
	}
	if manager.Enabled() {
		t.Fatal("protection stays on after Disable")
	}
}
