// SPDX-License-Identifier: Unlicense OR MIT

package ratex

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sync"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
)

// KaTeX's fonts, as RaTeX ships them, under the SIL Open Font License
// (fonts/OFL.txt, fonts/NOTICE), gzipped: 308 KB instead of 572.
//
//go:embed fonts/*.ttf.gz
var fontFiles embed.FS

// outline is a glyph's outline, in its font's units, its y up.
type outline struct {
	segments []opentype.Segment
	upem     float32
}

type glyphKey struct {
	font string
	r    rune
}

// glyphs keeps the KaTeX faces read and the outlines of their glyphs.
var glyphs struct {
	mu       sync.Mutex
	faces    map[string]*font.Face
	outlines map[glyphKey]*outline
}

// glyph returns the outline of code in the KaTeX font id names, nil when
// the font is not one of KaTeX's or has no glyph for it.
func glyph(id string, code rune) *outline {
	r := ttfChar(id, code)
	g := &glyphs
	g.mu.Lock()
	defer g.mu.Unlock()
	key := glyphKey{id, r}
	if o, ok := g.outlines[key]; ok {
		return o
	}
	if g.outlines == nil {
		g.outlines, g.faces = map[glyphKey]*outline{}, map[string]*font.Face{}
	}
	face, ok := g.faces[id]
	if !ok {
		face = readFace(id)
		g.faces[id] = face
	}
	var o *outline
	if face != nil {
		if gid, ok := face.NominalGlyph(r); ok && gid != 0 {
			if data, ok := face.GlyphData(gid).(font.GlyphOutline); ok {
				o = &outline{segments: data.Segments, upem: float32(face.Upem())}
			}
		}
	}
	g.outlines[key] = o
	return o
}

// readFace reads KaTeX's font id, nil when it has none of that name.
func readFace(id string) *font.Face {
	data, err := fontFiles.ReadFile("fonts/KaTeX_" + id + ".ttf.gz")
	if err != nil {
		return nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	ttf, err := io.ReadAll(zr)
	if err != nil {
		return nil
	}
	face, err := font.ParseTTF(bytes.NewReader(ttf))
	if err != nil {
		return nil
	}
	return face
}

// ttfChar is the character code's glyph in KaTeX's font id: KaTeX keeps the
// letters and digits of Unicode's Mathematical Alphanumeric Symbols at
// ASCII's places, as RaTeX's katex_ttf_glyph_char finds them
// (crates/ratex-font/src/math_alpha.rs, MIT).
func ttfChar(id string, code rune) rune {
	if code <= 0x7f {
		return code
	}
	letters := []struct {
		base rune
		font string
	}{
		{0x1D400, "Main-Bold"},
		{0x1D434, "Math-Italic"},
		{0x1D468, "Math-BoldItalic"},
		{0x1D504, "Fraktur-Regular"},
		{0x1D56C, "Fraktur-Bold"},
		{0x1D5A0, "SansSerif-Regular"},
		{0x1D5D4, "SansSerif-Bold"},
		{0x1D608, "SansSerif-Italic"},
		{0x1D670, "Typewriter-Regular"},
	}
	for _, l := range letters {
		if code >= l.base && code < l.base+52 && l.font == id {
			if i := code - l.base; i < 26 {
				return 'A' + i
			} else {
				return 'a' + i - 26
			}
		}
	}
	switch {
	case code >= 0x1D538 && code < 0x1D538+26 && id == "AMS-Regular":
		return 'A' + code - 0x1D538
	case code >= 0x1D49C && code < 0x1D49C+26 && id == "Script-Regular":
		return 'A' + code - 0x1D49C
	case code == 0x1D55C && id == "AMS-Regular":
		return 'k'
	}
	for _, d := range []struct {
		base rune
		font string
	}{{0x1D7CE, "Main-Bold"}, {0x1D7E2, "SansSerif-Regular"}, {0x1D7EC, "SansSerif-Bold"}, {0x1D7F6, "Typewriter-Regular"}} {
		if code >= d.base && code < d.base+10 && d.font == id {
			return '0' + code - d.base
		}
	}
	return code
}
