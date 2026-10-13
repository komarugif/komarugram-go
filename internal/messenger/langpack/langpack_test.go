// SPDX-License-Identifier: Unlicense OR MIT

package langpack

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/localization"
)

// fakeAPI serves one language's pack and counts what it was asked.
type fakeAPI struct {
	pack       *tg.LangPackDifference
	difference *tg.LangPackDifference
	language   tg.LangPackLanguage
	whole      int
	from       []int
}

func (a *fakeAPI) LangpackGetLangPack(_ context.Context, r *tg.LangpackGetLangPackRequest) (*tg.LangPackDifference, error) {
	a.whole++
	return a.pack, nil
}

func (a *fakeAPI) LangpackGetDifference(_ context.Context, r *tg.LangpackGetDifferenceRequest) (*tg.LangPackDifference, error) {
	a.from = append(a.from, r.FromVersion)
	return a.difference, nil
}

func (a *fakeAPI) LangpackGetLanguage(context.Context, *tg.LangpackGetLanguageRequest) (*tg.LangPackLanguage, error) {
	return &a.language, nil
}

func reset(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mu.Lock()
	states, watchers = map[string]*state{}, map[any]func(){}
	mu.Unlock()
	Configure(dir)
	return dir
}

func load(t *testing.T, code string, api API) {
	t.Helper()
	online := api != nil
	if !Due(code, online) {
		t.Fatalf("%s not due", code)
	}
	Load(context.Background(), code, api)
}

// A pack is asked of Telegram whole once, kept whole on disk and in memory
// only as far as the client reads it, then read from disk offline.
func TestFetchKeepAndReadOffline(t *testing.T) {
	dir := reset(t)
	api := &fakeAPI{
		pack: &tg.LangPackDifference{LangCode: "uk", Version: 7, Strings: []tg.LangPackStringClass{
			&tg.LangPackString{Key: "lng_saved_messages", Value: "Збережене"},
			&tg.LangPackString{Key: "lng_not_read_by_the_client", Value: "x"},
			&tg.LangPackStringPluralized{Key: "lng_profile_photos", OneValue: "{count} фото", FewValue: "{count} фотографії", ManyValue: "{count} фотографій"},
		}},
		language: tg.LangPackLanguage{LangCode: "uk", PluralCode: "uk"},
	}
	notified := 0
	Watch(t, func() { notified++ })
	load(t, "uk", api)
	if api.whole != 1 || notified != 1 {
		t.Fatalf("asked %d times, notified %d", api.whole, notified)
	}
	p := localization.Installed("uk")
	if p == nil || p.Version != 7 || p.Len() != 4 {
		t.Fatalf("installed %+v", p)
	}
	if got := localization.For("uk").Count("shared.count.photos", 3, nil); got != "3 фотографії" {
		t.Errorf("3 photos = %q", got)
	}
	if Due("uk", true) {
		t.Error("asked again before a day passed")
	}
	if _, err := os.Stat(filepath.Join(dir, "uk.json")); err != nil {
		t.Fatal(err)
	}

	// A new run, offline, reads it from disk.
	localization.Install(localization.NewPack("uk", "", "", false, 0, nil))
	mu.Lock()
	states = map[string]*state{}
	mu.Unlock()
	load(t, "uk", nil)
	if p := localization.Installed("uk"); p.Version != 7 || p.Len() != 4 {
		t.Fatalf("read back %+v", p)
	}
	if Due("uk", false) {
		t.Error("read from disk again")
	}
	if !Due("uk", true) {
		t.Error("not asked of Telegram once online")
	}
}

// What changed is asked from the version on disk and applied to it; a
// difference from another version has the pack asked again whole.
func TestDifference(t *testing.T) {
	reset(t)
	api := &fakeAPI{
		pack: &tg.LangPackDifference{LangCode: "de", Version: 1, Strings: []tg.LangPackStringClass{
			&tg.LangPackString{Key: "lng_saved_messages", Value: "Gespeichert"},
			&tg.LangPackStringPluralized{Key: "lng_profile_photos", OneValue: "{count} Foto", OtherValue: "{count} Fotos"},
			&tg.LangPackString{Key: "lng_edited", Value: "bearbeitet"},
		}},
	}
	load(t, "de", api)
	api.difference = &tg.LangPackDifference{LangCode: "de", FromVersion: 1, Version: 2, Strings: []tg.LangPackStringClass{
		&tg.LangPackString{Key: "lng_saved_messages", Value: "Gespeicherte Nachrichten"},
		&tg.LangPackStringPluralized{Key: "lng_profile_photos", OtherValue: "{count} Bilder"},
		&tg.LangPackStringDeleted{Key: "lng_edited"},
	}}
	Changed("de")
	load(t, "de", api)
	if len(api.from) != 1 || api.from[0] != 1 || api.whole != 1 {
		t.Fatalf("asked from %v, whole %d times", api.from, api.whole)
	}
	l := localization.For("de")
	if got := l.T("nav.saved"); got != "Gespeicherte Nachrichten" {
		t.Errorf("saved = %q", got)
	}
	if got := l.Count("shared.count.photos", 1, nil); got != "1 Bilder" {
		t.Errorf("1 photo = %q: the old form stayed", got)
	}
	if got := l.T("history.edited"); got != "edited" {
		t.Errorf("deleted text = %q", got)
	}

	api.difference = &tg.LangPackDifference{LangCode: "de", FromVersion: 5, Version: 6}
	Changed("de")
	load(t, "de", api)
	if api.whole != 2 {
		t.Errorf("a difference from another version was applied")
	}
}

func TestValidCode(t *testing.T) {
	for code, want := range map[string]bool{"ru": true, "pt-br": true, "uk-beta_2": true, "": false, "../x": false, "RU": false} {
		if validCode(code) != want {
			t.Errorf("%q: %v", code, !want)
		}
	}
}
