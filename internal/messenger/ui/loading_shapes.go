// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"math"

	"gioui.org/f32"
)

const loadingContourPoints = 120

// These radial contours describe the Material 3 indicator's seven silhouettes
// in sequence. Sampling each on the same rays lets the outline interpolate
// between them without matching polygons of different vertex counts.
var loadingContours = func() [7][loadingContourPoints]f32.Point {
	outlines := [...]func(float64) float64{
		lobedContour(10, .19, -math.Pi/2), // Soft Burst
		lobedContour(9, .07, -math.Pi/2),  // 9-sided Cookie
		loadingPentagon,
		loadingCapsule,
		lobedContour(8, .15, 0),         // Sunny
		lobedContour(4, .36, math.Pi/4), // 4-sided Cookie
		loadingOval,
	}
	var contours [len(outlines)][loadingContourPoints]f32.Point
	for i := range loadingContourPoints {
		angle := 2 * math.Pi * float64(i) / loadingContourPoints
		y, x := math.Sincos(angle)
		for shape, outline := range outlines {
			r := outline(angle)
			contours[shape][i] = f32.Pt(float32(r*x), float32(r*y))
		}
	}
	return contours
}()

func lobedContour(lobes int, depth, offset float64) func(float64) float64 {
	return func(angle float64) float64 {
		return 1 - depth*(1-math.Cos(float64(lobes)*(angle-offset)))/2
	}
}

// loadingPentagon is the outline of a pentagon rounded by a circular rim.
// Each ray meets either a straight side or the round corner beside it.
func loadingPentagon(angle float64) float64 {
	const halfSector = math.Pi / 5
	const vertex, round = .74, .26
	ray := math.Abs(math.Remainder(angle+math.Pi/2, 2*halfSector))
	cornerEnd := math.Atan2(round*math.Sin(halfSector), vertex+round*math.Cos(halfSector))
	if ray <= cornerEnd {
		return roundedVertex(ray, vertex, round)
	}
	return (vertex*math.Cos(halfSector) + round) / math.Cos(halfSector-ray)
}

func loadingCapsule(angle float64) float64 {
	const center, round = .43, .57
	ray := math.Abs(math.Remainder(angle+math.Pi/4, math.Pi))
	if ray <= math.Atan2(round, center) {
		return roundedVertex(ray, center, round)
	}
	return round / math.Sin(ray)
}

// roundedVertex intersects a ray with the circle at the end of a shape.
func roundedVertex(ray, center, radius float64) float64 {
	y, x := math.Sincos(ray)
	y *= center
	return center*x + math.Sqrt(radius*radius-y*y)
}

func loadingOval(angle float64) float64 {
	y, x := math.Sincos(angle + math.Pi/4)
	return 1 / math.Hypot(x, y/.64)
}
