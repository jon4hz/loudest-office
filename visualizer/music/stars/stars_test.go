package stars

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestStarsFlyFasterWithEnergyAndWarpOnADrop(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	s := New()
	musictest.Sized(s, 16, 8)
	silence := make([]float32, 256)
	musictest.Feed(a, s, silence)
	if len(s.stars) == 0 {
		t.Fatal("no stars")
	}
	if musictest.Lit(s.Frame()) == 0 {
		t.Fatal("no star drawn")
	}
	speed := func(block []float32) float32 {
		z := s.stars[0].z
		musictest.Feed(a, s, block)
		if s.stars[0].z > z {
			return 0 // respawned, no measurement
		}
		return z - s.stars[0].z
	}
	var slow, fast float32
	for slow == 0 {
		slow = speed(silence)
	}
	for fast == 0 {
		fast = speed(musictest.Sine(256, 1000))
	}
	if fast <= slow {
		t.Fatalf("loud block should move stars faster: %v vs %v", fast, slow)
	}

	s2 := New()
	musictest.Dropped(s2)
	if s2.warp == 0 {
		t.Fatal("drop should start a warp")
	}

	musictest.Sized(s, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, s, silence)
}

func TestStarsSurgeOnTheBeat(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	s := New()
	musictest.Sized(s, 16, 8)
	loud, quiet := musictest.Sine(1024, 1000), musictest.Sine(1024, 1000)
	for i := range quiet {
		quiet[i] *= 0.1
	}
	const period = 21
	musictest.Feed(a, s, quiet) // allocates the stars
	speed := func(block []float32) float32 {
		before := append([]star(nil), s.stars...)
		musictest.Feed(a, s, block)
		var sum float32
		n := 0
		for i, st := range s.stars {
			if st.z < before[i].z {
				sum += before[i].z - st.z
				n++
			}
		}
		return sum / float32(max(n, 1))
	}
	var onBeat, offBeat float32
	for b := range 40 {
		for i := range period {
			block := quiet
			if i == 0 {
				block = loud
			}
			v := speed(block)
			if b >= 30 && i == 1 { // the quiet block right after the beat
				onBeat += v
			}
			if b >= 30 && i == period/2 { // the quiet block halfway to the next
				offBeat += v
			}
		}
	}
	if a.Take().BPM == 0 {
		t.Fatal("no tempo found")
	}
	if onBeat < 2*offBeat {
		t.Fatalf("stars should surge on the beat: %v right after vs %v halfway", onBeat, offBeat)
	}
}

func TestDropSurvivesZeroStepTick(t *testing.T) {
	sig := dsp.Signal{Drop: true, Mix: make([]float32, 8)}

	s := New()
	s.Update(bubble.Tick{Signal: sig, Dt: 0})
	if s.warp == 0 {
		t.Fatal("stars: warp not started by a zero-step drop tick")
	}
}
