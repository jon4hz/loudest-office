package parrot

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestParrotDancesFasterWhenLoud(t *testing.T) {
	if len(parrotFrames) != 10 {
		t.Fatalf("%d parrot frames embedded", len(parrotFrames))
	}
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	p := New()
	musictest.Sized(p, 50, 36)
	silence := make([]float32, 256)
	advance := func(block []float32) int {
		n := 0
		for range 40 {
			before := p.idx
			musictest.Feed(a, p, block)
			n += (p.idx - before + len(parrotFrames)) % len(parrotFrames)
		}
		return n
	}
	slow := advance(silence)
	fast := advance(musictest.Sine(256, 1000))
	if slow == 0 || fast <= slow {
		t.Fatalf("loud blocks should dance faster: %d vs %d frames", fast, slow)
	}
	lit := musictest.Lit(p.Frame())
	dimmed := 0
	art := parrotFrames[p.idx]
	for _, line := range art {
		for _, ch := range line {
			if ch != ' ' {
				dimmed++
			}
		}
	}
	if lit != 2*dimmed { // at native size every character is exactly two pixels
		t.Fatalf("%d pixels lit for %d characters", lit, dimmed)
	}
	musictest.Sized(p, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, p, silence)
}

func TestParrotDancesOnTheBeat(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	p := New()
	musictest.Sized(p, 50, 36)
	loud, quiet := musictest.Sine(1024, 1000), musictest.Sine(1024, 1000)
	for i := range quiet {
		quiet[i] *= 0.1
	}
	const period = 21 // blocks of 1024 at 44.1 kHz: ~123 BPM
	crossings := 0
	beat := func() {
		for i := range period {
			block := quiet
			if i == 0 {
				block = loud
			}
			before := p.idx
			musictest.Feed(a, p, block)
			if before >= 5 && p.idx < 5 {
				crossings++
			}
		}
	}
	for range 30 {
		beat()
	}
	if bpm := a.Take().BPM; bpm < 118 || bpm > 128 {
		t.Fatalf("BPM %.1f, want ~123", bpm)
	}
	crossings = 0
	for range 20 {
		beat()
	}
	if crossings < 9 || crossings > 11 {
		t.Fatalf("%d dance cycles in 20 beats, want one every two beats", crossings)
	}
}

func TestParrotMaskFollowsResize(t *testing.T) {
	p := New()
	musictest.Sized(p, 50, 36)
	p.Bands = 8
	p.draw()
	before := p.mask
	if before == nil || len(before) != len(parrotFrames) || len(before[0]) != 36 || len(before[0][0]) != 50 {
		t.Fatalf("mask not built for 50x36: %v", before != nil)
	}
	p.draw()
	if &p.mask[0][0][0] != &before[0][0][0] {
		t.Fatal("mask rebuilt without a resize")
	}
	if musictest.Lit(p.Frame()) == 0 {
		t.Fatal("parrot drew nothing")
	}
	musictest.Sized(p, 100, 72)
	p.draw()
	if len(p.mask[0]) != 72 || len(p.mask[0][0]) != 100 {
		t.Fatal("mask not rebuilt for the new size")
	}
}
