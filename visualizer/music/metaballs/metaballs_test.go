package metaballs

import (
	"math"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestMetaballsSwellWithNoiseAndStayInside(t *testing.T) {
	an := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	m := New()
	musictest.Sized(m, 16, 8)
	m.Update(bubble.Activate{})
	silence := make([]float32, 256)
	for range 200 {
		musictest.Feed(an, m, silence)
	}
	quiet := musictest.Lit(m.Frame())
	if quiet == 0 || quiet == 16*8 || len(m.balls) != minCount {
		t.Fatalf("idle blobs should light some of the panel, not none or all: %d lit, %d blobs\n%v", quiet, len(m.balls), musictest.LitColumns(m.Frame()))
	}
	for range 200 {
		musictest.Feed(an, m, musictest.Noise(256, 0.5))
	}
	var mass float64 // births add mass, absorption only moves it around
	for _, b := range m.balls {
		mass += b.mass
	}
	if loud := musictest.Lit(m.Frame()); loud <= quiet || mass <= minCount {
		t.Fatalf("noise should swell the blobs and birth more: %d lit vs %d idle, %v blobs' worth\n%v", loud, quiet, mass, musictest.LitColumns(m.Frame()))
	}
	for _, b := range m.balls {
		if b.x < 0 || b.x > 15 || b.y < 0 || b.y > 7 || b.r < 0 || b.r > (max(1, minR*m.scale())+(maxR-minR)*m.scale())*math.Sqrt(maxMass)+0.01 {
			t.Fatalf("a blob should stay inside the panel with a sane radius: %+v", b)
		}
	}
	musictest.Sized(m, 3, 1) // resize must not panic
	an.SetBands(4)
	musictest.Feed(an, m, silence)
}

func TestMetaballsBirthOnBeatAndBass(t *testing.T) {
	m := New()
	musictest.Sized(m, 16, 8)
	sig := dsp.Signal{Mix: make([]float32, 8)}
	for range 10 { // let the population fill to its minimum first
		m.Update(bubble.Tick{Signal: sig, Dt: musictest.Step})
	}
	before := len(m.balls)
	for range beatsPerBirth {
		sig.Beats++
		m.Update(bubble.Tick{Signal: sig, Dt: musictest.Step})
	}
	if len(m.balls) != before+1 {
		t.Fatalf("every %d beats should birth one blob: %d -> %d", beatsPerBirth, before, len(m.balls))
	}
	sig.Mix[0], sig.Mix[1] = 1, 1 // loud bass, quiet everything else
	if l := loudness(sig); l != 1 {
		t.Fatalf("bass alone should count as full loudness, got %v (energy %v)", l, sig.Energy)
	}
}

func TestMetaballsFadeOut(t *testing.T) {
	m := New()
	musictest.Sized(m, 16, 8)
	m.Bands = 8
	sig := dsp.Signal{Mix: make([]float32, 8)}
	m.balls = []ball{{x: 8, y: 4, dx: 1, r: 1, mass: 1, age: fadeSteps, life: fadeSteps + fadeSteps/2}}
	for i := 0; i < maxCount; i++ { // fill up so nothing is born meanwhile
		m.balls = append(m.balls, ball{x: 8, y: 4, dx: 1, r: 0, mass: 1, age: 0, life: 100000, free: 1 << 30})
	}
	m.Steps = 1
	for range fadeSteps {
		m.Steps++
		m.step(sig)
	}
	if len(m.balls) != maxCount {
		t.Fatalf("a blob past its life should be buried: %d blobs, first %+v", len(m.balls), m.balls[0])
	}
}

func TestMetaballsFuseAbsorbAndSplit(t *testing.T) {
	m := New()
	musictest.Sized(m, 32, 16)
	m.Bands = 8
	sig := dsp.Signal{Mix: make([]float32, 8)} // no energy: nothing is born
	m.balls = []ball{
		{x: 8, y: 8, dx: 1, r: 3, mass: 1, age: fadeSteps, life: 100000},
		{x: 10, y: 8, dy: 1, r: 3, mass: 1, age: fadeSteps, life: 100000},
	}
	for len(m.balls) < maxCount { // park the rest far away, never fusing, so nothing is born
		m.balls = append(m.balls, ball{x: 30, y: 15, dx: 1, r: 1, mass: 1, age: fadeSteps, life: 100000, free: 1 << 30})
	}
	m.Steps = 1 // off the split schedule
	gap := func() float64 { return math.Hypot(m.balls[1].x-m.balls[0].x, m.balls[1].y-m.balls[0].y) }
	run := func(n int) {
		for range n {
			m.Steps++
			m.step(sig)
		}
	}
	before := gap()
	run(10)
	a, b := m.balls[0], m.balls[1]
	if dot := a.dx*b.dx + a.dy*b.dy; dot < 0.5 {
		t.Fatalf("overlapping blobs should share a heading: %+v %+v", a, b)
	}
	if gap() >= before {
		t.Fatalf("overlapping blobs should sink together: %v -> %v", before, gap())
	}
	run(40)
	if len(m.balls) != maxCount-1 || m.balls[0].mass != 2 || m.balls[0].r < max(1, minR*m.scale())*math.Sqrt2-0.05 { // rests at root two an idle blob
		t.Fatalf("blobs whose centres meet should be absorbed into one bigger blob: %d blobs, %+v", len(m.balls), m.balls[0])
	}
	m.split(0)
	if len(m.balls) != maxCount || m.balls[0].mass != 1 || m.balls[maxCount-1].mass != 1 {
		t.Fatalf("splitting a heavy blob should pinch off a child: %d blobs, %+v", len(m.balls), m.balls)
	}
	before = math.Hypot(m.balls[maxCount-1].x-m.balls[0].x, m.balls[maxCount-1].y-m.balls[0].y)
	run(20)
	if after := math.Hypot(m.balls[maxCount-1].x-m.balls[0].x, m.balls[maxCount-1].y-m.balls[0].y); after <= before {
		t.Fatalf("split blobs should fly apart: %v -> %v", before, after)
	}
}
