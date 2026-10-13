// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/security"
)

// What -demo-signin asks for, and answers to: the code is not sent anywhere,
// and the account has a cloud password.
const (
	demoCode         = "12345"
	demoPassword     = "demo"
	demoPasswordHint = "demo"
	// demoMinDigits is how few digits a phone number may have.
	demoMinDigits = 7
	// demoPause is how long the demo takes to answer, as Telegram does.
	demoPause = 700 * time.Millisecond
)

// demoSignIn goes through everything that comes before an account is added,
// as the real sign-in does: the offer to install the program, the offer to
// protect local data, the phone number, the code and the cloud password. It
// answers on its own, with the wrong ones too: a number that is too short, a
// code other than demoCode and a password other than demoPassword are refused
// as Telegram refuses them. Nothing is installed, saved or sent anywhere.
// It returns when the sign-in is done, or ctx ends.
func demoSignIn(ctx context.Context, l *login.Login, pause time.Duration) error {
	return l.Run(ctx, func(ctx context.Context, p account.Prompter) error {
		// The connection is made.
		if err := sleep(ctx, pause); err != nil {
			return err
		}
		if _, err := l.OfferInstall(ctx); err != nil {
			return err
		}
		if err := l.OfferProtection(ctx); err != nil {
			return err
		}
		var problem error
		for {
			phone, err := p.Phone(ctx, problem)
			if err != nil {
				return err
			}
			problem = nil
			if err := sleep(ctx, pause); err != nil {
				return err
			}
			// The phone is the plus and its digits.
			if len(phone)-1 < demoMinDigits {
				problem = tgerr.New(400, "PHONE_NUMBER_INVALID")
				continue
			}
			err = demoCheckCode(ctx, p, pause)
			switch {
			case err == nil:
				l.Finish()
				return nil
			case errors.Is(err, account.ErrBack):
			default:
				return err
			}
		}
	})
}

// demoCheckCode asks for the code and then for the password, until they are
// right. It returns account.ErrBack when the user goes back to the number.
func demoCheckCode(ctx context.Context, p account.Prompter, pause time.Duration) error {
	var problem error
	for {
		code, err := p.Code(ctx, account.CodeInfo{Kind: account.CodeApp, Length: len(demoCode)}, problem)
		if err != nil {
			return err
		}
		if err := sleep(ctx, pause); err != nil {
			return err
		}
		if code == demoCode {
			break
		}
		problem = tgerr.New(400, "PHONE_CODE_INVALID")
	}
	problem = nil
	for {
		password, err := p.Password(ctx, demoPasswordHint, problem)
		if err != nil {
			return err
		}
		if err := sleep(ctx, pause); err != nil {
			return err
		}
		if password == demoPassword {
			return nil
		}
		problem = auth.ErrPasswordInvalid
	}
}

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// demoProtection is the local data protection of the demo's sign-in, which
// the sign-in offers and the settings show. It is kept in a directory of its
// own, removed with the returned function, and by the password alone: the
// user's own protection is not touched, and the demo has no data to
// migrate.
func demoProtection() (*security.Manager, func(), error) {
	dir, err := os.MkdirTemp("", "komarugram-demo-")
	if err != nil {
		return nil, nil, err
	}
	remove := func() { os.RemoveAll(dir) }
	manager, err := security.OpenPath(filepath.Join(dir, "security.json"), nil)
	if err != nil {
		remove()
		return nil, nil, err
	}
	manager.SetMigration(func() error { return nil })
	manager.SetUnmigration(func() error { return nil })
	return manager, remove, nil
}
