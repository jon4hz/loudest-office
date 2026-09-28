package palette

import (
	"errors"
	"image/color"
	"slices"
	"strings"
	"testing"
)

func TestPalettesCoverEveryRow(t *testing.T) {
	if len(Palettes) < 17 {
		t.Fatalf("expected the shipped palettes, got %d", len(Palettes))
	}
	for _, p := range Palettes {
		for y := 0; y < 16; y++ {
			bar, peak := p.At(3, 32, y, 16)
			if bar.A == 0 && peak.A == 0 {
				t.Fatalf("%s draws nothing at row %d", p.Name, y)
			}
		}
	}
	if _, ok := ByName("rainbow"); !ok {
		t.Fatal("rainbow missing")
	}
	if _, ok := ByName("nope"); ok {
		t.Fatal("unknown palette found")
	}
}

func TestTriBarColorsByHeight(t *testing.T) {
	p, _ := ByName("tribar")
	lo, _ := p.At(0, 8, 0, 30)
	hi, _ := p.At(0, 8, 29, 30)
	if lo.G != 255 || lo.R != 0 || hi.R != 255 || hi.G != 0 {
		t.Fatalf("bottom %v top %v", lo, hi)
	}
}

func TestBiStripes(t *testing.T) {
	p, ok := ByName("bi pride")
	if !ok {
		t.Fatal("bi missing")
	}
	bottom, _ := p.At(0, 8, 0, 10)
	mid, _ := p.At(0, 8, 4, 10)
	top, _ := p.At(0, 8, 9, 10)
	if bottom != (color.RGBA{0xD6, 0x02, 0x70, 255}) || mid != (color.RGBA{0x9B, 0x4F, 0x96, 255}) || top != (color.RGBA{0x00, 0x38, 0xA8, 255}) {
		t.Fatalf("bottom %v mid %v top %v", bottom, mid, top)
	}
}

func TestTransStripes(t *testing.T) {
	p, ok := ByName("trans pride")
	if !ok {
		t.Fatal("trans missing")
	}
	blue, pink := color.RGBA{0x5B, 0xCE, 0xFA, 255}, color.RGBA{0xF5, 0xA9, 0xB8, 255}
	want := []color.RGBA{blue, pink, White, pink, blue}
	for i, w := range want {
		if got, _ := p.At(0, 8, i*2, 10); got != w {
			t.Fatalf("stripe %d: got %v want %v", i, got, w)
		}
	}
}

