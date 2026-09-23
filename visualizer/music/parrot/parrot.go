// Package parrot is the party parrot, dancing to the music.
package parrot

import (
	"embed"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// parrotFS holds the party parrot frames, 50x18 characters each, from
// github.com/caarlos0/parttysh (MIT, see frames/LICENSE).
//
//go:embed frames/*.txt
var parrotFS embed.FS

// parrotFrames are the frames in order, each a slice of rows.
var parrotFrames = func() [][]string {
	entries, _ := parrotFS.ReadDir("frames")
	var frames [][]string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		b, _ := parrotFS.ReadFile("frames/" + e.Name())
		frames = append(frames, strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
	}
	return frames
}()

// Parrot is the party parrot, dancing as fast as the music is loud.
type Parrot struct {
	music.Base
	idx  int         // frame shown
	acc  float32     // frame budget, one frame per whole unit, used without a tempo
	mask [][][]uint8 // frames scaled to the current size, see parrotMask
}

var _ bubble.Bubble = (*Parrot)(nil)

// New returns a new Parrot bubble.
func New() *Parrot { return &Parrot{Base: music.NewBase("parrot")} }

// resize nils the mask: it is dropped so it never outlives the frame it was
// built for.
func (p *Parrot) resize(w, h int) {
	p.Base.Resize(w, h)
	p.mask = nil
}

func (p *Parrot) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		p.resize(msg.W, msg.H)
	case bubble.Activate:
		p.Palette = p.PickPalette()
	case tea.KeyPressMsg:
		p.PaletteKey(msg)
	case bubble.Tick:
		p.Bands = len(msg.Signal.Mix)
		for range p.Take(msg.Dt) {
			p.Steps++
			if msg.Signal.BPM == 0 {
				p.step(msg.Signal)
			}
		}
		if msg.Signal.BPM > 0 {
			// dances one full cycle every two beats once the tempo is known
			phase := (float32(msg.Signal.Beats%2) + msg.Signal.Beat) / 2
			p.idx = min(int(phase*float32(len(parrotFrames))), len(parrotFrames)-1)
		}
		p.draw()
	}
	return nil
}

// step advances the dance frame on an energy budget when no tempo is known:
// ~1.5 frames/s in silence up to ~17 at full energy (parttysh runs at 15).
func (p *Parrot) step(sig dsp.Signal) {
	// ponytail: fixed rate; make it an option if the panel wants it calmer.
	p.acc += 0.035 + 0.35*sig.Energy
	for p.acc >= 1 {
		p.acc--
		p.idx = (p.idx + 1) % len(parrotFrames)
	}
}

// parrotMask scales every frame to the current size once, a character being
// one pixel wide and two tall: 0 is background, 1 a stroke, 2 a light stroke
// (punctuation) drawn dim.
func (p *Parrot) parrotMask() [][][]uint8 {
	if p.mask != nil {
		return p.mask
	}
	s := min(float32(p.W)/50, float32(p.H)/36)
	ox, oy := (float32(p.W)-50*s)/2, (float32(p.H)-36*s)/2
	p.mask = make([][][]uint8, len(parrotFrames))
	for f, art := range parrotFrames {
		rows := make([][]uint8, p.H)
		for y := range rows {
			rows[y] = make([]uint8, p.W)
			ay := int((float32(y) - oy) / (2 * s))
			if ay < 0 || ay >= len(art) {
				continue
			}
			for x := range rows[y] {
				ax := int((float32(x) - ox) / s)
				switch {
				case ax < 0 || ax >= len(art[ay]) || art[ay][ax] == ' ':
				case strings.IndexByte(".,':;", art[ay][ax]) >= 0:
					rows[y][x] = 2
				default:
					rows[y][x] = 1
				}
			}
		}
		p.mask[f] = rows
	}
	return p.mask
}

// draw paints the current frame's mask in the palette's colour for the frame
// so the parrot cycles through the palette like the real one cycles through
// the rainbow.
func (p *Parrot) draw() {
	p.Clear()
	if p.W == 0 || p.H == 0 || p.Bands == 0 {
		return
	}
	frame := p.Frame()
	mask := p.parrotMask()[p.idx]
	for y, row := range mask {
		c := p.CellColour(p.idx*p.Bands/len(parrotFrames), p.H-1-y, p.H)
		d := music.Dim(c, 0.5)
		for x, k := range row {
			switch k {
			case 1:
				frame[y][x] = c
			case 2:
				frame[y][x] = d
			}
		}
	}
}
