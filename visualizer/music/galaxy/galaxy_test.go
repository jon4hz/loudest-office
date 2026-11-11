package galaxy

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestStarsKeepTheirOrbitAndSpinFasterWhenLoud(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	g := New()
	musictest.Sized(g, 16, 8)
	silence := make([]float32, 1024)
	musictest.Feed(a, g, silence)
	if musictest.Lit(g.Frame()) == 0 {
		t.Fatal("no star drawn")
	}
	r, ang := g.stars[0].r, g.stars[0].a
	musictest.Feed(a, g, silence)
	slow := g.stars[0].a - ang
	if g.stars[0].r != r || slow <= 0 {
		t.Fatalf("a star should move along its orbit, r %v -> %v, turned %v", r, g.stars[0].r, slow)
	}
	ang = g.stars[0].a
	musictest.Feed(a, g, musictest.Sine(1024, 440))
	if fast := g.stars[0].a - ang; fast <= slow {
		t.Fatalf("a loud block should spin faster: %v vs %v", fast, slow)
	}
	musictest.Sized(g, 1, 1) // resize must not panic
	musictest.Feed(a, g, silence)
}
