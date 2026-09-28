package pitch

import (
	"math"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestPitchColoursByNoteRegardlessOfOctave(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 1024, 0, false)
	p := New()
	musictest.Sized(p, 16, 8)
	p.Update(bubble.Activate{})
	musictest.Feed(a, p, musictest.Sine(1024, 440))
	if math.Abs(p.note) > 0.03 && math.Abs(p.note-1) > 0.03 {
		t.Fatalf("440 Hz is an A, want note class ~0, got %v", p.note)
	}
	if c := musictest.Column(p.Frame(), 15); c[3] != '#' || c[4] != '#' {
		t.Fatalf("a loud block should draw a centred column on the right, got %q", c)
	}
	musictest.Feed(a, p, musictest.Sine(1024, 880))
	if math.Abs(p.note) > 0.03 && math.Abs(p.note-1) > 0.03 {
		t.Fatalf("880 Hz is an A too, got %v", p.note)
	}
	musictest.Feed(a, p, musictest.Sine(1024, 523.25))
	if p.note < 0.2 || p.note > 0.3 {
		t.Fatalf("523 Hz is a C, 3 semitones up, want ~0.25, got %v", p.note)
	}
	if c := musictest.Column(p.Frame(), 14); c[3] != '#' {
		t.Fatalf("the previous column should have slid left, got %q", c)
	}
	musictest.Sized(p, 3, 1) // resize must not panic
	a.SetBands(4)
	musictest.Feed(a, p, make([]float32, 1024))
}
