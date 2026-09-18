package spectrum

import (
	"fmt"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
)

// SamplesMsg carries one block of samples per channel, each in -1..1.
type SamplesMsg [][]float32

// Model is the spectrum analyzer bubble. Create it with New.
type Model struct {
	bands, channels, rate, fftSize int
	palette                        Palette
	fall, peakFall                 float32
	peakHold                       int
	gain, agc                      float64 // agc is the auto-gain offset in dB
	autoGain                       bool
	layout                         Layout
	peakStyle                      PeakStyle
	mode                           Mode
	trails                         bool
	tick                           int // blocks seen, drives animated palettes
	energyAvg                      float32
	burst                          int // blocks left in which Beat peaks fly
	fixedW, fixedH, w, h           int

	window []float32
	edges  []int
	bars   [][]float32
	peaks  [][]float32
	hold   [][]int
	vel    [][]float32 // vertical speed of flying peaks (negative = down)
	vx, px [][]float32 // sideways speed and offset of chaotic peaks, in bands
	frame  [][]color.RGBA
	mix    []float32   // channel-mixed levels of the last block, feeds the fire
	heat   [][]float32 // fire heat per frame row plus the source row at the bottom
}

// Option configures New.
type Option func(*Model)

func Bands(n int) Option                { return func(m *Model) { m.bands = n } }
func Channels(n int) Option             { return func(m *Model) { m.channels = n } }
func Rate(hz int) Option                { return func(m *Model) { m.rate = hz } }
func FFTSize(n int) Option              { return func(m *Model) { m.fftSize = n } }
func WithPalette(p Palette) Option      { return func(m *Model) { m.palette = p } }
func FallSpeed(perBlock float32) Option { return func(m *Model) { m.fall = perBlock } }
func PeakHold(blocks int) Option        { return func(m *Model) { m.peakHold = blocks } }
func PeakFall(perBlock float32) Option  { return func(m *Model) { m.peakFall = perBlock } }
func Gain(db float64) Option            { return func(m *Model) { m.gain = db } }

// AutoGain adapts the gain so the loudest band sits near the top: it backs
// off fast when a band clips and creeps up slowly while everything is low.
// It freezes during silence so pauses do not amplify the noise floor.
func AutoGain(on bool) Option { return func(m *Model) { m.autoGain = on } }

// Layout says how channels share the frame.
type Layout int

const (
	Stacked    Layout = iota // one spectrum per row, top to bottom
	SideBySide               // one spectrum per column, left to right
	Mirrored                 // like SideBySide, but even channels are flipped so band 0 meets at the centre
	HMirrored                // like Stacked, but odd channels grow downwards so the bars meet at the centre line
)

func (l Layout) String() string { return [...]string{"stacked", "side", "mirror", "hmirror"}[l] }

// ParseLayout accepts the names printed by Layout.String.
func ParseLayout(name string) (Layout, bool) {
	for l := Stacked; l <= HMirrored; l++ {
		if l.String() == name {
			return l, true
		}
	}
	return Stacked, false
}

func WithLayout(l Layout) Option { return func(m *Model) { m.layout = l } }

// PeakStyle says what a peak marker does once its hold time is over.
type PeakStyle int

const (
	Falling PeakStyle = iota // sinks back onto the bar
	Flying                   // accelerates upwards, leaves the frame, then re-arms at the bar
	Beat                     // falls, but flies for a moment when the energy jumps (a drop, a hit)
	NoPeaks                  // not drawn
)

func (s PeakStyle) String() string { return [...]string{"fall", "fly", "beat", "none"}[s] }

// ParsePeakStyle accepts the names printed by PeakStyle.String.
func ParsePeakStyle(name string) (PeakStyle, bool) {
	for s := Falling; s <= NoPeaks; s++ {
		if s.String() == name {
			return s, true
		}
	}
	return Falling, false
}

func WithPeakStyle(s PeakStyle) Option { return func(m *Model) { m.peakStyle = s } }

func (m *Model) SetPeakStyle(s PeakStyle) { m.peakStyle = s }
func (m Model) PeakStyle() PeakStyle      { return m.peakStyle }

func (m *Model) SetLayout(l Layout) { m.layout = l }
func (m Model) Layout() Layout      { return m.layout }

// Size fixes the frame size in pixels instead of following the window.
func Size(w, h int) Option { return func(m *Model) { m.fixedW, m.fixedH = w, h } }

