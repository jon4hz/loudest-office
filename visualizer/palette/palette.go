// Package palette holds the named colour palettes shared by the music
// bubbles: each one colours a pixel of the analyzer given its band and row.
package palette

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Palette colours a pixel of the analyzer. At is called per band and row
// (y=0 is the bottom row); a zero color means "do not draw". A Relative
// palette sees the bar's own height as height, so its whole gradient fits
// inside every bar instead of being pinned to screen rows. An Animate
// palette sees the band index rotated over time, so colours roll sideways.
// An Upright palette has a right way up (a flag): a mode that draws its bars
// downwards mirrors every other palette with the bar, but not this one.
type Palette struct {
	Name     string
	Relative bool
	Animate  bool
	Upright  bool
	Custom   *Custom
	At       func(band, nBands, y, height int) (bar, peak color.RGBA)
}

// rainbow is shared by the rainbow and drift palettes.
func rainbow(band, n, _, _ int) (color.RGBA, color.RGBA) {
	return hsv(float64(band) / float64(n)), White
}

// tip returns tipColor on the top pixel of a (relative) bar, else c.
func tip(c, tipColor color.RGBA, y, h int) color.RGBA {
	if y == h-1 {
		return tipColor
	}
	return c
}

var (
	// White, Red and Blue are also used directly by modes and their tests
	// (fire's gradient, and the "white"/"peaks" palettes' fixed colours).
	White  = color.RGBA{255, 255, 255, 255}
	Red    = color.RGBA{255, 0, 0, 255}
	Blue   = color.RGBA{0, 0, 255, 255}
	green  = color.RGBA{0, 255, 0, 255}
	yellow = color.RGBA{255, 255, 0, 255}
	none   = color.RGBA{}
)

