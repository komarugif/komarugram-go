// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"komarugram/internal/messenger/model"
)

func TestTextBlockMetadataSurvivesConversionCacheAndRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 4}, Message: "codequote", Entities: []tg.MessageEntityClass{
		&tg.MessageEntityPre{Length: 4, Language: "go"},
		&tg.MessageEntityBlockquote{Offset: 4, Length: 5, Collapsed: true},
	}}
	m, _ := convertMessage("a", msg, nil)
	if m.Entities[0].Language != "go" || !m.Entities[1].Collapsed {
		t.Fatalf("metadata lost: %+v", m.Entities)
	}
	if err := s.Cache().SaveMessages(ctx, []model.Message{m}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cache().Around(ctx, 4, 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("cache: %v, %v", got, err)
	}
	if got[0].Entities[0].Language != "go" || !got[0].Entities[1].Collapsed {
		t.Fatal("cache lost metadata")
	}
	before := model.Revision(m)
	m.Entities[0].Language = "python"
	if model.Revision(m) == before {
		t.Fatal("language edit does not invalidate layout")
	}
	before = model.Revision(m)
	m.Entities[1].Collapsed = false
	if model.Revision(m) == before {
		t.Fatal("collapse edit does not invalidate layout")
	}
}

// Hashtags, cashtags, commands, email, phones, cards and formatted dates
// keep their kinds, and a date its time and format, through the cache.
func TestActionEntitiesSurviveConversionAndCache(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 4}, Message: "#a $B /c d@e.f +1 4242 now", Entities: []tg.MessageEntityClass{
		&tg.MessageEntityHashtag{Offset: 0, Length: 2},
		&tg.MessageEntityCashtag{Offset: 3, Length: 2},
		&tg.MessageEntityBotCommand{Offset: 6, Length: 2},
		&tg.MessageEntityEmail{Offset: 9, Length: 5},
		&tg.MessageEntityPhone{Offset: 15, Length: 2},
		&tg.MessageEntityBankCard{Offset: 18, Length: 4},
		&tg.MessageEntityFormattedDate{Offset: 23, Length: 3, Date: 1790000000, LongDate: true, DayOfWeek: true},
	}}
	m, _ := convertMessage("a", msg, nil)
	want := []string{"hashtag", "cashtag", "bot_command", "email", "phone", "bank_card", "date"}
	check := func(where string, entities []model.Entity) {
		t.Helper()
		if len(entities) != len(want) {
			t.Fatalf("%s: %+v", where, entities)
		}
		for i, kind := range want {
			if entities[i].Kind != kind {
				t.Fatalf("%s: entity %d is %q, not %q", where, i, entities[i].Kind, kind)
			}
		}
		if d := entities[6]; d.Date != 1790000000 || d.DateFormat != model.DateLongDate|model.DateDayOfWeek {
			t.Fatalf("%s: date %+v", where, d)
		}
	}
	check("converted", m.Entities)
	if err := s.Cache().SaveMessages(ctx, []model.Message{m}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cache().Around(ctx, 4, 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("cache: %v, %v", got, err)
	}
	check("cached", got[0].Entities)
}
