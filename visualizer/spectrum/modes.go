package spectrum

import (
	"image/color"
	"math/rand/v2"
)

// Mode says what the frame shows.
type Mode int

const (
	Bars Mode = iota // one bar per band, the classic analyzer
	Fire             // flames fed by the band levels
)

func (m Mode) String() string { return [...]string{"bars", "fire"}[m] }

// ParseMode accepts the names printed by Mode.String.
func ParseMode(name string) (Mode, bool) {
	for m := Bars; m <= Fire; m++ {
		if m.String() == name {
			return m, true
		}
	}
	return Bars, false
}

func WithMode(mode Mode) Option { return func(m *Model) { m.mode = mode } }

func (m *Model) SetMode(mode Mode) { m.mode = mode }
func (m Model) Mode() Mode         { return m.mode }

// WithTrails fades the previous frame instead of clearing it. Only the bars
// mode leaves anything to fade; fire repaints every pixel.
func WithTrails(on bool) Option { return func(m *Model) { m.trails = on } }

func (m *Model) SetTrails(on bool) { m.trails = on }
func (m Model) Trails() bool       { return m.trails }

// stepFire seeds the source row from the band levels and lets the heat rise
// one row, Doom style: every cell takes the cell below it, drifted one pixel
// sideways at random and cooled at random.
func (m *Model) stepFire() {
	if len(m.heat) != m.h+1 || (m.h > 0 && len(m.heat[0]) != m.w) {
		m.heat = make([][]float32, m.h+1)
		for y := range m.heat {
			m.heat[y] = make([]float32, m.w)
		}
	}
	if m.w == 0 || m.h == 0 {
		return
	}
	// ponytail: fixed cooling of 0.7/height per row on average, so a full
	// band reaches the top about a third of the time. Make it an option if
	// the panel wants taller or shorter flames.
	cool := 1.4 / float32(m.h)
	for x := range m.heat[m.h] {
		m.heat[m.h][x] = m.mix[x*m.bands/m.w] * (0.7 + 0.3*rand.Float32())
	}
	for y := 0; y < m.h; y++ {
		for x := range m.heat[y] {
			src := max(0, min(m.w-1, x+rand.IntN(3)-1))
			m.heat[y][x] = max(0, m.heat[y+1][src]-rand.Float32()*cool)
		}
	}
}

// drawFire paints the heat buffer black through red and yellow to white.
func (m *Model) drawFire() {
	for y := 0; y < m.h && y < len(m.heat); y++ {
		for x := 0; x < m.w && x < len(m.heat[y]); x++ {
			if h := m.heat[y][x]; h > 0 {
				m.frame[y][x] = gradient(float64(h), color.RGBA{0, 0, 0, 255}, color.RGBA{180, 0, 0, 255},
					color.RGBA{255, 90, 0, 255}, color.RGBA{255, 220, 0, 255}, white)
			}
		}
	}
}
