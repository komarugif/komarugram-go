// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"time"
)

// RegistrationPoint is a user id with when the user joined Telegram, to the
// second, as the service message "joined Telegram" of a private chat tells
// it: what
// cmd/registration-table smooths into RegisteredAround's table.
type RegistrationPoint struct {
	User int64
	Date time.Time
}

// RegistrationPointsHeader is the comment a file of points begins with.
const RegistrationPointsHeader = "# KomaruGram registration points: a user id and the Unix second Telegram told the user joined at, one a line."

// RegistrationPointsName is the name of the file of points collected at.
func RegistrationPointsName(at time.Time) string {
	return "registration-points-" + at.UTC().Format(time.DateOnly) + ".txt"
}

// WriteRegistrationPoints writes points, collected at, as
// cmd/registration-table reads them: one "id unix-seconds" a line in the
// order of the ids, after RegistrationPointsHeader and the time they were
// collected at.
func WriteRegistrationPoints(w io.Writer, points []RegistrationPoint, at time.Time) error {
	sorted := slices.Clone(points)
	slices.SortFunc(sorted, func(a, b RegistrationPoint) int { return cmp.Compare(a.User, b.User) })
	b := bufio.NewWriter(w)
	fmt.Fprintln(b, RegistrationPointsHeader)
	fmt.Fprintf(b, "# Collected at %s.\n", at.UTC().Format(time.RFC3339))
	for _, p := range sorted {
		fmt.Fprintf(b, "%d %d\n", p.User, p.Date.Unix())
	}
	return b.Flush()
}

// RegistrationPointsMin is how many new points there are to be for the
// client to ask the user to send them.
const RegistrationPointsMin = 6

// RegistrationPointsRecipient is the username of the channel whose direct
// messages points are sent to.
const RegistrationPointsRecipient = "komarugram_labs"

// RegistrationPointSource collects the points the account's chats tell,
// for the user to send them to the direct messages of the channel
// RegistrationPointsRecipient.
type RegistrationPointSource interface {
	// RegistrationPoints are the points of the chat list and the cached
	// histories not sent yet, and how many of them the user was not asked
	// about. Never is set once the user asked not to be asked again.
	RegistrationPoints(ctx context.Context) (points []RegistrationPoint, unasked int, never bool, err error)
	// SendRegistrationPoints sends points as a text file to the direct
	// messages of RegistrationPointsRecipient, and remembers them as sent.
	SendRegistrationPoints(ctx context.Context, points []RegistrationPoint) error
	// PostponeRegistrationPoints remembers points as asked about: the user
	// is asked again once there are more new ones. With never, the user is
	// not asked again.
	PostponeRegistrationPoints(ctx context.Context, points []RegistrationPoint, never bool) error
}
