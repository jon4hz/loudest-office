package dsp

import (
	"math"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	const n = 64
	re := make([]float32, n)
	im := make([]float32, n)
	for i := range re {
		re[i] = float32(math.Sin(2*math.Pi*3*float64(i)/n) + 0.5*math.Cos(2*math.Pi*10*float64(i)/n))
	}
	want := make([]complex128, n)
	for k := range want {
		for i := range re {
			a := -2 * math.Pi * float64(k*i) / n
			want[k] += complex(float64(re[i])*math.Cos(a), float64(re[i])*math.Sin(a))
		}
	}
	FFT(re, im)
	for k := range want {
		if math.Abs(float64(re[k])-real(want[k])) > 1e-3 || math.Abs(float64(im[k])-imag(want[k])) > 1e-3 {
			t.Fatalf("bin %d: got (%v,%v) want %v", k, re[k], im[k], want[k])
		}
	}
}

func TestHann(t *testing.T) {
	w := Hann(8)
	if w[0] != 0 || w[7] != 0 || math.Abs(float64(w[3])-0.95048) > 1e-3 {
		t.Fatalf("unexpected window %v", w)
	}
}
