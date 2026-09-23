// Package bars is the classic analyzer, one bar per band with peak markers.
package bars

import (
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Layout says how many panes the frame is split into and what each pane
// shows. State stays per source (one slot per input channel, or one for
// Single's channel-mixed spectrum); a layout only decides how sources map
// onto panes. Single is appended after HMirrored so existing numeric values
// (and anything persisted with them) keep their meaning.
type Layout int

const (
	Stacked    Layout = iota // one pane per source, stacked top to bottom; a single source falls back to Single
	SideBySide               // one pane per source, left to right; a single source falls back to Single
	Mirrored                 // always two panes, side by side, band 0 at the centre; one source draws the same state on both, the left one flipped
	HMirrored                // always two panes, stacked, meeting at the centre line; one source draws the same state on both, the bottom one flipped
	Single                   // one full-frame spectrum of the channel-mixed levels (sig.Mix)
)

// Layouts are the names printed by Layout.String, in Layout order.
var Layouts = []string{"stacked", "side", "mirror", "hmirror", "single"}

func (l Layout) String() string { return Layouts[l] }

// ParseLayout accepts the names printed by Layout.String.
func ParseLayout(name string) (Layout, bool) {
	for l := Stacked; l < Layout(len(Layouts)); l++ {
		if l.String() == name {
			return l, true
		}
	}
	return Stacked, false
}

// PeakStyle says what a peak marker does once its hold time is over.
type PeakStyle int

const (
	Falling PeakStyle = iota // sinks back onto the bar
	Flying                   // accelerates upwards, leaves the frame, then re-arms at the bar
	Beat                     // falls, but flies for a moment when the energy jumps (a drop, a hit)
	NoPeaks                  // not drawn
)

// Peaks are the names printed by PeakStyle.String, in PeakStyle order.
var Peaks = []string{"fall", "fly", "beat", "none"}

func (s PeakStyle) String() string { return Peaks[s] }

// ParsePeakStyle accepts the names printed by PeakStyle.String.
func ParsePeakStyle(name string) (PeakStyle, bool) {
	for s := Falling; s <= NoPeaks; s++ {
		if s.String() == name {
			return s, true
		}
	}
	return Falling, false
}

// Settings is Bars' settings, patched by Configure and read by Settings.
type Settings struct {
	Palettes []string `json:"palettes"` // empty = all
	Layouts  []string `json:"layouts"`  // empty = all; default [single mirror hmirror]
	Peaks    []string `json:"peaks"`    // empty = all
	Trails   float64  `json:"trails"`   // chance per activation, 0..1, default 0.05
}

// Bars is one bar per band, the classic analyzer, with peak markers on top.
type Bars struct {
	music.Base
	sources        int // state slices allocated: 1 for Single, else len(sig.Levels)
	bars, peaks    [][]float32
	hold           [][]int
	vel, vx, px    [][]float32 // vel: vertical peak speed (negative = down); vx, px: sideways speed and offset, in bands
	layout         Layout
	peakStyle      PeakStyle
	trails         bool
	burst          int // blocks left in which Beat peaks fly
	fall, peakFall float32
	peakHold       int
	set            Settings
}

var _ bubble.Bubble = (*Bars)(nil)

// New returns a new Bars bubble.
func New() *Bars {
	return &Bars{Base: music.NewBase("bars"), fall: 0.04, peakFall: 0.02, peakHold: 20,
		set: Settings{Layouts: []string{"single", "mirror", "hmirror"}, Trails: 0.05}}
}

// pickLayout picks uniformly from names, or any layout when names is empty.
func pickLayout(names []string) Layout {
	if len(names) == 0 {
		return Layout(rand.IntN(len(Layouts)))
	}
	l, _ := ParseLayout(names[rand.IntN(len(names))])
	return l
}

// pickPeakStyle picks uniformly from names, or any peak style when names is empty.
func pickPeakStyle(names []string) PeakStyle {
	if len(names) == 0 {
		return PeakStyle(rand.IntN(int(NoPeaks) + 1))
	}
	s, _ := ParsePeakStyle(names[rand.IntN(len(names))])
	return s
}

func (b *Bars) Settings() any { return b.set }

// Configure patches the settings, rejecting unknown fields, unknown palette,
// layout or peak style names, and a trails chance outside 0..1.
func (b *Bars) Configure(raw json.RawMessage) error {
	out, err := bubble.Patch(b.set, raw)
	if err != nil {
		return err
	}
	if err := music.CheckPalettes(out.Palettes); err != nil {
		return err
	}
	for _, name := range out.Layouts {
		if _, ok := ParseLayout(name); !ok {
			return fmt.Errorf("unknown layout %q", name)
		}
	}
	for _, name := range out.Peaks {
		if _, ok := ParsePeakStyle(name); !ok {
			return fmt.Errorf("unknown peak style %q", name)
		}
	}
	if out.Trails < 0 || out.Trails > 1 {
		return fmt.Errorf("trails must be 0..1, got %v", out.Trails)
	}
	b.set = out
	return nil
}

// alloc (re)allocates the bar state for the current source and band count,
// zeroed so a stale bar from a previous activation or size does not flash.
func (b *Bars) alloc() {
	b.bars = make([][]float32, b.sources)
	b.peaks = make([][]float32, b.sources)
	b.hold = make([][]int, b.sources)
	b.vel = make([][]float32, b.sources)
	b.vx = make([][]float32, b.sources)
	b.px = make([][]float32, b.sources)
	for src := range b.bars {
		b.bars[src] = make([]float32, b.Bands)
		b.peaks[src] = make([]float32, b.Bands)
		b.hold[src] = make([]int, b.Bands)
		b.vel[src] = make([]float32, b.Bands)
		b.vx[src] = make([]float32, b.Bands)
		b.px[src] = make([]float32, b.Bands)
	}
}

// sourceCount is how many state slices the current layout needs: one for
// Single, whose one source is sig.Mix, else one per input channel.
func (b *Bars) sourceCount(sig dsp.Signal) int {
	if b.layout == Single {
		return 1
	}
	return len(sig.Levels)
}

// shape reallocates when the source count or band count no longer matches
// the allocated shape — either the signal's channel count changed, or a
// layout switch (key "l", bubble.Activate) moved to or from Single, which
// always has a single source regardless of the input's channel count.
func (b *Bars) shape(sig dsp.Signal) {
	sources := b.sourceCount(sig)
	if len(b.bars) != sources || b.Bands != len(sig.Mix) {
		b.sources, b.Bands = sources, len(sig.Mix)
		b.alloc()
	}
}

func (b *Bars) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		b.Resize(msg.W, msg.H)
	case bubble.Activate:
		b.alloc()
		b.Clear() // a re-activation must not show the last activation's picture for a zero-step tick
		b.Palette = music.PickPalette(b.set.Palettes)
		b.layout = pickLayout(b.set.Layouts)
		b.peakStyle = pickPeakStyle(b.set.Peaks)
		b.trails = rand.Float64() < b.set.Trails
		// a palette that hides bars paired with hidden peaks would draw nothing
		if bar, _ := b.Palette.At(0, 1, 0, 1); bar.A == 0 && b.peakStyle == NoPeaks {
			b.peakStyle = Falling
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "l":
			b.layout = (b.layout + 1) % Layout(len(Layouts))
		case "p":
			b.peakStyle = (b.peakStyle + 1) % (NoPeaks + 1)
		case "t":
			b.trails = !b.trails
		default:
			b.PaletteKey(msg)
		}
	case bubble.Tick:
		b.shape(msg.Signal)
		n := b.Take(msg.Dt)
		for range n {
			b.Steps++
			b.physics(msg.Signal)
		}
		if msg.Signal.Drop { // after the loop: the "caught up" case would re-arm the hold
			b.burst = 30
			if b.peakStyle == Beat { // Beat peaks fly for the burst, a big drop scatters them
				for src := range b.hold {
					clear(b.hold[src]) // launch now, do not wait out the hold
					for bnd := range b.hold[src] {
						if msg.Signal.BigDrop {
							b.vel[src][bnd] = rand.Float32()*0.08 - 0.03 // some up, some down
							b.vx[src][bnd] = rand.Float32()*0.3 - 0.15   // drift sideways
						}
					}
				}
			}
		}
		for range n - 1 {
			b.fade() // trails fade once per step, not per frame
		}
		if n > 0 {
			b.draw() // fade-or-clear, then paint
		}
	}
	return nil
}

