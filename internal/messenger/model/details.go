// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"time"
)

// ChatDetails are facts about a chat that its info shows, as materialgram's
// profile does.
type ChatDetails struct {
	// PhotoDC is the data center that keeps the chat's photo, 0 without a
	// photo.
	PhotoDC int
}

// ChatDetailer tells the details of chats.
type ChatDetailer interface {
	// ChatDetails are what is known of chat without asking Telegram.
	ChatDetails(chat int64) ChatDetails
	// Online is how many members of a group are online now.
	Online(ctx context.Context, chat int64) (int, error)
}

// Registration tells how RegisteredAround's date relates to the account's
// registration.
type Registration int

const (
	// RegisteredAbout: the account was registered around the date.
	RegisteredAbout Registration = iota
	// RegisteredBefore: before the date, older than every known account.
	RegisteredBefore
	// RegisteredAfter: after the date, newer than every known account.
	RegisteredAfter
)

// registration is a point of the table of registrations: a user id, the
// Unix second its account was registered at, smoothed, and how many days the
// accounts around it lie from that, as a median.
type registration struct {
	id, date, spread int64
}

// RegisteredAround estimates when the account of user was registered, between
// the dates of the known ids around it (cmd/registration-table), and how far
// from it the account's date may be: the median distance of the known
// accounts around it, or more between known ids far apart in time.
func RegisteredAround(user int64) (time.Time, Registration, time.Duration) {
	first, last := registrations[0], registrations[len(registrations)-1]
	switch {
	case user < first.id:
		return time.Unix(first.date, 0), RegisteredBefore, 0
	case user > last.id:
		return time.Unix(last.date, 0), RegisteredAfter, 0
	}
	for i := 1; i < len(registrations); i++ {
		a, b := registrations[i-1], registrations[i]
		if user <= b.id {
			t := float64(user-a.id) / float64(b.id-a.id)
			spread := float64(a.spread) + t*float64(b.spread-a.spread)
			// Between points far apart the line is a guess: a third of the
			// time between them is as near as it is likely to be.
			spread = max(spread, float64(b.date-a.date)/(3*24*60*60))
			return time.Unix(a.date+int64(t*float64(b.date-a.date)), 0), RegisteredAbout, time.Duration(spread * float64(24*time.Hour))
		}
	}
	return time.Unix(last.date, 0), RegisteredAfter, 0
}

// DataCenterName is where Telegram's data center dc is.
func DataCenterName(dc int) string {
	switch dc {
	case 1, 3:
		return "Miami"
	case 2, 4:
		return "Amsterdam"
	case 5:
		return "Singapore"
	}
	return ""
}
