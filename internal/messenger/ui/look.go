// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"sync"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"komarugram/internal/messenger/preferences"
)

// lookKey keeps the look of the frame in its layout.Context's Values, for
// everything that draws a bubble, an avatar or a message's time.
const lookKey = "komarugram/ui.look"

// withLook makes l the look of the frame of gtx.
func withLook(gtx layout.Context, l preferences.Look) {
	if gtx.Values != nil {
		gtx.Values[lookKey] = boxedLook(l)
	}
}

// looks keeps the look made a value of Values last: put there as it is,
// in every frame, a look was copied to the heap each time. Windows set it
// from their own goroutines.
var looks struct {
	mu    sync.Mutex
	look  preferences.Look
	boxed any
}

func boxedLook(l preferences.Look) any {
	looks.mu.Lock()
	defer looks.mu.Unlock()
	if looks.boxed == nil || looks.look != l {
		looks.look, looks.boxed = l, l
	}
	return looks.boxed
}

// lookOf is the look of the frame, or the default one.
func lookOf(gtx layout.Context) preferences.Look {
	if l, ok := gtx.Values[lookKey].(preferences.Look); ok {
		return l
	}
	return preferences.Look{BubbleRadius: preferences.BubbleRadiusMax, AvatarCorners: preferences.AvatarRound}
}

// bubbleRadiusOf is how round the corners of bubbles are.
func bubbleRadiusOf(gtx layout.Context) int {
	return gtx.Dp(unit.Dp(lookOf(gtx).BubbleRadius))
}

// avatarShape is the shape of an avatar of size: a circle, or a square
// with rounded corners, as the look asks.
func avatarShape(gtx layout.Context, size image.Point) clip.RRect {
	corners := min(max(lookOf(gtx).AvatarCorners, 0), preferences.AvatarRound)
	radius := min(size.X, size.Y) / 2 * corners / preferences.AvatarRound
	return clip.UniformRRect(image.Rectangle{Max: size}, radius)
}

// timeFormat is how a message's time is written.
func timeFormat(gtx layout.Context) string {
	if lookOf(gtx).Seconds {
		return "15:04:05"
	}
	return "15:04"
}
