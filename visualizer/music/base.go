// Package music holds the state every music-kind visualizer mode embeds:
// frame buffer, fixed-timestep clock and palette selection.
package music

import (
	"encoding/json"
	"fmt"
	"image/color"
	"math/rand/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// Step is one legacy audio block: 1024 samples at 44.1 kHz, the unit every
// mode's physics was originally tuned in.
const Step = time.Second * 1024 / 44100

// stepper turns frame time into whole physics steps, carrying the remainder
// forward so no time is lost or gained across ticks.
type stepper struct{ acc time.Duration }

// take returns how many whole steps dt is worth, accumulated with whatever
// was left over from previous calls.
func (s *stepper) take(dt time.Duration) int {
	s.acc += dt
	n := int(s.acc / Step)
	s.acc -= time.Duration(n) * Step
	return n
}

// Settings is the shared settings shape for every bubble but Fire, which
// ignores the palette.
type Settings struct {
	Palettes []string `json:"palettes"` // empty = all
}

// Base is embedded by every music bubble: frame state, the fixed-timestep
// clock and palette selection.
type Base struct {
	name        string
	frame       [][]color.RGBA
	W, H, Bands int
	Steps       int // physics steps run, drives animated palettes (was Model.tick)
	stepper
	Set       Settings
	Palette   palette.Palette
	SkipFlags bool // pick from every palette but the flags when Set.Palettes is empty
}

// NewBase returns a Base named name, starting on the first shipped palette.
func NewBase(name string) Base {
	return Base{name: name, Palette: palette.Palettes[0], Set: Settings{Palettes: []string{}}}
}

func (b *Base) Name() string      { return b.name }
func (b *Base) Kind() bubble.Kind { return bubble.Music }

// Frame is the current picture, rows top to bottom. Zero pixels are off.
func (b *Base) Frame() [][]color.RGBA { return b.frame }

func (b *Base) Resize(w, h int) {
	b.W, b.H = w, h
	b.frame = bubble.NewFrame(w, h)
}

func (b *Base) Clear() {
	for _, row := range b.frame {
		clear(row)
	}
}

// Take returns how many whole physics steps dt is worth, accumulated with
// whatever was left over from previous calls.
func (b *Base) Take(dt time.Duration) int { return b.take(dt) }

// CellColour is the palette's bar colour for a band and row, rolled over
// time for animated palettes, or its peak colour for a palette that hides
// bars.
func (b *Base) CellColour(band, y, h int) color.RGBA {
	if b.Palette.Animate {
		band = (band + b.Steps/8) % b.Bands
	}
	c, peak := b.Palette.At(band, b.Bands, y, h)
	if c.A == 0 {
		c = peak
	}
	return c
}

// Dim scales a colour's brightness by l in 0..1.
func Dim(c color.RGBA, l float32) color.RGBA {
	return color.RGBA{uint8(float32(c.R) * l), uint8(float32(c.G) * l), uint8(float32(c.B) * l), 255}
}

// PickPalette picks with r from the configured palettes, or from every
// palette (minus the flags when SkipFlags) when none are configured.
func (b *Base) PickPalette(r *rand.Rand) palette.Palette {
	return PickPalette(r, b.Set.Palettes, b.SkipFlags)
}

// PickPalette picks with r uniformly from names, or from every registered
// palette when names is empty, leaving out the Upright ones (flags) when
// skipFlags: a mode that swirls or warps the picture cannot use stripes. A
// nil r rolls the global source. A free function, not a Base method, since
// a mode with its own settings shape (e.g. bars) needs it too.
func PickPalette(r *rand.Rand, names []string, skipFlags bool) palette.Palette {
	r = bubble.Activate{Rand: r}.Source()
	if len(names) == 0 {
		pool := make([]palette.Palette, 0, len(palette.Palettes))
		for _, p := range palette.Palettes {
			if !(skipFlags && p.Upright) {
				pool = append(pool, p)
			}
		}
		return pool[r.IntN(len(pool))]
	}
	p, _ := palette.ByName(names[r.IntN(len(names))])
	return p
}

// PaletteKey steps the current palette forward on "c" and back on "C",
// walking the full list by the index of the current name. It reports
// whether it handled the key.
func (b *Base) PaletteKey(msg tea.KeyPressMsg) bool {
	i := 0
	for j, p := range palette.Palettes {
		if p.Name == b.Palette.Name {
			i = j
			break
		}
	}
	switch msg.String() {
	case "c":
		b.Palette = palette.Palettes[(i+1)%len(palette.Palettes)]
		return true
	case "C":
		b.Palette = palette.Palettes[(i+len(palette.Palettes)-1)%len(palette.Palettes)]
		return true
	}
	return false
}

func (b *Base) Settings() any { return b.Set }

// Hints describes the shared settings shape for the UI.
func (b *Base) Hints() map[string]bubble.Hint {
	return map[string]bubble.Hint{"palettes": {Type: "multi", Choices: "palettes"}}
}

// CheckPalettes returns an error naming the first of names that is not a
// registered palette, listing every name to choose from. Modes with
// their own settings shape (e.g. Bars) call this too, since they cannot use
// Base's Configure directly.
func CheckPalettes(names []string) error {
	for _, name := range names {
		if _, ok := palette.ByName(name); !ok {
			return fmt.Errorf("unknown palette %q, have: %s", name, strings.Join(palette.Names(), ", "))
		}
	}
	return nil
}

// Configure patches the settings, rejecting unknown fields and unknown
// palette names.
func (b *Base) Configure(raw json.RawMessage) error {
	out, err := bubble.Patch(b.Set, raw)
	if err != nil {
		return err
	}
	if err := CheckPalettes(out.Palettes); err != nil {
		return err
	}
	if out.Palettes == nil { // a stored null reads back as [], not null
		out.Palettes = []string{}
	}
	b.Set = out
	return nil
}