// physics runs one legacy step: bars ease toward the signal levels and each
// peak holds, falls, or flies per peakStyle, reading b.burst for a Beat
// launch window before decrementing it. Single's one source reads sig.Mix
// (the channel-mixed levels) instead of a channel's own Levels entry.
func (b *Bars) physics(sig dsp.Signal) {
	for src := range b.bars {
		levels := sig.Levels[src]
		if b.layout == Single {
			levels = sig.Mix
		}
		for bnd, l := range levels {
			b.bars[src][bnd] = max(l, b.bars[src][bnd]-b.fall)
			p := &b.peaks[src][bnd]
			switch {
			case (b.bars[src][bnd] >= *p && b.vel[src][bnd] >= 0) || *p > 1.2 || *p < -0.2: // caught up, or flown out
				*p, b.hold[src][bnd], b.vel[src][bnd], b.vx[src][bnd], b.px[src][bnd] = b.bars[src][bnd], b.peakHold, 0, 0, 0
			case b.hold[src][bnd] > 0:
				b.hold[src][bnd]--
			case b.peakStyle == Flying || (b.peakStyle == Beat && b.burst > 0):
				// ponytail: fixed launch acceleration, ~0.5 s from bar to top
				if b.vel[src][bnd] < 0 {
					b.vel[src][bnd] -= 0.004
				} else {
					b.vel[src][bnd] += 0.004
				}
				*p += b.vel[src][bnd]
				b.px[src][bnd] += b.vx[src][bnd]
			default:
				b.peaks[src][bnd] = max(b.bars[src][bnd], b.peaks[src][bnd]-b.peakFall)
			}
		}
	}
	b.burst = max(b.burst-1, 0)
}

