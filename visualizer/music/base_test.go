package music

import (
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// musictest cannot import music, so it carries its own copy of Step: every
// mode's tests would silently step at the wrong rate if the two drifted.
func TestMusictestStepMatches(t *testing.T) {
	if musictest.Step != Step {
		t.Fatalf("musictest.Step = %v, music.Step = %v", musictest.Step, Step)
	}
}

func TestConfigureNullPalettesReadsBackEmpty(t *testing.T) {
	b := NewBase("x")
	if err := b.Configure(json.RawMessage(`{"palettes":null}`)); err != nil {
		t.Fatal(err)
	}
	if buf, _ := json.Marshal(b.Settings()); string(buf) != `{"palettes":[]}` {
		t.Fatalf("settings = %s", buf)
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

func TestPickPaletteSkipsFlagsAndSeesCustomOnes(t *testing.T) {
	p, err := palette.Custom{Name: "zz test", Axis: "bands",
		Stops: []palette.Stop{{Pos: 0, Color: "#000000"}, {Pos: 1, Color: "#ffffff"}}}.Palette()
	if err != nil {
		t.Fatal(err)
	}
	if err := palette.Register(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { palette.Remove("zz test") })
	seen := map[string]bool{}
	for range 2000 {
		q := PickPalette(nil, nil, true)
		if q.Upright {
			t.Fatalf("picked the flag %q with skipFlags", q.Name)
		}
		seen[q.Name] = true
	}
	if !seen["zz test"] {
		t.Fatal("a registered custom palette was never picked")
	}
	if q := PickPalette(nil, []string{"zz test"}, false); q.Name != "zz test" {
		t.Fatalf("named pick = %q", q.Name)
	}

	// verify that NewBase emits empty array not null for palettes
	b := NewBase("x")
	data, _ := json.Marshal(b.Settings())
	if string(data) != `{"palettes":[]}` {
		t.Fatalf("NewBase settings should marshal to %q, got %q", `{"palettes":[]}`, string(data))
	}
}

func TestPickPaletteIsDeterministicPerSeed(t *testing.T) {
	for i := range 20 {
		a := PickPalette(rand.New(rand.NewPCG(uint64(i), 0)), nil, false)
		b := PickPalette(rand.New(rand.NewPCG(uint64(i), 0)), nil, false)
		if a.Name != b.Name {
			t.Fatalf("seed %d: %q vs %q", i, a.Name, b.Name)
		}
	}
	if p := PickPalette(nil, []string{"rainbow"}, false); p.Name != "rainbow" {
		t.Fatalf("nil source picked %q", p.Name)
	}
}
