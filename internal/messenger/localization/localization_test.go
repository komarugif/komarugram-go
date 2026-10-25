package localization

import (
	"strings"
	"testing"

	"komarugram/internal/messenger/model"
)

func testPack(code string, values map[string]string) *Pack {
	return NewPack(code, "", code, false, 1, values)
}

func TestCatalogSelectionAndFallback(t *testing.T) {
	for code, want := range map[string]string{"en": "Settings", "EN": "Settings", "": "Настройки", "ru": "Настройки", "de": "Settings", "en-gb": "Settings"} {
		if got := For(code).T("nav.settings"); got != want {
			t.Errorf("%q: settings = %q, want %q", code, got, want)
		}
	}
	if got := For("en").T("missing.key"); got != "missing.key" {
		t.Fatalf("missing key = %q", got)
	}
	if For("ru") != For("ru") || For("ru") == For("en") {
		t.Fatal("catalogs of one language differ, or of two do not")
	}
}

// The files of the languages are read whole, and every language has the
// keys the settings and number formats need.
func TestBundledFiles(t *testing.T) {
	if _, err := parseStrings(telegramSource); err != nil {
		t.Errorf("telegram_keys.strings: %v", err)
	}
	for key, telegram := range telegramKeys() {
		if !strings.HasPrefix(telegram, "lng_") {
			t.Errorf("%s maps to %q", key, telegram)
		}
	}
	for _, b := range bundled {
		if _, err := parseStrings(*b.source); err != nil {
			t.Errorf("%s: %v", b.code, err)
		}
		for _, key := range []string{"language.name", "number.decimal"} {
			if b.get()[key] == "" {
				t.Errorf("%s has no %s", b.code, key)
			}
		}
	}
}

// A key with a text in another language and none in Russian shows as
// itself in the Russian interface.
func TestEveryKeyHasRussian(t *testing.T) {
	russian := bundleFor(Russian).get()
	for _, b := range bundled {
		for key := range b.get() {
			if !strings.Contains(key, "#") && russian[key] == "" {
				t.Errorf("%s: %s has no Russian text", b.code, key)
			}
		}
	}
}

func TestParseStrings(t *testing.T) {
	src := "// comment\n\n\"a\" = \"plain\";\n  \"b\"=\"say \\\"hi\\\"\\n\\\\\" ;\n\"c\" = \"x\"\n\"d\" = \"y\";\n"
	got, err := parseStrings(src)
	if err == nil || !strings.Contains(err.Error(), "line 5") {
		t.Errorf("error = %v, want one on line 5", err)
	}
	want := map[string]string{"a": "plain", "b": "say \"hi\"\n\\", "d": "y"}
	if len(got) != len(want) {
		t.Errorf("got %q", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestPlurals(t *testing.T) {
	for _, c := range []struct {
		code string
		want map[int]string
	}{
		{"en", map[int]string{0: "other", 1: "one", 2: "other", 21: "other"}},
		{"ru", map[int]string{0: "many", 1: "one", 3: "few", 11: "many", 21: "one", 22: "few", 112: "many"}},
		{"uk", map[int]string{1: "one", 4: "few", 5: "many"}},
		{"pl", map[int]string{1: "one", 21: "many", 22: "few", 25: "many"}},
		{"cs", map[int]string{1: "one", 3: "few", 5: "other"}},
		{"pt-br", map[int]string{0: "one", 1: "one", 2: "other"}},
		{"ja", map[int]string{1: "other"}},
		{"ar", map[int]string{0: "zero", 2: "two", 5: "few", 11: "many", 100: "other"}},
		{"xx", map[int]string{1: "one", 5: "other"}},
	} {
		rule := pluralFor(c.code)
		for n, want := range c.want {
			if got := rule(n); got != want {
				t.Errorf("%s %d: %s, want %s", c.code, n, got, want)
			}
		}
	}
}

// A pack keeps only the texts of the keys the client reads, and a language
// the client has no texts of takes Telegram's, plural forms by its rule.
func TestPack(t *testing.T) {
	p := NewPack("uk", "", "uk", false, 3, map[string]string{
		"lng_settings_section_info": "unused", "lng_saved_messages": "Збережене",
		"lng_profile_photos#one": "{count} фото", "lng_profile_photos#few": "{count} фотографії", "lng_profile_photos#many": "{count} фотографій",
	})
	if p.Len() != 4 {
		t.Errorf("kept %d texts", p.Len())
	}
	c := For("uk").WithPack(p)
	if got := c.T("nav.saved"); got != "Збережене" {
		t.Errorf("saved = %q", got)
	}
	if got := c.Count("shared.count.photos", 3, nil); got != "3 фотографії" {
		t.Errorf("3 photos = %q", got)
	}
	if got := c.T("nav.settings"); got != "Settings" {
		t.Errorf("settings = %q", got)
	}
}

// English keeps its own text of a counted key it has no form of, not
// Russian's form.
func TestPluralFallsBackWithinLanguage(t *testing.T) {
	en := bundleFor(English).get()
	for key := range bundleFor(Russian).get() {
		name, form, ok := strings.Cut(key, "#")
		if !ok || form != "few" || en[name+"#other"] != "" || en[name] == "" {
			continue
		}
		if got := For("en").Count(name, 3, nil); strings.ContainsFunc(got, func(r rune) bool { return r >= 'а' && r <= 'я' }) {
			t.Errorf("%s: %q", name, got)
		}
		return
	}
	t.Fatal("no counted key without English forms to check")
}

// A sticker's emoji goes before the word, as the official clients show
// it, though a pack's sticker_emoji puts it after; the word is the pack's.
func TestStickerEmojiFirst(t *testing.T) {
	p := NewPack("ru", "", "ru", false, 1, map[string]string{"lng_in_dlg_sticker": "Стикер", "lng_in_dlg_sticker_emoji": "Стикер {emoji}"})
	for _, c := range []Catalog{For("ru"), For("en"), For("ru").WithPack(p)} {
		_, media, _ := c.PreviewLine(model.Preview{Media: "preview.sticker", Emoji: "😀"}, "Anna", false)
		if want := "😀 " + c.T("preview.sticker"); media != want {
			t.Errorf("%q, want %q", media, want)
		}
	}
}