// New returns a model with 32 bands, 2 channels, 44.1 kHz, 1024-point FFT and
// the rainbow palette.
func New(opts ...Option) Model {
	m := Model{bands: 32, channels: 2, rate: 44100, fftSize: 1024, palette: Palettes[0],
		fall: 0.04, peakFall: 0.02, peakHold: 20}
	for _, o := range opts {
		o(&m)
	}
	m.window = dsp.Hann(m.fftSize)
	m.SetBands(m.bands)
	m.resize(m.fixedW, m.fixedH)
	return m
}

// SetBands changes the band count and resets bar and peak state.
func (m *Model) SetBands(n int) {
	m.bands = n
	m.edges = dsp.Bands(n, m.fftSize, m.rate, 40, 16000)
	m.bars = make([][]float32, m.channels)
	m.peaks = make([][]float32, m.channels)
	m.hold = make([][]int, m.channels)
	m.vel = make([][]float32, m.channels)
	m.vx = make([][]float32, m.channels)
	m.px = make([][]float32, m.channels)
	m.mix = make([]float32, n)
	for ch := range m.bars {
		m.bars[ch] = make([]float32, n)
		m.peaks[ch] = make([]float32, n)
		m.hold[ch] = make([]int, n)
		m.vel[ch] = make([]float32, n)
		m.vx[ch] = make([]float32, n)
		m.px[ch] = make([]float32, n)
	}
}

func (m *Model) SetPalette(p Palette) { m.palette = p }
func (m Model) NumBands() int         { return m.bands }

// Frame is the current picture, rows top to bottom. Zero pixels are off.
func (m Model) Frame() [][]color.RGBA { return m.frame }

func (m *Model) resize(w, h int) {
	m.w, m.h = w, h
	m.frame = make([][]color.RGBA, h)
	for y := range m.frame {
		m.frame[y] = make([]color.RGBA, w)
	}
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles SamplesMsg and tea.WindowSizeMsg.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.fixedW == 0 {
			m.resize(msg.Width, 2*msg.Height)
			m.draw()
		}
	case SamplesMsg:
		m.tick++
		clear(m.mix)
		var loudest, energy float32
		for ch := 0; ch < m.channels && ch < len(msg); ch++ {
			if len(msg[ch]) < m.fftSize {
				continue
			}
			lv := dsp.Levels(msg[ch], m.window, m.edges, m.gain+m.agc, -60)
			for b, l := range lv {
				loudest = max(loudest, l)
				energy += l / float32(len(lv)*m.channels)
				m.mix[b] += l / float32(m.channels)
				m.bars[ch][b] = max(l, m.bars[ch][b]-m.fall)
				p := &m.peaks[ch][b]
				switch {
				case (m.bars[ch][b] >= *p && m.vel[ch][b] >= 0) || *p > 1.2 || *p < -0.2: // caught up, or flown out
					*p, m.hold[ch][b], m.vel[ch][b], m.vx[ch][b], m.px[ch][b] = m.bars[ch][b], m.peakHold, 0, 0, 0
				case m.hold[ch][b] > 0:
					m.hold[ch][b]--
				case m.peakStyle == Flying || (m.peakStyle == Beat && m.burst > 0):
					// ponytail: fixed launch acceleration, ~0.5 s from bar to top
					if m.vel[ch][b] < 0 {
						m.vel[ch][b] -= 0.004
					} else {
						m.vel[ch][b] += 0.004
					}
					*p += m.vel[ch][b]
					m.px[ch][b] += m.vx[ch][b]
				default:
					m.peaks[ch][b] = max(m.bars[ch][b], m.peaks[ch][b]-m.peakFall)
				}
			}
		}
		if m.peakStyle == Beat {
			// ponytail: fixed drop detector: energy 1.5x above a ~2 s average
			// launches every peak for ~0.7 s, 1.7x scatters them. A running
			// burst is never re-triggered, so the flight stays clean.
			m.burst = max(m.burst-1, 0)
			if m.burst == 0 && m.energyAvg > 0.02 && energy > 1.5*m.energyAvg {
				m.burst = 30
				chaos := energy > 1.7*m.energyAvg // a big drop: scatter the peaks
				for ch := range m.hold {
					clear(m.hold[ch]) // launch now, do not wait out the hold
					for b := range m.hold[ch] {
						if chaos {
							m.vel[ch][b] = rand.Float32()*0.08 - 0.03 // some up, some down
							m.vx[ch][b] = rand.Float32()*0.3 - 0.15   // drift sideways
						}
					}
				}
			}
			m.energyAvg += (energy - m.energyAvg) * 0.015
		}
		if m.mode == Fire {
			m.stepFire()
		}
		if m.autoGain {
			// ponytail: fixed attack/release in dB per block (~23 ms); make
			// them options if a track ever pumps visibly.
			switch {
			case loudest > 0.95:
				m.agc = max(m.agc-1, -30)
			case loudest > 0.05 && loudest < 0.6:
				m.agc = min(m.agc+0.05, 40)
			}
		}
		m.draw()
	}
	return m, nil
}

