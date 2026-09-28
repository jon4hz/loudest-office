package dsp

import (
	"encoding/json"
	"math"
	"testing"
)

func sine(n int, hz float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 44100))
	}
	return s
}

func maxLevel(v []float32) float32 {
	var mx float32
	for _, x := range v {
		mx = max(mx, x)
	}
	return mx
}

func TestAutoGain(t *testing.T) {
	quiet := sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.01 // -40 dBFS -> level 0.33 without gain
	}
	a := NewAnalyzer(1, 8, 44100, 256, 0, true)
	var sig Signal
	for range 400 {
		a.Add([][]float32{quiet})
		sig = a.Take()
	}
	if mx := maxLevel(sig.Levels[0]); mx < 0.6 {
		t.Fatalf("auto gain never lifted a quiet signal: %v", mx)
	}
	for range 60 {
		a.Add([][]float32{sine(256, 1000)}) // full scale: gain must back off fast
		a.Take()
	}
	if a.agc > 0 {
		t.Fatalf("gain still boosted at full scale: %v dB", a.agc)
	}
	silence := make([]float32, 256)
	before := a.agc
	for range 100 {
		a.Add([][]float32{silence})
		a.Take()
	}
	if a.agc != before {
		t.Fatalf("gain moved during silence: %v -> %v", before, a.agc)
	}
}

func TestDropLatchesUntilTake(t *testing.T) {
	quiet := sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.32 // a plain hit (measured 1.63x the average), between the hit and scatter lines
	}
	loud := sine(256, 1000)
	a := NewAnalyzer(1, 8, 44100, 256, 0, false)
	for range 300 { // settle the ~2 s running energy average on a quiet passage
		a.Add([][]float32{quiet})
		a.Take()
	}
	a.Add([][]float32{loud}) // the drop
	if !a.Take().Drop {
		t.Fatal("drop not detected")
	}
	if a.Take().Drop {
		t.Fatal("drop must clear once taken")
	}
	for i := range 29 {
		block := quiet
		if i%2 == 0 {
			block = loud
		}
		a.Add([][]float32{block})
		if a.Take().Drop {
			t.Fatalf("block %d: second drop within the refractory window", i)
		}
	}
}

func TestTakeHoldsLevelsWithoutNewBlock(t *testing.T) {
	a := NewAnalyzer(1, 8, 44100, 256, 0, false)
	a.Add([][]float32{sine(256, 1000)})
	first := a.Take()
	second := a.Take()
	if maxLevel(second.Mix) == 0 {
		t.Fatal("mix went to zero without a new block")
	}
	for b := range first.Mix {
		if first.Mix[b] != second.Mix[b] {
			t.Fatalf("mix changed without a new block: %v vs %v", first.Mix, second.Mix)
		}
	}
}

func TestTakeMaxMerges(t *testing.T) {
	a := NewAnalyzer(1, 8, 44100, 256, 0, false)
	silence := make([]float32, 256)
	a.Add([][]float32{sine(256, 1000)})
	a.Add([][]float32{silence})
	sig := a.Take()
	if maxLevel(sig.Mix) == 0 {
		t.Fatal("loud levels lost after a quieter block max-merged in")
	}
	a.Add([][]float32{silence})
	sig = a.Take()
	if maxLevel(sig.Mix) != 0 {
		t.Fatalf("mix should be zero after a fresh silent block, got %v", sig.Mix)
	}
}

func TestNewAnalyzerStartsSilent(t *testing.T) {
	a := NewAnalyzer(1, 8, 44100, 256, 0, false)
	if got := a.Take().DB; got != -120 {
		t.Fatalf("DB before any Add = %v, want -120", got)
	}
}

func TestDBClampedAndMarshals(t *testing.T) {
	a := NewAnalyzer(1, 8, 44100, 256, 0, false)
	a.Add([][]float32{make([]float32, 256)})
	sig := a.Take()
	if sig.DB != -120 {
		t.Fatalf("silent block: DB = %v, want -120", sig.DB)
	}
	if _, err := json.Marshal(sig); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	a = NewAnalyzer(1, 8, 44100, 256, 0, false)
	a.Add([][]float32{sine(256, 1000)})
	sig = a.Take()
	if sig.DB < -4 || sig.DB > -2 {
		t.Fatalf("full-scale sine: DB = %v, want -4..-2", sig.DB)
	}
}

func TestTempo(t *testing.T) {
	a := NewAnalyzer(1, 8, 44100, 1024, 0, false)
	loud, quiet := sine(1024, 1000), sine(1024, 1000)
	for i := range quiet {
		quiet[i] *= 0.1
	}
	const period = 21 // blocks of 1024 at 44.1 kHz: ~123 BPM
	var sig Signal
	for range 30 {
		for i := range period {
			block := quiet
			if i == 0 {
				block = loud
			}
			a.Add([][]float32{block})
			sig = a.Take()
		}
	}
	if sig.BPM < 118 || sig.BPM > 128 {
		t.Fatalf("BPM %.1f, want ~123", sig.BPM)
	}
}
