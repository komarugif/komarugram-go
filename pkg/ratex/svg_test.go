// SPDX-License-Identifier: Unlicense OR MIT

package ratex

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// A formula as SVG is well-formed XML, as wide and high as its box in ems
// and set on the baseline; KaTeX's glyphs are paths, a letter no KaTeX font
// has is text, and a colour of its own stays.
func TestSVG(t *testing.T) {
	r := newTestRuntime(t, DefaultLimits)
	list, err := r.Layout(context.Background(), `\frac{a}{b} + \text{ы} + \color{red}{x}`, true)
	if err != nil {
		t.Fatal(err)
	}
	svg := list.SVG("currentColor", `a & "b"`)
	elements := map[string]int{}
	d := xml.NewDecoder(strings.NewReader(svg))
	var root xml.StartElement
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("%v in %s", err, svg)
		}
		if e, ok := tok.(xml.StartElement); ok {
			if root.Name.Local == "" {
				root = e
			}
			elements[e.Name.Local]++
		}
	}
	attr := func(name string) string {
		for _, a := range root.Attr {
			if a.Name.Local == name {
				return a.Value
			}
		}
		return ""
	}
	if root.Name.Local != "svg" || attr("width") != num(list.Width, 3)+"em" || attr("style") != "vertical-align:"+num(-list.Depth, 3)+"em" || attr("fill") != "currentColor" {
		t.Fatalf("root %+v", root)
	}
	if elements["path"] < 4 || elements["rect"] == 0 || elements["text"] != 1 || elements["title"] != 1 {
		t.Fatalf("elements %v", elements)
	}
	if !strings.Contains(svg, ">ы</text>") || !strings.Contains(svg, `fill="rgba(255,0,0,1)"`) || !strings.Contains(svg, "<title>a &amp; &#34;b&#34;</title>") {
		t.Fatalf("svg %s", svg)
	}
}
