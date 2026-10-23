// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"strconv"
	"time"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// calmKey keeps in a frame's Values what of others it hides, a calm.
const calmKey = "komarugram/ui.calm"

// calm is what of others a frame hides (preferences.Calm), as bits: a
// value below 256 is put in Values without an allocation.
type calm uint8

const (
	// calmOnline hides who is online and when others were last.
	calmOnline calm = 1 << iota
	// calmRead hides the checks of messages delivered and read; the clock
	// of one on its way stays.
	calmRead
)

// withCalm makes c what the frame of gtx hides.
func withCalm(gtx layout.Context, c preferences.Calm) {
	if gtx.Values == nil {
		return
	}
	var bits calm
	if c.HidesOnline() {
		bits |= calmOnline
	}
	if c.HidesRead() {
		bits |= calmRead
	}
	gtx.Values[calmKey] = bits
}

// calmOf is what the frame hides.
func calmOf(gtx layout.Context) calm {
	c, _ := gtx.Values[calmKey].(calm)
	return c
}

// presenceText is the line under a user's name that tells when they were
// online, as Telegram Desktop's OnlineText; full is its OnlineTextFull,
// which the profile shows, with a date's time too and no "minutes ago".
func presenceText(p model.Presence, now time.Time, full bool, l localization.Catalog) string {
	switch p.Seen {
	case model.SeenRecently, model.SeenUnknown:
		return l.T("status.recently")
	case model.SeenLastWeek:
		return l.T("status.last_week")
	case model.SeenLastMonth:
		return l.T("status.last_month")
	case model.SeenLongAgo:
		return l.T("status.long_ago")
	}
	if p.Online(now) {
		return l.T("status.user_online")
	}
	ago := now.Sub(p.At)
	if !full {
		switch minutes := int(ago / time.Minute); {
		case minutes <= 0:
			return l.T("status.lastseen_now")
		case minutes < 60:
			return l.Count("status.lastseen_minutes", minutes, map[string]string{"count": strconv.Itoa(minutes)})
		case ago < 12*time.Hour:
			hours := int(ago / time.Hour)
			return l.Count("status.lastseen_hours", hours, map[string]string{"count": strconv.Itoa(hours)})
		}
	}
	at, local := p.At.Local(), now.Local()
	clock := at.Format("15:04")
	switch day := dayOf(at); {
	case day.Equal(dayOf(local)):
		return l.Format("status.lastseen_today", map[string]string{"time": clock})
	case day.AddDate(0, 0, 1).Equal(dayOf(local)):
		return l.Format("status.lastseen_yesterday", map[string]string{"time": clock})
	case full:
		return l.Format("status.lastseen_date_time", map[string]string{"date": at.Format("02.01.06"), "time": clock})
	}
	return l.Format("status.lastseen_date", map[string]string{"date": at.Format("02.01.06")})
}

// dayOf is the midnight that starts the day of t, in its location.
func dayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// presenceChange is when presenceText of p next says something else, zero
// for never: when an online user goes offline, the next minute or hour
// after it, or the next midnight.
func presenceChange(p model.Presence, now time.Time) time.Time {
	if p.Seen != model.SeenAt {
		return time.Time{}
	}
	if p.Online(now) {
		return p.At
	}
	ago := now.Sub(p.At)
	switch {
	case ago < time.Hour:
		return p.At.Add((ago/time.Minute + 1) * time.Minute)
	case ago < 12*time.Hour:
		return p.At.Add((ago/time.Hour + 1) * time.Hour)
	}
	return dayOf(now.Local()).AddDate(0, 0, 1)
}
