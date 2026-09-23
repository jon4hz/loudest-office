// Package fire is flames fed by the band levels.
package fire

import (
	"encoding/json"
	"image/color"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// Fire is flames fed by the band levels. It ignores the palette.
type Fire struct {
	music.Base
	heat [][]float32 // heat per frame row plus the source row at the bottom
}

var _ bubble.Bubble = (*Fire)(nil)

// New returns a new Fire bubble.
func New() *Fire { return &Fire{Base: music.NewBase("fire")} }

// Settings and Configure are trivial: fire has nothing to configure, so any
// field at all is rejected.
func (f *Fire) Settings() any { return struct{}{} }

func (f *Fire) Configure(raw json.RawMessage) error {
	_, err := bubble.Patch(struct{}{}, raw)
	return err
}

func (f *Fire) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		f.Resize(msg.W, msg.H)
	case bubble.Activate:
		f.Palette = f.PickPalette()
	case tea.KeyPressMsg:
		f.PaletteKey(msg)
	case bubble.Tick:
		f.Bands = len(msg.Signal.Mix)
		for range f.Take(msg.Dt) {
			f.Steps++
			f.step(msg.Signal)
		}
		f.draw()
	}
	return nil
}

// step seeds the source row from the band levels and lets the heat rise one
// row, Doom style: every cell takes the cell below it, drifted one pixel
// sideways at random and cooled at random.
func (f *Fire) step(sig dsp.Signal) {
	if len(f.heat) != f.H+1 || (f.H > 0 && len(f.heat[0]) != f.W) {
		f.heat = make([][]float32, f.H+1)
		for y := range f.heat {
			f.heat[y] = make([]float32, f.W)
		}
	}
	if f.W == 0 || f.H == 0 {
		return
	}
	// ponytail: fixed cooling of 0.7/height per row on average, so a full
	// band reaches the top about a third of the time. Make it an option if
	// the panel wants taller or shorter flames.
	cool := 1.4 / float32(f.H)
	for x := range f.heat[f.H] {
		f.heat[f.H][x] = sig.Mix[x*f.Bands/f.W] * (0.7 + 0.3*rand.Float32())
	}
	for y := 0; y < f.H; y++ {
		for x := range f.heat[y] {
			src := max(0, min(f.W-1, x+rand.IntN(3)-1))
			f.heat[y][x] = max(0, f.heat[y+1][src]-rand.Float32()*cool)
		}
	}
}

// draw paints the heat buffer black through red and yellow to white.
func (f *Fire) draw() {
	f.Clear()
	if f.W == 0 || f.H == 0 || f.Bands == 0 {
		return
	}
	frame := f.Frame()
	for y := 0; y < f.H && y < len(f.heat); y++ {
		for x := 0; x < f.W && x < len(f.heat[y]); x++ {
			if h := f.heat[y][x]; h > 0 {
				frame[y][x] = palette.Gradient(float64(h), color.RGBA{0, 0, 0, 255}, color.RGBA{180, 0, 0, 255},
					color.RGBA{255, 90, 0, 255}, color.RGBA{255, 220, 0, 255}, palette.White)
			}
		}
	}
}
