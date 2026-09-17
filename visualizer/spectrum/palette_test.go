package spectrum

import (
	"image/color"
	"testing"
)

func TestPalettesCoverEveryRow(t *testing.T) {
	if len(Palettes) < 9 {
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
	if _, ok := PaletteByName("rainbow"); !ok {
		t.Fatal("rainbow missing")
	}
	if _, ok := PaletteByName("nope"); ok {
		t.Fatal("unknown palette found")
	}
}

func TestTriBarColorsByHeight(t *testing.T) {
	p, _ := PaletteByName("tribar")
	lo, _ := p.At(0, 8, 0, 30)
	hi, _ := p.At(0, 8, 29, 30)
	if lo.G != 255 || lo.R != 0 || hi.R != 255 || hi.G != 0 {
		t.Fatalf("bottom %v top %v", lo, hi)
	}
}

func TestBiStripes(t *testing.T) {
	p, ok := PaletteByName("bi")
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
	p, ok := PaletteByName("trans")
	if !ok {
		t.Fatal("trans missing")
	}
	blue, pink := color.RGBA{0x5B, 0xCE, 0xFA, 255}, color.RGBA{0xF5, 0xA9, 0xB8, 255}
	want := []color.RGBA{blue, pink, white, pink, blue}
	for i, w := range want {
		if got, _ := p.At(0, 8, i*2, 10); got != w {
			t.Fatalf("stripe %d: got %v want %v", i, got, w)
		}
	}
}
