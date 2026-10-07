// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/op/paint"
	"gioui.org/text"
	"image"
	"image/color"
	"image/png"
	"komarugram/internal/messenger/styledtext"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"gioui.org/layout"
	"komarugram/internal/messenger/codehighlight"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

func TestRenderTextBlocks(t *testing.T) {
	dir := os.Getenv("TEXT_BLOCKS_PNG_DIR")
	if dir == "" {
		t.Skip("set TEXT_BLOCKS_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	checkTextAfterBitmap(t, filepath.Join(dir, "text-bitmap-tail.png"))
	text, entities := mockstore.TextBlocksExample()
	waitCodeColors(t, model.TextRuns(text, entities))
	for _, dark := range []bool{false, true} {
		for _, width := range []int{360, 640} {
			for _, expanded := range []bool{false, true} {
				p := newChatPage(benchmarkHistory{}, func() {})
				p.images = &imageOps{}
				m := model.Message{Key: model.MessageKey{MessageID: 1}, Text: text, Entities: entities, Date: time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC), ContentRevision: 1}
				r := &messageRow{revision: 1, runs: model.TextRuns(text, entities)}
				r.prepareTextBlocks()
				for i := range r.textBlocks {
					r.textBlocks[i].expanded = expanded
				}
				p.rows = map[model.MessageID]*messageRow{1: r}
				name := fmt.Sprintf("text-blocks-%d-dark-%t-expanded-%t.png", width, dark, expanded)
				renderToast(t, filepath.Join(dir, name), image.Pt(width, 920), dark, func(gtx layout.Context) {
					p.images.BeginFrame()
					layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Point{}
						return p.row(gtx, m, false, 0, localization.For("ru"), false)
					})
					p.images.EndFrame()
				})
				p.Close()
			}
		}
	}
	// What clicks act on, the dates written in the reader's language.
	text, entities = mockstore.EntitiesExample(time.Date(2026, 10, 5, 12, 30, 0, 0, time.Local))
	for _, dark := range []bool{false, true} {
		for _, lang := range []string{"ru", "en"} {
			p := newChatPage(benchmarkHistory{}, func() {})
			p.images = &imageOps{}
			p.rows = map[model.MessageID]*messageRow{}
			m := model.Message{Key: model.MessageKey{MessageID: 1}, Text: text, Entities: entities, Date: time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC), ContentRevision: 1}
			renderToast(t, filepath.Join(dir, fmt.Sprintf("text-entities-%s-dark-%t.png", lang, dark)), image.Pt(360, 260), dark, func(gtx layout.Context) {
				gtx.Now = time.Date(2026, 10, 5, 12, 30, 0, 0, time.Local)
				p.images.BeginFrame()
				layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Point{}
					return p.row(gtx, m, false, 0, localization.For(lang), false)
				})
				p.images.EndFrame()
			})
			p.Close()
		}
	}
}

