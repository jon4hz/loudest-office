// Package spray is a particle equalizer: every band is an emitter spraying
// particles as fast as it is loud. Upright, the emitters sit along the
// bottom and the particles fly up and fall back; radial, they sit at the
// centre and fire outwards, slowly turning (WLED's PS GEQ 2D and GEQ Nova).
package spray

import (
	"math"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// ponytail: fixed physics in pixels per step. Upright, a full-level
// particle just reaches the top; radial ones cross half a 64-wide panel in
// about half a second. Pool capped at 400, trails fade 40% per step.
const (
	gravity  = 0.02
	maxParts = 400
	keep     = 0.6
)

// particle is one spark: frame coordinates, speed per step, and life
// 1..0 that dims it as it ages.
type particle struct {
	x, y, vx, vy float32
	band         int
	life         float32
}

// Spray is the particle equalizer.
type Spray struct {
	music.Base
	parts  []particle
	radial bool    // emitters at the centre, else along the bottom
	rot    float64 // radial emitters' turn, radians
}

var _ bubble.Bubble = (*Spray)(nil)

// New returns a new Spray bubble.
func New() *Spray { return &Spray{Base: music.NewBase("spray")} }

func (s *Spray) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		s.Resize(msg.W, msg.H)
	case bubble.Activate:
		r := msg.Source()
		s.Palette = s.PickPalette(r)
		s.radial = r.IntN(2) == 0
	case tea.KeyPressMsg:
		if msg.String() == "r" {
			s.radial = !s.radial
		} else {
			s.PaletteKey(msg)
		}
	case bubble.Tick:
		s.Bands = len(msg.Signal.Mix)
		for range s.Take(msg.Dt) {
			s.Steps++
			s.step(msg.Signal)
		}
	}
	return nil
}

// emit sprays one particle per band that is loud enough this step.
func (s *Spray) emit(sig dsp.Signal) {
	w, h := float32(s.W), float32(s.H)
	for b, l := range sig.Mix {
		// ponytail: fixed emit chance per step equal to the level
		if l < 0.05 || rand.Float32() > l || len(s.parts) >= maxParts {
			continue
		}
		if s.radial {
			a := 2*math.Pi*(float64(b)+0.5)/float64(s.Bands) + s.rot
			v := 0.3 + 1.2*l
			s.parts = append(s.parts, particle{x: w / 2, y: h / 2, band: b, life: 1,
				vx: v * float32(math.Cos(a)), vy: v * float32(math.Sin(a))})
			continue
		}
		s.parts = append(s.parts, particle{x: (float32(b) + rand.Float32()) * w / float32(s.Bands), y: h - 1,
			vx: (rand.Float32() - 0.5) * 0.2, vy: -float32(math.Sqrt(2*gravity*float64(s.H))) * l, band: b, life: 1})
	}
}

// step fades the trails, emits, and moves every particle one step,
// dropping the ones that left the panel or ran out of life.
func (s *Spray) step(sig dsp.Signal) {
	if s.W == 0 || s.H == 0 || s.Bands == 0 {
		return
	}
	frame := s.Frame()
	music.Fade(frame, keep)
	s.emit(sig)
	s.rot += 0.01
	live := s.parts[:0]
	for _, p := range s.parts {
		p.x += p.vx
		p.y += p.vy
		if !s.radial {
			p.vy += gravity
		}
		p.life -= 0.01
		if p.x < 0 || p.x >= float32(s.W) || p.y < 0 || p.y >= float32(s.H) || p.life <= 0 {
			continue
		}
		live = append(live, p)
		frame[int(p.y)][int(p.x)] = music.Dim(s.CellColour(p.band, s.H-1-int(p.y), s.H), 0.3+0.7*p.life)
	}
	s.parts = live
}
