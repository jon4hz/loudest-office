// Package dsp holds the pure signal-processing helpers for the visualizer.
package dsp

import "math"

// Hann returns an n-point Hann window.
func Hann(n int) []float32 {
	w := make([]float32, n)
	for i := range w {
		w[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

// FFT computes the in-place radix-2 FFT of re/im. len(re) must be a power of two.
func FFT(re, im []float32) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := -2 * math.Pi / float64(size)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for start := 0; start < n; start += size {
			cr, ci := 1.0, 0.0
			for k := 0; k < size/2; k++ {
				a, b := start+k, start+k+size/2
				tr := float32(cr)*re[b] - float32(ci)*im[b]
				ti := float32(ci)*re[b] + float32(cr)*im[b]
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}
