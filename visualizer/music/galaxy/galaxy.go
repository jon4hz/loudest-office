// Package galaxy is a spiral galaxy seen from above: stars orbit the centre
// of the panel on ellipses, the inner ones faster, spinning up with the
// music and surging on the beat.
package galaxy

import (
	"math"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// star orbits at radius r (0..1 of the half panel) and angle a.
type star struct{ r, a float64 }

// Galaxy is the orbiting stars.
type Galaxy struct {
	music.Base
	stars []star
}

var _ bubble.Bubble = (*Galaxy)(nil)

// New returns a new Galaxy bubble. It defaults to every palette but the
// flags, whose stripes the orbits would cross.
func New() *Galaxy {
	g := &Galaxy{Base: music.NewBase("galaxy")}
	g.SkipFlags = true
	return g
}

func (g *Galaxy) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		g.Resize(msg.W, msg.H)
	case bubble.Activate:
		g.Palette = g.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		g.PaletteKey(msg)
	case bubble.Tick:
		g.Bands = len(msg.Signal.Mix)
		if g.W == 0 || g.H == 0 || g.Bands == 0 {
			return nil
		}
		for range g.Take(msg.Dt) {
			g.Steps++
			g.step(msg.Signal)
		}
		g.draw()
	}
	return nil
}

// step turns every star on its orbit, the inner ones faster, at a speed
// that follows the energy and surges on the beat once the tempo is known.
func (g *Galaxy) step(sig dsp.Signal) {
	if n := max(12, g.W*g.H/20); len(g.stars) != n {
		g.stars = make([]star, n)
		for i := range g.stars {
			g.stars[i] = star{0.15 + 0.85*math.Sqrt(rand.Float64()), rand.Float64() * 2 * math.Pi}
		}
	}
	// ponytail: fixed speeds, an outer orbit takes ~25 s idle and ~6 s at full energy
	speed := 0.006 + 0.02*float64(min(sig.Energy, 1))
	if sig.BPM > 0 {
		pulse := float64((1 - sig.Beat) * (1 - sig.Beat))
		speed *= 0.5 + 2*pulse
	}
	for i := range g.stars {
		st := &g.stars[i]
		st.a += speed / (0.3 + st.r)
	}
}

// draw plots every star on its ellipse, coloured by where it is.
func (g *Galaxy) draw() {
	g.Clear()
	frame := g.Frame()
	cx, cy := float64(g.W-1)/2, float64(g.H-1)/2
	for _, st := range g.stars {
		x := int(cx + st.r*cx*math.Cos(st.a) + 0.5)
		y := int(cy + st.r*cy*math.Sin(st.a) + 0.5)
		frame[y][x] = g.CellColour(x*g.Bands/g.W, y, g.H)
	}
}
