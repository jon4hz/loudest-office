package polygon

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestPolygonBouncesInsideAndFliesFasterWhenLoud(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	p := New()
	musictest.Sized(p, 16, 8)
	travel := func(block []float32) (sum float32) {
		for range 300 {
			before := p.verts
			musictest.Feed(a, p, block)
			for i, v := range p.verts {
				if v.x < 0 || v.x > 1 || v.y < 0 || v.y > 1 {
					t.Fatalf("corner %d left the frame: %+v", i, v)
				}
				sum += max(v.x-before[i].x, before[i].x-v.x) + max(v.y-before[i].y, before[i].y-v.y)
			}
		}
		return sum
	}
	slow := travel(make([]float32, 1024))
	if musictest.Lit(p.Frame()) == 0 {
		t.Fatal("nothing drawn")
	}
	if len(p.history) != trail {
		t.Fatalf("trail of %d, want %d", len(p.history), trail)
	}
	if fast := travel(musictest.Noise(1024, 1)); fast < 2*slow {
		t.Fatalf("loud should fly faster: %v vs %v in silence", fast, slow)
	}

	musictest.Sized(p, 1, 1) // resize must not panic
	a.SetBands(2)            // fewer bands than corners
	musictest.Feed(a, p, make([]float32, 1024))
}

func TestDropKicksOnAZeroStepTick(t *testing.T) {
	p := New()
	p.Update(bubble.Tick{Signal: dsp.Signal{Drop: true, Mix: make([]float32, 8)}, Dt: 0})
	if p.kick == 0 {
		t.Fatal("drop did not kick")
	}
}
