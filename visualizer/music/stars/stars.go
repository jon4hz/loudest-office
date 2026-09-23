// Package stars is a starfield flying at the viewer.
package stars

import (
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// star is a point in front of the viewer: x, y in -1..1 at depth z in 0..1.
type star struct{ x, y, z float32 }

// Stars is a starfield flying at the viewer, faster with energy, with a drop
// starting a hyperspace jump.
type Stars struct {
	music.Base
	stars []star
	warp  int // blocks of hyperspace left after a drop
}

var _ bubble.Bubble = (*Stars)(nil)

// New returns a new Stars bubble.
func New() *Stars { return &Stars{Base: music.NewBase("stars")} }

func (s *Stars) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		s.Resize(msg.W, msg.H)
	case bubble.Activate:
		s.Palette = s.PickPalette()
	case tea.KeyPressMsg:
		s.PaletteKey(msg)
	case bubble.Tick:
		s.Bands = len(msg.Signal.Mix)
		if msg.Signal.Drop {
			s.warp = 30
		}
		for range s.Take(msg.Dt) {
			s.Steps++
			s.step(msg.Signal)
		}
		s.draw()
	}
	return nil
}

// step flies the stars at the viewer at a speed that follows the energy,
// surging on every beat once the tempo is known, four times faster in warp,
// and respawns the ones that passed by.
func (s *Stars) step(sig dsp.Signal) {
	if n := max(20, s.W*s.H/16); len(s.stars) != n {
		s.stars = make([]star, n)
		for i := range s.stars {
			s.stars[i] = star{rand.Float32()*2 - 1, rand.Float32()*2 - 1, rand.Float32()}
		}
	}
	// ponytail: fixed speeds, 1% of the depth per block idle up to 6% at full
	// energy; with a beat, half that between beats and 3.5x on the beat
	speed := 0.01 + 0.05*sig.Energy
	if sig.BPM > 0 {
		pulse := (1 - sig.Beat) * (1 - sig.Beat)
		speed *= 0.5 + 3*pulse
	}
	if s.warp > 0 {
		speed *= 4
	}
	for i := range s.stars {
		st := &s.stars[i]
		st.z -= speed
		if st.z <= 0.02 || max(st.x, -st.x, st.y, -st.y)/st.z > 1 {
			*st = star{rand.Float32()*2 - 1, rand.Float32()*2 - 1, 1}
		}
	}
	s.warp = max(s.warp-1, 0)
}

// draw projects every star, as a radial streak in warp, brighter the nearer.
func (s *Stars) draw() {
	s.Clear()
	if s.W == 0 || s.H == 0 || s.Bands == 0 {
		return
	}
	frame := s.Frame()
	steps := 1
	if s.warp > 0 {
		steps = 6
	}
	cx, cy := float32(s.W)/2, float32(s.H)/2
	for i, st := range s.stars {
		for k := range steps {
			z := st.z + float32(k)*0.02
			x, y := int(cx+st.x/z*cx), int(cy+st.y/z*cy)
			if z > 1 || x < 0 || x >= s.W || y < 0 || y >= s.H {
				continue
			}
			frame[y][x] = music.Dim(s.CellColour(i%s.Bands, int((1-z)*float32(s.H-1)), s.H), 1-0.7*z)
		}
	}
}