// Palettes are the shipped palettes, in cycling order. Ported from
// github.com/donnersm/FFT_ESP32_Analyzer colour modes.
var Palettes = []Palette{
	{Name: "rainbow", At: rainbow},
	{Name: "drift", Animate: true, At: rainbow},
	{Name: "bi pride", Relative: true, Upright: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) { // bi pride flag, stripes 2:1:2
		c := color.RGBA{0xD6, 0x02, 0x70, 255} // magenta
		switch f := float64(y) / float64(h); {
		case f >= 0.6:
			c = color.RGBA{0x00, 0x38, 0xA8, 255} // blue
		case f >= 0.4:
			c = color.RGBA{0x9B, 0x4F, 0x96, 255} // purple
		}
		return c, White
	}},
	{Name: "trans pride", Relative: true, Upright: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) { // trans pride flag, 5 equal stripes
		blue, pink := color.RGBA{0x5B, 0xCE, 0xFA, 255}, color.RGBA{0xF5, 0xA9, 0xB8, 255}
		c := [...]color.RGBA{blue, pink, White, pink, blue}[min(y*5/h, 4)]
		return c, c
	}},
	{Name: "tribar", At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		c := green
		switch f := float64(y) / float64(h); {
		case f >= 2.0/3:
			c = Red
		case f >= 1.0/3:
			c = yellow
		}
		return c, c
	}},
	{Name: "red", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return Red, Blue }},
	{Name: "blue", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return Blue, Red }},
	{Name: "purple", At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return Gradient(frac(y, h), color.RGBA{0, 212, 255, 255}, color.RGBA{179, 0, 255, 255}), White
	}},
	{Name: "outrun", At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return Gradient(frac(y, h), color.RGBA{141, 0, 100, 255}, color.RGBA{255, 192, 0, 255}, color.RGBA{0, 5, 255, 255}), none
	}},
	{Name: "peaks", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return none, Blue }},
	{Name: "fire", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		c := Gradient(frac(y, h), color.RGBA{128, 0, 0, 255}, Red, color.RGBA{255, 128, 0, 255}, yellow)
		return tip(c, White, y, h), Red
	}},
	{Name: "ice", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return Gradient(frac(y, h), color.RGBA{0, 0, 128, 255}, color.RGBA{0, 255, 255, 255}, White), White
	}},
	{Name: "matrix", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return color.RGBA{0, uint8(100 + 155*band/max(n-1, 1)), 0, 255}, color.RGBA{180, 255, 180, 255}
	}},
	{Name: "freq", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) { // colour by frequency band
		return Gradient(float64(band)/float64(max(n-1, 1)), Red, yellow, Blue), White
	}},
	{Name: "ukraine", Relative: true, Upright: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		if float64(y)/float64(h) >= 0.5 {
			return color.RGBA{0, 87, 183, 255}, White
		}
		return color.RGBA{255, 213, 0, 255}, White
	}},
	{Name: "antifa", Relative: true, Upright: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		// black cannot be drawn on a black frame, so the black flag is near-black
		if float64(y)/float64(h) >= 0.5 {
			return color.RGBA{50, 50, 50, 255}, White
		}
		return color.RGBA{228, 0, 43, 255}, White
	}},
	{Name: "synthwave", Relative: true, At: func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return tip(color.RGBA{255, 0, 255, 255}, White, y, h), color.RGBA{0, 255, 255, 255}
	}},
	{Name: "white", At: func(_, _, _, _ int) (color.RGBA, color.RGBA) { return White, Red }},
	// Gradients across the bands, resampled to 8 even stops from WLED's
	// palettes.cpp (cpt-city and WLED originals), 8..92% of each so no band
	// gets a black end.
	{Name: "sunset", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{215, 77, 0, 255}, color.RGBA{255, 170, 0, 255}, color.RGBA{215, 92, 70, 255}, color.RGBA{187, 39, 127, 255}, color.RGBA{155, 0, 171, 255}, color.RGBA{110, 0, 180, 255}, color.RGBA{65, 0, 190, 255}, color.RGBA{26, 0, 200, 255}), White
	}},
	{Name: "rivendell", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{34, 76, 49, 255}, color.RGBA{49, 87, 57, 255}, color.RGBA{64, 98, 65, 255}, color.RGBA{83, 111, 75, 255}, color.RGBA{110, 128, 88, 255}, color.RGBA{136, 147, 104, 255}, color.RGBA{165, 172, 132, 255}, color.RGBA{194, 198, 160, 255}), White
	}},
	{Name: "breeze", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{18, 75, 79, 255}, color.RGBA{22, 116, 122, 255}, color.RGBA{26, 157, 165, 255}, color.RGBA{88, 190, 204, 255}, color.RGBA{170, 223, 242, 255}, color.RGBA{158, 216, 235, 255}, color.RGBA{98, 189, 204, 255}, color.RGBA{39, 162, 172, 255}), White
	}},
	{Name: "departure", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{69, 42, 0, 255}, color.RGBA{112, 75, 21, 255}, color.RGBA{206, 160, 102, 255}, color.RGBA{247, 238, 225, 255}, color.RGBA{138, 255, 140, 255}, color.RGBA{0, 246, 0, 255}, color.RGBA{0, 152, 0, 255}, color.RGBA{0, 128, 0, 255}), White
	}},
	{Name: "sherbet", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{255, 120, 64, 255}, color.RGBA{255, 123, 90, 255}, color.RGBA{255, 59, 90, 255}, color.RGBA{255, 116, 140, 255}, color.RGBA{255, 191, 199, 255}, color.RGBA{244, 255, 236, 255}, color.RGBA{131, 255, 106, 255}, color.RGBA{138, 255, 114, 255}), White
	}},
	{Name: "aurora", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{1, 66, 38, 255}, color.RGBA{0, 160, 27, 255}, color.RGBA{0, 215, 17, 255}, color.RGBA{0, 241, 6, 255}, color.RGBA{0, 251, 16, 255}, color.RGBA{0, 232, 41, 255}, color.RGBA{0, 126, 10, 255}, color.RGBA{1, 52, 31, 255}), White
	}},
	{Name: "atlantica", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{13, 55, 169, 255}, color.RGBA{31, 99, 251, 255}, color.RGBA{12, 190, 121, 255}, color.RGBA{3, 207, 54, 255}, color.RGBA{10, 116, 77, 255}, color.RGBA{18, 139, 88, 255}, color.RGBA{26, 189, 94, 255}, color.RGBA{35, 177, 85, 255}), White
	}},
	{Name: "toxy reef", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{13, 223, 133, 255}, color.RGBA{31, 198, 144, 255}, color.RGBA{48, 173, 155, 255}, color.RGBA{65, 149, 166, 255}, color.RGBA{82, 125, 177, 255}, color.RGBA{99, 101, 188, 255}, color.RGBA{116, 76, 199, 255}, color.RGBA{134, 51, 210, 255}), White
	}},
	{Name: "candy", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{237, 162, 42, 255}, color.RGBA{205, 126, 70, 255}, color.RGBA{173, 90, 98, 255}, color.RGBA{142, 56, 124, 255}, color.RGBA{110, 21, 151, 255}, color.RGBA{91, 22, 150, 255}, color.RGBA{66, 20, 147, 255}, color.RGBA{26, 8, 129, 255}), White
	}},
	{Name: "retro clown", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{239, 153, 45, 255}, color.RGBA{235, 129, 56, 255}, color.RGBA{231, 105, 67, 255}, color.RGBA{227, 82, 78, 255}, color.RGBA{214, 73, 107, 255}, color.RGBA{200, 68, 139, 255}, color.RGBA{185, 63, 171, 255}, color.RGBA{170, 57, 204, 255}), White
	}},
	{Name: "sakura", At: func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return Gradient(float64(band)/float64(max(n-1, 1)), color.RGBA{214, 34, 21, 255}, color.RGBA{242, 58, 37, 255}, color.RGBA{247, 63, 52, 255}, color.RGBA{232, 52, 65, 255}, color.RGBA{229, 52, 78, 255}, color.RGBA{244, 69, 93, 255}, color.RGBA{250, 72, 90, 255}, color.RGBA{234, 36, 46, 255}), White
	}},
}

