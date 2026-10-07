// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"bytes"
	"regexp"
	"strconv"
	"testing"
)

// The offsets of "HTML Format" point at the document and at the body
// between the fragment's comments, in bytes, for text of any letters.
func TestCFHTML(t *testing.T) {
	for _, doc := range []string{
		"<!DOCTYPE html>\n<html><head><title>Т</title></head><BODY class=\"x\"><p>Привет, <b>мир</b> 👋</p></body></html>\n",
		"<p>no body</p>",
	} {
		b := cfHTML([]byte(doc))
		if b[len(b)-1] != 0 {
			t.Fatalf("%q: no NUL", b)
		}
		b = b[:len(b)-1]
		offset := func(name string) int {
			m := regexp.MustCompile(name + `:(\d{10})\r\n`).FindSubmatch(b)
			if m == nil {
				t.Fatalf("%q: no %s", b, name)
			}
			n, _ := strconv.Atoi(string(m[1]))
			return n
		}
		start, end := offset("StartHTML"), offset("EndHTML")
		fragment, fragmentEnd := offset("StartFragment"), offset("EndFragment")
		if end != len(b) || string(bytes.ReplaceAll(bytes.ReplaceAll(b[start:end], []byte("<!--StartFragment-->"), nil), []byte("<!--EndFragment-->"), nil)) != doc {
			t.Fatalf("%q: document at %d–%d", b, start, end)
		}
		got := string(b[fragment:fragmentEnd])
		want := "<p>Привет, <b>мир</b> 👋</p>"
		if doc == "<p>no body</p>" {
			want = doc
		}
		if got != want || !bytes.HasSuffix(b[:fragment], []byte("<!--StartFragment-->")) || !bytes.HasPrefix(b[fragmentEnd:], []byte("<!--EndFragment-->")) {
			t.Fatalf("fragment %q in %q", got, b)
		}
	}
}
