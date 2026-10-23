// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEditable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	own := Message{Key: MessageKey{MessageID: 5}, Outgoing: true, Text: "hi", Date: now.Add(-time.Hour)}
	old := own
	old.Date = now.Add(-49 * time.Hour)
	forwarded := own
	forwarded.ForwardName = "Ann"
	sticker := own
	sticker.Kind = MessageSticker
	sending := own
	sending.Sending = true
	others := own
	others.Outgoing = false
	for name, c := range map[string]struct {
		m    Message
		kind ChatKind
		want bool
	}{
		"own":                    {own, KindUser, true},
		"too old":                {old, KindUser, false},
		"old in saved messages":  {old, KindSaved, true},
		"forwarded":              {forwarded, KindUser, false},
		"sticker":                {sticker, KindUser, false},
		"on its way":             {sending, KindUser, false},
		"others'":                {others, KindGroup, false},
		"others' in saved, mine": {others, KindSaved, true},
	} {
		if got := Editable(c.m, c.kind, now, DefaultEditTimeLimit); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestCheckEdit(t *testing.T) {
	text := Message{Kind: MessageText}
	photo := Message{Kind: MessagePhoto, Media: &MessageMedia{ID: "p"}}
	if err := CheckEdit(text, " "); !errors.Is(err, ErrMessageEmpty) {
		t.Fatal("empty text:", err)
	}
	if err := CheckEdit(photo, ""); err != nil {
		t.Fatal("a photo without its caption:", err)
	}
	var long *EditTooLong
	if err := CheckEdit(text, strings.Repeat("я", MaxMessageLength+3)); !errors.As(err, &long) || long.Over != 3 {
		t.Fatal("long text:", err)
	}
	if err := CheckEdit(photo, strings.Repeat("a", MaxCaptionLength+1)); !errors.As(err, &long) || long.Over != 1 {
		t.Fatal("long caption:", err)
	}
}