// TestRenderCodeColors draws code in several languages, every class of
// color among them, in both themes, for looking at the palettes:
//
//	CODE_COLORS_PNG_DIR=/tmp/code go test ./internal/messenger/ui -run RenderCodeColors
func TestRenderCodeColors(t *testing.T) {
	dir := os.Getenv("CODE_COLORS_PNG_DIR")
	if dir == "" {
		t.Skip("set CODE_COLORS_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	blocks := [][2]string{
		{"javascript", "/** Greets. */\nclass Greeter extends Base {\n  greet(name = 'мир') {\n    return `Привет, ${name}!` + 42; // done\n  }\n}"},
		{"python", "@cache\ndef area(r: float) -> float:\n    \"\"\"Area of a circle.\"\"\"\n    return 3.14 * r ** 2  # approx"},
		{"html", "<!-- note -->\n<a href=\"/x\" class=\"b\">&amp; link</a>"},
		{"diff", "@@ -1,2 +1,2 @@\n-old line\n+new line\n context"},
		{"go", "func main() {\n\tif ok {\n\t\tprintln(\"tab\")\n\t}\n}"},
		{"", "plain\tcolumns\tafter tabs"},
	}
	var text strings.Builder
	var entities []model.Entity
	for _, b := range blocks {
		text.WriteString(b[0] + ":\n")
		start := len(utf16.Encode([]rune(text.String())))
		text.WriteString(b[1])
		entities = append(entities, model.Entity{Kind: "pre", Offset: start, Length: len(utf16.Encode([]rune(b[1]))), Language: b[0]})
		text.WriteString("\n")
	}
	// Inline code, in Latin and Cyrillic letters.
	start := len(utf16.Encode([]rune(text.String())))
	text.WriteString("Inline go build and запуск.")
	entities = append(entities, model.Entity{Kind: "code", Offset: start + 7, Length: 8}, model.Entity{Kind: "code", Offset: start + 20, Length: 6})
	runs := model.TextRuns(text.String(), entities)
	waitCodeColors(t, runs)
	for _, dark := range []bool{false, true} {
		p := newChatPage(benchmarkHistory{}, func() {})
		p.images = &imageOps{}
		m := model.Message{Key: model.MessageKey{MessageID: 1}, Text: text.String(), Entities: entities, Date: time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC), ContentRevision: 1}
		p.rows = map[model.MessageID]*messageRow{1: {revision: 1, runs: runs}}
		renderToast(t, filepath.Join(dir, fmt.Sprintf("code-colors-dark-%t.png", dark)), image.Pt(640, 1300), dark, func(gtx layout.Context) {
			p.images.BeginFrame()
			layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return p.row(gtx, m, false, 0, localization.For("ru"), false)
			})
			p.images.EndFrame()
		})
		p.Close()
	}
}

// waitCodeColors colors the code blocks of runs, so that a render shows
// them as they are once their colors came.
func waitCodeColors(t *testing.T, runs []model.TextRun) {
	t.Helper()
	r := &messageRow{runs: runs}
	r.prepareTextBlocks()
	for _, b := range r.textBlocks {
		run := r.runs[b.first]
		if !run.Pre || run.Language == "" {
			continue
		}
		var text strings.Builder
		for _, run := range r.runs[b.first:b.end] {
			text.WriteString(run.Text)
		}
		done := make(chan struct{})
		codehighlight.Request(codehighlight.KeyOf(run.Language, text.String()), run.Language, text.String(), func() { close(done) })
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("no colors for the code")
		}
	}
}

// A transparent bitmap changes Gio's paint material just like a color emoji.
// The vector glyph in the next 32-glyph batch must restore the text color.
type textTestEmoji struct{}

func (textTestEmoji) Match(r []rune) (int, int) {
	if len(r) > 0 && r[0] == '👋' {
		return 0, 1
	}
	return 0, 0
}
func (textTestEmoji) Image(_, size int) image.Image {
	return image.NewRGBA(image.Rect(0, 0, size, size))
}
func checkTextAfterBitmap(t *testing.T, path string) {
	t.Helper()
	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()), text.WithEmojiImages(textTestEmoji{}))
	var last image.Rectangle
	renderToast(t, path, image.Pt(800, 80), false, func(gtx layout.Context) {
		paint.Fill(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		gtx.Constraints.Min = image.Point{}
		style := styledtext.Text(shaper, styledtext.SpanStyle{Font: font.Font{Typeface: "Go Mono"}, Content: strings.Repeat("a", 30) + "👋aX", Size: 20, Color: color.NRGBA{A: 255}})
		style.Decorate = func(_ layout.Context, f styledtext.Fragment, draw func()) {
			for _, c := range f.Clusters {
				if c.Start == 32 {
					last = c.Bounds
				}
			}
			draw()
		}
		style.Layout(gtx, nil)
	})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if last.Empty() {
		t.Fatal("missing trailing glyph hit region")
	}
	for y := last.Min.Y; y < last.Max.Y; y++ {
		for x := last.Min.X; x < last.Max.X; x++ {
			r, g, b, _ := im.At(x, y).RGBA()
			if r < 0x8000 && g < 0x8000 && b < 0x8000 {
				return
			}
		}
	}
	t.Fatal("bitmap hid the following vector glyph")
}
