// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"maps"
	"os"
	"testing"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
)

func TestLoadingIndicatorRespectsMotion(t *testing.T) {
	gtx := layout.Context{
		Ops: new(op.Ops), Now: time.Unix(1000, 0), Values: map[string]any{},
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, testDefaultLight))
	s := loadingIndicator{}
	l := localization.For("en")
	if dims := s.sized(gtx, l, 20); dims.Size != image.Pt(20, 20) || s.started != gtx.Now || s.classic != nil {
		t.Fatalf("animated indicator: size %v, start %v", dims.Size, s.started)
	}
	wdk.SetAnimationsEnabled(gtx, false)
	gtx.Now = gtx.Now.Add(time.Second)
	if dims := s.sized(gtx, l, 20); dims.Size != image.Pt(20, 20) || !s.started.IsZero() || s.classic == nil {
		t.Fatalf("static indicator: size %v, start %v", dims.Size, s.started)
	}
}

// TestRenderLoadingIndicator shows key poses, intermediate shapes and the
// classic ring with animations disabled.
func TestRenderLoadingIndicator(t *testing.T) {
	path := os.Getenv("LOADING_PNG")
	if path == "" {
		t.Skip("set LOADING_PNG to render")
	}
	l := localization.For("en")
	renderFrames(t, image.Pt(672, 300), path, func(gtx layout.Context) {
		for i, palette := range []*token.Scheme{testDefaultLight, testDefaultDark} {
			row := gtx
			row.Values = maps.Clone(gtx.Values)
			wdk.InitMaterialThemeInContext(row, testTheme(row, palette))
			for j := range 2 {
				y := (i*2 + j) * 75
				offset(row, image.Pt(0, y), func(gtx layout.Context) layout.Dimensions {
					size := image.Pt(672, 75)
					fillRect(gtx, scheme(gtx).Surface.Color, size)
					for k := range len(loadingContours) {
						at := time.Duration(k)*loadingMorph + time.Duration(j)*100*time.Millisecond
						s := loadingIndicator{started: gtx.Now.Add(-at)}
						offset(gtx, image.Pt(k*84+12, 7), func(gtx layout.Context) layout.Dimensions {
							return s.sized(gtx, l, loadingSize)
						})
					}
					still := gtx
					still.Values = maps.Clone(gtx.Values)
					wdk.SetAnimationsEnabled(still, false)
					offset(still, image.Pt(len(loadingContours)*84+12, 7), func(gtx layout.Context) layout.Dimensions {
						var s loadingIndicator
						return s.sized(gtx, l, loadingSize)
					})
					return layout.Dimensions{Size: size}
				})
			}
		}
	})
}
