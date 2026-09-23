// Package invaders: bars hang from the top, their peaks stay behind as
// invaders and a ship at the bottom shoots them back, loudest first. A
// cleared board is a win, celebrated with fireworks.
package invaders

import (
	"image/color"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
	"github.com/jon4hz/loudest-office/visualizer/music/fireworks"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// ponytail: fixed game physics, tuned per block on a 64x32 panel. The ship
// crosses the panel in ~1 s, a shot the field in ~0.5 s; bars fall like the
// bars mode's. Make them settings if another panel size feels off.
const (
	fall      = 0.04   // bar fall per step, in levels
	creep     = 0.0005 // an unshot peak's own way back, ~45 s for the full height
	shipSpeed = 1.5    // px per step
	shotSpeed = 1.5    // rows per step
	cooldown  = 3      // steps between shots
	shipRows  = 3      // ship plus one row of air below the field
	winSteps  = 180    // length of the win screen, ~4 s
	volley    = 36     // steps between the win screen's volleys, ~0.8 s
)

// shot flies straight up column x, the ship's when it fired, at band's
// peak; y is its frame row.
type shot struct {
	band, x int
	y       float32
}

// spark is the flash a hit leaves at the peak's old row.
type spark struct{ band, row, ttl int }

// Invaders is the analyzer upside down, with a ship hunting the peaks.
type Invaders struct {
	music.Base
	bars, peaks []float32 // levels in 0..1, growing down from the top row
	ship, dir   float32   // ship centre column and its idle drift direction
	wait        int       // steps until the ship may fire again
	shots       []shot
	sparks      []spark
	kills       int // hits since the last win
	win         int // steps left of the win screen, 0 while playing
	fw          *fireworks.Fireworks
}

var _ bubble.Bubble = (*Invaders)(nil)

// New returns a new Invaders bubble.
func New() *Invaders {
	return &Invaders{Base: music.NewBase("invaders"), dir: 1, fw: fireworks.New()}
}

// Frame is the game, or the fireworks' own frame during the win screen.
func (in *Invaders) Frame() [][]color.RGBA {
	if in.win > 0 {
		return in.fw.Frame()
	}
	return in.Base.Frame()
}

func (in *Invaders) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		in.Resize(msg.W, msg.H)
		in.fw.Update(msg)
		in.reset()
	case bubble.Activate:
		in.Palette = in.PickPalette()
		in.reset()
	case tea.KeyPressMsg:
		in.PaletteKey(msg)
	case bubble.Tick:
		if in.Bands != len(msg.Signal.Mix) {
			in.Bands = len(msg.Signal.Mix)
			in.reset()
		}
		if in.win > 0 {
			in.celebrate(msg)
			return nil
		}
		for range in.Take(msg.Dt) {
			in.Steps++
			if in.step(msg.Signal); in.win > 0 { // won: the screen opens this very frame
				msg.Dt = 0
				in.celebrate(msg)
				return nil
			}
		}
		in.draw()
	}
	return nil
}

// reset zeroes the game for the current size and band count, so nothing
// stale from another activation or shape flashes or indexes out of range.
func (in *Invaders) reset() {
	in.bars, in.peaks = make([]float32, in.Bands), make([]float32, in.Bands)
	in.shots, in.sparks = nil, nil
	in.ship, in.wait, in.kills, in.win = float32(in.W/2), 0, 0, 0
}

// celebrate runs the win screen: the fireworks bubble plays the tick, fed a
// volley now and then since a cleared board is usually a silent one, with the
// verdict on top. Then a fresh game starts.
func (in *Invaders) celebrate(tick bubble.Tick) {
	in.fw.Update(tick)
	for range in.Take(tick.Dt) {
		if in.win%volley == 0 && in.win > 72 { // none late: the sky is empty when the screen ends
			in.fw.Volley()
		}
		if in.win--; in.win == 0 {
			in.reset()
			in.draw()
			return
		}
	}
	const text = "YOU WIN"
	scale := 1
	if bubble.TextWidth(text, 2) <= in.W && in.H >= 14 {
		scale = 2
	}
	frame, tw := in.fw.Frame(), bubble.TextWidth(text, scale)
	x, y := (in.W-tw)/2, (in.H-7*scale)/2
	for r := max(y-2, 0); r < min(y+7*scale+2, in.H); r++ { // keep the sparks off the text
		clear(frame[r][max(x-2, 0):min(x+tw+2, in.W)])
	}
	bubble.DrawText(frame, x, y, text, palette.White, scale)
}

// geom is the layout: band width, the gap kept between bars, the left margin
// centring the bands, and the field height left above the ship. ok is false
// when the frame is too small to play on.
func (in *Invaders) geom() (barW, gap, left, fh int, ok bool) {
	if in.Bands == 0 {
		return
	}
	barW, fh = in.W/in.Bands, in.H-shipRows
	if barW >= 3 {
		gap = 1
	}
	return barW, gap, (in.W - barW*in.Bands) / 2, fh, barW > 0 && fh > 0
}

// centre is the column the ship heads for to get under a band.
func (in *Invaders) centre(band int) float32 {
	barW, gap, left, _, _ := in.geom()
	return float32(left + band*barW + (barW-gap)/2)
}

