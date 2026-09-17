package spectrum

import (
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sine(n int, hz float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 44100))
	}
	return s
}

func TestModelDrawsBarsAndHoldsPeaks(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), PeakHold(2), PeakFall(0.5), FallSpeed(1))
	m, _ = m.Update(SamplesMsg{sine(256, 1000)})
	f := m.Frame()
	if len(f) != 8 || len(f[0]) != 16 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	lit := 0
	for _, row := range f {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("nothing drawn")
	}
	peakBefore := append([]float32(nil), m.peaks[0]...)
	silence := make([]float32, 256)
	m, _ = m.Update(SamplesMsg{silence}) // bars drop, hold 2
	m, _ = m.Update(SamplesMsg{silence}) // hold 1
	for b := range peakBefore {
		if m.peaks[0][b] != peakBefore[b] {
			t.Fatalf("peak %d moved during hold", b)
		}
	}
	m, _ = m.Update(SamplesMsg{silence}) // hold 0
	m, _ = m.Update(SamplesMsg{silence}) // falls
	moved := false
	for b := range peakBefore {
		if m.peaks[0][b] < peakBefore[b] {
			moved = true
		}
	}
	if !moved {
		t.Fatal("peak never fell")
	}
}

func TestModelFollowsWindowSize(t *testing.T) {
	m := New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if f := m.Frame(); len(f) != 20 || len(f[0]) != 40 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	if lines := strings.Count(m.View(), "\n"); lines != 9 {
		t.Fatalf("view has %d newlines, want 9", lines)
	}
	m.SetBands(16)
	if m.NumBands() != 16 || len(m.bars[0]) != 16 {
		t.Fatal("SetBands did not resize")
	}
}
