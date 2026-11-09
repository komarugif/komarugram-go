package historylog

import (
	"fmt"
	"testing"
)

// Kept, the journal's last lines are there to read, without a file, the
// oldest going past the limit; let go, they are gone and nothing is kept.
func TestKeepKeepsTheLastLines(t *testing.T) {
	Keep(true)
	defer Keep(false)
	if !On() {
		t.Fatal("kept, the journal is not on")
	}
	for i := range keepLimit + 5 {
		Printf("line %d", i)
	}
	lines, total := Lines()
	if len(lines) != keepLimit || total != keepLimit+5 {
		t.Fatalf("%d lines kept of %d", len(lines), total)
	}
	if lines[0].Text != "line 5" || lines[len(lines)-1].Text != fmt.Sprintf("line %d", keepLimit+4) {
		t.Fatalf("kept from %q to %q", lines[0].Text, lines[len(lines)-1].Text)
	}
	Keep(false)
	if On() {
		t.Fatal("let go, the journal is still on")
	}
	Printf("after")
	if lines, _ := Lines(); len(lines) != 0 {
		t.Fatalf("%d lines kept after", len(lines))
	}
}
