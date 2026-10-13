package localization

import (
	"komarugram/internal/messenger/model"
	"strings"
	"testing"
)

func TestChatStringsPreferTelegramAndPluralForms(t *testing.T) {
	c := For("ru").WithPack(testPack("ru", map[string]string{"lng_media_type_photos": "SERVER PHOTOS", "lng_profile_photos#one": "ONE {count}", "lng_profile_photos#few": "FEW {count}", "lng_profile_photos#many": "MANY {count}", "lng_gift_unique_model": "SERVER MODEL", "lng_gift_unique_number": "NUMBER {index}", "lng_credits_box_out_about": "Please read {link}.", "lng_gift_stars_title#many": "{count} SERVER STARS"}))
	if c.T("shared.photos") != "SERVER PHOTOS" || c.T("gift.model") != "SERVER MODEL" {
		t.Fatal("server strings ignored")
	}
	for n, want := range map[int]string{1: "ONE 1", 2: "FEW 2", 11: "MANY 11", 21: "ONE 21", 22: "FEW 22"} {
		if got := c.SharedCount(model.SharedPhotos, n); got != want {
			t.Fatal(n, got, want)
		}
	}
	if got := c.Format("gift.terms", map[string]string{"link": "100% terms"}); got != "Please read 100% terms." {
		t.Fatal(got)
	}
	if got := c.Count("gift.stars", 100, nil); got != "100 SERVER STARS" {
		t.Fatal(got)
	}
}
func TestAllSharedHeadingsAndGiftLabelsHaveServerKeys(t *testing.T) {
	keys := []string{"gift.from", "gift.date", "gift.value", "gift.model", "gift.symbol", "gift.backdrop", "gift.quantity", "gift.hidden_sender", "gift.title", "gift.number", "gift.issued", "gift.stars", "gift.terms", "gift.terms_link", "chat_theme.title", "settings.back", "viewer.close"}
	for _, k := range append(append([]model.SharedKind{}, model.SharedKinds...), model.SharedStories, model.SharedGifts, model.SharedGroups) {
		keys = append(keys, "shared."+string(k), "shared.count."+string(k))
	}
	for _, key := range keys {
		if !strings.HasPrefix(telegramKey(key), "lng_") {
			t.Errorf("missing server mapping: %s", key)
		}
		if For("en").T(key) == key || For("ru").T(key) == key {
			t.Errorf("missing offline fallback: %s", key)
		}
	}
}
