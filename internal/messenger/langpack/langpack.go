// SPDX-License-Identifier: Unlicense OR MIT

// Package langpack keeps Telegram's language packs (Telegram Desktop's,
// "tdesktop") for localization: one copy for every account, whole on disk,
// in the configuration directory, and in memory only the texts the client
// reads (localization.NewPack). A pack is asked of Telegram once, then
// only what changed in it (langpack.getDifference), as Telegram Desktop
// does: when the client starts, a day later, and when Telegram says it
// changed (updateLangPack, updateLangPackTooLong).
package langpack

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/localization"
)

// API is the part of Telegram's API packs are asked of.
type API interface {
	LangpackGetLangPack(context.Context, *tg.LangpackGetLangPackRequest) (*tg.LangPackDifference, error)
	LangpackGetDifference(context.Context, *tg.LangpackGetDifferenceRequest) (*tg.LangPackDifference, error)
	LangpackGetLanguage(context.Context, *tg.LangpackGetLanguageRequest) (*tg.LangPackLanguage, error)
}

const (
	// name is the platform whose packs the client takes: their keys are
	// the ones its texts map to.
	name = "tdesktop"
	// recheck is how long a pack is not asked about again; retry, after a
	// failure.
	recheck = 24 * time.Hour
	retry   = time.Minute
)

// file is a pack as it is kept on disk.
type file struct {
	Code    string            `json:"code"`
	Base    string            `json:"base,omitempty"`
	Plural  string            `json:"plural,omitempty"`
	RTL     bool              `json:"rtl,omitempty"`
	Version int               `json:"version"`
	Strings map[string]string `json:"strings"`
}

type state struct {
	busy bool
	read bool      // the copy on disk was read
	next time.Time // when to ask Telegram again
}

var (
	mu       sync.Mutex
	dir      string
	states   = map[string]*state{}
	watchers = map[any]func(){}
)

// Configure sets the directory packs are kept in; without one they are
// kept in memory only and asked of Telegram whole.
func Configure(directory string) {
	mu.Lock()
	dir = directory
	mu.Unlock()
}

// Watch calls changed when a pack is installed or changes, until Unwatch
// with the same owner.
func Watch(owner any, changed func()) {
	mu.Lock()
	watchers[owner] = changed
	mu.Unlock()
}

func Unwatch(owner any) {
	mu.Lock()
	delete(watchers, owner)
	mu.Unlock()
}

