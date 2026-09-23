// Package life is Conway's game of life, cells born where the bands are loud.
package life

import (
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Life is Conway's game of life, cells born on the bottom row where the
// bands are loud.
type Life struct {
	music.Base
	life    [][]bool
	lifeAcc float32 // generation budget, one generation per whole unit
}

var _ bubble.Bubble = (*Life)(nil)

// New returns a new Life bubble.
func New() *Life { return &Life{Base: music.NewBase("life")} }

// resize nils the grid: a stale one would index past the new frame.
func (l *Life) resize(w, h int) {
	l.Base.Resize(w, h)
	l.life = nil
}

func (l *Life) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		l.resize(msg.W, msg.H)
	case bubble.Activate:
		l.Palette = l.PickPalette()
	case tea.KeyPressMsg:
		l.PaletteKey(msg)
	case bubble.Tick:
		l.Bands = len(msg.Signal.Mix)
		for range l.Take(msg.Dt) {
			l.Steps++
			l.step(msg.Signal)
		}
		l.draw()
	}
	return nil
}

// stepLife runs one generation on a torus, or just allocates a fresh grid
// when the frame size changed.
func (l *Life) stepLife() {
	if len(l.life) != l.H || (l.H > 0 && len(l.life[0]) != l.W) {
		l.life = make([][]bool, l.H)
		for y := range l.life {
			l.life[y] = make([]bool, l.W)
		}
		return
	}
	next := make([][]bool, l.H)
	for y := range l.life {
		next[y] = make([]bool, l.W)
		for x := range l.life[y] {
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if (dx != 0 || dy != 0) && l.life[(y+dy+l.H)%l.H][(x+dx+l.W)%l.W] {
						n++
					}
				}
			}
			next[y][x] = n == 3 || (n == 2 && l.life[y][x])
		}
	}
	l.life = next
}

// step births cells on the bottom row, each column with its band level
// squared as the chance, then runs generations on an energy budget.
func (l *Life) step(sig dsp.Signal) {
	if len(l.life) != l.H || (l.H > 0 && len(l.life[0]) != l.W) {
		l.stepLife()
	}
	if l.H == 0 || l.W == 0 {
		return
	}
	for x := range l.life[l.H-1] {
		if lvl := sig.Mix[x*l.Bands/l.W]; rand.Float32() < lvl*lvl {
			l.life[l.H-1][x] = true
		}
	}
	// ponytail: fixed rate of ~6 generations/s in silence up to ~20 at full
	// energy. Make it an option if the soup boils too fast on the panel.
	l.lifeAcc += 0.15 + 0.35*sig.Energy
	for l.lifeAcc >= 1 {
		l.lifeAcc--
		l.stepLife()
	}
}

// draw paints live cells in the palette's colour for their column and row.
func (l *Life) draw() {
	l.Clear()
	if l.W == 0 || l.H == 0 || l.Bands == 0 {
		return
	}
	frame := l.Frame()
	for y, row := range l.life {
		for x, alive := range row {
			if alive {
				frame[y][x] = l.CellColour(x*l.Bands/l.W, l.H-1-y, l.H)
			}
		}
	}
}
