package music

import (
	"testing"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

// musictest cannot import music, so it carries its own copy of Step: every
// mode's tests would silently step at the wrong rate if the two drifted.
func TestMusictestStepMatches(t *testing.T) {
	if musictest.Step != Step {
		t.Fatalf("musictest.Step = %v, music.Step = %v", musictest.Step, Step)
	}
}

func TestStepperTakesWholeSteps(t *testing.T) {
	var s stepper
	for i := 0; i < 1000; i++ {
		if n := s.take(Step); n != 1 {
			t.Fatalf("iteration %d: take(step) = %d, want 1", i, n)
		}
	}
}

func TestStepperAlternatesOnHalfSteps(t *testing.T) {
	var s stepper
	for i := 0; i < 10; i++ {
		want := i % 2
		if n := s.take(Step / 2); n != want {
			t.Fatalf("iteration %d: take(step/2) = %d, want %d", i, n, want)
		}
	}
}

func TestStepperTotalsOverASecond(t *testing.T) {
	var s stepper
	total := 0
	for d := time.Duration(0); d < time.Second; d += 7 * time.Millisecond {
		total += s.take(7 * time.Millisecond)
	}
	if total != 43 {
		t.Fatalf("total steps over ~1s in 7ms slices = %d, want 43", total)
	}
}
