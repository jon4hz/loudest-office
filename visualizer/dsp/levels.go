package dsp

import "math"

// Bands returns n+1 FFT bin edges for n log-spaced bands between lo and hi Hz.
// Band i covers bins [edges[i], edges[i+1]). Each band is at least one bin
// wide, so at low frequencies the spacing degrades to linear.
func Bands(n, fftSize, rate int, lo, hi float64) []int {
	edges := make([]int, n+1)
	binHz := float64(rate) / float64(fftSize)
	for i := range edges {
		f := lo * math.Pow(hi/lo, float64(i)/float64(n))
		b := int(math.Round(f / binHz))
		if i > 0 && b <= edges[i-1] {
			b = edges[i-1] + 1
		}
		edges[i] = min(b, fftSize/2)
	}
	return edges
}

// Levels windows and FFTs samples (len(window) samples are used) and returns
// one 0..1 level per band: the loudest bin of the band in dBFS, offset by
// gainDB, mapped so floorDB (e.g. -60) is 0 and 0 dBFS is 1.
func Levels(samples, window []float32, edges []int, gainDB, floorDB float64) []float32 {
	n := len(window)
	re := make([]float32, n)
	im := make([]float32, n)
	var wsum float64
	for i := range re {
		re[i] = samples[i] * window[i]
		wsum += float64(window[i])
	}
	FFT(re, im)
	out := make([]float32, len(edges)-1)
	for b := range out {
		var peak float64
		for k := edges[b]; k < edges[b+1]; k++ {
			if m := float64(re[k]*re[k] + im[k]*im[k]); m > peak {
				peak = m
			}
		}
		mag := 2 * math.Sqrt(peak) / wsum // full-scale sine -> 1.0
		db := 20*math.Log10(mag+1e-12) + gainDB
		out[b] = float32(max(0, min(1, (db-floorDB)/-floorDB)))
	}
	return out
}