// fade dims the previous frame for trails, or clears it, once per physics step.
func (b *Bars) fade() {
	for _, row := range b.Frame() {
		if !b.trails {
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
}

// panes is how many spectra the current layout shows: always 1 for Single,
// always 2 for the mirror layouts (with one source, both panes draw that
// same source, one of them flipped, rather than running physics twice),
// else 1 for Stacked/SideBySide with a single source (two identical copies
// would be pointless — same fallback as Single) or one pane per source.
func (b *Bars) panes() int {
	switch {
	case b.layout == Single:
		return 1
	case b.layout == Mirrored || b.layout == HMirrored:
		return 2
	case len(b.bars) == 1:
		return 1
	default:
		return len(b.bars)
	}
}

// draw fades or clears the trail, then paints one bar per band and its peak
// marker, per pane, Layout and PeakStyle. Geometry (cols, rows, flips,
// centre butting) is keyed by pane index; each pane reads the state of
// source pane % sources, so a mono mirror layout draws its one source's
// state twice, geometry flipped, rather than duplicating its physics.
func (b *Bars) draw() {
	b.fade()
	if b.W == 0 || b.H == 0 || b.Bands == 0 || len(b.bars) == 0 {
		return
	}
	frame := b.Frame()
	sources := len(b.bars)
	panes := b.panes()
	cols, rows := 1, panes
	if b.layout == SideBySide || b.layout == Mirrored {
		cols, rows = panes, 1
	}
	height := b.H / rows
	regionW := b.W / cols
	barW := regionW / b.Bands
	if barW == 0 || height == 0 {
		return
	}
	gap := 0
	if barW >= 3 {
		gap = 1
	}
	for pane := range panes {
		src := pane % sources
		top := (pane / cols) * height
		left := (pane%cols)*regionW + (regionW-barW*b.Bands)/2
		flipped := b.layout == Mirrored && pane%2 == 0
		if cols > 1 { // butt both panes against the centre line
			left = (pane % cols) * regionW
			if pane%2 == 0 {
				left += regionW - barW*b.Bands
			}
		}
		vflipped := b.layout == HMirrored && pane%2 == 1
		xOf := func(bnd int) int {
			if flipped {
				return left + (b.Bands-1-bnd)*barW + gap // gap on the centre side
			}
			return left + bnd*barW
		}
		rowOf := func(y int) int {
			if vflipped {
				return top + y
			}
			return top + height - 1 - y
		}
		paint := func(row, x int, c color.RGBA) {
			for i := 0; i < barW-gap; i++ {
				frame[row][x+i] = c
			}
		}
		for bnd := range b.bars[src] {
			barH := int(b.bars[src][bnd]*float32(height) + 0.5)
			ph := height
			if b.Palette.Relative {
				ph = max(barH, 1)
			}
			pb := bnd
			if b.Palette.Animate {
				// ponytail: fixed roll speed of one band per 8 blocks (~5 bands/s)
				pb = (bnd + b.Steps/8) % b.Bands
			}
			for y := 0; y < barH && y < height; y++ {
				if bar, _ := b.Palette.At(pb, b.Bands, y, ph); bar.A != 0 {
					paint(rowOf(y), xOf(bnd), bar)
				}
			}
			peakY := int(b.peaks[src][bnd]*float32(height-1) + 0.5)
			pc := bnd + int(math.Round(float64(b.px[src][bnd])))
			if b.peakStyle == NoPeaks || b.peaks[src][bnd] <= 0 || peakY >= height || pc < 0 || pc >= b.Bands {
				continue
			}
			if peakY < int(b.bars[src][pc]*float32(height)+0.5) {
				continue // never inside the bar of the column it lands in
			}
			if _, peak := b.Palette.At(pb, b.Bands, peakY, ph); peak.A != 0 {
				paint(rowOf(peakY), xOf(pc), peak)
			}
		}
	}
}
