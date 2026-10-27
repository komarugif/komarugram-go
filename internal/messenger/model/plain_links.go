// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// plainLink finds the links of a plain text: an address with its scheme,
// or a domain alone, with what follows it; and a mention, @ and a
// username. Whether a domain is one, plainDomain tells.
var plainLink = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"]+|((?:[\p{L}\p{N}](?:[\p{L}\p{N}-]*[\p{L}\p{N}])?\.)+(\p{L}{2,63}))(?::\d{1,5})?(?:/[^\s<>"]*)?|(@[a-z][a-z0-9_]{3,31})\b`)

// genericDomains are the top-level domains beside the countries' that a
// domain alone may end with, as Telegram Desktop's list of them has the
// common ones: a file's name, "notes.txt", is no link.
var genericDomains = map[string]bool{
	"com": true, "net": true, "org": true, "info": true, "biz": true, "edu": true, "gov": true, "mil": true,
	"int": true, "name": true, "pro": true, "aero": true, "coop": true, "museum": true, "jobs": true,
	"mobi": true, "travel": true, "tel": true, "asia": true, "cat": true, "post": true, "xxx": true,
	"dev": true, "app": true, "page": true, "xyz": true, "online": true, "site": true, "store": true,
	"shop": true, "tech": true, "blog": true, "club": true, "top": true, "live": true, "space": true,
	"website": true, "world": true, "today": true, "news": true, "art": true, "design": true, "cloud": true,
	"link": true, "fun": true, "games": true, "life": true, "media": true, "network": true, "one": true,
	"photo": true, "photography": true, "pics": true, "pw": true, "ton": true, "wiki": true, "work": true,
	"zone": true, "moscow": true, "tatar": true, "global": true, "agency": true, "studio": true,
	"рф": true, "рус": true, "москва": true, "онлайн": true, "сайт": true, "орг": true, "укр": true, "бел": true, "қаз": true, "срб": true,
}

// plainDomain tells whether top ends a domain: a country's, two letters,
// or one of genericDomains.
func plainDomain(top string) bool {
	top = strings.ToLower(top)
	return utf8.RuneCountInString(top) == 2 || genericDomains[top]
}

// PlainEntities are the links and mentions of text, which Telegram tells
// without entities, as a user's bio: url and mention entities, in UTF-16
// offsets. An address does not take the punctuation after it, and a domain
// of an e-mail or in the middle of a word is none.
func PlainEntities(text string) []Entity {
	var out []Entity
	for _, at := range plainLink.FindAllStringSubmatchIndex(text, -1) {
		start, end, kind := at[0], at[1], "url"
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		switch {
		case at[6] >= 0:
			// A mention, not in a word or an address.
			if start > 0 && (before == '_' || before == '.' || before == '/' || unicode.IsLetter(before) || unicode.IsDigit(before)) {
				continue
			}
			kind = "mention"
		case at[2] >= 0:
			// A domain alone.
			if start > 0 && (before == '@' || before == '.' || before == '-' || before == '_' || before == '/' || unicode.IsLetter(before) || unicode.IsDigit(before)) {
				continue
			}
			if !plainDomain(text[at[4]:at[5]]) {
				continue
			}
		}
		if kind == "url" {
			end = start + len(strings.TrimRight(text[start:end], ".,:;!?'\")]}"))
		}
		offset := len(utf16.Encode([]rune(text[:start])))
		out = append(out, Entity{Kind: kind, Offset: offset, Length: len(utf16.Encode([]rune(text[start:end])))})
	}
	return out
}
