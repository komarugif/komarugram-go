// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"log"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

var (
	iconFrozenTerms    = wdk.RequireIconWidget(icons.ContentBlock)
	iconFrozenReadOnly = wdk.RequireIconWidget(icons.ActionLock)
	iconFrozenAppeal   = wdk.RequireIconWidget(icons.ActionHourglassEmpty)
)

// frozenView shows that Telegram froze the window's account, as Telegram
// Desktop does: a bar over the chat list and one in place of the composer,
// either of which opens a dialog that tells what freezing means and until
// when it can be appealed. Editing the profile opens the dialog too.
type frozenView struct {
	source        model.FreezeSource
	modal         modal
	appeal, close *button.Button
	bar, composer surface
}

func newFrozenView(store model.Store) *frozenView {
	source, _ := store.(model.FreezeSource)
	return &frozenView{source: source, appeal: button.Filled(), close: button.Text()}
}

// Freeze returns the account's freeze, the zero one when it is not frozen.
func (f *frozenView) Freeze() model.Freeze {
	if f == nil || f.source == nil {
		return model.Freeze{}
	}
	return f.source.Freeze()
}

// Frozen reports whether the account is frozen.
func (f *frozenView) Frozen() bool { return f.Freeze().Frozen() }

// Show opens the dialog.
func (f *frozenView) Show() {
	if !f.modal.Shown() {
		f.modal.Open()
	}
}

// Update opens the dialog from the bars and handles its buttons.
func (f *frozenView) Update(gtx layout.Context) {
	if f.bar.Clicked(gtx) || f.composer.Clicked(gtx) {
		f.Show()
	}
	if !f.modal.Shown() {
		return
	}
	if f.close.Clicked(gtx) {
		f.modal.Close()
	}
	if f.appeal.Clicked(gtx) {
		if url := f.Freeze().AppealURL; url != "" {
			go func() {
				if err := openBrowser(url); err != nil {
					log.Printf("open appeal: %v", err)
				}
			}()
		}
	}
}

// layoutBar draws the bar over the chat list.
func (f *frozenView) layoutBar(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(56))
	fillRect(gtx, sc.SurfaceContainerLow, size)
	style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	f.bar.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		return f.layoutNotice(gtx, l.T("frozen.bar_title"), l.T("frozen.details"), size)
	})
	fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
	return layout.Dimensions{Size: size}
}

// layoutComposer draws the bar in place of the composer, of size.
func (f *frozenView) layoutComposer(gtx layout.Context, l localization.Catalog, size image.Point, radius int) layout.Dimensions {
	sc := scheme(gtx)
	style := surfaceStyle{radius: radius, background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return f.composer.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		return f.layoutNotice(gtx, l.T("frozen.restrict_title"), l.T("frozen.details"), size)
	})
}

// layoutNotice draws title in the error color over details, centered in size.
func (f *frozenView) layoutNotice(gtx layout.Context, title, details string, size image.Point) layout.Dimensions {
	sc := scheme(gtx)
	gtx.Constraints = layout.Exact(size)
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return centeredLabel(gtx, title, token.TypestyleTitleSmall, sc.Error.Color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return centeredLabel(gtx, details, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
			}),
		)
	})
}

// Layout draws the dialog when it is open.
func (f *frozenView) Layout(gtx layout.Context, l localization.Catalog) {
	if !f.modal.Shown() {
		return
	}
	sc := scheme(gtx)
	info := f.Freeze()
	until := "—"
	if !info.Until.IsZero() {
		until = info.Until.Local().Format("02.01.2006")
	}
	row := func(icon wdk.IconWidget, title, text string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, sc.Primary.Color)
					}),
					layout.Rigid(layout.Spacer{Width: 16}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, title, token.TypestyleTitleSmall, sc.Surface.OnColor, 2)
							}),
							vspace(2),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
							}),
						)
					}),
				)
			})
		})
	}
	f.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		return f.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return centeredLabel(gtx, l.T("frozen.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 2)
				}),
				vspace(16),
				row(iconFrozenTerms, l.T("frozen.subtitle1"), l.T("frozen.text1")),
				row(iconFrozenReadOnly, l.T("frozen.subtitle2"), l.T("frozen.text2")),
				row(iconFrozenAppeal, l.T("frozen.subtitle3"), l.Format("frozen.text3", map[string]string{"link": "@SpamBot", "date": until})),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return f.close.Layout(gtx, l.T("frozen.close"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if info.AppealURL == "" {
								return layout.Dimensions{}
							}
							return f.appeal.Layout(gtx, l.T("frozen.appeal"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
}
