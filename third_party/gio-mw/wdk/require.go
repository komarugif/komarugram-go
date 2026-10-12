// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import (
	"bytes"
	"fmt"
	"gio-mw/token"
	"image"
	"image/color"
	"io"
	"io/fs"
	"log"
	"os"
	"slices"
	"sync"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// iconColors is how many colors and sizes of an icon stay rasterized.
const iconColors = 4

// RequireIconWidget returns the icon of IconVG data, or panics.
//
// A widget.Icon keeps the picture of the last color and size it drew: an
// icon drawn in two colors in a frame, as the kind of a chat is in the
// selected row of a list and in the others, would be rasterized again each
// time. The few colors and sizes drawn last keep an Icon each. The icon may
// be drawn by several windows at once.
func RequireIconWidget(data []byte) IconWidget {
	if _, err := widget.NewIcon(data); err != nil {
		panic(err)
	}
	type drawn struct {
		color color.NRGBA
		size  int
		icon  *widget.Icon
	}
	var mu sync.Mutex
	var recent []drawn // The last drawn first.
	return func(gtx layout.Context, foreground token.MatColor) layout.Dimensions {
		c, size := foreground.AsNRGBA(), gtx.Constraints.Min.X
		mu.Lock()
		defer mu.Unlock()
		i := slices.IndexFunc(recent, func(d drawn) bool { return d.color == c && d.size == size })
		var d drawn
		if i >= 0 {
			d = recent[i]
			recent = slices.Delete(recent, i, i+1)
		} else {
			icon, _ := widget.NewIcon(data)
			d = drawn{color: c, size: size, icon: icon}
			if len(recent) == iconColors {
				recent = recent[:iconColors-1]
			}
		}
		recent = slices.Insert(recent, 0, d)
		return d.icon.Layout(gtx, c)
	}
}

func RequireImage(imageFile io.Reader) *widget.Image {
	decodedImage, format, err := image.Decode(imageFile)
	if err != nil {
		panic(fmt.Errorf("failed to decode image file: %v", err))
	}
	size := decodedImage.Bounds().Size()
	log.Printf("decoded iamge format: %s, size: %s\n", format, size)
	imageWidget := &widget.Image{
		Src: paint.NewImageOp(decodedImage),
		Fit: widget.Contain,
	}
	return imageWidget
}

func RequireImageFromFS(assetsFS fs.ReadFileFS, imagePath string) *widget.Image {
	imageBytes, err := assetsFS.ReadFile(imagePath)
	if err != nil {
		panic(fmt.Errorf("failed to open image file: %v", err))
	}
	return RequireImage(bytes.NewReader(imageBytes))
}

func RequireImageByPath(imagePath string) *widget.Image {
	imageFile, err := os.Open(imagePath)
	if err != nil {
		panic(fmt.Errorf("failed to open image file: %v", err))
	}
	defer func(imageFile *os.File) {
		err := imageFile.Close()
		if err != nil {
			panic(fmt.Errorf("failed to close image: %v", err))
		}
	}(imageFile)
	return RequireImage(imageFile)
}