// draw paints the current mode into the frame.
func (m *Model) draw() {
	for _, row := range m.frame {
		if !m.trails {
			clear(row)
			continue
		}
		for x, px := range row { // ponytail: fixed fade of 20% per frame
			row[x] = color.RGBA{uint8(float32(px.R) * 0.8), uint8(float32(px.G) * 0.8), uint8(float32(px.B) * 0.8), 255}
			if row[x].R|row[x].G|row[x].B == 0 {
				row[x] = color.RGBA{}
			}
		}
	}
	if m.w == 0 || m.h == 0 || m.bands == 0 {
		return
	}
	if m.mode == Fire {
		m.drawFire()
		return
	}
	cols, rows := 1, m.channels
	if m.layout == SideBySide || m.layout == Mirrored {
		cols, rows = m.channels, 1
	}
	height := m.h / rows
	regionW := m.w / cols
	barW := regionW / m.bands
	if barW == 0 || height == 0 {
		return
	}
	gap := 0
	if barW >= 3 {
		gap = 1
	}
	for ch := range m.bars {
		top := (ch / cols) * height
		left := (ch%cols)*regionW + (regionW-barW*m.bands)/2
		flipped := m.layout == Mirrored && ch%2 == 0
		if cols > 1 { // butt both channels against the centre line
			left = (ch % cols) * regionW
			if ch%2 == 0 {
				left += regionW - barW*m.bands
			}
		}
		vflipped := m.layout == HMirrored && ch%2 == 1
		xOf := func(b int) int {
			if flipped {
				return left + (m.bands-1-b)*barW + gap // gap on the centre side
			}
			return left + b*barW
		}
		rowOf := func(y int) int {
			if vflipped {
				return top + y
			}
			return top + height - 1 - y
		}
		paint := func(row, x int, c color.RGBA) {
			for i := 0; i < barW-gap; i++ {
				m.frame[row][x+i] = c
			}
		}
		for b := range m.bars[ch] {
			barH := int(m.bars[ch][b]*float32(height) + 0.5)
			ph := height
			if m.palette.Relative {
				ph = max(barH, 1)
			}
			pb := b
			if m.palette.Animate {
				// ponytail: fixed roll speed of one band per 8 blocks (~5 bands/s)
				pb = (b + m.tick/8) % m.bands
			}
			for y := 0; y < barH && y < height; y++ {
				if bar, _ := m.palette.At(pb, m.bands, y, ph); bar.A != 0 {
					paint(rowOf(y), xOf(b), bar)
				}
			}
			peakY := int(m.peaks[ch][b]*float32(height-1) + 0.5)
			pc := b + int(math.Round(float64(m.px[ch][b])))
			if m.peakStyle == NoPeaks || m.peaks[ch][b] <= 0 || peakY >= height || pc < 0 || pc >= m.bands {
				continue
			}
			if peakY < int(m.bars[ch][pc]*float32(height)+0.5) {
				continue // never inside the bar of the column it lands in
			}
			if _, peak := m.palette.At(pb, m.bands, peakY, ph); peak.A != 0 {
				paint(rowOf(peakY), xOf(pc), peak)
			}
		}
	}
}

// View paints the frame two pixels per cell using the upper half block:
// foreground is the upper pixel, background the lower one.
func (m Model) View() string {
	var sb strings.Builder
	for y := 0; y < m.h; y += 2 {
		var prevFg, prevBg color.RGBA
		first := true
		for x := 0; x < m.w; x++ {
			fg := m.frame[y][x]
			var bg color.RGBA
			if y+1 < m.h {
				bg = m.frame[y+1][x]
			}
			if first || fg != prevFg {
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
			}
			if first || bg != prevBg {
				fmt.Fprintf(&sb, "\x1b[48;2;%d;%d;%dm", bg.R, bg.G, bg.B)
			}
			sb.WriteString("▀")
			prevFg, prevBg, first = fg, bg, false
		}
		sb.WriteString("\x1b[0m")
		if y+2 < m.h {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
