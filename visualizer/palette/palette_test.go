package palette

import (
	"image/color"
	"slices"
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
