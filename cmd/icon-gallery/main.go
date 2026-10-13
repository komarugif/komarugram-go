// SPDX-License-Identifier: Unlicense OR MIT

// Command icon-gallery shows every icon of
// golang.org/x/exp/shiny/materialdesign/icons, the set the UI draws its icons
// from, by category and with a search by name. A click on an icon copies its
// name as Go code, icons.Name.
package main

//go:generate go run gen.go

import (
	"fmt"
	"image"
	"io"
	"maps"
	"strings"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp"
	"gio-mw/exp/appearance"
	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/wdk/block"
	"gio-mw/widget/button"
	"gio-mw/widget/overlay"
	"gio-mw/widget/scroll"
	"gio-mw/widget/search"
	"gio-mw/widget/snackbar"

	"gioui.org/io/clipboard"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/appwindow"
)

type icon struct {
	category, name string
	data           []byte
}

const (
	cellWidth  = unit.Dp(128)
	cellHeight = unit.Dp(96)
	iconSize   = unit.Dp(32)
	padding    = unit.Dp(16)
)

func main() {
	opts := appwindow.Options{
		Title:  "Material icons",
		Width:  unit.Dp(1100),
		Height: unit.Dp(760),
		Locale: system.Locale{Language: "en", Direction: system.LTR},
	}
	appwindow.SetLanguage("en")
	appwindow.Main(opts, func(w *appwindow.Window) appwindow.Content {
		return newGallery(w)
	})
}

// themeMode is the theme the window is shown in.
type themeMode int

const (
	themeSystem themeMode = iota
	themeLight
	themeDark
	themeModes
)

var (
	themeLabels = [themeModes]string{"System theme", "Light theme", "Dark theme"}
	themeIcons  = [themeModes]wdk.IconWidget{
		wdk.RequireIconWidget(icons.DeviceBrightnessAuto),
		wdk.RequireIconWidget(icons.ImageWBSunny),
		wdk.RequireIconWidget(icons.ImageBrightness2),
	}
)

// isDark reports whether mode shows the dark theme while the system has
// the scheme system.
func (mode themeMode) isDark(system appearance.Scheme) bool {
	return mode == themeDark || mode == themeSystem && system == appearance.Dark
}

// row is a line of the gallery: the heading of a category, or up to a
// row's worth of the icons shown from first on.
type row struct {
	heading  bool
	category string
	count    int
	first, n int
}

type gallery struct {
	window      *appwindow.Window
	light, dark *token.Theme
	// mode is the theme chosen with themeButton, which cycles through them.
	mode        themeMode
	themeButton *button.Button
	// themeMeasure lays out every label of themeButton, unseen, to find
	// the widest, themeWidth, at the scale themeMetric.
	themeMeasure *button.Button
	themeWidth   int
	themeMetric  unit.Metric

	search  *search.Search
	focused bool
	query   string
	// lower holds the names of allIcons in lower case, for the search.
	lower []string
	// shown are the indices in allIcons of the icons the query matches.
	shown []int

	// rows are the lines of shown for columns icons a line.
	rows    []row
	columns int
	list    scroll.List

	// cells and widgets belong to allIcons by index; an icon is decoded
	// the first time it is drawn.
	cells   []widget.Clickable
	widgets []wdk.IconWidget

	overlay overlay.Overlay
}

func newGallery(w *appwindow.Window) *gallery {
	s := search.Bar()
	s.LeadingIcon.Icon = wdk.RequireIconWidget(icons.ActionSearch)
	s.TrailingIcon.Icon = wdk.RequireIconWidget(icons.ContentClear)
	s.SupportingText = "Search icons"
	g := &gallery{
		window:       w,
		themeButton:  button.Text(),
		themeMeasure: button.Text(),
		search:       s,
		lower:        make([]string, len(allIcons)),
		cells:        make([]widget.Clickable, len(allIcons)),
		widgets:      make([]wdk.IconWidget, len(allIcons)),
		list:         scroll.List{List: layout.List{Axis: layout.Vertical}},
	}
	for i, ic := range allIcons {
		g.lower[i] = strings.ToLower(ic.name)
	}
	g.filter("")
	return g
}

// Theme is the one chosen, or follows the system between light and dark.
func (g *gallery) Theme(gtx layout.Context) *token.Theme {
	return g.theme(gtx, g.mode.isDark(g.window.Appearance.Scheme()))
}