// Due reports whether code's pack is to be read from disk, or asked of
// Telegram when online, and if so marks it busy: Load must follow.
func Due(code string, online bool) bool {
	if !validCode(code) {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	s := states[code]
	if s == nil {
		s = &state{}
		states[code] = s
	}
	if s.busy || s.read && (!online || time.Now().Before(s.next)) {
		return false
	}
	s.busy = true
	return true
}

// Load installs code's pack from disk the first time, then, with api,
// brings it up to date with Telegram. It follows Due.
func Load(ctx context.Context, code string, api API) {
	wait := retry
	defer func() {
		mu.Lock()
		s := states[code]
		s.busy, s.read = false, true
		if api != nil {
			s.next = time.Now().Add(wait)
		}
		mu.Unlock()
	}()
	mu.Lock()
	first := !states[code].read
	mu.Unlock()
	if first {
		if f, err := readFile(code); err == nil {
			install(f)
		}
	}
	if api == nil {
		return
	}
	if err := update(ctx, code, api); err == nil {
		wait = recheck
	}
}

// update asks Telegram for what changed in code's pack since the version
// installed, or for the whole pack.
func update(ctx context.Context, code string, api API) error {
	f, err := readFile(code)
	if f == nil || f.Version == 0 {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return fetch(ctx, code, api)
	}
	diff, err := api.LangpackGetDifference(ctx, &tg.LangpackGetDifferenceRequest{LangPack: name, LangCode: code, FromVersion: f.Version})
	if err != nil {
		return err
	}
	return apply(ctx, f, diff, api)
}

// fetch asks Telegram for the whole pack, and for what its language is.
func fetch(ctx context.Context, code string, api API) error {
	diff, err := api.LangpackGetLangPack(ctx, &tg.LangpackGetLangPackRequest{LangPack: name, LangCode: code})
	if err != nil {
		return err
	}
	f := &file{Code: code, Version: diff.Version, Strings: map[string]string{}}
	if language, err := api.LangpackGetLanguage(ctx, &tg.LangpackGetLanguageRequest{LangPack: name, LangCode: code}); err == nil {
		f.Base, f.Plural, f.RTL = language.BaseLangCode, language.PluralCode, language.Rtl
	}
	merge(f.Strings, diff.Strings)
	if err := writeFile(f); err != nil {
		return err
	}
	install(f)
	if f.Base != "" && f.Base != code && Due(f.Base, true) {
		Load(ctx, f.Base, api)
	}
	return nil
}

// apply brings f up to diff: a pack whole replaces it, a difference from
// another version than f's is asked again whole.
func apply(ctx context.Context, f *file, diff *tg.LangPackDifference, api API) error {
	switch {
	case diff.FromVersion == 0:
		f.Strings = map[string]string{}
	case diff.FromVersion != f.Version:
		return fetch(ctx, f.Code, api)
	}
	if diff.Version == f.Version && len(diff.Strings) == 0 {
		return nil
	}
	merge(f.Strings, diff.Strings)
	f.Version = diff.Version
	if err := writeFile(f); err != nil {
		return err
	}
	install(f)
	return nil
}

// Changed is told of a pack Telegram says changed (updateLangPack,
// updateLangPackTooLong): it is asked about at its next Due.
func Changed(code string) {
	mu.Lock()
	if s := states[code]; s != nil {
		s.next = time.Time{}
	}
	mu.Unlock()
}

// merge applies Telegram's strings to a pack's: a plural one replaces each
// of its forms, as key#one, key#few…
func merge(values map[string]string, strings []tg.LangPackStringClass) {
	for _, raw := range strings {
		switch s := raw.(type) {
		case *tg.LangPackString:
			values[s.Key] = s.Value
		case *tg.LangPackStringPluralized:
			removeForms(values, s.Key)
			for form, value := range map[string]string{"zero": s.ZeroValue, "one": s.OneValue, "two": s.TwoValue, "few": s.FewValue, "many": s.ManyValue, "other": s.OtherValue} {
				if value != "" {
					values[s.Key+"#"+form] = value
				}
			}
		case *tg.LangPackStringDeleted:
			delete(values, s.Key)
			removeForms(values, s.Key)
		}
	}
}

func removeForms(values map[string]string, key string) {
	for _, form := range []string{"zero", "one", "two", "few", "many", "other"} {
		delete(values, key+"#"+form)
	}
}

func install(f *file) {
	localization.Install(localization.NewPack(f.Code, f.Base, f.Plural, f.RTL, f.Version, f.Strings))
	mu.Lock()
	notify := make([]func(), 0, len(watchers))
	for _, changed := range watchers {
		notify = append(notify, changed)
	}
	mu.Unlock()
	for _, changed := range notify {
		changed()
	}
}

// validCode accepts the codes of Telegram's languages ("pt-br", a
// translation's "uk-beta"), which name files on disk.
func validCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func path(code string) string {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, code+".json")
}

func readFile(code string) (*file, error) {
	p := path(code)
	if p == "" {
		return nil, fs.ErrNotExist
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Code != code || f.Strings == nil {
		return nil, errors.New("langpack: " + p + " is not " + code + "'s pack")
	}
	return &f, nil
}

// writeFile replaces the copy on disk whole: a new file renamed over it.
func writeFile(f *file) error {
	p := path(f.Code)
	if p == "" {
		return nil
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), strings.TrimSuffix(filepath.Base(p), ".json")+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
