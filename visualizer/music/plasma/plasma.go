// Package plasma is the demoscene sine plasma, flowing with the music.
package plasma

import (
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Plasma is a full-panel sine plasma: it flows faster with energy, the bass
// swells its waves, it pulses on the beat and a drop jumps its colours.
type Plasma struct {
	music.Base
	t      float64 // flow phase
	bass   float64 // smoothed level of the lowest bands, 0..1
	hue    float64 // colour offset 0..1, jumped by a drop
	bright float32 // brightness 0..1, from the beat or the energy
}

var _ bubble.Bubble = (*Plasma)(nil)

// New returns a new Plasma bubble. It defaults to every palette but the
// flags, whose stripes make no sense swirled.
func New() *Plasma {
	p := &Plasma{Base: music.NewBase("plasma")}
	p.Set.Palettes = music.NoFlags()
	return p
}

func (p *Plasma) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		p.Resize(msg.W, msg.H)
	case bubble.Activate:
		p.Palette = p.PickPalette()
	case tea.KeyPressMsg:
		p.PaletteKey(msg)
	case bubble.Tick:
		sig := msg.Signal
		p.Bands = len(sig.Mix)
		if sig.Drop {
			p.hue = math.Mod(p.hue+0.37, 1) // ponytail: fixed jump, far enough to read as a new colour scheme
		}
		// ponytail: fixed brightness, half idle up to full with energy or on the beat
		p.bright = 0.5 + 0.5*min(sig.Energy, 1)
		if sig.BPM > 0 {
			p.bright = 0.5 + 0.5*(1-sig.Beat)*(1-sig.Beat)
		}
		for range p.Take(msg.Dt) {
			p.Steps++
			p.step(sig)
		}
		p.draw()
	}
	return nil
}

// step flows the plasma at a speed that follows the energy and eases the
// bass towards the level of the lowest quarter of the bands.
func (p *Plasma) step(sig dsp.Signal) {
	// ponytail: fixed speeds, 0.02 rad per block idle up to 0.2 at full energy
	p.t += 0.02 + 0.18*float64(min(sig.Energy, 1))
	var bass float64
	low := sig.Mix[:max(1, len(sig.Mix)/4)]
	for _, l := range low {
		bass += float64(l)
	}
	p.bass += (min(bass/float64(len(low)), 1) - p.bass) * 0.2
}

// draw sums four sine waves per pixel, the classic plasma, and maps the sum
// onto the palette's bands, folded so that it never jumps between the last
// band and the first.
func (p *Plasma) draw() {
	if p.W == 0 || p.H == 0 || p.Bands == 0 {
		return
	}
	frame := p.Frame()
	k := 0.25 / (1 + p.bass) // waves get longer with the bass
	cx, cy := float64(p.W)/2, float64(p.H)/2
	for y := range p.H {
		for x := range p.W {
			fx, fy := float64(x), float64(y)
			v := math.Sin(fx*k+p.t) +
				math.Sin(fy*k*1.3-p.t*0.7) +
				math.Sin((fx+fy)*k*0.7+p.t*1.3) +
				math.Sin(math.Hypot(fx-cx, fy-cy)*k*1.5-p.t)
			v = (v + 4) / 8                // 0..1
			_, f := math.Modf(v*2 + p.hue) // two colour cycles across the range
			band := int((1 - math.Abs(2*f-1)) * float64(p.Bands-1))
			frame[y][x] = music.Dim(p.CellColour(band, p.H-1-y, p.H), p.bright*float32(0.4+0.6*v))
		}
	}
}
