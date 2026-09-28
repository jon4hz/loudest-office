package ripples

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

// disturbed sums the absolute height of the water.
func disturbed(r *Ripples) float32 {
	var sum float32
	for _, h := range r.cur {
		sum += max(h, -h)
	}
	return sum
}

func TestRipplesFillThePanelAndCalmDownInSilence(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	r := New()
	musictest.Sized(r, 16, 8)
	silence := make([]float32, 256)
	musictest.Feed(a, r, silence)
	if lit := musictest.Lit(r.Frame()); lit != 16*8 {
		t.Fatalf("still water should light the whole panel, lit %d of %d", lit, 16*8)
	}
	if d := disturbed(r); d != 0 {
		t.Fatalf("silence should leave the water still, disturbed %v", d)
	}

	r.pebble(8, 4, 2)
	musictest.Feed(a, r, silence)
	if r.at(8, 4) == 2 || r.at(9, 4) == 0 {
		t.Fatalf("a pebble should spread: centre %v, neighbour %v", r.at(8, 4), r.at(9, 4))
	}
	for range 600 {
		musictest.Feed(a, r, silence)
	}
	if d := disturbed(r); d > 0.01 {
		t.Fatalf("ripples should die out, still disturbed %v", d)
	}

	for range 50 {
		musictest.Feed(a, r, musictest.Sine(256, 1000))
	}
	if disturbed(r) == 0 {
		t.Fatal("loud bands should rain on the water")
	}

	musictest.Sized(r, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, r, silence)
}

func TestBeatAndDropThrowPebblesEvenOnAZeroStepTick(t *testing.T) {
	mix := make([]float32, 8)
	mix[6] = 1

	r := New()
	musictest.Sized(r, 16, 8)
	r.Update(bubble.Tick{Signal: dsp.Signal{Mix: mix, BPM: 120, Beats: 1}})
	if disturbed(r) == 0 {
		t.Fatal("a beat should throw a pebble")
	}
	var left, right float32
	for y := range 8 {
		for x := range 8 {
			left += max(r.at(x, y), -r.at(x, y))
			right += max(r.at(x+8, y), -r.at(x+8, y))
		}
	}
	if right <= left {
		t.Fatalf("the beat's pebble should land at the loudest band: left %v, right %v", left, right)
	}

	r = New()
	musictest.Sized(r, 16, 8)
	r.Update(bubble.Tick{Signal: dsp.Signal{Mix: make([]float32, 8), Drop: true}})
	if disturbed(r) == 0 {
		t.Fatal("a drop should make a splash")
	}

	New().Update(bubble.Tick{Signal: dsp.Signal{Mix: mix, Drop: true, Beats: 1}}) // unsized must not panic
}
