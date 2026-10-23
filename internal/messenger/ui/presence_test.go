// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// When a user was online is told as Telegram Desktop tells it: in minutes
// and hours in the header, by the day after that, and with the time in the
// profile.
func TestPresenceText(t *testing.T) {
	l := localization.For("en")
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.Local)
	at := func(d time.Duration) model.Presence { return model.Presence{Seen: model.SeenAt, At: now.Add(d)} }
	for _, c := range []struct {
		p    model.Presence
		full bool
		want string
	}{
		{at(time.Minute), false, "online"},
		{at(-30 * time.Second), false, "last seen just now"},
		{at(-time.Minute), false, "last seen 1 minute ago"},
		{at(-25 * time.Minute), false, "last seen 25 minutes ago"},
		{at(-3 * time.Hour), false, "last seen 3 hours ago"},
		{at(-3 * time.Hour), true, "last seen today at 12:00"},
		{at(-20 * time.Hour), false, "last seen yesterday at 19:00"},
		{at(-50 * time.Hour), false, "last seen 20.10.26"},
		{at(-50 * time.Hour), true, "last seen 20.10.26 at 13:00"},
		{model.Presence{Seen: model.SeenLastMonth}, false, "last seen within a month"},
		{model.Presence{Seen: model.SeenLongAgo}, false, "last seen a long time ago"},
	} {
		if got := presenceText(c.p, now, c.full, l); got != c.want {
			t.Errorf("%+v full %v: %q, want %q", c.p, c.full, got, c.want)
		}
	}
	if got := presenceChange(at(time.Minute), now); !got.Equal(now.Add(time.Minute)) {
		t.Errorf("online changes at %v", got)
	}
	if got := presenceChange(at(-90*time.Second), now); !got.Equal(now.Add(30 * time.Second)) {
		t.Errorf("minutes change at %v", got)
	}
}

// The header tells a user online in the accent color; hiding who is online
// leaves the line it had before, and hiding the checks keeps the clock of
// a message on its way.
func TestCalmHidesPresenceAndChecks(t *testing.T) {
	p := newChatPage(mockstore.New(time.Now(), 0), func() {})
	defer p.Close()
	l := localization.For("en")
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Values: map[string]any{}}
	anna := model.Chat{ID: 2, Kind: model.KindUser}
	if line, active := p.chatStatusLine(gtx, anna, false, l); line != "online" || !active {
		t.Fatalf("online: %q %v", line, active)
	}
	p.kind, p.readOutbox = model.KindUser, 5
	sent := model.Message{Key: model.MessageKey{MessageID: 6}, Outgoing: true}
	sending := model.Message{Key: model.MessageKey{MessageID: -1}, Outgoing: true, Sending: true}
	if p.sendState(gtx, sent) != model.SendSent {
		t.Fatal("no check")
	}

	withCalm(gtx, preferences.Calm{On: true, KeepRead: true})
	if line, active := p.chatStatusLine(gtx, anna, false, l); line != "last seen recently" || active {
		t.Fatalf("online hidden: %q %v", line, active)
	}
	if p.sendState(gtx, sent) != model.SendSent {
		t.Fatal("the checks kept were hidden")
	}

	withCalm(gtx, preferences.Calm{On: true, KeepOnline: true})
	if line, _ := p.chatStatusLine(gtx, anna, false, l); line != "online" {
		t.Fatalf("online kept: %q", line)
	}
	if p.sendState(gtx, sent) != model.SendNone {
		t.Fatal("checks shown")
	}
	if p.sendState(gtx, sending) != model.SendSending {
		t.Fatal("the clock hidden")
	}
	if rendererRevision(gtx) == rendererRevision(layout.Context{}) {
		t.Fatal("the layouts of the history with and without checks are one")
	}
}
