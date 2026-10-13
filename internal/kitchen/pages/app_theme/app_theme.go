// SPDX-License-Identifier: Unlicense OR MIT

package app_theme

import (
	"komarugram/internal/kitchen/services"
	"log"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp"
	"gio-mw/exp/examples"
	"gio-mw/exp/router"
	"gio-mw/token"
	"gio-mw/wdk/block"
	"gio-mw/widget/button"
	"gio-mw/widget/card"
	"gio-mw/widget/dialog"
	"gio-mw/widget/input"
	"gio-mw/widget/rail"
	"gio-mw/widget/search"
	"gio-mw/widget/slider"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/localization/viewtexts"
	"komarugram/internal/motion"
)

type Page struct {
	lightBaseline *button.Button
	darkBaseline  *button.Button
	darkGreen     *button.Button
	lightGreen    *button.Button
	angular       *button.Button
	animations    *motion.View
	previewCards  []*card.Card
}

func NewPage() router.PageWidget {
	navigationService := services.GetNavigationService()
	routesWithPreview := navigationService.GetRoutesWithPreview()

	previewCards := make([]*card.Card, len(routesWithPreview))
	for idx, route := range routesWithPreview {
		previewCards[idx] = &card.Card{
			Kind: card.Outlined,
			Data: route,
		}
	}
	var animations *motion.View
	if settings := services.GetMotionService(); settings != nil {
		animations = motion.NewView(settings, viewtexts.Motion(localization.For("en")))
	}
	return &Page{
		animations:    animations,
		lightBaseline: button.Text(),
		darkBaseline:  button.Text(),
		darkGreen:     button.Text(),
		lightGreen:    button.Text(),
		angular:       button.Text(),
		previewCards:  previewCards,
	}
}

func (p *Page) IsWide() bool {
	return false
}

func (p *Page) Update(gtx layout.Context) {
	if p.animations != nil {
		p.animations.Update(gtx)
	}
	notificationService := services.GetNotificationService()
	themeService := services.GetThemeService()
	if p.lightBaseline.Clicked(gtx) {
		themeService.SetTheme(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		notificationService.ShowNotification("Light Baseline theme activated.")
		gtx.Execute(op.InvalidateCmd{})
	} else if p.darkBaseline.Clicked(gtx) {
		themeService.SetTheme(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineDark()))
		notificationService.ShowNotification("Dark Baseline theme activated.")
		gtx.Execute(op.InvalidateCmd{})
	} else if p.lightGreen.Clicked(gtx) {
		themeService.SetTheme(gtx, defaults.NewTheme(gtx, schemes.SchemeGreenLight()))
		notificationService.ShowNotification("Light Green theme activated.")
		gtx.Execute(op.InvalidateCmd{})
	} else if p.darkGreen.Clicked(gtx) {
		themeService.SetTheme(gtx, defaults.NewTheme(gtx, schemes.SchemeGreenDarkMediumContrast()))
		notificationService.ShowNotification("Dark Green (medium contrast) theme activated.")
		gtx.Execute(op.InvalidateCmd{})
	} else if p.angular.Clicked(gtx) {
		buildCustomTheme(themeService, gtx)
		notificationService.ShowNotification("Angular theme activated.")
		gtx.Execute(op.InvalidateCmd{})
	}

	navigationService := services.GetNavigationService()
	for _, previewCard := range p.previewCards {
		if previewCard.Clickable.Clicked(gtx) {
			route := previewCard.Data.(*router.Route)
			err := navigationService.NavigateTo(route.PageUrl)
			if err != nil {
				log.Fatal(err)
			}
			break
		}
	}
}

