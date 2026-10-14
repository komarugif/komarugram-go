// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// Telegram's limits on what the owner of an account may set. MaxBioLength is
// that of an account without Premium; with it, see Premium.Limit.
const (
	MaxNameLength     = 64
	MaxBioLength      = 70
	MinUsernameLength = 5
	MaxUsernameLength = 32
)

// Reasons a profile edit is refused, by the client or by Telegram.
var (
	ErrNameInvalid     = errors.New("profile: first name is empty or too long")
	ErrUsernameInvalid = errors.New("profile: username is not a valid one")
	ErrUsernameTaken   = errors.New("profile: username is taken")
	ErrBioTooLong      = errors.New("profile: bio is too long")
	ErrProfileOffline  = errors.New("profile: not connected")
)

// ProfileEdit is what the owner of an account can change about its profile.
type ProfileEdit struct {
	FirstName, LastName string
	// Username is without the "@"; empty removes it.
	Username string
	Bio      string
}

// ProfileEditor is a Store whose own profile can be edited.
type ProfileEditor interface {
	// EditProfile saves what of e differs from Me, and blocks until the
	// server has answered. It fails with one of the Err… values above when
	// the reason is the user's to fix.
	EditProfile(ctx context.Context, e ProfileEdit) error
}

// Validate checks e against Telegram's rules for an account without
// Premium, so that a mistake is shown before anything is sent.
func (e ProfileEdit) Validate() error {
	return e.ValidateWith(MaxBioLength)
}

// ValidateWith is Validate with bioLimit characters allowed in the bio.
func (e ProfileEdit) ValidateWith(bioLimit int) error {
	if strings.TrimSpace(e.FirstName) == "" || utf8.RuneCountInString(e.FirstName) > MaxNameLength || utf8.RuneCountInString(e.LastName) > MaxNameLength {
		return ErrNameInvalid
	}
	if utf8.RuneCountInString(e.Bio) > bioLimit {
		return ErrBioTooLong
	}
	if e.Username != "" && !ValidUsername(e.Username) {
		return ErrUsernameInvalid
	}
	return nil
}

// ValidUsername follows Telegram: 5–32 Latin letters, digits and
// underscores, starting with a letter and not ending with an underscore.
func ValidUsername(s string) bool {
	if len(s) < MinUsernameLength || len(s) > MaxUsernameLength {
		return false
	}
	for i, r := range s {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		switch {
		case i == 0 && !letter:
			return false
		case !letter && !(r >= '0' && r <= '9') && r != '_':
			return false
		}
	}
	return s[len(s)-1] != '_'
}
