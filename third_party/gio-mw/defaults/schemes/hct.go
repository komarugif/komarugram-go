// SPDX-License-Identifier: Unlicense OR MIT

package schemes

import (
	"image/color"
	"math"
)

// HCT is the color space Material's tonal palettes are made in: the hue and
// chroma of CAM16 (CIE's color appearance model, in Material's default
// viewing conditions) and the tone, CIE L*. Written from the published
// equations of CAM16 (Li et al., 2017) and their inverse.

// viewing holds the constants of the viewing conditions: a D65 white, the
// adapting luminance of a mid-gray (L* 50) surround of 200 lux, an average
// surround.
type viewing struct {
	n, aw, nbb, ncb, c, nc, fl, z float64
	drgb                          [3]float64
}

var m16 = [3][3]float64{
	{0.401288, 0.650173, -0.051461},
	{-0.250268, 1.204414, 0.045854},
	{-0.002079, 0.048952, 0.953127},
}

var m16Inv = invert3(m16)

var whiteD65 = [3]float64{95.047, 100.0, 108.883}

var defaultViewing = newViewing()

func newViewing() viewing {
	la := 200 / math.Pi * yFromLstar(50) / 100
	yb := yFromLstar(50)
	const f, c, nc = 1.0, 0.69, 1.0
	rgbW := mul3(m16, whiteD65)
	d := f * (1 - 1/3.6*math.Exp((-la-42)/92))
	d = math.Max(0, math.Min(1, d))
	var v viewing
	for i := range rgbW {
		v.drgb[i] = d*(100/rgbW[i]) + 1 - d
	}
	k := 1 / (5*la + 1)
	k4 := k * k * k * k
	v.fl = k4*la + 0.1*(1-k4)*(1-k4)*math.Cbrt(5*la)
	v.n = yb / whiteD65[1]
	v.z = 1.48 + math.Sqrt(v.n)
	v.nbb = 0.725 / math.Pow(v.n, 0.2)
	v.ncb = v.nbb
	v.c, v.nc = c, nc
	var aw [3]float64
	for i := range rgbW {
		aw[i] = v.adapt(v.drgb[i] * rgbW[i])
	}
	v.aw = (2*aw[0] + aw[1] + 0.05*aw[2]) * v.nbb
	return v
}

func (v *viewing) adapt(c float64) float64 {
	af := math.Pow(v.fl*math.Abs(c)/100, 0.42)
	return math.Copysign(400*af/(af+27.13), c)
}

func (v *viewing) unadapt(a float64) float64 {
	abs := math.Min(math.Abs(a), 399.999)
	base := math.Max(0, 27.13*abs/(400-abs))
	return math.Copysign(100/v.fl*math.Pow(base, 1/0.42), a)
}

// hctOf returns the hue, chroma and tone of c.
func hctOf(c color.NRGBA) (hue, chroma, tone float64) {
	xyz := xyzOf(c)
	v := &defaultViewing
	rgb := mul3(m16, xyz)
	var a3 [3]float64
	for i := range rgb {
		a3[i] = v.adapt(v.drgb[i] * rgb[i])
	}
	r, g, b := a3[0], a3[1], a3[2]
	ca := (11*r - 12*g + b) / 11
	cb := (r + g - 2*b) / 9
	u := (20*r + 20*g + 21*b) / 20
	p2 := (40*r + 20*g + b) / 20
	hue = math.Mod(math.Atan2(cb, ca)*180/math.Pi+360, 360)
	ac := p2 * v.nbb
	j := 100 * math.Pow(ac/v.aw, v.c*v.z)
	hp := hue
	if hp < 20.14 {
		hp += 360
	}
	eHue := 0.25 * (math.Cos(hp*math.Pi/180+2) + 3.8)
	p1 := 50000.0 / 13 * eHue * v.nc * v.ncb
	t := p1 * math.Hypot(ca, cb) / (u + 0.305)
	alpha := math.Pow(t, 0.9) * math.Pow(1.64-math.Pow(0.29, v.n), 0.73)
	chroma = alpha * math.Sqrt(j/100)
	return hue, chroma, lstarFromY(xyz[1])
}

