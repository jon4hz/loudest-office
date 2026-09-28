package spray

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestSprayEmitsUpwardsThenFallsAndDies(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	s := New()
	musictest.Sized(s, 16, 8)
	s.Update(bubble.Activate{})
	s.radial = false
	noise := musictest.Noise(256, 0.5)
	for range 5 {
		musictest.Feed(a, s, noise)
	}
	if len(s.parts) == 0 || musictest.Lit(s.Frame()) == 0 {
		t.Fatal("noise should spray particles")
	}
	for _, p := range s.parts {
		if p.y >= 7 {
			t.Fatalf("a fresh particle should have left the bottom row: %+v", p)
		}
	}
	silence := make([]float32, 256)
	for range 300 {
		musictest.Feed(a, s, silence)
	}
	if len(s.parts) != 0 || musictest.Lit(s.Frame()) != 0 {
		t.Fatalf("silence should let every particle fall out and the trails fade: %d left, %d lit", len(s.parts), musictest.Lit(s.Frame()))
	}
}

func TestSprayRadialFliesOutFromTheCentre(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	s := New()
	musictest.Sized(s, 16, 8)
	s.radial = true
	noise := musictest.Noise(256, 0.5)
	for range 3 {
		musictest.Feed(a, s, noise)
	}
	if len(s.parts) == 0 {
		t.Fatal("noise should spray particles")
	}
	for _, p := range s.parts {
		if p.vx*(p.x-8)+p.vy*(p.y-4) < 0 {
			t.Fatalf("a radial particle should move away from the centre: %+v", p)
		}
	}
	musictest.Sized(s, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, s, noise)
}
