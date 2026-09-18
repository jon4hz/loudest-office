package main

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/spectrum"
)

func TestNextFlavorNeverRepeats(t *testing.T) {
	cur := flavor{0, spectrum.Stacked, spectrum.Falling, spectrum.Bars, false}
	allowed := []spectrum.Layout{spectrum.Mirrored, spectrum.HMirrored}
	modes := []spectrum.Mode{spectrum.Bars, spectrum.Life}
	for range 500 {
		f := nextFlavor(cur, allowed, modes)
		if f == cur {
			t.Fatal("picked the current flavor")
		}
		if f.pal >= len(spectrum.Palettes) || f.peaks > spectrum.NoPeaks {
			t.Fatalf("out of range: %+v", f)
		}
		if f.lay != spectrum.Mirrored && f.lay != spectrum.HMirrored {
			t.Fatalf("layout %v not in the allowed list", f.lay)
		}
		if f.mode == spectrum.Fire {
			t.Fatal("picked a mode not in the list")
		}
		cur = f
	}
}

func TestLoopModesRepeatsWeigh(t *testing.T) {
	cur := flavor{0, spectrum.Stacked, spectrum.Falling, spectrum.Bars, false}
	modes := []spectrum.Mode{spectrum.Bars, spectrum.Bars, spectrum.Bars, spectrum.Fire}
	fire := 0
	for range 2000 {
		if nextFlavor(cur, []spectrum.Layout{spectrum.Mirrored}, modes).mode == spectrum.Fire {
			fire++
		}
	}
	if fire < 350 || fire > 650 { // about a quarter
		t.Fatalf("fire picked %d of 2000 with a 1-in-4 weight", fire)
	}
}

func TestLoopTrailsOneInTwenty(t *testing.T) {
	cur := flavor{0, spectrum.Stacked, spectrum.Falling, spectrum.Bars, false}
	trails := 0
	for range 4000 {
		if nextFlavor(cur, []spectrum.Layout{spectrum.Mirrored}, []spectrum.Mode{spectrum.Bars}).trails {
			trails++
		}
	}
	if trails < 120 || trails > 300 { // about 5%
		t.Fatalf("trails on in %d of 4000 picks, want about 200", trails)
	}
}

func TestLoopTrailsOnlyForBars(t *testing.T) {
	cur := flavor{0, spectrum.Stacked, spectrum.Falling, spectrum.Bars, false}
	for range 2000 {
		if nextFlavor(cur, []spectrum.Layout{spectrum.Mirrored}, []spectrum.Mode{spectrum.Fire, spectrum.Life}).trails {
			t.Fatal("trails switched on for a mode other than bars")
		}
	}
}

func TestLoopNeverPicksAnInvisibleFlavor(t *testing.T) {
	hidden := -1
	for i, p := range spectrum.Palettes {
		if p.Name == "peaks" {
			hidden = i
		}
	}
	cur := flavor{0, spectrum.Stacked, spectrum.Falling, spectrum.Bars, false}
	for range 5000 {
		f := nextFlavor(cur, []spectrum.Layout{spectrum.Mirrored}, []spectrum.Mode{spectrum.Bars})
		if f.pal == hidden && f.peaks == spectrum.NoPeaks {
			t.Fatal("picked the peaks palette with no peaks: nothing would be drawn")
		}
	}
}
