// Package metaballs is a lava lamp: blobs are born, drift and bounce around
// the panel, and blobs that meet sink into each other and are absorbed into
// one bigger blob until a drop, or time, pinches a piece off again. Old blobs
// fade away and new ones are born every other beat and the faster the louder
// the music. Each blob is tied to a band and swells with it, so the field
// breathes with the music; the drift follows the loudness.
package metaballs

import (
	"image/color"
	"math"
	"math/rand/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
)

// Sizes are fractions of the panel's shorter side, so the lamp looks the
// same on any panel: a blob is 5% of it idle (but never under a pixel) up
// to 19% at full level, drifting 0.1% of it per step idle up to 0.6% at
// full loudness, and two blobs whose centres come within 3% of it are one.
const (
	minR      = 0.05
	maxR      = 0.19
	minV      = 0.001
	maxV      = 0.006
	absorbGap = 0.03
	cutoff    = 0.6 // field value where a pixel switches on
)

// ponytail: fixed population: never fewer than 4 blobs, never more than 12,
// one born every other beat and with 1% chance per step at full loudness, each
// living 400 to 1200 steps (~9-28 s) and fading in and out over 80 steps
// (~2 s).
const (
	minCount      = 4
	maxCount      = 12
	spawnChance   = 0.01
	beatsPerBirth = 2
	minLife       = 400
	maxLife       = 1200
	fadeSteps     = 80
)

// ponytail: fixed fusing: overlapping blobs steer 5% per step towards a
// shared heading and sink 2% of the gap into each other, and once their
// centres meet the bigger absorbs the smaller, up to four blobs' worth of
// mass. A split blob flies free of fusing for 80 steps (~2 s), and one blob
// splits every 600 steps (~14 s) on its own, lava lamp style.
const (
	steer      = 0.05
	pull       = 0.02
	maxMass    = 4
	freeSteps  = 80
	splitEvery = 600
)

// ball is one blob: frame coordinates, direction (unit vector), smoothed
// radius in pixels, its mass in blobs absorbed (0 once absorbed itself),
// the band it follows, its age and lifetime in steps and the steps left in
// which it ignores fusing.
type ball struct {
	x, y, dx, dy float64
	r, mass      float64
	band         int
	age, life    int
	free         int
}

// aim steers the blob's heading k of the way towards (dx, dy), keeping it a
// unit vector.
func (b *ball) aim(dx, dy, k float64) {
	b.dx += (dx - b.dx) * k
	b.dy += (dy - b.dy) * k
	if n := math.Hypot(b.dx, b.dy); n > 1e-9 {
		b.dx, b.dy = b.dx/n, b.dy/n
	}
}

// envelope is the blob's size factor over its life, 0..1: ramping up over
// the first fadeSteps and down over the last.
func (b *ball) envelope() float64 {
	return max(0, min(1, float64(b.age)/fadeSteps, float64(b.life-b.age)/fadeSteps))
}

// Metaballs is the lava lamp bubble.
type Metaballs struct {
	music.Base
	balls []ball
	speed float64 // smoothed drift, pixels per step
	beats int     // Signal.Beats at the last tick
}

var _ bubble.Bubble = (*Metaballs)(nil)

// scale is the panel's shorter side in pixels, the unit every size is in.
func (m *Metaballs) scale() float64 { return float64(min(m.W, m.H)) }

// New returns a new Metaballs bubble. It defaults to every palette but the
// flags, whose stripes make no sense in a blob.
func New() *Metaballs {
	m := &Metaballs{Base: music.NewBase("metaballs")}
	m.SkipFlags = true
	return m
}

func (m *Metaballs) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		m.Resize(msg.W, msg.H)
		m.balls = m.balls[:0] // born again at the new size
	case bubble.Activate:
		m.Palette = m.PickPalette(msg.Source())
	case tea.KeyPressMsg:
		m.PaletteKey(msg)
	case bubble.Tick:
		m.Bands = len(msg.Signal.Mix)
		if msg.Signal.Beats != m.beats { // every other beat births a blob
			m.beats = msg.Signal.Beats
			if m.beats%beatsPerBirth == 0 && m.W > 0 && m.Bands > 0 && len(m.balls) < maxCount {
				m.spawn()
			}
		}
		if msg.Signal.Drop {
			for i := range m.balls {
				m.split(i) // a drop splits everything up and sends it off anew
			}
		}
		for range m.Take(msg.Dt) {
			m.Steps++
			m.step(msg.Signal)
		}
		m.draw()
	}
	return nil
}

// spawn gives birth to a blob at a random spot, heading and band, with a
// random lifetime.
func (m *Metaballs) spawn() {
	a := rand.Float64() * 2 * math.Pi
	m.balls = append(m.balls, ball{x: rand.Float64() * float64(m.W), y: rand.Float64() * float64(m.H),
		dx: math.Cos(a), dy: math.Sin(a), mass: 1, band: rand.IntN(m.Bands), life: minLife + rand.IntN(maxLife-minLife)})
}

