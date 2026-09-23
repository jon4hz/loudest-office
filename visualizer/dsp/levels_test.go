package dsp

import (
	"math"
	"testing"
)

func TestBands(t *testing.T) {
	e := Bands(32, 1024, 44100, 40, 16000)
	if len(e) != 33 {
		t.Fatalf("want 33 edges, got %d", len(e))
	}
	for i := 1; i < len(e); i++ {
		if e[i] <= e[i-1] {
			t.Fatalf("edges not strictly increasing at %d: %v", i, e)
		}
	}
	if e[32] > 512 {
		t.Fatalf("top edge above nyquist: %d", e[32])
	}
}

func TestLevelsSine(t *testing.T) {
	const n, rate = 1024, 44100
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / rate))
	}
	e := Bands(32, n, rate, 40, 16000)
	lv := Levels(s, Hann(n), e, 0, -60)
	best := 0
	for b := range lv {
		if lv[b] > lv[best] {
			best = b
		}
	}
	bin := 1000.0 * n / rate
	if float64(e[best]) > bin || float64(e[best+1]) <= bin {
		t.Fatalf("loudest band %d covers bins [%d,%d), 1 kHz is bin %.1f", best, e[best], e[best+1], bin)
	}
	if lv[best] < 0.9 || lv[best] > 1 {
		t.Fatalf("full-scale sine should be near 1, got %v", lv[best])
	}
	if lv[0] > 0.2 {
		t.Fatalf("lowest band should be quiet, got %v", lv[0])
	}
}
