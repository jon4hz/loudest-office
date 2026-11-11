// Package tunnel is a rectangular tunnel: every beat a rectangle is born at
// the centre and grows out to the edges, changing colour on its way, while
// a bar in the middle pulses with the music.
package tunnel

import (
	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Tunnel is the rings, each a fraction of the way from the centre to the
// edge, oldest first.
type Tunnel struct {
	music.Base
	rings []float64
	beats int // Signal.Beats at the last tick
}

var _ bubble.Bubble = (*Tunnel)(nil)

// New returns a new Tunnel bubble. It defaults to every palette but the
// flags.
func New() *Tunnel {
	t := &Tunnel{Base: music.NewBase("tunnel")}
	t.SkipFlags = true
	return t
}

func (t *Tunnel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		t.Resize(msg.W, msg.H)
	case bubble.Activate:
		t.Palette = t.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		t.PaletteKey(msg)
	case bubble.Tick:
		sig := msg.Signal
		t.Bands = len(sig.Mix)
		if t.W == 0 || t.H == 0 || t.Bands == 0 {
			return nil
		}
		if sig.Beats != t.beats || sig.Drop {
			t.beats = sig.Beats
			t.rings = append(t.rings, 0)
		}
		for range t.Take(msg.Dt) {
			t.Steps++
			t.step(sig)
		}
		t.draw(sig)
	}
	return nil
}

// step grows the rings, faster the louder, drops the ones that left the
// panel and, with no tempo to follow, births one on its own now and then.
func (t *Tunnel) step(sig dsp.Signal) {
	// ponytail: fixed speeds, a ring reaches the edge in ~2 s idle, ~0.7 s at full energy
	speed := 0.012 + 0.024*float64(min(sig.Energy, 1))
	kept := t.rings[:0]
	for _, p := range t.rings {
		if p += speed; p < 1 {
			kept = append(kept, p)
		}
	}
	t.rings = kept
	if sig.BPM == 0 && t.Steps%24 == 0 && sig.Energy > 0.1 {
		t.rings = append(t.rings, 0)
	}
}

// draw paints each ring one pixel wide, coloured by how far out it is and
// fading at the edge, and the centre bar as wide as the music is loud.
func (t *Tunnel) draw(sig dsp.Signal) {
	t.Clear()
	frame := t.Frame()
	cx, cy := float64(t.W-1)/2, float64(t.H-1)/2
	for _, p := range t.rings {
		l, r := int(cx-p*cx+0.5), int(cx+p*cx+0.5)
		top, bot := int(cy-p*cy+0.5), int(cy+p*cy+0.5)
		c := music.Dim(t.CellColour(int(p*float64(t.Bands-1)), int(p*float64(t.H-1)), t.H), float32(1.2-p))
		for x := l; x <= r; x++ {
			frame[top][x], frame[bot][x] = c, c
		}
		for y := top; y <= bot; y++ {
			frame[y][l], frame[y][r] = c, c
		}
	}
	half := int(min(sig.Energy, 1) * float32(t.W) / 4)
	for x := int(cx) - half; x <= int(cx+0.5)+half; x++ {
		frame[int(cy+0.5)][x] = t.CellColour(0, 0, t.H)
	}
}
