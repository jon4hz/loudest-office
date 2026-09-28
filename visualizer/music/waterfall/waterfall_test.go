package waterfall

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestWaterfallPaintsTheBottomRowAndScrollsUp(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	w := New()
	musictest.Sized(w, 16, 8)
	w.Update(bubble.Activate{})
	noise := musictest.Noise(256, 0.5)
	musictest.Feed(a, w, noise, noise) // two steps: one scroll
	frame := w.Frame()
	if musictest.Lit(frame[7:]) == 0 || musictest.Lit(frame[:7]) != 0 {
		t.Fatalf("noise should paint only the bottom row:\n%v", musictest.LitColumns(frame))
	}
	silence := make([]float32, 256)
	musictest.Feed(a, w, silence, silence)
	frame = w.Frame()
	if musictest.Lit(frame[6:7]) == 0 || musictest.Lit(frame[7:]) != 0 {
		t.Fatalf("the painted row should have moved up one:\n%v", musictest.LitColumns(frame))
	}
	musictest.Sized(w, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, w, silence, silence)
}
