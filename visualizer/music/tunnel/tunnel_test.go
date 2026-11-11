package tunnel

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestRingsGrowFromTheCentreAndLeave(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	tu := New()
	musictest.Sized(tu, 16, 8)
	silence := make([]float32, 1024)
	tu.rings = append(tu.rings, 0)
	musictest.Feed(a, tu, silence)
	if len(tu.rings) != 1 || tu.rings[0] <= 0 {
		t.Fatalf("the ring should have grown a little, got %v", tu.rings)
	}
	if c := musictest.Column(tu.Frame(), 7); c[3] != '#' && c[4] != '#' {
		t.Fatalf("a young ring sits at the centre, got %q", c)
	}
	for range 200 {
		musictest.Feed(a, tu, silence)
	}
	if len(tu.rings) != 0 {
		t.Fatalf("rings should leave the panel in silence, still have %v", tu.rings)
	}
	musictest.Sized(tu, 1, 1) // resize must not panic
	tu.rings = append(tu.rings, 0.5)
	musictest.Feed(a, tu, musictest.Sine(1024, 440))
}
