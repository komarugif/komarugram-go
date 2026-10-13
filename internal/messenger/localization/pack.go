// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"strings"
	"sync"
)

// A Pack is a language pack of Telegram's (Telegram Desktop's lng_… keys)
// as the client keeps it in memory: only the texts its own keys map to
// (telegram_keys.strings), about one in fifteen of the pack. Its texts
// take the place of the client's own for those keys.
type Pack struct {
	// Code is the language's code in Telegram; Base, the language a
	// pack of a translation of Telegram's falls back to ("" for none).
	Code, Base string
	// RTL is set for a language written right to left.
	RTL bool
	// Version is the pack's version, to ask Telegram for what changed.
	Version int
	plural  pluralRule
	texts   map[string]string
}

// NewPack keeps the texts of values the client reads, by Telegram's keys,
// plural forms as key#one, key#few…; pluralCode is the language whose
// plural rule its forms follow.
func NewPack(code, base, pluralCode string, rtl bool, version int, values map[string]string) *Pack {
	used := map[string]bool{}
	for _, key := range telegramKeys() {
		used[key] = true
	}
	texts := map[string]string{}
	for key, value := range values {
		name, _, _ := strings.Cut(key, "#")
		if used[name] && value != "" {
			texts[key] = value
		}
	}
	if pluralCode == "" {
		pluralCode = code
	}
	return &Pack{Code: code, Base: base, RTL: rtl, Version: version, plural: pluralFor(pluralCode), texts: texts}
}

// Len is the number of texts kept.
func (p *Pack) Len() int { return len(p.texts) }

var packs sync.Map // Language → *Pack

// Install makes p the pack of its language for every catalog made after.
func Install(p *Pack) { packs.Store(Language(p.Code), p) }

// Installed is the pack of a language, or nil.
func Installed(code string) *Pack {
	p, _ := packs.Load(Language(code))
	pack, _ := p.(*Pack)
	return pack
}