// ByName looks a palette up case-insensitively.
func ByName(name string) (Palette, bool) {
	for _, p := range Palettes {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Palette{}, false
}

// Names lists every shipped palette name, in cycling order.
func Names() []string {
	names := make([]string, len(Palettes))
	for i, p := range Palettes {
		names[i] = p.Name
	}
	return names
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

// Gradient linearly interpolates evenly spaced stops at t in [0,1].
func Gradient(t float64, stops ...color.RGBA) color.RGBA {
	t = max(0, min(1, t)) * float64(len(stops)-1)
	i := min(int(t), len(stops)-2)
	f := t - float64(i)
	a, b := stops[i], stops[i+1]
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

// ErrShipped is returned when a custom palette would take a shipped name.
var ErrShipped = errors.New("shipped palette")

// Axes are the directions a custom gradient can run: across the bands
// (like rainbow) or up each bar (like fire).
var Axes = []string{"bands", "height"}

// Stop is one colour of a custom gradient at a position 0..1.
type Stop struct {
	Pos   float64 `json:"pos"`
	Color string  `json:"color"` // #rrggbb
}

// Custom is a palette made in the UI: a positioned gradient, the axis it
// runs along and the colour of bars' peak markers ("" = white). It lives
// in the state file and turns into a Palette with Palette().
type Custom struct {
	Name  string `json:"name"`
	Stops []Stop `json:"stops"` // 2..16, sorted, first at 0, last at 1
	Axis  string `json:"axis"`  // one of Axes
	Peak  string `json:"peak"`  // #rrggbb or ""
}

// ColorStop is a parsed Stop.
type ColorStop struct {
	Pos   float64
	Color color.RGBA
}

// Palette validates c and builds the palette; the error names the rule
// that failed.
func (c Custom) Palette() (Palette, error) {
	name := strings.TrimSpace(c.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 32 || strings.Contains(name, "/") {
		return Palette{}, fmt.Errorf("name must be 1..32 characters without '/', got %q", c.Name)
	}
	if len(c.Stops) < 2 || len(c.Stops) > 16 {
		return Palette{}, fmt.Errorf("need 2..16 stops, got %d", len(c.Stops))
	}
	c.Stops = slices.Clone(c.Stops) // Color gets normalised below; do not mutate the caller's slice
	stops := make([]ColorStop, len(c.Stops))
	for i, s := range c.Stops {
		if s.Pos < 0 || s.Pos > 1 || (i > 0 && s.Pos < c.Stops[i-1].Pos) {
			return Palette{}, fmt.Errorf("stops must be sorted within 0..1, stop %d is at %v", i, s.Pos)
		}
		col, err := ParseHex(s.Color)
		if err != nil {
			return Palette{}, fmt.Errorf("stop %d: %w", i, err)
		}
		stops[i] = ColorStop{s.Pos, col}
		c.Stops[i].Color = Hex(col)
	}
	if stops[0].Pos != 0 || stops[len(stops)-1].Pos != 1 {
		return Palette{}, fmt.Errorf("the first stop must be at 0 and the last at 1")
	}
	if !slices.Contains(Axes, c.Axis) {
		return Palette{}, fmt.Errorf("unknown axis %q, have: %s", c.Axis, strings.Join(Axes, ", "))
	}
	peak := White
	if c.Peak != "" {
		var err error
		if peak, err = ParseHex(c.Peak); err != nil {
			return Palette{}, fmt.Errorf("peak: %w", err)
		}
		c.Peak = Hex(peak)
	}
	c.Name = name
	p := Palette{Name: name, Custom: &c}
	if c.Axis == "height" {
		p.Relative = true
		p.At = func(_, _, y, h int) (color.RGBA, color.RGBA) { return GradientAt(frac(y, h), stops), peak }
	} else {
		p.At = func(band, n, _, _ int) (color.RGBA, color.RGBA) {
			return GradientAt(float64(band)/float64(max(n-1, 1)), stops), peak
		}
	}
	return p, nil
}

// GradientAt interpolates positioned stops at t in [0,1]; stops are sorted
// with the first at 0 and the last at 1.
func GradientAt(t float64, stops []ColorStop) color.RGBA {
	t = max(0, min(1, t))
	i := 0
	for i < len(stops)-2 && t > stops[i+1].Pos {
		i++
	}
	a, b := stops[i], stops[i+1]
	f := 0.0
	if b.Pos > a.Pos {
		f = (t - a.Pos) / (b.Pos - a.Pos)
	}
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f) }
	return color.RGBA{l(a.Color.R, b.Color.R), l(a.Color.G, b.Color.G), l(a.Color.B, b.Color.B), 255}
}

// Register adds a custom palette, replacing one of the same name
// (case-insensitively). A shipped name is ErrShipped. Only the controller
// goroutine calls this.
func Register(p Palette) error {
	for i, q := range Palettes {
		if strings.EqualFold(q.Name, p.Name) {
			if q.Custom == nil {
				return fmt.Errorf("%q: %w", p.Name, ErrShipped)
			}
			Palettes[i] = p
			return nil
		}
	}
	Palettes = append(Palettes, p)
	return nil
}

// Remove drops a custom palette by name and reports whether there was one.
// A shipped name is never removed.
func Remove(name string) bool {
	i := slices.IndexFunc(Palettes, func(q Palette) bool { return q.Custom != nil && strings.EqualFold(q.Name, name) })
	if i < 0 {
		return false
	}
	Palettes = slices.Delete(Palettes, i, i+1)
	return true
}

// Preview samples n colours along the palette for a UI strip: band i of n
// and row i of n at once, so palettes along either axis show their run.
func Preview(p Palette, n int) []string {
	out := make([]string, n)
	for i := range out {
		c, peak := p.At(i, n, i, n)
		if c.A == 0 {
			c = peak
		}
		out[i] = Hex(c)
	}
	return out
}

// ParseHex reads #rrggbb, any case.
func ParseHex(s string) (color.RGBA, error) {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, fmt.Errorf("colour must be #rrggbb, got %q", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("colour must be #rrggbb, got %q", s)
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, nil
}

// Hex formats a colour as #rrggbb.
func Hex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
