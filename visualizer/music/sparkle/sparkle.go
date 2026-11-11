// Package sparkle is static that follows the music: every band flickers
// random pixels in its share of the panel, the more the louder it is, and
// they die out in a few steps.
package sparkle

import (
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Sparkle is the flicker, bass on the left and treble on the right.
type Sparkle struct{ music.Base }

var _ bubble.Bubble = (*Sparkle)(nil)

// New returns a new Sparkle bubble.
func New() *Sparkle { return &Sparkle{Base: music.NewBase("sparkle")} }

func (s *Sparkle) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		s.Resize(msg.W, msg.H)
	case bubble.Activate:
		s.Palette = s.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		s.PaletteKey(msg)
	case bubble.Tick:
		s.Bands = len(msg.Signal.Mix)
		if s.W == 0 || s.H == 0 || s.Bands == 0 {
			return nil
		}
		for range s.Take(msg.Dt) {
			s.Steps++
			s.step(msg.Signal)
		}
	}
	return nil
}

// step fades what is lit and lights new pixels in every band's columns,
// a full-level band lighting a third of its share each step.
func (s *Sparkle) step(sig dsp.Signal) {
	music.Fade(s.Frame(), 0.5) // ponytail: fixed, a pixel is gone after ~8 steps
	frame := s.Frame()
	cols := float32(s.W) / float32(s.Bands)
	for b, l := range sig.Mix {
		l = min(l, 1)
		n := l * l * cols * float32(s.H) / 3
		for n > 0 {
			if n < 1 && rand.Float32() > n {
				break
			}
			n--
			x := min(int((float32(b)+rand.Float32())*cols), s.W-1)
			y := rand.IntN(s.H)
			frame[y][x] = music.Dim(s.CellColour(b, y, s.H), 0.5+0.5*l)
		}
	}
}