func (g *gallery) theme(gtx layout.Context, dark bool) *token.Theme {
	if dark {
		if g.dark == nil {
			g.dark = defaults.NewTheme(gtx, schemes.SchemeBaselineDark())
		}
		return g.dark
	}
	if g.light == nil {
		g.light = defaults.NewTheme(gtx, schemes.SchemeBaselineLight())
	}
	return g.light
}

// normalizeQuery makes a query match names however it is written: in any
// case, with spaces or underscores between words, or as Go code.
func normalizeQuery(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	q = strings.TrimPrefix(q, "icons.")
	return strings.NewReplacer(" ", "", "_", "", "-", "").Replace(q)
}

// filter shows the icons whose names contain query.
func (g *gallery) filter(query string) {
	g.query = query
	g.shown = g.shown[:0]
	for i, name := range g.lower {
		if strings.Contains(name, query) {
			g.shown = append(g.shown, i)
		}
	}
	g.columns = 0
	g.list.Position = layout.Position{}
}

// arrange splits the icons shown into rows of columns icons, each category
// under a heading of its own.
func (g *gallery) arrange(columns int) {
	g.columns = columns
	g.rows = g.rows[:0]
	for start := 0; start < len(g.shown); {
		category := allIcons[g.shown[start]].category
		end := start
		for end < len(g.shown) && allIcons[g.shown[end]].category == category {
			end++
		}
		g.rows = append(g.rows, row{heading: true, category: category, count: end - start})
		for i := start; i < end; i += columns {
			g.rows = append(g.rows, row{first: i, n: min(columns, end-i)})
		}
		start = end
	}
}

func (g *gallery) Update(gtx layout.Context) {
	if !g.focused {
		g.focused = true
		g.search.Focus(gtx)
	}
	if g.themeButton.Clicked(gtx) {
		// The window takes the new theme from Theme on its next frame.
		g.mode = (g.mode + 1) % themeModes
		g.window.Invalidate()
	}
	if g.search.TrailingIcon.Clickable.Clicked(gtx) {
		g.search.ClearText()
	}
	if q := normalizeQuery(g.search.GetText()); q != g.query {
		g.filter(q)
	}
	// The search shows its clear button only while it has a label.
	g.search.TrailingIcon.Label = ""
	if g.search.GetText() != "" {
		g.search.TrailingIcon.Label = "Clear"
	}
	for _, i := range g.shown {
		if g.cells[i].Clicked(gtx) {
			name := "icons." + allIcons[i].name
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(name))})
			g.notify("Copied " + name)
		}
	}
	g.overlay.Update(gtx)
}

func (g *gallery) notify(msg string) {
	g.overlay.Clear()
	g.overlay.Show(overlay.NewItem(snackbar.Plain(msg).Layout, block.GravityBottomCenter).WithDuration(2 * time.Second))
}

func (g *gallery) Layout(gtx layout.Context) {
	exp.Background(gtx)
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(g.layoutHeader),
		layout.Flexed(1, g.layoutGrid),
	)
	g.overlay.Layout(gtx)
}

func (g *gallery) layoutHeader(gtx layout.Context) layout.Dimensions {
	return layout.UniformInset(padding).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// The search takes the room the button leaves, up to its own
				// widest, and the two are centered together.
				gtx.Constraints.Min.X = 0
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, g.search.Layout),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(g.layoutThemeButton),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				count := fmt.Sprintf("%d icons", len(allIcons))
				if len(g.shown) != len(allIcons) {
					count = fmt.Sprintf("%d of %d icons", len(g.shown), len(allIcons))
				}
				return g.label(gtx, count+" · click one to copy its name", token.TypestyleBodyMedium, 1)
			}),
		)
	})
}

// layoutThemeButton takes the room of the button's widest label whatever
// the label is, so that the search centered with it stays in place when
// the theme changes.
func (g *gallery) layoutThemeButton(gtx layout.Context) layout.Dimensions {
	if g.themeWidth == 0 || g.themeMetric != gtx.Metric {
		g.themeWidth, g.themeMetric = g.widestThemeLabel(gtx), gtx.Metric
	}
	dims := g.themeButton.LayoutWithIcon(gtx, themeLabels[g.mode], themeIcons[g.mode])
	dims.Size.X = max(dims.Size.X, g.themeWidth)
	return dims
}

