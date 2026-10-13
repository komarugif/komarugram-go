// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

// The texts of the languages the client comes with, a file each in
// languages/, and the keys of Telegram's packs they map to. They stay in
// the binary's read-only data: a language is parsed when first used, and
// its texts are slices of the file, not copies on the heap.
var (
	//go:embed languages/ru.strings
	russianSource string
	//go:embed languages/en.strings
	englishSource string
	//go:embed telegram_keys.strings
	telegramSource string
)

// bundled are the languages the client comes with, in the order the
// settings offer them. A new language is a file in languages/ and a line
// here.
var bundled = []*bundle{
	{code: Russian, source: &russianSource},
	{code: English, source: &englishSource},
}

// A bundle is one language's texts of the client.
type bundle struct {
	code   Language
	source *string
	once   sync.Once
	texts  map[string]string
	plural pluralRule
}

func (b *bundle) get() map[string]string {
	b.once.Do(func() {
		b.texts, _ = parseStrings(*b.source)
		b.plural = pluralFor(string(b.code))
	})
	return b.texts
}

func bundleFor(code Language) *bundle {
	for _, b := range bundled {
		if b.code == code {
			return b
		}
	}
	return nil
}

var telegramKeys = sync.OnceValue(func() map[string]string {
	keys, _ := parseStrings(telegramSource)
	return keys
})

// telegramKey is the key of Telegram's language pack a client's key
// takes its text from, or "".
func telegramKey(key string) string { return telegramKeys()[key] }

// parseStrings reads a file in the format of Telegram's language packs,
// one `"key" = "text";` a line, with // comments and empty lines between.
// A text without escapes is a slice of src. It returns the first error,
// with every line it could read.
func parseStrings(src string) (map[string]string, error) {
	texts := make(map[string]string, strings.Count(src, "\n"))
	var first error
	for n := 1; src != ""; n++ {
		line, rest, _ := strings.Cut(src, "\n")
		src = rest
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		key, line, ok := quoted(line)
		if ok {
			line = strings.TrimSpace(line)
			line, ok = strings.CutPrefix(line, "=")
		}
		var text string
		if ok {
			text, line, ok = quoted(strings.TrimSpace(line))
		}
		if !ok || strings.TrimSpace(line) != ";" {
			if first == nil {
				first = fmt.Errorf("line %d: want \"key\" = \"text\";", n)
			}
			continue
		}
		texts[key] = text
	}
	return texts, first
}

// quoted reads the string s starts with: \\, \", \n and \t are escapes.
func quoted(s string) (text, rest string, ok bool) {
	if !strings.HasPrefix(s, `"`) {
		return "", s, false
	}
	s = s[1:]
	end := strings.IndexByte(s, '"')
	if end >= 0 && !strings.Contains(s[:end], `\`) {
		return s[:end], s[end+1:], true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), s[i+1:], true
		case '\\':
			if i++; i == len(s) {
				return "", s, false
			}
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", s, false
}
