package spectrum

import "testing"

func TestPalettesCoverEveryRow(t *testing.T) {
	if len(Palettes) < 7 {
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
