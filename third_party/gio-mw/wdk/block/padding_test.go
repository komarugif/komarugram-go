// SPDX-License-Identifier: Unlicense OR MIT

package block

import (
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"image"
	"testing"
)

type paddingTest struct {
	name             string
	locale           system.Locale
	constraints      layout.Constraints
	padding          Padding
	child            layout.Widget
	expectedSize     image.Point
	expectedOffset   image.Point
	expectedBaseline int
}

// TestPadding_Layout tests the public API.
func TestPadding_Layout(t *testing.T) {
	gtx := layout.Context{
		Constraints: constraintsDefault,
		Locale:      english,
		Ops:         new(op.Ops),
	}
	padding := Padding{
		Start:  16,
		Top:    16,
		End:    16,
		Bottom: 16,
	}
	expectedSize := image.Pt(40, 40)
	got := padding.Layout(gtx, widget008By008)
	if got.Size != expectedSize {
		t.Errorf("got %v, want expectedSize %v", got.Size, expectedSize)
	}
}

func TestPadding_validateConstraints(t *testing.T) {
	tests := []struct {
		name        string
		constraints layout.Constraints
		padding     Padding
		wantErr     bool
	}{
		{
			name: "None",
			constraints: layout.Constraints{
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{},
			wantErr: false,
		},
		{
			name: "NoHorizontalSpace",
			constraints: layout.Constraints{
				Max: image.Point{X: 0, Y: 128},
			},
			padding: Padding{},
			wantErr: true,
		},
		{
			name: "NoVerticalSpace",
			constraints: layout.Constraints{
				Max: image.Point{X: 128, Y: 0},
			},
			padding: Padding{},
			wantErr: true,
		},
		{
			name:        "NegativeStart",
			constraints: constraintsDefault,
			padding:     Padding{Start: -16},
			wantErr:     true,
		},
		{
			name:        "NegativeEnd",
			constraints: constraintsDefault,
			padding:     Padding{End: -16},
			wantErr:     true,
		},
		{
			name:        "NegativeTop",
			constraints: constraintsDefault,
			padding:     Padding{Top: -16},
			wantErr:     true,
		},
		{
			name:        "NegativeBottom",
			constraints: constraintsDefault,
			padding:     Padding{Bottom: -16},
			wantErr:     true,
		},
		{
			name: "NotEnoughHorizontalSpace",
			constraints: layout.Constraints{
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{Start: 128, End: 128},
			wantErr: true,
		},
		{
			name: "NotEnoughVerticalSpace",
			constraints: layout.Constraints{
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{Top: 128, Bottom: 128},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gtx := layout.Context{
				Constraints: tt.constraints,
				Ops:         new(op.Ops),
			}
			gotErr := tt.padding.validateConstraints(gtx)
			if (gotErr != nil) != tt.wantErr {
				t.Errorf("gotErr = %v, wantErr %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestPadding_updateConstraints(t *testing.T) {
	tests := []struct {
		name        string
		constraints layout.Constraints
		padding     Padding
		wantMin     image.Point
		wantMax     image.Point
	}{
		{
			name: "None",
			constraints: layout.Constraints{
				Min: image.Point{X: 32, Y: 32},
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{},
			wantMin: image.Point{X: 32, Y: 32},
			wantMax: image.Point{X: 128, Y: 128},
		},
		{
			name: "HorizontalSmall",
			constraints: layout.Constraints{
				Min: image.Point{X: 32, Y: 32},
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{Start: 8, End: 8},
			wantMin: image.Point{X: 16, Y: 32},
			wantMax: image.Point{X: 112, Y: 128},
		},
		{
			name: "HorizontalBig",
			constraints: layout.Constraints{
				Min: image.Point{X: 32, Y: 32},
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{Start: 16, End: 16},
			wantMin: image.Point{X: 0, Y: 32},
			wantMax: image.Point{X: 96, Y: 128},
		},
		{
			name: "VerticalSmall",
			constraints: layout.Constraints{
				Min: image.Point{X: 32, Y: 32},
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{Top: 8, Bottom: 8},
			wantMin: image.Point{X: 32, Y: 16},
			wantMax: image.Point{X: 128, Y: 112},
		},
		{
			name: "VerticalBig",
			constraints: layout.Constraints{
				Min: image.Point{X: 32, Y: 32},
				Max: image.Point{X: 128, Y: 128},
			},
			padding: Padding{Top: 16, Bottom: 16},
			wantMin: image.Point{X: 32, Y: 0},
			wantMax: image.Point{X: 128, Y: 96},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gtx := layout.Context{
				Constraints: tt.constraints,
			}
			got := tt.padding.updateConstraints(gtx)
			if got.Constraints.Min != tt.wantMin {
				t.Errorf("Min = %v, want %v", got.Constraints.Min, tt.wantMin)
			}
			if got.Constraints.Max != tt.wantMax {
				t.Errorf("Max = %v, want %v", got.Constraints.Max, tt.wantMax)
			}
		})
	}
}

func TestPadding_layout_LTR(t *testing.T) {
	tests := []paddingTest{
		{
			name:             "None",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "NoneRectWide",
			locale:           english,
			constraints:      constraintsRectWide,
			padding:          Padding{},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "NoneRectTall",
			locale:           english,
			constraints:      constraintsRectTall,
			padding:          Padding{},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "Start",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Start: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 16, Y: 0},
			expectedSize:     image.Point{X: 24, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "End",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{End: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 24, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "Horizontal",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Start: 16, End: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 16, Y: 0},
			expectedSize:     image.Point{X: 40, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "Top",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 8, Y: 24},
			expectedBaseline: 4,
		},
		{
			name:             "Bottom",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Bottom: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 24},
			expectedBaseline: 20,
		},
		{
			name:             "Vertical",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16, Bottom: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 8, Y: 40},
			expectedBaseline: 20,
		},
		{
			name:           "HorizontalZeroWidget",
			locale:         english,
			constraints:    constraintsDefault,
			padding:        Padding{Start: 16, End: 16},
			child:          widget000By008,
			expectedOffset: image.Point{X: 16, Y: 0},
			expectedSize:   image.Point{X: 32, Y: 8},
		},
		{
			name:             "VerticalZeroWidget",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16, Bottom: 16},
			child:            widget008By000,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 8, Y: 32},
			expectedBaseline: 16,
		},
		{
			name:           "HorizontalLargeWidget",
			locale:         english,
			constraints:    constraintsDefault,
			padding:        Padding{Start: 16},
			child:          widget128By128,
			expectedOffset: image.Point{X: 16, Y: 0},
			expectedSize:   image.Point{X: 144, Y: 128},
		},
		{
			name:           "VerticalLargeWidget",
			locale:         english,
			constraints:    constraintsDefault,
			padding:        Padding{Top: 16},
			child:          widget128By128,
			expectedOffset: image.Point{X: 0, Y: 16},
			expectedSize:   image.Point{X: 128, Y: 144},
		},
		{
			name:           "HorizontalHugeWidget",
			locale:         english,
			constraints:    constraintsDefault,
			padding:        Padding{Start: 16},
			child:          widget256By256,
			expectedOffset: image.Point{X: 16, Y: 0},
			expectedSize:   image.Point{X: 272, Y: 256},
		},
		{
			name:             "VerticalHugeWidgetTop",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16},
			child:            widget256By256,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 256, Y: 272},
			expectedBaseline: 0,
		},
		{
			name:             "VerticalHugeWidgetBottom",
			locale:           english,
			constraints:      constraintsDefault,
			padding:          Padding{Bottom: 16},
			child:            widget256By256,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 256, Y: 272},
			expectedBaseline: 16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gtx := layout.Context{
				Constraints: tt.constraints,
				Locale:      tt.locale,
				Ops:         new(op.Ops),
			}
			gtx = tt.padding.updateConstraints(gtx)
			got, gotOffset := tt.padding.layoutWidget(gtx, tt.child)
			if got.Size != tt.expectedSize {
				t.Errorf("got %v, want expectedSize %v", got.Size, tt.expectedSize)
			}
			if gotOffset != tt.expectedOffset {
				t.Errorf("got %v, want expectedOffset %v", gotOffset, tt.expectedOffset)
			}
			if got.Baseline != tt.expectedBaseline {
				t.Errorf("got %v, want expectedBaseline %v", got.Baseline, tt.expectedBaseline)
			}
		})
	}
}

func TestPadding_layout_RTL(t *testing.T) {
	tests := []paddingTest{
		{
			name:             "None",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "NoneRectWide",
			locale:           yidish,
			constraints:      constraintsRectWide,
			padding:          Padding{},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "NoneRectTall",
			locale:           yidish,
			constraints:      constraintsRectTall,
			padding:          Padding{},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "Start",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Start: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 24, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "End",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{End: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 16, Y: 0},
			expectedSize:     image.Point{X: 24, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "Horizontal",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Start: 16, End: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 16, Y: 0},
			expectedSize:     image.Point{X: 40, Y: 8},
			expectedBaseline: 4,
		},
		{
			name:             "Top",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 8, Y: 24},
			expectedBaseline: 4,
		},
		{
			name:             "Bottom",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Bottom: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 8, Y: 24},
			expectedBaseline: 20,
		},
		{
			name:             "Vertical",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16, Bottom: 16},
			child:            widget008By008,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 8, Y: 40},
			expectedBaseline: 20,
		},
		{
			name:           "HorizontalZeroWidget",
			locale:         yidish,
			constraints:    constraintsDefault,
			padding:        Padding{Start: 16, End: 16},
			child:          widget000By008,
			expectedOffset: image.Point{X: 16, Y: 0},
			expectedSize:   image.Point{X: 32, Y: 8},
		},
		{
			name:             "VerticalZeroWidget",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16, Bottom: 16},
			child:            widget008By000,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 8, Y: 32},
			expectedBaseline: 16,
		},
		{
			name:           "HorizontalLargeWidget",
			locale:         yidish,
			constraints:    constraintsDefault,
			padding:        Padding{Start: 16},
			child:          widget128By128,
			expectedOffset: image.Point{X: 0, Y: 0},
			expectedSize:   image.Point{X: 144, Y: 128},
		},
		{
			name:           "VerticalLargeWidget",
			locale:         yidish,
			constraints:    constraintsDefault,
			padding:        Padding{Top: 16},
			child:          widget128By128,
			expectedOffset: image.Point{X: 0, Y: 16},
			expectedSize:   image.Point{X: 128, Y: 144},
		},
		{
			name:           "HorizontalHugeWidget",
			locale:         yidish,
			constraints:    constraintsDefault,
			padding:        Padding{Start: 16},
			child:          widget256By256,
			expectedOffset: image.Point{X: 0, Y: 0},
			expectedSize:   image.Point{X: 272, Y: 256},
		},
		{
			name:             "VerticalHugeWidgetTop",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Top: 16},
			child:            widget256By256,
			expectedOffset:   image.Point{X: 0, Y: 16},
			expectedSize:     image.Point{X: 256, Y: 272},
			expectedBaseline: 0,
		},
		{
			name:             "VerticalHugeWidgetBottom",
			locale:           yidish,
			constraints:      constraintsDefault,
			padding:          Padding{Bottom: 16},
			child:            widget256By256,
			expectedOffset:   image.Point{X: 0, Y: 0},
			expectedSize:     image.Point{X: 256, Y: 272},
			expectedBaseline: 16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gtx := layout.Context{
				Constraints: tt.constraints,
				Locale:      tt.locale,
				Ops:         new(op.Ops),
			}
			gtx = tt.padding.updateConstraints(gtx)
			got, gotOffset := tt.padding.layoutWidget(gtx, tt.child)
			if got.Size != tt.expectedSize {
				t.Errorf("got %v, want expectedSize %v", got.Size, tt.expectedSize)
			}
			if gotOffset != tt.expectedOffset {
				t.Errorf("got %v, want expectedOffset %v", gotOffset, tt.expectedOffset)
			}
			if got.Baseline != tt.expectedBaseline {
				t.Errorf("got %v, want expectedBaseline %v", got.Baseline, tt.expectedBaseline)
			}
		})
	}
}
