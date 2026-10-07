// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"net/url"
	"strings"
	"unicode/utf16"

	"gio-mw/token"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/widget"
)

type sharedLinkRow struct {
	open  widget.Clickable
	links []widget.Clickable
}

// Link entities use UTF-16 offsets. Styling entities never enter this renderer.
func sharedURLs(m model.Message) []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		if u, ok := safeURL(raw); ok && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	units := utf16.Encode([]rune(m.Text))
	for _, e := range m.Entities {
		if e.Kind != "url" {
			continue
		}
		if e.URL != "" {
			add(e.URL)
		} else if e.Offset >= 0 && e.Length > 0 && e.Offset <= len(units) && e.Length <= len(units)-e.Offset {
			add(string(utf16.Decode(units[e.Offset : e.Offset+e.Length])))
		}
	}
	if len(out) == 0 && m.WebPage != nil {
		add(m.WebPage.URL)
	}
	return out
}
func (p *chatInfo) linkRow(gtx layout.Context, m model.Message, l localization.Catalog) layout.Dimensions {
	urls := sharedURLs(m)
	if len(urls) == 0 {
		return layout.Dimensions{}
	}
	r := p.linkRows[m.Key]
	if r == nil {
		r = &sharedLinkRow{}
		p.linkRows[m.Key] = r
	}
	if len(r.links) != len(urls) {
		r.links = make([]widget.Clickable, len(urls))
	}
	if r.open.Clicked(gtx) {
		p.renderer.askLink(urls[0])
	}
	for i := range urls {
		if r.links[i].Clicked(gtx) {
			p.renderer.askLink(urls[i])
		}
	}
	u, _ := url.Parse(urls[0])
	title := u.Hostname()
	snippet := sharedSnippet(m)
	var photo *model.MessageMedia
	if w := m.WebPage; w != nil {
		photo = w.Photo
		if w.Title != "" {
			title = w.Title
		} else if w.Site != "" {
			title = w.Site
		}
		if snippet == "" || snippet == urls[0] {
			snippet = w.Description
		}
	}
	return layout.Inset{Top: 4, Bottom: 4, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return r.open.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						size := image.Pt(gtx.Dp(72), gtx.Dp(72))
						gtx.Constraints = layout.Exact(size)
						defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(10)).Push(gtx.Ops).Pop()
						fillRect(gtx, scheme(gtx).PrimaryContainer.Color, size)
						if photo != nil {
							variant := *photo.Variant(size.X, size.Y)
							if variant.Preview == nil {
								variant.Preview = photo.Preview
							}
							st := p.renderer.media.StatusFit(model.Message{Key: m.Key, Kind: model.MessagePhoto, Media: &variant}, false, size, true)
							im := st.Frame
							if im == nil {
								im = st.Preview
							}
							if im != nil {
								widget.Image{Src: p.renderer.images.Op(im), Fit: widget.Cover}.Layout(gtx)
								return layout.Dimensions{Size: size}
							}
						}
						layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return label(gtx, initials(title), token.TypestyleHeadlineMedium, scheme(gtx).PrimaryContainer.OnColor, 1)
						})
						return layout.Dimensions{Size: size}
					})
				}), layout.Rigid(layout.Spacer{Width: 12}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, title, token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 1)
					})}
					if snippet != "" {
						rows = append(rows, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return label(gtx, snippet, token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 3)
						}))
					}
					for i, link := range urls {
						rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return r.links[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return label(gtx, link, token.TypestyleBodyMedium, scheme(gtx).Primary.Color, 1)
								})
							})
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
				}),
			)
		}, 12)
	})
}

func sharedSnippet(m model.Message) string {
	var b strings.Builder
	for _, r := range model.TextRuns(m.Text, m.Entities) {
		if r.Spoiler {
			b.WriteString("•••")
		} else {
			b.WriteString(r.Text)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
