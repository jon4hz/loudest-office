package fireworks

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestFireworksLaunchFromLoudBandsAndBurst(t *testing.T) {
	// 4 bands: 2.15 kHz is band 2 and the only band with a level.
	a := dsp.NewAnalyzer(1, 4, 44100, 1024, 0, false)
	fw := New()
	musictest.Sized(fw, 16, 16)
	loud := musictest.Sine(1024, 2150)
	for range 300 { // settle the drop detector: the onset itself counts as a drop
		musictest.Feed(a, fw, loud)
	}
	fw.parts = nil
	for range 100 {
		musictest.Feed(a, fw, loud)
	}
	if len(fw.parts) == 0 {
		t.Fatal("no rockets launched")
	}
	for _, p := range fw.parts {
		if p.band != 2 {
			t.Fatalf("particle from silent band %d", p.band)
		}
	}
	silence := make([]float32, 1024)
	sparks := 0
	for i := 0; i < 400 && len(fw.parts) > 0; i++ {
		musictest.Feed(a, fw, silence)
		for _, p := range fw.parts {
			if !p.rocket {
				sparks++
			}
		}
	}
	if sparks == 0 {
		t.Fatal("no rocket ever burst into sparks")
	}
	if len(fw.parts) != 0 {
		t.Fatalf("%d particles never died", len(fw.parts))
	}
	for range 40 {
		musictest.Feed(a, fw, silence)
		if len(fw.parts) != 0 {
			t.Fatal("silence launched a rocket")
		}
	}
}

func TestFireworksVolleyOnADrop(t *testing.T) {
	fw := New()
	a := musictest.Dropped(fw)
	rockets := 0
	for _, p := range fw.parts {
		if p.rocket {
			rockets++
		}
	}
	if rockets < 3 {
		t.Fatalf("drop should fire a volley, got %d rockets", rockets)
	}
	if l := musictest.Lit(fw.Frame()); l <= rockets {
		t.Fatal("rockets should draw a tail below the head")
	}
	musictest.Sized(fw, 3, 1) // resize must not panic
	a.SetBands(2)
	musictest.Feed(a, fw, make([]float32, 256))
}

func TestDropSurvivesZeroStepTick(t *testing.T) {
	sig := dsp.Signal{Drop: true, Mix: make([]float32, 8)}

	fw := New()
	fw.Update(bubble.Tick{Signal: sig, Dt: 0})
	rockets := 0
	for _, p := range fw.parts {
		if p.rocket {
			rockets++
		}
	}
	if rockets < 3 {
		t.Fatalf("fireworks: %d rockets after a zero-step drop, want >= 3", rockets)
	}
}
