package helix

import (
	"strings"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestHelixSpansThePanelAndTurnsFasterWhenLoud(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	h := New()
	musictest.Sized(h, 16, 8)
	silence := make([]float32, 1024)
	musictest.Feed(a, h, silence)
	for x := range 16 {
		if !strings.Contains(musictest.Column(h.Frame(), x), "#") {
			t.Fatalf("column %d is dark, a strand should cross every column", x)
		}
	}
	p := h.phase
	musictest.Feed(a, h, silence)
	slow := h.phase - p
	p = h.phase
	musictest.Feed(a, h, musictest.Sine(1024, 440))
	if fast := h.phase - p; fast <= slow {
		t.Fatalf("a loud block should turn the helix faster: %v vs %v", fast, slow)
	}
	musictest.Sized(h, 2, 1) // resize must not panic
	musictest.Feed(a, h, silence)
}
