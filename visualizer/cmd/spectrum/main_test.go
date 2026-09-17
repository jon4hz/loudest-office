package main

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/spectrum"
)

func TestNextFlavorNeverRepeats(t *testing.T) {
	cur := flavor{0, spectrum.Stacked, spectrum.Falling}
	allowed := []spectrum.Layout{spectrum.Mirrored, spectrum.HMirrored}
	for range 500 {
		f := nextFlavor(cur, allowed)
		if f == cur {
			t.Fatal("picked the current flavor")
		}
		if f.pal >= len(spectrum.Palettes) || f.peaks > spectrum.NoPeaks {
			t.Fatalf("out of range: %+v", f)
		}
		if f.lay != spectrum.Mirrored && f.lay != spectrum.HMirrored {
			t.Fatalf("layout %v not in the allowed list", f.lay)
		}
		cur = f
	}
}