// split sends blob i off in a random direction, free of fusing for a while.
// A blob that has absorbed others pinches off a child carrying half its
// mass, sent the opposite way.
func (m *Metaballs) split(i int) {
	b := &m.balls[i]
	a := rand.Float64() * 2 * math.Pi
	b.dx, b.dy = math.Cos(a), math.Sin(a)
	b.free = freeSteps
	if b.mass < 2 {
		return
	}
	b.mass /= 2
	b.r /= math.Sqrt2
	child := *b
	child.dx, child.dy = -b.dx, -b.dy
	child.age, child.life = fadeSteps, minLife+rand.IntN(maxLife-minLife)
	m.balls = append(m.balls, child)
}

// loudness is the energy or the bass, whichever is louder: energy is the
// mean over every band and stays low on bass-heavy music, so the bass
// (lowest quarter of the bands) gets a say of its own.
func loudness(sig dsp.Signal) float64 {
	var bass float32
	low := sig.Mix[:max(1, len(sig.Mix)/4)]
	for _, l := range low {
		bass += l / float32(len(low))
	}
	return float64(min(max(sig.Energy, bass), 1))
}

// step eases the drift towards the loudness, births and buries blobs, eases
// every radius towards its band's level, fuses the blobs that overlap,
// then moves them, bouncing them off the edges. Every splitEvery steps one
// blob splits off.
func (m *Metaballs) step(sig dsp.Signal) {
	if m.W == 0 || m.H == 0 || m.Bands == 0 {
		return
	}
	e := loudness(sig)
	m.speed += ((minV+(maxV-minV)*e)*m.scale() - m.speed) * 0.1
	if len(m.balls) < minCount || len(m.balls) < maxCount && rand.Float64() < spawnChance*e {
		m.spawn()
	}
	if n := len(m.balls); n > 0 && m.Steps%splitEvery == 0 {
		m.split(m.Steps / splitEvery % n)
	}
	m.fuse()
	w, h := float64(m.W), float64(m.H)
	live := m.balls[:0]
	for _, b := range m.balls {
		b.age++
		if b.age >= b.life && b.r < 0.5 {
			continue // faded out
		}
		l := float64(min(sig.Mix[min(b.band, m.Bands-1)], 1))
		b.r += ((max(1, minR*m.scale())+(maxR-minR)*l*m.scale())*math.Sqrt(b.mass)*b.envelope() - b.r) * 0.1
		b.free = max(b.free-1, 0)
		b.x += b.dx * m.speed
		b.y += b.dy * m.speed
		if b.x < 0 || b.x > w-1 {
			b.dx = -b.dx
			b.x = min(max(b.x, 0), w-1)
		}
		if b.y < 0 || b.y > h-1 {
			b.dy = -b.dy
			b.y = min(max(b.y, 0), h-1)
		}
		live = append(live, b)
	}
	m.balls = live
}

// fuse makes every overlapping pair of blobs that is not flying free steer
// towards a shared heading and sink into each other, so they travel on as
// one; once their centres meet the bigger absorbs the smaller, keeping the
// area (its radius grows to sqrt(r₁²+r₂²)) and their combined mass. A pair
// heading straight at each other is left alone until it crosses.
func (m *Metaballs) fuse() {
	for i := range m.balls {
		for j := i + 1; j < len(m.balls); j++ {
			a, b := &m.balls[i], &m.balls[j]
			if a.free > 0 || b.free > 0 || a.mass == 0 || b.mass == 0 {
				continue
			}
			gx, gy := b.x-a.x, b.y-a.y
			gap := math.Hypot(gx, gy)
			if gap > a.r+b.r {
				continue
			}
			if gap < absorbGap*m.scale() && a.mass+b.mass <= maxMass {
				if a.r < b.r {
					a, b = b, a
				}
				a.r = math.Hypot(a.r, b.r)
				a.mass += b.mass
				b.mass = 0
				continue
			}
			mx, my := a.dx+b.dx, a.dy+b.dy
			if math.Hypot(mx, my) < 1e-6 {
				continue
			}
			a.aim(mx, my, steer)
			b.aim(mx, my, steer)
			a.x, a.y = a.x+gx*pull, a.y+gy*pull
			b.x, b.y = b.x-gx*pull, b.y-gy*pull
		}
	}
	live := m.balls[:0]
	for _, b := range m.balls {
		if b.mass > 0 {
			live = append(live, b)
		}
	}
	m.balls = live
}

// draw sums every blob's r²/d² at each pixel, the classic metaball field,
// and lights the pixels above the cutoff in the colour of the blob that
// contributes most, brighter the deeper into the field they are.
func (m *Metaballs) draw() {
	if m.W == 0 || m.H == 0 || m.Bands == 0 {
		return
	}
	frame := m.Frame()
	for y := range m.H {
		for x := range m.W {
			var f, best float64
			top := 0
			for i, b := range m.balls {
				d := (float64(x)-b.x)*(float64(x)-b.x) + (float64(y)-b.y)*(float64(y)-b.y)
				v := b.r * b.r / max(d, 0.25)
				f += v
				if v > best {
					best, top = v, i
				}
			}
			if f < cutoff {
				frame[y][x] = color.RGBA{}
				continue
			}
			frame[y][x] = music.Dim(m.CellColour(min(m.balls[top].band, m.Bands-1), m.H-1-y, m.H), float32(min(1, f/(2*cutoff))))
		}
	}
}
