// Package spectrum is a bubbletea component that renders audio blocks as a
// spectrum-analyzer pixel frame.
package spectrum

import (
	"image/color"
	"math"
	"strings"
)

// Palette colours a pixel of the analyzer. At is called per band and row
// (y=0 is the bottom row); a zero color means "do not draw".
type Palette struct {
	Name string
	At   func(band, nBands, y, height int) (bar, peak color.RGBA)
}

var (
	white  = color.RGBA{255, 255, 255, 255}
	red    = color.RGBA{255, 0, 0, 255}
	green  = color.RGBA{0, 255, 0, 255}
	yellow = color.RGBA{255, 255, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	none   = color.RGBA{}
)

// Palettes are the shipped palettes, in cycling order. Ported from
// github.com/donnersm/FFT_ESP32_Analyzer colour modes.
var Palettes = []Palette{
	{"rainbow", func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return hsv(float64(band) / float64(n)), white
	}},
	{"tribar", func(_, _, y, h int) (color.RGBA, color.RGBA) {
		c := green
		switch f := float64(y) / float64(h); {
		case f >= 2.0/3:
			c = red
		case f >= 1.0/3:
			c = yellow
		}
		return c, c
	}},
	{"red", func(_, _, _, _ int) (color.RGBA, color.RGBA) { return red, blue }},
	{"blue", func(_, _, _, _ int) (color.RGBA, color.RGBA) { return blue, red }},
	{"purple", func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{0, 212, 255, 255}, color.RGBA{179, 0, 255, 255}), white
	}},
	{"outrun", func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{141, 0, 100, 255}, color.RGBA{255, 192, 0, 255}, color.RGBA{0, 5, 255, 255}), none
	}},
	{"peaks", func(_, _, _, _ int) (color.RGBA, color.RGBA) { return none, blue }},
}

// PaletteByName looks a palette up case-insensitively.
func PaletteByName(name string) (Palette, bool) {
	for _, p := range Palettes {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Palette{}, false
}

// frac is y as a fraction of the top row, safe for h == 1.
func frac(y, h int) float64 { return float64(y) / float64(max(h-1, 1)) }

// hsv returns a fully saturated colour for hue in [0,1).
func hsv(h float64) color.RGBA {
	h = math.Mod(h, 1) * 6
	x := uint8(255 * (1 - math.Abs(math.Mod(h, 2)-1)))
	switch int(h) {
	case 0:
		return color.RGBA{255, x, 0, 255}
	case 1:
		return color.RGBA{x, 255, 0, 255}
	case 2:
		return color.RGBA{0, 255, x, 255}
	case 3:
		return color.RGBA{0, x, 255, 255}
	case 4:
		return color.RGBA{x, 0, 255, 255}
	default:
		return color.RGBA{255, 0, x, 255}
	}
}

// gradient linearly interpolates evenly spaced stops at t in [0,1].
func gradient(t float64, stops ...color.RGBA) color.RGBA {
	t = max(0, min(1, t)) * float64(len(stops)-1)
	i := min(int(t), len(stops)-2)
	f := t - float64(i)
	a, b := stops[i], stops[i+1]
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}
