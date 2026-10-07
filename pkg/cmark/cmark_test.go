// SPDX-License-Identifier: Unlicense OR MIT

package cmark

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestRuntime(t testing.TB) *Runtime {
	t.Helper()
	r, err := NewRuntime(context.Background(), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(context.Background()) })
	return r
}

// find returns the first node of kind in n's tree, in pre-order.
func find(n *Node, kind Kind) *Node {
	if n == nil {
		return nil
	}
	if n.Kind == kind {
		return n
	}
	for _, c := range n.Children {
		if f := find(c, kind); f != nil {
			return f
		}
	}
	return nil
}

// A document of GFM comes back as its tree, with what each kind of node
// carries and where it is.
func TestParse(t *testing.T) {
	r := newTestRuntime(t)
	src := "# Заголовок\n\n" +
		"Text with *emph*, **strong**, ~~gone~~, `code` and [a link](https://example.com \"t\").[^1]\n\n" +
		"- [x] done\n- [ ] open\n\n" +
		"3. third\n4. fourth\n\n" +
		"| a | b |\n|:--|--:|\n| 1 | 2 |\n\n" +
		"```go\nfmt.Println()\n```\n\n" +
		"> quote\n\n---\n\n[^1]: The note.\n"
	root, err := r.Parse(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if root.Kind != Document {
		t.Fatalf("the root is %v", root.Kind)
	}
	if h := find(root, Heading); h == nil || h.Level != 1 || h.StartLine != 1 || find(h, Text).Literal != "Заголовок" {
		t.Fatalf("heading %+v", h)
	}
	for _, k := range []Kind{Emphasis, Strong, Strikethrough, Code, FootnoteReference, FootnoteDefinition, BlockQuote, ThematicBreak} {
		if find(root, k) == nil {
			t.Errorf("no node of kind %d", k)
		}
	}
	if l := find(root, Link); l == nil || l.URL != "https://example.com" || l.Title != "t" {
		t.Fatalf("link %+v", l)
	}
	list := find(root, List)
	if list == nil || list.Ordered || len(list.Children) != 2 || list.Children[0].Task != Done || list.Children[1].Task != Open {
		t.Fatalf("task list %+v", list)
	}
	var ordered *Node
	for _, c := range root.Children {
		if c.Kind == List && c.Ordered {
			ordered = c
		}
	}
	if ordered == nil || ordered.Start != 3 || ordered.Delimiter != Period || !ordered.Tight {
		t.Fatalf("ordered list %+v", ordered)
	}
	table := find(root, Table)
	if table == nil || len(table.Alignments) != 2 || table.Alignments[0] != 'l' || table.Alignments[1] != 'r' || !table.Children[0].Header {
		t.Fatalf("table %+v", table)
	}
	if code := find(root, CodeBlock); code == nil || code.Info != "go" || code.Literal != "fmt.Println()\n" {
		t.Fatalf("code block %+v", code)
	}
}

// Hostile documents stay bounded: too deep or too many nodes is an error,
// too long is not parsed, and quadratic inputs end in time.
func TestParseHostile(t *testing.T) {
	r := newTestRuntime(t)
	if _, err := r.Parse(context.Background(), []byte(strings.Repeat(">", 1000)+" deep")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("1,000 nested quotes: %v", err)
	}
	if _, err := r.Parse(context.Background(), []byte(strings.Repeat("a\n\n", 60_000))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("120,000 nodes: %v", err)
	}
	if _, err := r.Parse(context.Background(), make([]byte, DefaultLimits.MaxSource+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("a long document: %v", err)
	}
	for _, src := range []string{
		strings.Repeat("*a **a ", 100_000),
		strings.Repeat("[a](<", 200_000),
		strings.Repeat("[", 200_000) + strings.Repeat("]", 200_000),
		strings.Repeat("|a", 2000) + "\n" + strings.Repeat("|-", 2000) + "\n" + strings.Repeat(strings.Repeat("|a", 2000)+"\n", 200),
	} {
		start := time.Now()
		_, err := r.Parse(context.Background(), []byte(src))
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%.20q took %v", src, d)
		}
		t.Logf("%.20q: %v, %v", src, time.Since(start), err)
	}
	// It parses on after all of that.
	if _, err := r.Parse(context.Background(), []byte("fine")); err != nil {
		t.Fatal(err)
	}
}
