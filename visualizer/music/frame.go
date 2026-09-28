package music

import (
	"image/color"
	"math"
)

// Fade dims every pixel of frame to keep (0..1) of its brightness, switching
// off the ones that reach black. Called once per physics step it leaves
// trails behind whatever is drawn on top.
func Fade(frame [][]color.RGBA, keep float32) {
	for _, row := range frame {
		for x, px := range row {
			if px.A == 0 {
				continue
			}
			row[x] = color.RGBA{uint8(float32(px.R) * keep), uint8(float32(px.G) * keep), uint8(float32(px.B) * keep), 255}
			if row[x].R|row[x].G|row[x].B == 0 {
				row[x] = color.RGBA{}
			}
		}
	}
}

// Blur smears frame sideways and then up and down, WLED's blur2D: every
// pixel gives seep (0..0.5) of itself to each neighbour and keeps the rest,
// so light spreads without getting brighter. Lit pixels stay lit, and a
// dark one lights up when a neighbour spills into it.
func Blur(frame [][]color.RGBA, seep float32) {
	if len(frame) == 0 || len(frame[0]) == 0 {
		return
	}
	w, h := len(frame[0]), len(frame)
	keep := 1 - 2*seep
	tmp := make([]color.RGBA, max(w, h))
	mix := func(c color.RGBA, k float32, into *[3]float32) {
		into[0] += float32(c.R) * k
		into[1] += float32(c.G) * k
		into[2] += float32(c.B) * k
	}
	pix := func(v [3]float32) color.RGBA {
		c := color.RGBA{uint8(min(v[0], 255)), uint8(min(v[1], 255)), uint8(min(v[2], 255)), 255}
		if c.R|c.G|c.B == 0 {
			return color.RGBA{}
		}
		return c
	}
	for y := range h {
		copy(tmp, frame[y])
		for x := range w {
			var v [3]float32
			mix(tmp[x], keep, &v)
			if x > 0 {
				mix(tmp[x-1], seep, &v)
			}
			if x < w-1 {
				mix(tmp[x+1], seep, &v)
			}
			frame[y][x] = pix(v)
		}
	}
	for x := range w {
		for y := range h {
			tmp[y] = frame[y][x]
		}
		for y := range h {
			var v [3]float32
			mix(tmp[y], keep, &v)
			if y > 0 {
				mix(tmp[y-1], seep, &v)
			}
			if y < h-1 {
				mix(tmp[y+1], seep, &v)
			}
			frame[y][x] = pix(v)
		}
	}
}

// Noise is smooth 2D value noise in 0..1: random at the integer lattice,
// eased in between. Move x or y slowly for a drifting, cloudy pattern.
func Noise(x, y float64) float64 {
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	fx, fy = fx*fx*(3-2*fx), fy*fy*(3-2*fy) // smoothstep
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
	ix, iy := int64(x0), int64(y0)
	return lerp(lerp(lattice(ix, iy), lattice(ix+1, iy), fx),
		lerp(lattice(ix, iy+1), lattice(ix+1, iy+1), fx), fy)
}

// lattice is a fixed pseudo-random value in 0..1 per lattice point.
func lattice(x, y int64) float64 {
	h := uint64(x)*0x9E3779B97F4A7C15 ^ uint64(y)*0xC2B2AE3D27D4EB4F
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return float64(h>>11) / float64(1<<53)
}
