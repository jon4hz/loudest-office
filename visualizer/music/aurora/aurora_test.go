package aurora

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestAuroraGlowsInSilenceAndDriftsFasterWithEnergy(t *testing.T) {
	an := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	a := New()
	musictest.Sized(a, 16, 8)
	a.Update(bubble.Activate{})
	silence := make([]float32, 256)
	musictest.Feed(an, a, silence)
	frame := a.Frame()
	if musictest.Lit(frame[3:5]) == 0 || musictest.Lit(frame) == 16*8 {
		t.Fatalf("aurora should light the middle but not the whole panel in silence:\n%v", musictest.LitColumns(frame))
	}
	drift := func(block []float32) float64 {
		before := a.t
		musictest.Feed(an, a, block)
		return a.t - before
	}
	slow, fast := drift(silence), drift(musictest.Noise(256, 0.5))
	if slow <= 0 || fast <= slow {
		t.Fatalf("a loud block should drift faster: %v vs %v", fast, slow)
	}
	musictest.Sized(a, 3, 1) // resize must not panic
	an.SetBands(4)
	musictest.Feed(an, a, silence)
}