// xyzFromJCh is the inverse of CAM16: the XYZ of a lightness J, chroma and
// hue.
func xyzFromJCh(j, chroma, hue float64) [3]float64 {
	v := &defaultViewing
	alpha := 0.0
	if chroma != 0 && j != 0 {
		alpha = chroma / math.Sqrt(j/100)
	}
	t := math.Pow(alpha/math.Pow(1.64-math.Pow(0.29, v.n), 0.73), 1/0.9)
	h := hue * math.Pi / 180
	eHue := 0.25 * (math.Cos(h+2) + 3.8)
	ac := v.aw * math.Pow(j/100, 1/v.c/v.z)
	p1 := eHue * (50000.0 / 13) * v.nc * v.ncb
	p2 := ac / v.nbb
	hSin, hCos := math.Sincos(h)
	gamma := 23 * (p2 + 0.305) * t / (23*p1 + 11*t*hCos + 108*t*hSin)
	a, b := gamma*hCos, gamma*hSin
	ra := (460*p2 + 451*a + 288*b) / 1403
	ga := (460*p2 - 891*a - 261*b) / 1403
	ba := (460*p2 - 220*a - 6300*b) / 1403
	rgb := [3]float64{v.unadapt(ra) / v.drgb[0], v.unadapt(ga) / v.drgb[1], v.unadapt(ba) / v.drgb[2]}
	return mul3(m16Inv, rgb)
}

// colorOfHCT returns the sRGB color of a hue, chroma and tone; when the
// chroma is out of sRGB's gamut at that hue and tone, the color of the most
// chroma that is in it.
func colorOfHCT(hue, chroma, tone float64) color.NRGBA {
	if tone <= 0.0001 {
		return color.NRGBA{A: 255}
	}
	if tone >= 99.9999 {
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	y := yFromLstar(tone)
	if chroma < 0.0001 {
		// Gray, exactly: CAM16's own gray is a shade warm in sRGB.
		g := y / 100
		return srgbOfLinear([3]float64{g, g, g})
	}
	if lin, ok := linearOfHCT(hue, chroma, y); ok {
		return srgbOfLinear(lin)
	}
	// The most chroma in gamut, by bisection; gray is always in it.
	lo, hi := 0.0, chroma
	best, _ := linearOfHCT(hue, 0, y)
	for range 24 {
		mid := (lo + hi) / 2
		if lin, ok := linearOfHCT(hue, mid, y); ok {
			lo, best = mid, lin
		} else {
			hi = mid
		}
	}
	return srgbOfLinear(best)
}

// linearOfHCT returns the linear sRGB of a hue and chroma at the luminance
// y, finding the lightness J that has it, and whether it is in gamut.
func linearOfHCT(hue, chroma, y float64) ([3]float64, bool) {
	lo, hi := 0.0, 100.0
	var xyz [3]float64
	for range 40 {
		j := (lo + hi) / 2
		xyz = xyzFromJCh(j, chroma, hue)
		if xyz[1] < y {
			lo = j
		} else {
			hi = j
		}
	}
	xyz = xyzFromJCh((lo+hi)/2, chroma, hue)
	lin := mul3(xyzToLinearSRGB, xyz)
	const eps = 1e-4
	ok := true
	for i, c := range lin {
		c /= 100
		if math.IsNaN(c) || c < -eps || c > 1+eps {
			ok = false
		}
		lin[i] = math.Max(0, math.Min(1, c))
	}
	return lin, ok
}

var linearSRGBToXYZ = [3][3]float64{
	{0.41233895, 0.35762064, 0.18051042},
	{0.2126, 0.7152, 0.0722},
	{0.01932141, 0.11916382, 0.95034478},
}

var xyzToLinearSRGB = invert3(linearSRGBToXYZ)

// xyzOf returns the XYZ of c, Y from 0 to 100.
func xyzOf(c color.NRGBA) [3]float64 {
	lin := [3]float64{linearize(c.R), linearize(c.G), linearize(c.B)}
	xyz := mul3(linearSRGBToXYZ, lin)
	for i := range xyz {
		xyz[i] *= 100
	}
	return xyz
}

func linearize(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.040449936 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func srgbOfLinear(lin [3]float64) color.NRGBA {
	ch := func(v float64) uint8 {
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255))
	}
	return color.NRGBA{R: ch(lin[0]), G: ch(lin[1]), B: ch(lin[2]), A: 255}
}

func yFromLstar(l float64) float64 {
	ft := (l + 16) / 116
	if ft*ft*ft > 216.0/24389 {
		return 100 * ft * ft * ft
	}
	return 100 * l / (24389.0 / 27)
}

func lstarFromY(y float64) float64 {
	y /= 100
	if y <= 216.0/24389 {
		return y * 24389.0 / 27
	}
	return 116*math.Cbrt(y) - 16
}

func mul3(m [3][3]float64, v [3]float64) [3]float64 {
	return [3]float64{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

func invert3(m [3][3]float64) [3][3]float64 {
	a, b, c := m[0][0], m[0][1], m[0][2]
	d, e, f := m[1][0], m[1][1], m[1][2]
	g, h, i := m[2][0], m[2][1], m[2][2]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	return [3][3]float64{
		{(e*i - f*h) / det, (c*h - b*i) / det, (b*f - c*e) / det},
		{(f*g - d*i) / det, (a*i - c*g) / det, (c*d - a*f) / det},
		{(d*h - e*g) / det, (b*g - a*h) / det, (a*e - b*d) / det},
	}
}
