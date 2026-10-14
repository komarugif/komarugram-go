// SPDX-License-Identifier: Unlicense OR MIT
package physical

import (
	"gioui.org/io/key"
	"testing"
)

func TestPlatformPositionsAgree(t *testing.T) {
	for _, v := range []struct {
		x    string
		scan uint32
		mac  uint16
		dom  string
		want key.Name
	}{{"AC01", 30, 0, "KeyA", "A"}, {"AD01", 16, 12, "KeyQ", "Q"}, {"AB03", 46, 8, "KeyC", "C"}, {"AE01", 2, 18, "Digit1", "1"}, {"AE12", 13, 24, "Equal", "="}, {"AB08", 51, 43, "Comma", ","}, {"AD11", 26, 33, "BracketLeft", "["}, {"FK01", 59, 122, "F1", "F1"}, {"RTRN", 28, 36, "Enter", key.NameReturn}} {
		for name, got := range map[string]key.Name{"xkb": XKB(v.x), "win": Windows(v.scan, false), "mac": Mac(v.mac), "web": DOM(v.dom)} {
			if got != v.want {
				t.Errorf("%s %s: %q != %q", name, v.x, got, v.want)
			}
		}
	}
	if XKB("bogus") != "" || Mac(0xffff) != "" || Windows(0xff, false) != "" || DOM("Unidentified") != "" {
		t.Fatal("unknown position fabricated")
	}
}
func TestExtendedNavigationPositions(t *testing.T) {
	if Windows(0x1c, true) != key.NameEnter || Windows(0x4b, true) != key.NameLeftArrow || XKB("KPEN") != key.NameEnter {
		t.Fatal("extended positions lost")
	}
}

var physicalSink key.Name

func BenchmarkMacPosition(b *testing.B) {
	for b.Loop() {
		physicalSink = Mac(8)
	}
}
func BenchmarkXKBPosition(b *testing.B) {
	for b.Loop() {
		physicalSink = XKB("AB03")
	}
}
