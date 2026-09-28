// Package aurora is the northern lights: noise-lit curtains across the
// middle of the panel, drifting faster and reaching further up and down
// the louder the music (WLED's Polar Lights).
package aurora

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Aurora is the northern lights bubble.
type Aurora struct {
	music.Base
	t     float64 // drift phase
	reach float64 // smoothed loudness, 0..1: how far the curtains reach from the middle
}

var _ bubble.Bubble = (*Aurora)(nil)

// New returns a new Aurora bubble. It defaults to every palette but the
// flags, whose stripes make no sense in a cloud.
func New() *Aurora {
	a := &Aurora{Base: music.NewBase("aurora")}
	a.SkipFlags = true
	return a
}

func (a *Aurora) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		a.Resize(msg.W, msg.H)
	case bubble.Activate:
		a.Palette = a.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		a.PaletteKey(msg)
	case bubble.Tick:
		a.Bands = len(msg.Signal.Mix)
		for range a.Take(msg.Dt) {
			a.Steps++
			a.step(msg.Signal)
		}
		a.draw()
	}
	return nil
}

// step drifts the curtains at a speed that follows the energy and eases
// the reach towards it.
func (a *Aurora) step(sig dsp.Signal) {
	// ponytail: fixed drift, 0.01 per step idle up to 0.06 at full energy;
	// the mean band level is ~0.3-0.5 on music with auto-gain, so 1.5x
	// reaches the edges on the loud bits
	e := float64(min(sig.Energy, 1))
	a.t += 0.01 + 0.05*e
	a.reach += (min(1, 1.5*e) - a.reach) * 0.1
}

// draw lights every pixel by the noise at it, dimmed towards the top and
// bottom edges so a band of light hangs across the middle; loud music lets
// it reach the edges.
func (a *Aurora) draw() {
	if a.W == 0 || a.H == 0 || a.Bands == 0 {
		return
	}
	frame := a.Frame()
	cy := float64(a.H-1) / 2
	for y := range a.H {
		edge := math.Abs(float64(y)-cy) / max(cy, 1) // 0 at the middle row, 1 at the edges
		for x := range a.W {
			// ponytail: fixed curtain shape after WLED's: noise cells 4 px
			// wide and 16 px tall, slid sideways by t and rippled by t/3
			// vertically, fully cut at the edges when quiet, half when loud
			v := music.Noise(float64(x)*0.25+a.t, float64(y)*0.06+a.t/3) - edge*(1-0.5*a.reach)
			if v < 0.05 {
				frame[y][x] = color.RGBA{}
				continue
			}
			frame[y][x] = music.Dim(a.CellColour(int(v*float64(a.Bands-1)), a.H-1-y, a.H), float32(min(1, 1.5*v)))
		}
	}
}
