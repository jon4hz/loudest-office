// Package fireworks: bands launch rockets that burst into sparks.
package fireworks

import (
	"math"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// particle is a firework rocket or spark: x in 0..1 across, y in 0..1 up,
// speeds per block. A spark fades as bright falls to 0.
type particle struct {
	x, y, vx, vy float32
	band         int
	rocket       bool
	bright       float32
}

// ponytail: fixed fireworks physics, after maaslalani/confetty. Rockets fly
// under the flying peaks' gravity so a full-level one reaches the top; sparks
// fall at a quarter of that and fade over ~1 s. Pool capped at 2000.
const (
	rocketGravity  = 0.004
	rocketSpeed    = 0.0894 // sqrt(2 * rocketGravity): reaches the top
	sparkGravity   = 0.001
	sparkSpeed     = 0.02
	sparksPerBurst = 30
)

// Fireworks: bands launch rockets, loud bands higher, that burst into
// sparks; a drop fires a volley.
type Fireworks struct {
	music.Base
	parts []particle
}

var _ bubble.Bubble = (*Fireworks)(nil)

// New returns a new Fireworks bubble.
func New() *Fireworks { return &Fireworks{Base: music.NewBase("fireworks")} }

func (f *Fireworks) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		f.Resize(msg.W, msg.H)
	case bubble.Activate:
		f.Palette = f.PickPalette()
	case tea.KeyPressMsg:
		f.PaletteKey(msg)
	case bubble.Tick:
		f.Bands = len(msg.Signal.Mix)
		if msg.Signal.Drop {
			f.Volley()
		}
		for range f.Take(msg.Dt) {
			f.Steps++
			f.step(msg.Signal)
		}
		f.draw()
	}
	return nil
}

// launch adds a rocket from band b, its height set by level l in 0..1.
func (f *Fireworks) launch(b int, l float32) {
	if len(f.parts) < 2000 {
		f.parts = append(f.parts, particle{x: (float32(b) + rand.Float32()) / float32(f.Bands),
			vy: rocketSpeed * (0.5 + 0.5*l), band: b, rocket: true, bright: 1})
	}
}

// Volley launches 5 rockets from random bands at full height, fired on a
// drop, or by a mode that embeds fireworks to celebrate (after a Tick, which
// sets the band count).
func (f *Fireworks) Volley() {
	for range 5 {
		f.launch(rand.IntN(f.Bands), 1)
	}
}

// step launches at most one rocket per block, from a band with its level
// squared as the chance and a height set by the level, flies everything and
// bursts rockets near their apex.
func (f *Fireworks) step(sig dsp.Signal) {
	off := rand.IntN(f.Bands)
	for i := range sig.Mix {
		if b := (i + off) % f.Bands; rand.Float32() < sig.Mix[b]*sig.Mix[b]*0.1 {
			f.launch(b, sig.Mix[b])
			break
		}
	}
	aspect := float32(f.H) / float32(max(f.W, 1)) // round bursts on a wide frame
	kept, born := f.parts[:0], []particle(nil)
	for _, p := range f.parts {
		switch {
		case p.rocket && p.vy <= 0.1*rocketSpeed: // near the apex: burst
			for range sparksPerBurst {
				if len(f.parts)+len(born) >= 2000 {
					break
				}
				a, v := rand.Float64()*2*math.Pi, sparkSpeed*(0.5+0.5*rand.Float32())
				born = append(born, particle{x: p.x, y: p.y, vx: float32(math.Cos(a)) * v * aspect,
					vy: float32(math.Sin(a)) * v, band: p.band, bright: 1})
			}
		case p.rocket:
			p.vy -= rocketGravity
			p.y += p.vy
			kept = append(kept, p)
		default:
			p.vy -= sparkGravity
			p.x += p.vx
			p.y += p.vy
			p.bright -= 1.0 / 45
			if p.bright > 0 && p.y >= 0 && p.x >= 0 && p.x < 1 {
				kept = append(kept, p)
			}
		}
	}
	f.parts = append(kept, born...)
}

// draw paints rockets with a tail as long as they are fast and sparks fading
// out, all in the palette's colour for their band and height.
func (f *Fireworks) draw() {
	f.Clear()
	if f.W == 0 || f.H == 0 || f.Bands == 0 {
		return
	}
	frame := f.Frame()
	for _, p := range f.parts {
		x, y := int(p.x*float32(f.W)), int(p.y*float32(f.H-1)+0.5)
		if x < 0 || x >= f.W || y < 0 || y >= f.H {
			continue
		}
		c := f.CellColour(p.band, y, f.H)
		if !p.rocket {
			frame[f.H-1-y][x] = music.Dim(c, p.bright)
			continue
		}
		frame[f.H-1-y][x] = c
		for t := 1; t <= int(p.vy/rocketSpeed*8) && y-t >= 0; t++ {
			frame[f.H-1-(y-t)][x] = music.Dim(c, 0.4)
		}
	}
}