func TestNewPalettes(t *testing.T) {
	at := func(name string, y, h int) color.RGBA {
		p, ok := ByName(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		c, _ := p.At(0, 8, y, h)
		return c
	}
	if at("fire", 9, 10) != White || at("synthwave", 9, 10) != White {
		t.Error("fire and synthwave need a white tip on the top pixel")
	}
	if at("fire", 0, 10) != (color.RGBA{128, 0, 0, 255}) {
		t.Errorf("fire bottom should be dark red, got %v", at("fire", 0, 10))
	}
	if at("ukraine", 0, 10) != (color.RGBA{255, 213, 0, 255}) || at("ukraine", 5, 10) != (color.RGBA{0, 87, 183, 255}) {
		t.Error("ukraine should be yellow below blue")
	}
	if at("white", 0, 10) != White || at("ice", 9, 10) != White {
		t.Error("white bars / ice top should be white")
	}
	for _, name := range []string{"fire", "ice", "ukraine", "synthwave", "bi pride", "trans pride", "antifa"} {
		if p, _ := ByName(name); !p.Relative {
			t.Errorf("%s should be relative", name)
		}
	}
	// freq colours by band, not height; matrix gets brighter with the band
	freq, _ := ByName("freq")
	lo, _ := freq.At(0, 8, 0, 10)
	hi, _ := freq.At(7, 8, 0, 10)
	if lo == hi {
		t.Error("freq should differ between bands")
	}
	mat, _ := ByName("matrix")
	dim, _ := mat.At(0, 8, 0, 10)
	bright, _ := mat.At(7, 8, 0, 10)
	if dim.G >= bright.G || dim.R != 0 || dim.B != 0 {
		t.Errorf("matrix should be green getting brighter by band: %v %v", dim, bright)
	}
}

func TestAntifaStripes(t *testing.T) {
	p, ok := ByName("antifa")
	if !ok || !p.Relative {
		t.Fatal("antifa missing or not relative")
	}
	lo, _ := p.At(0, 8, 0, 10)
	hi, _ := p.At(0, 8, 5, 10)
	if lo != (color.RGBA{228, 0, 43, 255}) || hi != (color.RGBA{50, 50, 50, 255}) {
		t.Fatalf("want red below near-black, got %v %v", lo, hi)
	}
}

// Flags have a right way up, so a mode that hangs its bars from the top must
// not mirror them.
func TestFlagsAreUpright(t *testing.T) {
	for _, p := range Palettes {
		flag := slices.Contains([]string{"bi pride", "trans pride", "ukraine", "antifa"}, p.Name)
		if p.Upright != flag {
			t.Errorf("%s: Upright = %v, want %v", p.Name, p.Upright, flag)
		}
	}
}

func custom() Custom {
	return Custom{Name: "lagoon", Axis: "bands", Peak: "#ff00ff",
		Stops: []Stop{{0, "#000080"}, {0.5, "#00FFFF"}, {1, "#ffffff"}}}
}

func TestCustomPaletteAcrossBands(t *testing.T) {
	p, err := custom().Palette()
	if err != nil {
		t.Fatal(err)
	}
	if p.Relative || p.Custom == nil || p.Name != "lagoon" {
		t.Fatalf("palette = %+v", p)
	}
	first, peak := p.At(0, 8, 0, 8)
	last, _ := p.At(7, 8, 0, 8)
	mid, _ := p.At(3, 7, 0, 8) // band 3 of 7 is t=0.5
	if first != (color.RGBA{0, 0, 128, 255}) || last != White || mid != (color.RGBA{0, 255, 255, 255}) {
		t.Fatalf("first %v mid %v last %v", first, mid, last)
	}
	if peak != (color.RGBA{255, 0, 255, 255}) {
		t.Fatalf("peak = %v", peak)
	}
}

func TestCustomPaletteUpTheBarIsRelative(t *testing.T) {
	c := custom()
	c.Axis, c.Peak = "height", ""
	p, err := c.Palette()
	if err != nil {
		t.Fatal(err)
	}
	bottom, peak := p.At(5, 8, 0, 4)
	top, _ := p.At(0, 8, 3, 4)
	if !p.Relative || bottom != (color.RGBA{0, 0, 128, 255}) || top != White || peak != White {
		t.Fatalf("relative %v bottom %v top %v peak %v", p.Relative, bottom, top, peak)
	}
}

func TestCustomPaletteRejects(t *testing.T) {
	bad := map[string]func(*Custom){
		"empty name":     func(c *Custom) { c.Name = "  " },
		"long name":      func(c *Custom) { c.Name = strings.Repeat("x", 33) },
		"slash":          func(c *Custom) { c.Name = "a/b" },
		"one stop":       func(c *Custom) { c.Stops = c.Stops[:1] },
		"17 stops":       func(c *Custom) { c.Stops = append(c.Stops, make([]Stop, 14)...) },
		"unsorted":       func(c *Custom) { c.Stops[1].Pos = 0.9; c.Stops[2].Pos = 0.8 },
		"out of range":   func(c *Custom) { c.Stops[1].Pos = 1.5 },
		"first not at 0": func(c *Custom) { c.Stops[0].Pos = 0.1 },
		"last not at 1":  func(c *Custom) { c.Stops[2].Pos = 0.9 },
		"bad colour":     func(c *Custom) { c.Stops[0].Color = "red" },
		"bad peak":       func(c *Custom) { c.Peak = "#12345" },
		"bad axis":       func(c *Custom) { c.Axis = "rows" },
	}
	for name, mutate := range bad {
		c := custom()
		mutate(&c)
		if _, err := c.Palette(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestCustomPaletteDoesNotMutateCallerOnFailure guards against Palette()
// normalising hex case in place on the caller's Stops slice: a failure after
// the stop loop (a bad axis) must leave the caller's Custom untouched.
func TestCustomPaletteDoesNotMutateCallerOnFailure(t *testing.T) {
	c := custom()
	c.Axis = "rows"
	if _, err := c.Palette(); err == nil {
		t.Fatal("bad axis accepted")
	}
	if got := c.Stops[1].Color; got != "#00FFFF" {
		t.Fatalf("caller's stop colour = %q, want untouched #00FFFF", got)
	}
}

func TestRegisterRemoveAndShippedNames(t *testing.T) {
	p, _ := custom().Palette()
	if got := p.Custom.Stops[1].Color; got != "#00ffff" {
		t.Fatalf("stop colour = %q, want lowercase #00ffff", got)
	}
	if err := Register(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Remove("lagoon") })
	if q, ok := ByName("LAGOON"); !ok || q.Custom == nil {
		t.Fatal("registered palette not found by name")
	}
	if !slices.Contains(Names(), "lagoon") {
		t.Fatal("registered palette missing from Names")
	}
	p2, _ := custom().Palette()
	p2.Custom.Peak = "#000000"
	if err := Register(p2); err != nil || len(Names()) != len(Palettes) {
		t.Fatalf("re-register should replace: %v", err)
	}
	for _, name := range []string{"rainbow", "Rainbow"} {
		c := custom()
		c.Name = name
		q, _ := c.Palette()
		if err := Register(q); !errors.Is(err, ErrShipped) {
			t.Fatalf("%q: err = %v, want ErrShipped", name, err)
		}
	}
	if Remove("rainbow") {
		t.Fatal("a shipped palette must not be removable")
	}
	if !Remove("lagoon") || Remove("lagoon") {
		t.Fatal("Remove should report the first removal only")
	}
}

func TestPreviewSamplesEveryPalette(t *testing.T) {
	for _, p := range Palettes {
		cols := Preview(p, 16)
		if len(cols) != 16 {
			t.Fatalf("%s: %d colours", p.Name, len(cols))
		}
		for _, c := range cols {
			if _, err := ParseHex(c); err != nil {
				t.Fatalf("%s: %q: %v", p.Name, c, err)
			}
		}
	}
	if cols := Preview(Palettes[0], 3); cols[0] != "#ff0000" { // rainbow starts red
		t.Fatalf("rainbow preview = %v", cols)
	}
}
