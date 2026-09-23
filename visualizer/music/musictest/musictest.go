// Package musictest holds the test helpers shared by every music bubble's
// tests: synthetic signals and frame assertions. It imports only dsp and
// bubble (not music, not any mode) so every mode's tests can depend on it
// without a cycle.
package musictest

import (
	"image/color"
	"math"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
)

// Step is one legacy audio block: 1024 samples at 44.1 kHz. It duplicates
// music.Step since this package cannot import music; music's tests assert
// the two stay equal.
const Step = time.Second * 1024 / 44100

func Sine(n int, hz float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 44100))
	}
	return s
}

// Noise is deterministic broadband noise so every band gets a level.
func Noise(n int, amp float32) []float32 {
	s := make([]float32, n)
	x := uint32(12345)
	for i := range s {
		x = x*1664525 + 1013904223
		s[i] = amp * (float32(x>>8)/float32(1<<24)*2 - 1)
	}
	return s
}

// Feed plays blocks through the analyzer and gives the bubble one step each.
func Feed(a *dsp.Analyzer, b bubble.Bubble, blocks ...[]float32) {
	for _, blk := range blocks {
		a.Add([][]float32{blk})
		b.Update(bubble.Tick{Signal: a.Take(), Dt: Step})
	}
}

func Sized(b bubble.Bubble, w, h int) { b.Update(bubble.Resize{W: w, H: h}) }

// Lit counts the frame's non-transparent pixels.
func Lit(frame [][]color.RGBA) int {
	n := 0
	for _, row := range frame {
		for _, px := range row {
			if px.A != 0 {
				n++
			}
		}
	}
	return n
}

// LitColumns returns, per row, which x positions are lit, as a compact string.
func LitColumns(frame [][]color.RGBA) []string {
	var out []string
	for _, row := range frame {
		s := make([]byte, len(row))
		for x, px := range row {
			s[x] = '.'
			if px.A != 0 {
				s[x] = '#'
			}
		}
		out = append(out, string(s))
	}
	return out
}

// Column returns the lit rows of column x as a string, top to bottom.
func Column(frame [][]color.RGBA, x int) string {
	s := make([]byte, len(frame))
	for y, row := range frame {
		s[y] = '.'
		if row[x].A != 0 {
			s[y] = '#'
		}
	}
	return string(s)
}

// Dropped feeds b 300 quiet blocks and one loud one: a detected drop.
func Dropped(b bubble.Bubble) *dsp.Analyzer {
	Sized(b, 16, 8)
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	quiet := Sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.32
	}
	for range 300 {
		Feed(a, b, quiet)
	}
	Feed(a, b, Sine(256, 1000))
	return a
}
