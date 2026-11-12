// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package alert

import (
	"slices"
	"testing"
)

// The last resort, xmessage, draws bytes as ISO 8859-1: a Russian text
// passed as UTF-8 was mojibake.
func TestXmessageText(t *testing.T) {
	title := "Ошибка приложения"
	text := "Не удалось показать окно «KomaruGram Go»:\n\nvulkan: no suitable device found — café…"
	var xmessage []string
	for _, c := range commands("linux", title, text) {
		if c[0] == "xmessage" {
			xmessage = c
		}
	}
	if xmessage == nil {
		t.Fatal("no xmessage among the commands")
	}
	got := xmessage[len(xmessage)-1]
	want := "Oshibka prilozheniya\n\nNe udalos pokazat okno \xabKomaruGram Go\xbb:\n\nvulkan: no suitable device found - caf\xe9..."
	if got != want {
		t.Errorf("xmessage text\n got %q\nwant %q", got, want)
	}
}

// yad, a GTK dialog, shows UTF-8 itself and comes before xmessage.
func TestYadBeforeXmessage(t *testing.T) {
	var names []string
	for _, c := range commands("linux", "t", "x") {
		names = append(names, c[0])
	}
	if y, x := slices.Index(names, "yad"), slices.Index(names, "xmessage"); y < 0 || y > x {
		t.Errorf("commands %v: yad should come before xmessage", names)
	}
}
