// Package spectrum is a bubbletea component that renders audio blocks as a
// spectrum-analyzer pixel frame.
package spectrum

import (
	"image/color"
	"math"
	"strings"
)

// Palette colours a pixel of the analyzer. At is called per band and row
// (y=0 is the bottom row); a zero color means "do not draw". A Relative
// palette sees the bar's own height as height, so its whole gradient fits
// inside every bar instead of being pinned to screen rows. An Animate
// palette sees the band index rotated over time, so colours roll sideways.
type Palette struct {
	Name     string
	Relative bool
	Animate  bool
	At       func(band, nBands, y, height int) (bar, peak color.RGBA)
}

// rainbow is shared by the rainbow and drift palettes.
func rainbow(band, n, _, _ int) (color.RGBA, color.RGBA) {
	return hsv(float64(band) / float64(n)), white
}

// tip returns tipColor on the top pixel of a (relative) bar, else c.
func tip(c, tipColor color.RGBA, y, h int) color.RGBA {
	if y == h-1 {
		return tipColor
	}
	return c
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
	{Name: "rainbow", At: rainbow},
	{Name: "drift", Animate: true, At: rainbow},
	{Name: "bi", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) { // bi pride flag, stripes 2:1:2
		c := color.RGBA{0xD6, 0x02, 0x70, 255} // magenta
		switch f := float64(y) / float64(h); {
		case f >= 0.6:
			c = color.RGBA{0x00, 0x38, 0xA8, 255} // blue
		case f >= 0.4:
			c = color.RGBA{0x9B, 0x4F, 0x96, 255} // purple
		}
		return c, white
	}},
	{Name: "trans", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) { // trans pride flag, 5 equal stripes
		blue, pink := color.RGBA{0x5B, 0xCE, 0xFA, 255}, color.RGBA{0xF5, 0xA9, 0xB8, 255}
		c := [...]color.RGBA{blue, pink, white, pink, blue}[min(y*5/h, 4)]
		return c, c
	}},
	{Name: "tribar", At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		c := green
		switch f := float64(y) / float64(h); {
		case f >= 2.0/3:
			c = red
		case f >= 1.0/3:
			c = yellow
		}
		return c, c
	}},
	{Name: "red", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return red, blue }},
	{Name: "blue", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return blue, red }},
	{Name: "purple", At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{0, 212, 255, 255}, color.RGBA{179, 0, 255, 255}), white
	}},
	{Name: "outrun", At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{141, 0, 100, 255}, color.RGBA{255, 192, 0, 255}, color.RGBA{0, 5, 255, 255}), none
	}},
	{Name: "peaks", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return none, blue }},
	{Name: "fire", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		c := gradient(frac(y, h), color.RGBA{128, 0, 0, 255}, red, color.RGBA{255, 128, 0, 255}, yellow)
		return tip(c, white, y, h), red
	}},
	{Name: "ice", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{0, 0, 128, 255}, color.RGBA{0, 255, 255, 255}, white), white
	}},
	{Name: "matrix", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return color.RGBA{0, uint8(100 + 155*band/max(n-1, 1)), 0, 255}, color.RGBA{180, 255, 180, 255}
	}},
	{Name: "freq", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) { // colour by frequency band
		return gradient(float64(band)/float64(max(n-1, 1)), red, yellow, blue), white
	}},
	{Name: "ukraine", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		if float64(y)/float64(h) >= 0.5 {
			return color.RGBA{0, 87, 183, 255}, white
		}
		return color.RGBA{255, 213, 0, 255}, white
	}},
	{Name: "antifa", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		// black cannot be drawn on a black frame, so the black flag is near-black
		if float64(y)/float64(h) >= 0.5 {
			return color.RGBA{50, 50, 50, 255}, white
		}
		return color.RGBA{228, 0, 43, 255}, white
	}},
	{Name: "synthwave", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return tip(color.RGBA{255, 0, 255, 255}, white, y, h), color.RGBA{0, 255, 255, 255}
	}},
	{Name: "white", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return white, red }},
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