// bandAt is the band painted in column x, or -1 in a gap or margin.
func (in *Invaders) bandAt(x int) int {
	barW, gap, left, _, _ := in.geom()
	if i := x - left; i >= 0 && i < barW*in.Bands && i%barW < barW-gap {
		return i / barW
	}
	return -1
}

// peakRow is the frame row of a band's peak marker.
func (in *Invaders) peakRow(band, fh int) int {
	return int(in.peaks[band]*float32(fh-1) + 0.5)
}

// step runs one legacy block: bars ease toward the signal and push their
// peaks down, shots fly and knock peaks back to the bar, and the ship chases
// the hanging peak with the highest level, shooting whatever hangs above it
// on the way. Shooting the last hanging peak wins, once the ship made as many
// hits as there are bands: a lone pulsing band is no board to clear.
func (in *Invaders) step(sig dsp.Signal) {
	_, _, _, fh, ok := in.geom()
	if !ok {
		return
	}
	for b, l := range sig.Mix {
		in.bars[b] = max(l, in.bars[b]-fall)
		in.peaks[b] = max(in.bars[b], in.peaks[b]-creep)
	}

	hanging := func(b int) bool { return in.peaks[b]-in.bars[b] > 1.5/float32(fh) }
	kept, hit := in.shots[:0], false
	for _, s := range in.shots {
		s.y -= shotSpeed
		if row := in.peakRow(s.band, fh); s.y > float32(row) {
			kept = append(kept, s)
		} else if hanging(s.band) { // a shot at a peak the bar took back just fizzles
			in.sparks = append(in.sparks, spark{s.band, row, 3})
			in.peaks[s.band] = in.bars[s.band]
			in.kills++
			hit = true
		}
	}
	in.shots = kept
	if hit && in.kills >= in.Bands {
		cleared := true
		for b := range in.peaks {
			cleared = cleared && !hanging(b)
		}
		if cleared {
			in.win, in.fw.Palette = winSteps, in.Palette
			return
		}
	}

	live := in.sparks[:0]
	for _, s := range in.sparks {
		if s.ttl--; s.ttl > 0 {
			live = append(live, s)
		}
	}
	in.sparks = live

	open := func(b int) bool { // hanging, and no shot on its way yet
		for _, s := range in.shots {
			if s.band == b {
				return false
			}
		}
		return hanging(b)
	}
	target := -1
	for b := range in.peaks {
		if (target < 0 || in.peaks[b] > in.peaks[target]) && open(b) {
			target = b
		}
	}
	in.wait = max(in.wait-1, 0)
	if target < 0 { // nothing to shoot: patrol
		in.ship += in.dir * shipSpeed / 3
		if in.ship <= 1 || in.ship >= float32(in.W-2) {
			in.dir = -in.dir
		}
		in.ship = min(max(in.ship, 1), float32(max(in.W-2, 1)))
		return
	}
	in.ship += min(max(in.centre(target)-in.ship, -shipSpeed), shipSpeed)
	x := int(in.ship + 0.5)
	if b := in.bandAt(x); b >= 0 && in.wait == 0 && open(b) {
		in.shots = append(in.shots, shot{b, x, float32(fh)})
		in.wait = cooldown
	}
}

// draw paints the bars from the top row down, their peaks, the sparks of
// fresh hits, the shots and the ship.
func (in *Invaders) draw() {
	in.Clear()
	barW, gap, left, fh, ok := in.geom()
	if !ok {
		return
	}
	frame := in.Frame()
	paint := func(row, band int, c color.RGBA) {
		for i := range barW - gap {
			frame[row][left+band*barW+i] = c
		}
	}
	for b := range in.bars {
		barH := int(in.bars[b]*float32(fh) + 0.5)
		ph := fh // the palette's y runs from the bar's base, here the top row: mirrored with the bar
		if in.Palette.Relative {
			ph = max(barH, 1)
		}
		py := func(row int) int { // the palette's y for a frame row
			if in.Palette.Upright {
				return max(ph-1-row, 0) // a peak may hang below an upright bar's gradient
			}
			return row
		}
		for y := 0; y < barH && y < fh; y++ {
			paint(y, b, in.CellColour(b, py(y), ph))
		}
		if row := in.peakRow(b, fh); in.peaks[b] > 0 && row >= barH {
			pb := b
			if in.Palette.Animate {
				pb = (b + in.Steps/8) % in.Bands
			}
			_, c := in.Palette.At(pb, in.Bands, py(row), ph)
			if c.A == 0 { // a palette without peaks still needs its invaders
				c = palette.White
			}
			paint(row, b, c)
		}
	}
	for _, s := range in.sparks {
		paint(s.row, s.band, music.Dim(palette.White, float32(s.ttl)/3))
	}
	for _, s := range in.shots {
		if y := int(s.y); y >= 0 && y < in.H {
			frame[y][s.x] = palette.White
		}
	}
	x := int(in.ship + 0.5)
	for dx := -1; dx <= 1; dx++ {
		if x+dx >= 0 && x+dx < in.W {
			frame[in.H-1][x+dx] = palette.White
		}
	}
	if x >= 0 && x < in.W {
		frame[in.H-2][x] = palette.White
	}
}