func buildCustomTheme(themeService services.AppThemes, gtx layout.Context) {
	materialTheme := defaults.NewTheme(gtx, schemes.SchemeAngularDarkMediumContrast())
	materialTheme.Scheme.Shadow = materialTheme.Scheme.PrimaryFixed.Color
	themeService.SetTheme(gtx, materialTheme)

	angledCorners := token.CornerShapes{
		TopStart: token.CornerShape{
			Kind: token.CornerKindChamfer,
			Size: unit.Dp(16),
		},
		TopEnd: token.CornerShape{
			Kind: token.CornerKindChamfer,
			Size: unit.Dp(8),
		},
		BottomStart: token.CornerShape{
			Kind: token.CornerKindChamfer,
			Size: unit.Dp(8),
		},
		BottomEnd: token.CornerShape{
			Kind: token.CornerKindChamfer,
			Size: unit.Dp(16),
		},
	}
	fullAngledCorners := angledCorners
	fullAngledCorners.TopStart.Size = 0
	fullAngledCorners.TopStart.AdaptToSize = true
	pressedAngledCorners := fullAngledCorners
	pressedAngledCorners.TopEnd.Size = unit.Dp(4)
	pressedAngledCorners.BottomStart.Size = unit.Dp(4)

	{
		widgetTheme := button.BuildElevatedTheme(gtx)
		widgetTheme.EnabledContainerShape = fullAngledCorners
		widgetTheme.PressedContainerShape = pressedAngledCorners

		widgetTheme = button.BuildFilledTheme(gtx)
		widgetTheme.EnabledContainerShape = fullAngledCorners
		widgetTheme.PressedContainerShape = pressedAngledCorners

		widgetTheme = button.BuildFilledTonalTheme(gtx)
		widgetTheme.EnabledContainerShape = fullAngledCorners
		widgetTheme.PressedContainerShape = pressedAngledCorners

		widgetTheme = button.BuildOutlinedTheme(gtx)
		widgetTheme.EnabledContainerShape = fullAngledCorners
		widgetTheme.PressedContainerShape = pressedAngledCorners

		widgetTheme = button.BuildTextTheme(gtx)
		widgetTheme.EnabledContainerShape = fullAngledCorners
		widgetTheme.PressedContainerShape = pressedAngledCorners
	}
	{
		widgetTheme := card.BuildElevatedTheme(gtx)
		widgetTheme.EnabledContainerShape = angledCorners

		widgetTheme = card.BuildFilledTheme(gtx)
		widgetTheme.EnabledContainerShape = angledCorners

		widgetTheme = card.BuildOutlinedTheme(gtx)
		widgetTheme.EnabledContainerShape = angledCorners
	}
	{
		widgetTheme := dialog.BuildBasicTheme(gtx)
		widgetTheme.EnabledContainerShape = angledCorners
	}
	{
		widgetTheme := search.BuildTheme(gtx)
		widgetTheme.EnabledContainerShape = angledCorners
	}
	{
		widgetTheme := input.BuildTheme(gtx)
		widgetTheme.EnabledContainerShape = angledCorners
	}
	{
		widgetTheme := rail.BuildTheme(gtx)
		widgetTheme.ExpandedModalContainerShape.TopEnd.Kind = token.CornerKindChamfer
		widgetTheme.ExpandedModalContainerShape.BottomEnd.Kind = token.CornerKindChamfer
		widgetTheme.ItemActiveIndicatorShape = angledCorners
	}
	{
		widgetTheme := slider.BuildTheme(gtx)
		widgetTheme.SliderActiveTrackInnerCornerShape.Kind = token.CornerKindChamfer
		widgetTheme.SliderActiveTrackOuterCornerShape.Kind = token.CornerKindChamfer
	}
}

func (p *Page) View(gtx layout.Context) layout.Dimensions {
	return block.UniformPadding(examples.SpacingMedium).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return block.Line{
			Axis:     block.AxisVertical,
			Overflow: block.OverflowClip,
		}.Layout(gtx,
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "App Theme"
				return exp.HeadlineL(gtx, txt)
			}),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "Used to configure the look and feel of the application"
				return exp.BodyL(gtx, txt)
			}),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(p.buttonConfig),
			block.NewVerticalSpacer(examples.SpacingSmall),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				if p.animations == nil {
					return layout.Dimensions{}
				}
				return p.animations.Layout(gtx)
			}),
			block.NewVerticalSpacer(examples.SpacingLarge),
			block.NewSegment(func(gtx layout.Context) layout.Dimensions {
				txt := "Previews"
				return exp.HeadlineL(gtx, txt)
			}),
			block.NewVerticalSpacer(examples.SpacingLarge),
			block.NewSegment(p.pagePreviews),
		)
	})
}

func (p *Page) buttonConfig(gtx layout.Context) layout.Dimensions {
	return block.Line{
		Axis:     block.AxisHorizontal,
		Overflow: block.OverflowWrap,
		Expand:   true,
	}.Layout(gtx,
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return p.lightBaseline.Layout(gtx, "Light Baseline")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return p.lightGreen.Layout(gtx, "Light Green")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return p.darkBaseline.Layout(gtx, "Dark Baseline")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return p.darkGreen.Layout(gtx, "Dark Green (medium contrast)")
		}),
		block.NewHorizontalSpacer(examples.SpacingSmall),
		block.NewSegment(func(gtx layout.Context) layout.Dimensions {
			return p.angular.Layout(gtx, "Angular")
		}),
	)
}

func (p *Page) pagePreviews(gtx layout.Context) layout.Dimensions {
	if len(p.previewCards) == 0 {
		return layout.Dimensions{}
	}

	segments := make([]block.Segment, len(p.previewCards))
	for idx, previewCard := range p.previewCards {
		segments[idx] = block.Segment{BaseSize: unit.Dp(220), Flex: 1, Widget: func(gtx layout.Context) layout.Dimensions {
			route := previewCard.Data.(*router.Route)
			newPreviewWidget := route.PagePreview(previewCard)
			return block.UniformPadding(8).Layout(gtx, newPreviewWidget)
		}}
	}

	return block.Line{
		Axis:     block.AxisHorizontal,
		Overflow: block.OverflowWrap,
		Expand:   true,
	}.Layout(gtx, segments...)
}
