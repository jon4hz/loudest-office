// Package ribbon is a spectrum analyzer bent around a wave: a band of light
// snakes across the panel on a slow travelling sine, as thick at every
// column as that band is loud, coloured from the core outwards.
package ribbon

import (
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Ribbon is the wavy band, bass on the left and treble on the right.
type Ribbon struct {
	music.Base
	phase  float64   // of the wave, advances with the energy
	levels []float32 // smoothed band levels
}

var _ bubble.Bubble = (*Ribbon)(nil)

// New returns a new Ribbon bubble. It defaults to every palette but the
// flags, whose stripes the wave would bend.
func New() *Ribbon {
	r := &Ribbon{Base: music.NewBase("ribbon")}
	r.SkipFlags = true
	return r
}

func (r *Ribbon) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		r.Resize(msg.W, msg.H)
	case bubble.Activate:
		r.Palette = r.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		r.PaletteKey(msg)
	case bubble.Tick:
		r.Bands = len(msg.Signal.Mix)
		if r.W == 0 || r.H == 0 || r.Bands == 0 {
			return nil
		}
		for range r.Take(msg.Dt) {
			r.Steps++
			r.step(msg.Signal)
		}
		r.draw()
	}
	return nil
}

// step moves the wave on, faster the louder, and eases the levels towards
// the signal so the band breathes instead of jittering.
func (r *Ribbon) step(sig dsp.Signal) {
	// ponytail: fixed speeds, a wavelength crosses the panel in ~7 s idle, ~2 s at full energy
	r.phase += 0.02 + 0.06*float64(min(sig.Energy, 1))
	if len(r.levels) != r.Bands {
		r.levels = make([]float32, r.Bands)
	}
	for b, l := range sig.Mix {
		r.levels[b] += (l - r.levels[b]) * 0.3
	}
}

// level is the smoothed level at column x, interpolated between band centres.
func (r *Ribbon) level(x int) float32 {
	p := (float32(x)+0.5)/float32(r.W)*float32(r.Bands) - 0.5
	b := int(math.Floor(float64(p)))
	t := p - float32(b)
	lo, hi := r.levels[max(b, 0)], r.levels[min(b+1, r.Bands-1)]
	return lo + (hi-lo)*t
}

// draw fills every column around its point on the wave, at least one pixel
// thick so the ribbon never breaks, coloured by distance from the core.
func (r *Ribbon) draw() {
	r.Clear()
	frame := r.Frame()
	// ponytail: fixed shape, the wave swings a quarter of the height and a full band is two thirds thick
	amp := float64(r.H) / 4
	for x := range r.W {
		band := x * r.Bands / r.W
		centre := float64(r.H)/2 + amp*math.Sin(2*math.Pi*float64(x)/float64(r.W)-r.phase)
		half := max(0.5, float64(min(r.level(x), 1))*float64(r.H)/3)
		for y := range r.H {
			d := math.Abs(float64(y)+0.5-centre) / half
			if d <= 1 {
				frame[y][x] = r.CellColour(band, int((1-d)*float64(r.H-1)), r.H)
			}
		}
	}
}
