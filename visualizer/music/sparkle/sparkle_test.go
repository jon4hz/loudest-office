package sparkle

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestSparkleLightsWithTheMusicAndDiesInSilence(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	s := New()
	musictest.Sized(s, 16, 8)
	for range 3 {
		musictest.Feed(a, s, musictest.Sine(1024, 440))
	}
	if musictest.Lit(s.Frame()) == 0 {
		t.Fatal("a loud block should light some pixels")
	}
	silence := make([]float32, 1024)
	for range 30 {
		musictest.Feed(a, s, silence)
	}
	if n := musictest.Lit(s.Frame()); n != 0 {
		t.Fatalf("silence should go dark, %d still lit", n)
	}
	musictest.Sized(s, 1, 1) // resize must not panic
	musictest.Feed(a, s, musictest.Sine(1024, 440))
}
