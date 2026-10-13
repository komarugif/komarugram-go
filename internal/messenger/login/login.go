// SPDX-License-Identifier: Unlicense OR MIT

// Package login is the state of signing in as the interface sees it: which
// step the user is on, what went wrong with the last answer, and the answers
// going the other way. It has no widgets and no network of its own. The
// account manager asks it questions through account.Prompter, and the UI
// reads State and calls Submit, so the two run in separate goroutines
// without knowing about each other.
package login

import (
	"context"
	"strings"
	"sync"

	"komarugram/internal/messenger/account"
)

// Step is what the sign-in is doing or waiting for.
type Step int

const (
	// StepStarting is the connection being made and the saved session checked.
	StepStarting Step = iota
	// StepProtect offers to protect local data with the TPM and a master
	// password before an account is added; Continue goes on either way.
	StepProtect
	StepPhone
	StepCode
	StepPassword
	// StepDone means the account is signed in.
	StepDone
	// StepFailed is a failure that is not the user's to fix; Retry tries again.
	StepFailed
	// StepInstall offers to install the program into the system before
	// the first account is added: Installed tells it was, and the program
	// to start in place of this one, Continue that it was declined.
	StepInstall
)

// State is a snapshot of the sign-in for drawing.
type State struct {
	// Seq grows with every change of the step or new question, so that the
	// interface can tell a question asked again from the one it is showing.
	Seq  int
	Step Step
	// Busy is set from an answer being sent until the next step is ready.
	Busy bool
	// Problem is what was wrong with the previous answer or attempt; nil
	// if nothing. The interface words it.
	Problem error
	// Phone is the number the code was sent to, at StepCode.
	Phone string
	// Delivery says where the code went, at StepCode.
	Delivery account.CodeKind
	// CodeLength is the number of digits in the code, 0 if unknown.
	CodeLength int
	// Hint is the account's hint for its 2FA password, at StepPassword.
	Hint string
}

type answer struct {
	text string
	back bool
}

// Login is the state of one sign-in. It is safe for use from any goroutine.
type Login struct {
	changed func()

	mu      sync.Mutex
	state   State
	seq     int
	phone   string // The phone number last answered.
	waiting bool   // The sign-in is waiting for an answer.
	answers chan answer
	retry   chan struct{}
}

// New returns a Login at StepStarting that calls changed whenever its state
// changes, from any goroutine.
func New(changed func()) *Login {
	if changed == nil {
		changed = func() {}
	}
	return &Login{
		changed: changed,
		answers: make(chan answer, 1),
		retry:   make(chan struct{}, 1),
	}
}

// State returns the current state.
func (l *Login) State() State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// Submit answers the current question: the phone number, the code or the
// password. It does nothing when no answer is awaited, or one was given.
func (l *Login) Submit(text string) {
	l.give(answer{text: text}, StepPhone, StepCode, StepPassword)
}

// Continue leaves StepProtect, whether protection was enabled or not, or
// StepInstall, declining the installation.
func (l *Login) Continue() {
	l.give(answer{}, StepProtect, StepInstall)
}

// Installed leaves StepInstall with the program installed at exe.
func (l *Login) Installed(exe string) {
	l.give(answer{text: exe}, StepInstall)
}

// Back leaves the code or password step for the phone number.
func (l *Login) Back() {
	l.give(answer{back: true}, StepCode, StepPassword)
}

func (l *Login) give(a answer, steps ...Step) {
	l.mu.Lock()
	ok := l.waiting
	if ok {
		ok = false
		for _, s := range steps {
			ok = ok || l.state.Step == s
		}
	}
	if ok {
		l.waiting = false
		l.state.Busy = true
		l.state.Problem = nil
	}
	l.mu.Unlock()
	if ok {
		l.answers <- a // Never blocks: one answer per question.
		l.changed()
	}
}

// Retry starts over after StepFailed.
func (l *Login) Retry() {
	l.mu.Lock()
	failed := l.state.Step == StepFailed
	if failed {
		l.seq++
		l.state = State{Seq: l.seq, Step: StepStarting, Busy: true}
	}
	l.mu.Unlock()
	if failed {
		l.retry <- struct{}{}
		l.changed()
	}
}

// Finish records that the account is signed in.
func (l *Login) Finish() {
	l.set(State{Step: StepDone})
}

func (l *Login) set(s State) {
	l.mu.Lock()
	l.seq++
	s.Seq = l.seq
	l.state = s
	l.waiting = false
	l.mu.Unlock()
	l.changed()
}

// Run calls connect until it succeeds or ctx ends, passing the Login as the
// Prompter. A failure is shown as StepFailed, and connect runs again when the
// user asks for it with Retry.
func (l *Login) Run(ctx context.Context, connect func(ctx context.Context, p account.Prompter) error) error {
	for {
		l.set(State{Step: StepStarting})
		err := connect(ctx, l)
		if err == nil || ctx.Err() != nil {
			return err
		}
		l.set(State{Step: StepFailed, Problem: err})
		select {
		case <-l.retry:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ask shows s and waits for the answer to it.
func (l *Login) ask(ctx context.Context, s State) (answer, error) {
	l.mu.Lock()
	l.seq++
	s.Seq = l.seq
	l.state = s
	l.waiting = true
	l.mu.Unlock()
	l.changed()
	select {
	case a := <-l.answers:
		return a, nil
	case <-ctx.Done():
		return answer{}, ctx.Err()
	}
}

// OfferProtection shows StepProtect and waits until the user goes on.
func (l *Login) OfferProtection(ctx context.Context) error {
	_, err := l.ask(ctx, State{Step: StepProtect})
	return err
}

// OfferInstall shows StepInstall and waits until the user installs the
// program or declines: it returns the installed program, or "".
func (l *Login) OfferInstall(ctx context.Context) (string, error) {
	a, err := l.ask(ctx, State{Step: StepInstall})
	return a.text, err
}

// Phone implements account.Prompter.
func (l *Login) Phone(ctx context.Context, problem error) (string, error) {
	a, err := l.ask(ctx, State{Step: StepPhone, Problem: problem})
	phone := normalizePhone(a.text)
	l.mu.Lock()
	l.phone = phone
	l.mu.Unlock()
	return phone, err
}

// Code implements account.Prompter.
func (l *Login) Code(ctx context.Context, sent account.CodeInfo, problem error) (string, error) {
	l.mu.Lock()
	phone := l.phone
	l.mu.Unlock()
	a, err := l.ask(ctx, State{
		Step:       StepCode,
		Phone:      phone,
		Problem:    problem,
		Delivery:   sent.Kind,
		CodeLength: sent.Length,
	})
	if a.back {
		return "", account.ErrBack
	}
	return strings.TrimSpace(a.text), err
}

// Password implements account.Prompter.
func (l *Login) Password(ctx context.Context, hint string, problem error) (string, error) {
	a, err := l.ask(ctx, State{Step: StepPassword, Problem: problem, Hint: hint})
	if a.back {
		return "", account.ErrBack
	}
	return a.text, err
}

// normalizePhone keeps the digits of what the user typed and puts the plus
// in front, so that "+7 (900) 123-45-67" and "79001234567" are the same.
func normalizePhone(s string) string {
	var digits []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) == 0 {
		return ""
	}
	return "+" + string(digits)
}
