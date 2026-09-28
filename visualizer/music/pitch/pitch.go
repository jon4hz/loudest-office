// Package pitch is a scrolling volume meter coloured by the note being
// played: every step a column grows from the centre as tall as the music is
// loud, in the colour of the dominant frequency's note, the same in every
// octave (WLED's Rocktaves and Freqmatrix), and the picture slides left.
package pitch

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Pitch is the note-coloured volume history, newest column on the right.
type Pitch struct {
	music.Base
	note float64 // note class of the last dominant frequency, 0..1 from A
}

var _ bubble.Bubble = (*Pitch)(nil)

// New returns a new Pitch bubble. It defaults to every palette but the
// flags, whose stripes would hide the note colour.
func New() *Pitch {
	p := &Pitch{Base: music.NewBase("pitch")}
	p.SkipFlags = true
	return p
}

func (p *Pitch) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		p.Resize(msg.W, msg.H)
	case bubble.Activate:
		p.Palette = p.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		p.PaletteKey(msg)
	case bubble.Tick:
		p.Bands = len(msg.Signal.Mix)
		for range p.Take(msg.Dt) {
			p.Steps++
			p.step(msg.Signal)
		}
	}
	return nil
}

// step slides the picture left and draws the new column: as tall as the
// music is loud, centred, coloured by the note class of the dominant
// frequency mapped onto the palette's bands.
func (p *Pitch) step(sig dsp.Signal) {
	if p.W == 0 || p.H == 0 || p.Bands == 0 {
		return
	}
	frame := p.Frame()
	for _, row := range frame {
		copy(row, row[1:])
		row[p.W-1] = color.RGBA{}
	}
	// ponytail: fixed scale, the mean band level is ~0.3-0.5 on music with
	// auto-gain, so 1.5x fills the panel on the loud bits
	loud := min(1, 1.5*sig.Energy)
	if sig.Peak > 0 {
		n := math.Log2(sig.Peak / 27.5) // octaves above A0
		p.note = n - math.Floor(n)
	}
	band := int(p.note*float64(p.Bands)) % p.Bands
	h := int(loud*float32(p.H) + 0.5)
	top := (p.H - h) / 2
	for y := top; y < top+h; y++ {
		frame[y][p.W-1] = music.Dim(p.CellColour(band, p.H-1-y, p.H), 0.3+0.7*loud)
	}
}
