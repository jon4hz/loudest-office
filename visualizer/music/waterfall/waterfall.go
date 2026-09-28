// Package waterfall is a spectrogram: the picture scrolls up and the band
// levels paint the bottom row, so time runs upwards (WLED's Funky Plank).
package waterfall

import (
	"image/color"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// ponytail: fixed scroll of one row every 2 steps, ~1.5 s up a 32-row panel
const rowSteps = 2

// Waterfall is a spectrogram scrolling upwards, bands left to right.
type Waterfall struct{ music.Base }

var _ bubble.Bubble = (*Waterfall)(nil)

// New returns a new Waterfall bubble.
func New() *Waterfall { return &Waterfall{Base: music.NewBase("waterfall")} }

func (w *Waterfall) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		w.Resize(msg.W, msg.H)
	case bubble.Activate:
		w.Palette = w.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		w.PaletteKey(msg)
	case bubble.Tick:
		w.Bands = len(msg.Signal.Mix)
		for range w.Take(msg.Dt) {
			w.Steps++
			w.step(msg.Signal)
		}
	}
	return nil
}

// step scrolls the rows up by one and paints the band levels into the
// bottom row, brighter and higher up the palette the louder the band.
func (w *Waterfall) step(sig dsp.Signal) {
	if w.W == 0 || w.H == 0 || w.Bands == 0 || w.Steps%rowSteps != 0 {
		return
	}
	frame := w.Frame()
	bottom := frame[0]
	copy(frame, frame[1:])
	frame[w.H-1] = bottom
	for x := range bottom {
		band := x * w.Bands / w.W
		l := sig.Mix[band]
		if l < 0.02 {
			bottom[x] = color.RGBA{}
			continue
		}
		bottom[x] = music.Dim(w.CellColour(band, int(l*float32(w.H-1)), w.H), 0.2+0.8*l)
	}
}
