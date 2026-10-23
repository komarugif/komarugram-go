// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"strings"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/countries"
	"komarugram/internal/messenger/localization"
)

// countryEntry is a country as the picker lists it.
type countryEntry struct {
	iso, name, code, flag string
	// lower is the names as a search is matched against them, the language's
	// and English, digits the calling code's, which a search for a number
	// matches the start of.
	lower, digits string
}

// countryPicker is the dialog that picks a country from the list, searching
// by its name or its calling code. Picking one reports its ISO code.
type countryPicker struct {
	modal  modal
	search widget.Editor
	list   scroll.List
	rows   map[string]*surface
	cancel surface

	// entries are every country by name in the language of catalog, shown
	// the ones that match query.
	entries []countryEntry
	catalog localization.Catalog
	// fonts tell whether the window's fonts show a flag, in a picture or as
	// letters, or nothing.
	fonts emojiDrawn
	query string
	shown []countryEntry
}

func newCountryPicker() *countryPicker {
	return &countryPicker{
		search: widget.Editor{SingleLine: true, Submit: true},
		rows:   map[string]*surface{},
		list:   scroll.List{List: layout.List{Axis: layout.Vertical}},
	}
}

// Shown reports whether the dialog is open.
func (c *countryPicker) Shown() bool { return c.modal.Shown() }

// Open shows the dialog, with an empty search.
func (c *countryPicker) Open(gtx layout.Context) {
	c.search.SetText("")
	c.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	// The search takes the focus, not the dialog.
	c.modal.field = &c.search
	c.modal.Open()
	gtx.Execute(op.InvalidateCmd{})
}

// refresh makes the list the search asks for.
func (c *countryPicker) refresh(l localization.Catalog) {
	if c.entries == nil || c.catalog != l {
		c.catalog = l
		c.entries = c.entries[:0]
		// A name is found by its English spelling too, as by its own.
		english := localization.For("en")
		for _, country := range countries.All() {
			name := l.T("country." + country.ISO)
			c.entries = append(c.entries, countryEntry{
				iso: country.ISO, name: name, code: country.Code(), flag: country.Flag(),
				lower:  strings.ToLower(name) + "\n" + strings.ToLower(english.T("country."+country.ISO)),
				digits: country.Digits(),
			})
		}
		slices.SortFunc(c.entries, func(a, b countryEntry) int { return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)) })
		c.query = "\x00"
	}
	query := strings.ToLower(strings.TrimSpace(c.search.Text()))
	if query == c.query {
		return
	}
	c.query = query
	c.shown = c.shown[:0]
	// A code may be typed with its plus.
	digits := strings.TrimPrefix(query, "+")
	for _, e := range c.entries {
		if query == "" || strings.Contains(e.lower, query) || digits != "" && strings.HasPrefix(e.digits, digits) {
			c.shown = append(c.shown, e)
		}
	}
}

// Layout draws the dialog when it is open, and returns the ISO code of the
// country that was picked in this frame, if one was.
func (c *countryPicker) Layout(gtx layout.Context, l localization.Catalog) (string, bool) {
	if !c.modal.Shown() {
		return "", false
	}
	c.refresh(l)
	picked, ok := "", false
	pick := func(iso string) {
		if !ok {
			picked, ok = iso, true
			c.modal.Close()
		}
	}
	if !c.modal.closing {
		// Enter takes the first of the list.
		for {
			ev, more := c.search.Update(gtx)
			if !more {
				break
			}
			if _, submitted := ev.(widget.SubmitEvent); submitted && len(c.shown) > 0 {
				pick(c.shown[0].iso)
			}
		}
		if c.cancel.Clicked(gtx) {
			c.modal.Close()
		}
		for _, e := range c.shown {
			if r := c.rows[e.iso]; r != nil && r.Clicked(gtx) {
				pick(e.iso)
			}
		}
	}
	sc := scheme(gtx)
	c.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(420))
		height := min(gtx.Constraints.Max.Y-gtx.Dp(48), gtx.Dp(560))
		gtx.Constraints = layout.Exact(image.Pt(width, max(height, 0)))
		return c.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("login.field_country"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(40))
					fillRounded(gtx, sc.SurfaceContainerHigh, size, gtx.Dp(20))
					return layout.Inset{Top: 10, Bottom: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return flatEditor(gtx, &c.search, l.T("chat.search"))
					})
				}),
				vspace(8),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if len(c.shown) == 0 {
						return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return label(gtx, l.T("chat.not_found"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
							})
						})
					}
					return c.list.Layout(gtx, len(c.shown), func(gtx layout.Context, i int) layout.Dimensions {
						return c.row(gtx, c.shown[i])
					})
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return textButton(gtx, &c.cancel, l.T("history.cancel"))
					})
				}),
			)
		}, defaultCardPadding)
	})
	return picked, ok
}

// row draws a country of the list: its name, and its code at the end.
func (c *countryPicker) row(gtx layout.Context, e countryEntry) layout.Dimensions {
	r := c.rows[e.iso]
	if r == nil {
		r = new(surface)
		c.rows[e.iso] = r
	}
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(48))
	style := surfaceStyle{radius: gtx.Dp(10), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return r.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		pad := gtx.Dp(12)
		// The flags have a column of their own, so that the names keep to one
		// line. Where the font has no flags, as Windows' has not, a flag is the
		// two letters of its country.
		flags := gtx.Dp(countryFlagColumn)
		macro := op.Record(gtx.Ops)
		flag := gtx
		flag.Constraints = layout.Constraints{Max: image.Pt(flags, size.Y)}
		flagDims := label(flag, e.flag, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		flagCall := macro.Stop()
		offset(gtx, image.Pt(pad, (size.Y-flagDims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			flagCall.Add(gtx.Ops)
			return flagDims
		})
		// The code keeps its width; the name takes the rest and is cut.
		macro = op.Record(gtx.Ops)
		code := gtx
		code.Constraints = layout.Constraints{Max: image.Pt(size.X/2, size.Y)}
		codeDims := label(code, e.code, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
		codeCall := macro.Stop()
		offset(gtx, image.Pt(size.X-pad-codeDims.Size.X, (size.Y-codeDims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			codeCall.Add(gtx.Ops)
			return codeDims
		})
		name := gtx
		name.Constraints = layout.Constraints{Max: image.Pt(max(size.X-3*pad-flags-codeDims.Size.X, 0), size.Y)}
		macro = op.Record(gtx.Ops)
		dims := label(name, e.name, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		call := macro.Stop()
		offset(gtx, image.Pt(pad+flags, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return layout.Dimensions{Size: size}
	})
}

// countryFlagColumn is the width of the column of flags in the list.
const countryFlagColumn = unit.Dp(32)

// showsFlags reports whether the list has a column of flags: whether the
// window's fonts, or the emoji set chosen, show a flag as a picture or as the
// letters of its country, which Windows' font does. A system with no emoji
// font at all shows boxes, and a column of them says nothing.
func (c *countryPicker) showsFlags(gtx layout.Context) bool {
	c.fonts.use(wdk.GetMaterialTheme(gtx))
	return c.fonts.shows(countryFlagProbe)
}

// countryFlagProbe is a flag that stands for all of them: a font that shows
// one shows the others.
const countryFlagProbe = "\U0001F1FA\U0001F1F8"
