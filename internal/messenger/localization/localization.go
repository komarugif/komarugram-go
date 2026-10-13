// SPDX-License-Identifier: Unlicense OR MIT

// Package localization words the client's interface. Its own texts come
// with it, a file a language in languages/; Telegram's language pack for
// the language, when loaded (Install), takes their place for the keys
// Telegram Desktop has (telegram_keys.strings). A language the client has
// no file for shows Telegram's texts where the pack has them, English
// elsewhere.
package localization

import (
	"strconv"
	"strings"
	"sync"
)

// Language is a language's code, as Telegram names it ("ru", "pt-br").
type Language string

const (
	Russian Language = "ru"
	English Language = "en"
)

// Languages are the languages the client comes with.
func Languages() []Language {
	out := make([]Language, len(bundled))
	for i, b := range bundled {
		out[i] = b.code
	}
	return out
}

// Name is a language's name in itself, or its code.
func (l Language) Name() string {
	if b := bundleFor(l); b != nil {
		if name := b.get()["language.name"]; name != "" {
			return name
		}
	}
	return string(l)
}

// A locale is the bundles a language's texts are looked for in: its own,
// its main language's ("pt" for "pt-br"), English, Russian.
type locale struct {
	code  Language
	chain []*bundle
}

var locales sync.Map // Language → *locale

func localeFor(code Language) *locale {
	if l, ok := locales.Load(code); ok {
		return l.(*locale)
	}
	l := &locale{code: code}
	primary, _, _ := strings.Cut(string(code), "-")
	for _, c := range []Language{code, Language(primary), English, Russian} {
		if b := bundleFor(c); b != nil && !containsBundle(l.chain, b) {
			l.chain = append(l.chain, b)
		}
	}
	actual, _ := locales.LoadOrStore(code, l)
	return actual.(*locale)
}

func containsBundle(chain []*bundle, b *bundle) bool {
	for _, c := range chain {
		if c == b {
			return true
		}
	}
	return false
}

// Catalog is a language's texts as they are when it is made: cheap to make
// for each frame, and comparable, so a cache of texts made with one can
// tell when the language or Telegram's pack changed.
type Catalog struct {
	locale     *locale
	pack, base *Pack
}

// For is the catalog of a language, with Telegram's pack for it when one
// is installed. An empty code is Russian, the client's first language.
func For(code string) Catalog {
	code = strings.ToLower(code)
	if code == "" {
		code = string(Russian)
	}
	c := Catalog{locale: localeFor(Language(code))}
	return c.WithPack(Installed(code))
}

// WithPack is c with p in place of Telegram's pack for the language.
func (c Catalog) WithPack(p *Pack) Catalog {
	c.pack, c.base = p, nil
	if p != nil && p.Base != "" {
		c.base = Installed(p.Base)
	}
	return c
}

func (c Catalog) Language() Language { return c.locale.code }

// RTL reports whether the language is written right to left: as
// Telegram's pack says, or the client's file ("language.rtl" = "true").
func (c Catalog) RTL() bool {
	if c.pack != nil {
		return c.pack.RTL
	}
	return c.locale.chain[0].get()["language.rtl"] == "true"
}

// T is the text of key: Telegram's, then the language's own, then
// English, then Russian, which has every text; a key no language has is
// shown as itself.
func (c Catalog) T(key string) string {
	if c.pack != nil {
		if telegram := telegramKey(key); telegram != "" {
			for _, p := range [...]*Pack{c.pack, c.base} {
				if p == nil {
					continue
				}
				if value := p.texts[telegram]; value != "" {
					return value
				}
			}
		}
	}
	for _, b := range c.locale.chain {
		if value := b.get()[key]; value != "" {
			return value
		}
	}
	return key
}

// Count is the text of key for n things, in the plural form the language
// has for n, with {count} and args filled in.
func (c Catalog) Count(key string, n int, args map[string]string) string {
	tags := map[string]string{"count": strconv.Itoa(n)}
	for k, v := range args {
		tags[k] = v
	}
	return replaceTags(c.plural(key, max(n, -n)), tags)
}

func (c Catalog) plural(key string, n int) string {
	if c.pack != nil {
		if telegram := telegramKey(key); telegram != "" {
			for _, p := range [...]*Pack{c.pack, c.base} {
				if p == nil {
					continue
				}
				if value := p.texts[telegram+"#"+p.plural(n)]; value != "" {
					return value
				}
				if value := p.texts[telegram+"#other"]; value != "" {
					return value
				}
				if value := p.texts[telegram]; value != "" {
					return value
				}
			}
		}
	}
	// Each language's forms before the next language's: English's text
	// for a key, not Russian's form of it, in an English interface.
	for _, b := range c.locale.chain {
		texts := b.get()
		if value := texts[key+"#"+b.plural(n)]; value != "" {
			return value
		}
		if value := texts[key]; value != "" {
			return value
		}
	}
	return key
}
