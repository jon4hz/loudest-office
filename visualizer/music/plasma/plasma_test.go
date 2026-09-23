package plasma

import (
	"slices"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestPlasmaFillsThePanelAndFlowsFasterWithEnergy(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	p := New()
	musictest.Sized(p, 16, 8)
	silence := make([]float32, 256)
	musictest.Feed(a, p, silence)
	if lit := musictest.Lit(p.Frame()); lit != 16*8 {
		t.Fatalf("plasma should light the whole panel in silence, lit %d of %d", lit, 16*8)
	}
	flow := func(block []float32) float64 {
		before := p.t
		musictest.Feed(a, p, block)
		return p.t - before
	}
	slow, fast := flow(silence), flow(musictest.Sine(256, 1000))
	if slow <= 0 || fast <= slow {
		t.Fatalf("loud block should flow faster: %v vs %v", fast, slow)
	}

	musictest.Sized(p, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, p, silence)
}

func TestPlasmaPulsesOnTheBeat(t *testing.T) {
	p := New()
	musictest.Sized(p, 16, 8)
	mix := make([]float32, 8)
	p.Update(bubble.Tick{Signal: dsp.Signal{Mix: mix, BPM: 120, Beat: 0}})
	onBeat := p.bright
	p.Update(bubble.Tick{Signal: dsp.Signal{Mix: mix, BPM: 120, Beat: 0.5}})
	if p.bright >= onBeat {
		t.Fatalf("plasma should be brightest on the beat: %v on it vs %v halfway", onBeat, p.bright)
	}
}

func TestPlasmaDefaultsToEveryPaletteButTheFlags(t *testing.T) {
	p := New()
	if slices.Contains(p.Set.Palettes, "ukraine") || !slices.Contains(p.Set.Palettes, "fire") {
		t.Fatalf("want every palette but the flags, have %v", p.Set.Palettes)
	}
	for range 50 {
		p.Update(bubble.Activate{})
		if !slices.Contains(p.Set.Palettes, p.Palette.Name) {
			t.Fatalf("picked %q, want one of %v", p.Palette.Name, p.Set.Palettes)
		}
	}
	if err := music.CheckPalettes(p.Set.Palettes); err != nil {
		t.Fatal(err)
	}
}

func TestDropSurvivesZeroStepTick(t *testing.T) {
	p := New()
	p.Update(bubble.Tick{Signal: dsp.Signal{Drop: true, Mix: make([]float32, 8)}, Dt: 0})
	if p.hue == 0 {
		t.Fatal("plasma: hue not shifted by a zero-step drop tick")
	}
}
