// Package polygon is the bouncing-polygon screensaver, after tronbyt's
// polygon_bounce app: corners bounce off the walls and leave a trail.
package polygon

import (
	"math"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// ponytail: fixed shape and physics. 4 corners, a trail of 6 outlines taken
// every 3 blocks, speeds in frame widths per block.
const (
	sides     = 4
	trail     = 6
	trailGap  = 3
	baseSpeed = 0.004
)

// vertex is a corner: x, y in 0..1 with a unit direction dx, dy.
type vertex struct{ x, y, dx, dy float32 }

// Polygon is a bouncing polygon. Every corner listens to its own slice of
// the bands and flies faster the louder it is, all corners surge on the
// beat, and a drop kicks them off in new directions at triple speed.
type Polygon struct {
	music.Base
	verts   [sides]vertex
	history [][sides]vertex // oldest first
	kick    int             // blocks of triple speed left after a drop
}

var _ bubble.Bubble = (*Polygon)(nil)

// New returns a new Polygon bubble.
func New() *Polygon {
	p := &Polygon{Base: music.NewBase("polygon")}
	for i := range p.verts {
		p.verts[i].x, p.verts[i].y = rand.Float32(), rand.Float32()
	}
	p.scatter()
	return p
}

// scatter points every corner in a new random direction.
func (p *Polygon) scatter() {
	for i := range p.verts {
		a := rand.Float64() * 2 * math.Pi
		p.verts[i].dx, p.verts[i].dy = float32(math.Cos(a)), float32(math.Sin(a))
	}
}

func (p *Polygon) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		p.Resize(msg.W, msg.H)
	case bubble.Activate:
		p.Palette = p.PickPalette()
	case tea.KeyPressMsg:
		p.PaletteKey(msg)
	case bubble.Tick:
		p.Bands = len(msg.Signal.Mix)
		if msg.Signal.Drop {
			p.kick = 30
			p.scatter()
		}
		for range p.Take(msg.Dt) {
			p.Steps++
			p.step(msg.Signal)
		}
		p.draw()
	}
	return nil
}

// step flies every corner at a speed set by the loudest band in its slice,
// bounces it off the walls and records the outline for the trail.
func (p *Polygon) step(sig dsp.Signal) {
	pulse := float32(1)
	if sig.BPM > 0 { // half speed between beats, 2x on the beat, as in stars
		pulse = 0.5 + 1.5*(1-sig.Beat)*(1-sig.Beat)
	}
	if p.kick > 0 {
		pulse *= 3
		p.kick--
	}
	for i := range p.verts {
		v := &p.verts[i]
		var level float32
		if p.Bands > 0 {
			lo := i * p.Bands / sides
			for _, l := range sig.Mix[lo:max(lo+1, (i+1)*p.Bands/sides)] {
				level = max(level, l)
			}
		}
		speed := baseSpeed * (1 + 4*level) * pulse
		v.x, v.dx = bounce(v.x+v.dx*speed, v.dx)
		v.y, v.dy = bounce(v.y+v.dy*speed, v.dy)
	}
	if p.Steps%trailGap == 0 {
		p.history = append(p.history, p.verts)
		p.history = p.history[max(len(p.history)-trail, 0):]
	}
}

// bounce reflects a position that left 0..1 and turns its direction around.
func bounce(x, d float32) (float32, float32) {
	switch {
	case x < 0:
		return min(-x, 1), -d
	case x > 1:
		return max(2-x, 0), -d
	}
	return x, d
}

// draw paints the trail, oldest and dimmest first, then the polygon itself,
// in the palette's colours for the column and row.
func (p *Polygon) draw() {
	p.Clear()
	if p.W == 0 || p.H == 0 || p.Bands == 0 {
		return
	}
	for i, poly := range p.history {
		p.outline(poly, 0.15+0.5*float32(i)/trail)
	}
	p.outline(p.verts, 1)
}

// outline draws the polygon's edges at brightness l.
func (p *Polygon) outline(poly [sides]vertex, l float32) {
	px := func(v vertex) (int, int) { return int(v.x * float32(p.W-1)), int(v.y * float32(p.H-1)) }
	for i, v := range poly {
		x0, y0 := px(v)
		x1, y1 := px(poly[(i+1)%sides])
		p.line(x0, y0, x1, y1, l)
	}
}

// line is Bresenham's.
func (p *Polygon) line(x0, y0, x1, y1 int, l float32) {
	frame := p.Frame()
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	for e := dx + dy; ; {
		frame[y0][x0] = music.Dim(p.CellColour(x0*p.Bands/p.W, p.H-1-y0, p.H), l)
		if x0 == x1 && y0 == y1 {
			return
		}
		if e2 := 2 * e; e2 >= dy {
			e += dy
			x0 += sx
		} else {
			e += dx
			y0 += sy
		}
	}
}

func abs(x int) int { return max(x, -x) }
