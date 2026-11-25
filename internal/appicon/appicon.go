// SPDX-License-Identifier: Unlicense OR MIT

// Package appicon gives the application's icon at the sizes windows show it,
// and the monochrome system tray icon.
package appicon

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"math"
	"slices"
	"sync"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"

	"komarugram/assets"
	"komarugram/pkg/resample"
)

const (
	// Default is the standard KomaruGram application icon.
	Default = ""
	// NewYear is the winter/snowflake KomaruGram application icon.
	NewYear = "newyear"
)

// All lists the selectable application icons in display order.
var All = []string{Default, NewYear}

var (
	currentMu sync.RWMutex
	current   = Default
)

// Set selects the active application icon for new windows and default calls.
func Set(name string) {
	if name != NewYear {
		name = Default
	}
	currentMu.Lock()
	current = name
	currentMu.Unlock()
}

// Current returns the currently selected application icon name.
func Current() string {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current
}

// WindowSizes are the sizes a window's icon is given in, from the taskbar's
// 16 pixels to a task switcher's large icons.
var WindowSizes = []int{16, 24, 32, 48, 64, 128, 256}

// Images returns the currently selected icon at each size, square.
func Images(sizes ...int) []*image.NRGBA {
	return ImagesFor(Current(), sizes...)
}

// ImagesFor returns the named icon at each size, square: the logo, which is
// not quite square, is centered on a transparent background. The logo is
// decoded once per call and not kept.
func ImagesFor(name string, sizes ...int) []*image.NRGBA {
	raw := assets.LogoRound
	if name == NewYear {
		raw = assets.LogoRoundNewYear
	}
	logo, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		// The logo is built in: a test decodes it.
		panic(err)
	}
	return resizeCascade(logo, sizes...)
}

// Image returns the currently selected icon size pixels square.
func Image(size int) *image.NRGBA {
	return Images(size)[0]
}

// ImageFor returns the named icon size pixels square.
func ImageFor(name string, size int) *image.NRGBA {
	return ImagesFor(name, size)[0]
}

// TrayImages returns the monochrome system tray icon at each size, square.
func TrayImages(sizes ...int) []*image.NRGBA {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(assets.TrayMonochrome))
	if err != nil {
		panic(err)
	}
	native := max(int(math.Round(math.Max(icon.ViewBox.W, icon.ViewBox.H))), 1)
	src := image.NewRGBA(image.Rect(0, 0, native, native))
	icon.SetTarget(0, 0, float64(native), float64(native))
	icon.Draw(rasterx.NewDasher(native, native, rasterx.NewScannerGV(native, native, src, src.Bounds())), 1)
	return resizeCascade(src, sizes...)
}

// TrayImage returns the monochrome system tray icon size pixels square.
func TrayImage(size int) *image.NRGBA {
	return TrayImages(size)[0]
}

func resizeCascade(logo image.Image, sizes ...int) []*image.NRGBA {
	out := make([]*image.NRGBA, len(sizes))
	// Each size is scaled from the smallest image made so far that is at
	// least twice as large, or from the logo: as sharp, and the large logo
	// is filtered once instead of for every size.
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return sizes[b] - sizes[a] })
	var made []*image.RGBA
	for _, i := range order {
		size := sizes[i]
		var src image.Image = logo
		for _, m := range made {
			if m.Rect.Dx() >= 2*size {
				src = m
			}
		}
		b := src.Bounds()
		fit := resample.Fit(b.Dx(), b.Dy(), image.Pt(size, size), false)
		scaled := resample.Resize(src, fit.X, fit.Y)
		made = append(made, scaled)
		im := image.NewNRGBA(image.Rect(0, 0, size, size))
		at := image.Pt((size-fit.X)/2, (size-fit.Y)/2)
		draw.Draw(im, scaled.Bounds().Add(at), scaled, image.Point{}, draw.Src)
		out[i] = im
	}
	return out
}