// widestThemeLabel measures the theme button with each label in each
// theme: a dark theme sets emphasized text in a lighter weight, which is
// narrower.
func (g *gallery) widestThemeLabel(gtx layout.Context) int {
	widest := 0
	macro := op.Record(gtx.Ops)
	for _, dark := range []bool{false, true} {
		mgtx := gtx
		mgtx.Constraints.Min = image.Point{}
		mgtx.Values = maps.Clone(gtx.Values)
		wdk.InitMaterialThemeInContext(mgtx, g.theme(gtx, dark))
		for mode := range themeModes {
			widest = max(widest, g.themeMeasure.LayoutWithIcon(mgtx, themeLabels[mode], themeIcons[mode]).Size.X)
		}
	}
	macro.Stop()
	return widest
}

func (g *gallery) layoutGrid(gtx layout.Context) layout.Dimensions {
	if len(g.shown) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return exp.BodyL(gtx, fmt.Sprintf("No icon's name contains %q", g.query))
		})
	}
	cell := gtx.Dp(cellWidth)
	columns := max(1, (gtx.Constraints.Max.X-2*gtx.Dp(padding))/cell)
	if columns != g.columns {
		g.arrange(columns)
	}
	// The grid is centered: its rows start where the widest would.
	left := (gtx.Constraints.Max.X - columns*cell) / 2
	return g.list.Layout(gtx, len(g.rows), func(gtx layout.Context, index int) layout.Dimensions {
		r := g.rows[index]
		if r.heading {
			return layout.Inset{
				Left: unit.Dp(gtx.Metric.PxToDp(left)) + unit.Dp(8), Top: padding, Bottom: unit.Dp(8),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return exp.TitleM(gtx, fmt.Sprintf("%s · %d", r.category, r.count))
			})
		}
		height := gtx.Dp(cellHeight)
		for k := range r.n {
			i := g.shown[r.first+k]
			stack := op.Offset(image.Pt(left+k*cell, 0)).Push(gtx.Ops)
			cgtx := gtx
			cgtx.Constraints = layout.Exact(image.Pt(cell, height))
			g.layoutCell(cgtx, i)
			stack.Pop()
		}
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, height)}
	})
}

func (g *gallery) layoutCell(gtx layout.Context, i int) layout.Dimensions {
	theme := wdk.GetMaterialTheme(gtx)
	return g.cells[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Max
		area := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(12))
		switch {
		case g.cells[i].Pressed():
			paint.FillShape(gtx.Ops, theme.Scheme.SurfaceContainerHighest.AsNRGBA(), area.Op(gtx.Ops))
		case g.cells[i].Hovered():
			paint.FillShape(gtx.Ops, theme.Scheme.SurfaceContainerHigh.AsNRGBA(), area.Op(gtx.Ops))
		}
		pointer.CursorPointer.Add(gtx.Ops)

		if g.widgets[i] == nil {
			g.widgets[i] = wdk.RequireIconWidget(allIcons[i].data)
		}
		top := gtx.Dp(14)
		side := gtx.Dp(iconSize)
		stack := op.Offset(image.Pt((size.X-side)/2, top)).Push(gtx.Ops)
		igtx := gtx
		igtx.Constraints = layout.Exact(image.Pt(side, side))
		g.widgets[i](igtx, theme.Scheme.Surface.OnColor)
		stack.Pop()

		// The name without its category, which the heading gives.
		inset := gtx.Dp(6)
		stack = op.Offset(image.Pt(inset, top+side+gtx.Dp(8))).Push(gtx.Ops)
		lgtx := gtx
		lgtx.Constraints = layout.Exact(image.Pt(size.X-2*inset, size.Y))
		lgtx.Constraints.Min.Y = 0
		name := strings.TrimPrefix(allIcons[i].name, allIcons[i].category)
		g.label(lgtx, name, token.TypestyleLabelSmall, 2)
		stack.Pop()
		return layout.Dimensions{Size: size}
	})
}

func (g *gallery) label(gtx layout.Context, s string, style token.Typestyle, lines int) layout.Dimensions {
	theme := wdk.GetMaterialTheme(gtx)
	return wdk.LayoutLabel(gtx, wdk.LabelStyle{
		Alignment:  text.Middle,
		Color:      theme.Scheme.SurfaceVariant.OnColor,
		MaxLines:   lines,
		Typestyle:  style,
		WrapPolicy: text.WrapGraphemes,
	}, s)
}
