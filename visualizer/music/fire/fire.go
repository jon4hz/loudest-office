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

// ponytail: fixed embers, WLED's defaults scaled to 0..1: the bottom tenth
// of the panel (at least 3 rows) never cools below 16/255, and every step
// each column has a 2% chance of a spark of 0.5-0.88 heat landing in that
// area, so the fire keeps glowing and licking in silence. WLED sparks 62% of
// the time, but it is a screensaver; here the bands feed the flames. Make
// these options if the panel wants a livelier or a deader idle fire.
const (
	floor       = 16.0 / 255
	sparkChance = 0.02
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

// Hints overrides Base's promoted palette hint: fire ignores the palette,
// so it has nothing to hint.
func (f *Fire) Hints() map[string]bubble.Hint { return map[string]bubble.Hint{} }

func (f *Fire) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		f.Resize(msg.W, msg.H)
	case bubble.Activate:
		f.Palette = f.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		f.PaletteKey(msg)
	case bubble.Tick:
		f.Bands = len(msg.Signal.Mix)
		for range f.Take(msg.Dt) {
			f.Steps++
			f.step(msg.Signal)
		}
		f.draw()
		music.Blur(f.Frame(), 0.125)
	}
	return nil
}

// step seeds the source row from the band levels and lets the heat rise,
// Fire2012 style (WLED's mode_fire_2012 run per column): every cell takes
// the weighted mean of the two cells below it and cools at random, the
// ignition rows at the bottom never below the floor, and sparks land in
// them at random. Blur in Update then spreads the columns into each other
// like WLED's 2D blur.
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
	// ponytail: fixed cooling of 1.2/height per step on average; heat climbs
	// 5/3 rows a step, so a full band reaches the top about a third of the
	// time. Make it an option if the panel wants taller or shorter flames.
	cool := 2.4 / float32(f.H)
	for x := range f.heat[f.H] {
		f.heat[f.H][x] = sig.Mix[x*f.Bands/f.W] * (0.7 + 0.3*rand.Float32())
	}
	for y := 0; y < f.H; y++ {
		for x := range f.heat[y] {
			below, below2 := f.heat[y+1][x], f.heat[min(y+2, f.H)][x]
			f.heat[y][x] = max(0, (below+2*below2)/3-rand.Float32()*cool)
		}
	}
	ignition := max(3, f.H/10)
	for y := max(0, f.H+1-ignition); y <= f.H; y++ {
		for x := range f.heat[y] {
			f.heat[y][x] = max(f.heat[y][x], floor)
		}
	}
	for x := range f.W {
		if rand.Float32() < sparkChance {
			y := max(0, f.H-1-rand.IntN(ignition)) // above the source row, which is reseeded next step
			f.heat[y][x] = min(1, f.heat[y][x]+0.5+0.38*rand.Float32())
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
