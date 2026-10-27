// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

// The links of a bio are its addresses, domains alone among them, and its
// mentions; not a file's name, an e-mail or the punctuation after an
// address.
func TestPlainEntities(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"Пишу на Go: https://go.dev/doc. Канал — t.me/gonews, чат @gophers_ru!", []string{"url:https://go.dev/doc", "url:t.me/gonews", "mention:@gophers_ru"}},
		{"(www.example.com) и @bobby", []string{"url:www.example.com", "mention:@bobby"}},
		{"сайт example.org/about?x=1, блог пример.рф и komaru.dev:8080/a.", []string{"url:example.org/about?x=1", "url:пример.рф", "url:komaru.dev:8080/a"}},
		{"файл notes.txt, почта me@mail.ru, @abc", nil},
		{"🌅 @anna_s", []string{"mention:@anna_s"}},
		{"version 1.2.3 и a_b.com", nil},
	} {
		var got []string
		units := utf16.Encode([]rune(c.text))
		for _, e := range PlainEntities(c.text) {
			got = append(got, e.Kind+":"+string(utf16.Decode(units[e.Offset:e.Offset+e.Length])))
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}
