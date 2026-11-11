package ribbon

import (
	"strings"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestRibbonIsUnbrokenAndThickensWithTheMusic(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	r := New()
	musictest.Sized(r, 16, 8)
	silence := make([]float32, 1024)
	for range 5 {
		musictest.Feed(a, r, silence)
	}
	for x := range 16 {
		if !strings.Contains(musictest.Column(r.Frame(), x), "#") {
			t.Fatalf("column %d is dark, the ribbon should never break", x)
		}
	}
	thin := musictest.Lit(r.Frame())
	for range 5 {
		musictest.Feed(a, r, musictest.Sine(1024, 440))
	}
	if thick := musictest.Lit(r.Frame()); thick <= thin {
		t.Fatalf("a loud block should thicken the ribbon: %d vs %d lit", thick, thin)
	}
	musictest.Sized(r, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, r, silence)
}
