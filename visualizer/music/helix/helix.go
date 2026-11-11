// Package helix is a DNA double helix scrolling across the panel: two
// strands twisted around each other, joined by rungs, turning faster the
// louder the music and breathing with it.
package helix

import (
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Helix is the strands, bass on the left and treble on the right.
type Helix struct {
	music.Base
	phase float64 // of the twist, advances with the energy
	amp   float64 // 0..1 of the panel's half height, follows the energy
}

var _ bubble.Bubble = (*Helix)(nil)

// New returns a new Helix bubble. It defaults to every palette but the
// flags.
func New() *Helix {
	h := &Helix{Base: music.NewBase("helix")}
	h.SkipFlags = true
	return h
}

func (h *Helix) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		h.Resize(msg.W, msg.H)
	case bubble.Activate:
		h.Palette = h.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		h.PaletteKey(msg)
	case bubble.Tick:
		h.Bands = len(msg.Signal.Mix)
		if h.W == 0 || h.H == 0 || h.Bands == 0 {
			return nil
		}
		for range h.Take(msg.Dt) {
			h.Steps++
			h.step(msg.Signal)
		}
		h.draw()
	}
	return nil
}

// step twists the helix on, faster the louder, and eases its width towards
// the energy so it swells on the loud bits.
func (h *Helix) step(sig dsp.Signal) {
	e := float64(min(sig.Energy, 1))
	// ponytail: fixed speeds, a full turn every ~5 s idle, ~1.5 s at full energy
	h.phase += 0.03 + 0.07*e
	h.amp += (0.6 + 0.4*e - h.amp) * 0.1
}

// draw plots both strands, the one in front brighter, and a rung between
// them every fourth column.
func (h *Helix) draw() {
	h.Clear()
	frame := h.Frame()
	cy, amp := float64(h.H-1)/2, h.amp*float64(h.H-1)/2
	// ponytail: fixed pitch, one and a half turns across the panel
	k := 3 * math.Pi / float64(h.W)
	for x := range h.W {
		band := x * h.Bands / h.W
		a := k*float64(x) + h.phase
		s, depth := math.Sin(a), float32(0.5+0.5*math.Cos(a))
		y1, y2 := int(cy+amp*s+0.5), int(cy-amp*s+0.5)
		if x%4 == 2 {
			c := music.Dim(h.CellColour(band, h.H/2, h.H), 0.4)
			for y := min(y1, y2); y <= max(y1, y2); y++ {
				frame[y][x] = c
			}
		}
		frame[y1][x] = music.Dim(h.CellColour(band, 0, h.H), 0.3+0.7*depth)
		frame[y2][x] = music.Dim(h.CellColour(band, h.H-1, h.H), 1-0.7*depth)
	}
}
