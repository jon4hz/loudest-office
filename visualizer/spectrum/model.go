package spectrum

import (
	"fmt"
	"image/color"
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
	fixedW, fixedH, w, h           int

	window []float32
	edges  []int
	bars   [][]float32
	peaks  [][]float32
	hold   [][]int
	frame  [][]color.RGBA
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
	for ch := range m.bars {
		m.bars[ch] = make([]float32, n)
		m.peaks[ch] = make([]float32, n)
		m.hold[ch] = make([]int, n)
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
		var loudest float32
		for ch := 0; ch < m.channels && ch < len(msg); ch++ {
			if len(msg[ch]) < m.fftSize {
				continue
			}
			lv := dsp.Levels(msg[ch], m.window, m.edges, m.gain+m.agc, -60)
			for b, l := range lv {
				loudest = max(loudest, l)
				m.bars[ch][b] = max(l, m.bars[ch][b]-m.fall)
				switch {
				case m.bars[ch][b] >= m.peaks[ch][b]:
					m.peaks[ch][b], m.hold[ch][b] = m.bars[ch][b], m.peakHold
				case m.hold[ch][b] > 0:
					m.hold[ch][b]--
				default:
					m.peaks[ch][b] = max(m.bars[ch][b], m.peaks[ch][b]-m.peakFall)
				}
			}
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

// draw paints bars and peaks of every channel into the frame.
func (m *Model) draw() {
	for _, row := range m.frame {
		clear(row)
	}
	if m.w == 0 || m.h == 0 || m.bands == 0 {
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
		for b := range m.bars[ch] {
			barH := int(m.bars[ch][b]*float32(height) + 0.5)
			peakY := int(m.peaks[ch][b]*float32(height-1) + 0.5)
			x := left + b*barW
			if flipped {
				x = left + (m.bands-1-b)*barW + gap // gap on the centre side
			}
			ph := height
			if m.palette.Relative {
				ph = max(barH, 1)
			}
			for y := 0; y < height; y++ {
				bar, peak := m.palette.At(b, m.bands, y, ph)
				c := color.RGBA{}
				if y < barH {
					c = bar
				}
				if y == peakY && m.peaks[ch][b] > 0 && peak.A != 0 {
					c = peak
				}
				if c.A == 0 {
					continue
				}
				r := top + height - 1 - y
				if vflipped {
					r = top + y
				}
				for i := 0; i < barW-gap; i++ {
					m.frame[r][x+i] = c
				}
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
