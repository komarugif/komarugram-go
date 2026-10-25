// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"
	"time"

	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

func TestAvatarColorIndex(t *testing.T) {
	for _, id := range []int64{0, 1, 6, 7, 1 << 40, -1, -2, -7, -1001234567890, -1 << 62} {
		if i := avatarColorIndex(id); i < 0 || i >= len(avatarColors) {
			t.Errorf("avatarColorIndex(%d) = %d, out of range", id, i)
		}
	}
}

func TestGroupDigits(t *testing.T) {
	for n, want := range map[int]string{7: "7", 999: "999", 1000: "1 000", 48210: "48 210", 1234567: "1 234 567"} {
		if got := groupDigits(n); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Анна Смирнова": "АС", "Мама": "М", "Новости Go": "НG", "  ": ""} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
	// An avatar draws them every frame: once made, they cost nothing.
	if n := testing.AllocsPerRun(100, func() { initials("Анна Смирнова") }); n != 0 {
		t.Errorf("initials allocate %v times a call", n)
	}
}

func TestChatTime(t *testing.T) {
	now := time.Date(2026, 9, 19, 15, 0, 0, 0, time.Local) // Saturday
	cases := map[time.Time]string{
		now.Add(-time.Hour):                           "14:00",
		now.Add(-26 * time.Hour):                      "пт",
		now.Add(-5 * 24 * time.Hour):                  "пн",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local): "01.08.26",
	}
	for tm, want := range cases {
		if got := chatTime(tm, now, localization.For("ru")); got != want {
			t.Errorf("chatTime(%v) = %q, want %q", tm, got, want)
		}
	}
	if got := chatTime(now.Add(-26*time.Hour), now, localization.For("en")); got != "Fri" {
		t.Fatalf("English weekday = %q", got)
	}
}

func TestListWidth(t *testing.T) {
	cases := []struct {
		requested, available, want float32
		narrow                     bool
	}{
		{100, 1200, 80, true},
		{200, 1200, 260, false},
		{400, 1200, 400, false},
		{900, 1200, 600, false},
		{700, 900, 580, false},
		{400, 560, 80, true}, // Too narrow for list and page.
	}
	for _, c := range cases {
		got, narrow := listWidth(unit.Dp(c.requested), unit.Dp(c.available))
		if float32(got) != c.want || narrow != c.narrow {
			t.Errorf("listWidth(%v, %v) = %v %v, want %v %v", c.requested, c.available, got, narrow, c.want, c.narrow)
		}
	}
}

// A quote of a sticker says the emoji it stands for before the word.
func TestStickerQuoteEmoji(t *testing.T) {
	m := model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{Emoji: "😀"}}
	if got := foundText(m, localization.For("en")); got != "😀 Sticker" {
		t.Fatalf("%q", got)
	}
}
