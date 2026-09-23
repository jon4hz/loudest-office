// Package ripples is a pool of water: loud bands rain on it, every beat
// throws a pebble and a drop makes a splash.
package ripples

import (
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Ripples is a water surface seen from above, brighter where it is higher.
// Bands run left to right, so bass rains on the left and treble on the right.
type Ripples struct {
	music.Base
	// cur and prev are the water's height now and one step ago, with a border
	// of one cell that stays 0 all around: the pool's walls, which the waves
	// bounce off.
	cur, prev []float32
	beats     int // Signal.Beats at the last tick
}

var _ bubble.Bubble = (*Ripples)(nil)

// New returns a new Ripples bubble. It defaults to every palette but the
// flags.
func New() *Ripples {
	r := &Ripples{Base: music.NewBase("ripples")}
	r.Set.Palettes = music.NoFlags()
	return r
}

func (r *Ripples) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		r.Resize(msg.W, msg.H)
		r.cur = make([]float32, (msg.W+2)*(msg.H+2))
		r.prev = make([]float32, len(r.cur))
	case bubble.Activate:
		r.Palette = r.PickPalette()
	case tea.KeyPressMsg:
		r.PaletteKey(msg)
	case bubble.Tick:
		sig := msg.Signal
		r.Bands = len(sig.Mix)
		if r.W == 0 || r.H == 0 || r.Bands == 0 {
			return nil
		}
		// ponytail: fixed pebble sizes, 3 for a beat, 5 for a drop, 8 for a big one
		if sig.Beats != r.beats { // the beat's pebble lands at the loudest band
			r.beats = sig.Beats
			loudest := 0
			for b, l := range sig.Mix {
				if l > sig.Mix[loudest] {
					loudest = b
				}
			}
			r.pebble(r.bandX(loudest), rand.IntN(r.H), 3)
		}
		if sig.BigDrop {
			r.pebble(r.W/2, r.H/2, 8)
		} else if sig.Drop {
			r.pebble(r.W/2, r.H/2, 5)
		}
		for range r.Take(msg.Dt) {
			r.Steps++
			r.step(sig)
		}
		r.draw()
	}
	return nil
}

// bandX is a random column within band's share of the width.
func (r *Ripples) bandX(band int) int {
	return min(int((float32(band)+rand.Float32())*float32(r.W)/float32(r.Bands)), r.W-1)
}

// at is the water's height at x, y.
func (r *Ripples) at(x, y int) float32 { return r.cur[(y+1)*(r.W+2)+x+1] }

// pebble pushes the water up at x, y and half as much right around it,
// leaving the walls alone.
func (r *Ripples) pebble(x, y int, strength float32) {
	for _, d := range [][3]int{{0, 0, 2}, {1, 0, 1}, {-1, 0, 1}, {0, 1, 1}, {0, -1, 1}} {
		if px, py := x+d[0], y+d[1]; px >= 0 && px < r.W && py >= 0 && py < r.H {
			r.cur[(py+1)*(r.W+2)+px+1] += strength * float32(d[2]) / 2
		}
	}
}

// step lets one random band rain, the likelier the louder it is, and moves
// the waves on: the textbook two-buffer water, every cell heading for the
// average of its neighbours, overshooting, and losing a little each step.
func (r *Ripples) step(sig dsp.Signal) {
	// ponytail: fixed rain, a full-level band drips on every fourth step
	if b := rand.IntN(r.Bands); rand.Float32() < sig.Mix[b]*0.25 {
		r.pebble(r.bandX(b), rand.IntN(r.H), 0.5+sig.Mix[b])
	}
	const damping = 0.97 // ponytail: fixed, a ripple is gone after ~3 s
	w := r.W + 2
	for y := 1; y <= r.H; y++ {
		for i := y*w + 1; i <= y*w+r.W; i++ {
			r.prev[i] = ((r.cur[i-1]+r.cur[i+1]+r.cur[i-w]+r.cur[i+w])/2 - r.prev[i]) * damping
		}
	}
	r.cur, r.prev = r.prev, r.cur
}

// draw lights the whole pool in the palette's colours, band by column, dim
// at rest, brighter on the crests and darker in the troughs.
func (r *Ripples) draw() {
	frame := r.Frame()
	for y := range r.H {
		for x := range r.W {
			l := min(max(0.4+0.6*r.at(x, y), 0.08), 1)
			frame[y][x] = music.Dim(r.CellColour(x*r.Bands/r.W, r.H-1-y, r.H), l)
		}
	}
}
