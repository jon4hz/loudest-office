package spectrum

import (
	"embed"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"
)

// Mode says what the frame shows.
type Mode int

const (
	Bars      Mode = iota // one bar per band, the classic analyzer
	Fire                  // flames fed by the band levels
	Life                  // Conway's game of life, cells born on the bottom row where the bands are loud
	Starfield             // stars flying at the viewer, faster with energy, a drop is a hyperspace jump
	Fireworks             // bands launch rockets, loud bands higher, that burst into sparks; a drop fires a volley
	Parrot                // party parrot, dancing as fast as the music is loud
)

func (m Mode) String() string {
	return [...]string{"bars", "fire", "life", "stars", "fireworks", "parrot"}[m]
}

// ParseMode accepts the names printed by Mode.String.
func ParseMode(name string) (Mode, bool) {
	for m := Bars; m <= Parrot; m++ {
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
// mode leaves anything to fade; the other modes repaint every pixel.
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

// stepLife runs one generation on a torus, or just allocates a fresh grid
// when the frame size changed.
func (m *Model) stepLife() {
	if len(m.life) != m.h || (m.h > 0 && len(m.life[0]) != m.w) {
		m.life = make([][]bool, m.h)
		for y := range m.life {
			m.life[y] = make([]bool, m.w)
		}
		return
	}
	next := make([][]bool, m.h)
	for y := range m.life {
		next[y] = make([]bool, m.w)
		for x := range m.life[y] {
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if (dx != 0 || dy != 0) && m.life[(y+dy+m.h)%m.h][(x+dx+m.w)%m.w] {
						n++
					}
				}
			}
			next[y][x] = n == 3 || (n == 2 && m.life[y][x])
		}
	}
	m.life = next
}

// feedLife births cells on the bottom row, each column with its band level
// squared as the chance, then runs generations on an energy budget.
func (m *Model) feedLife(energy float32) {
	if len(m.life) != m.h || (m.h > 0 && len(m.life[0]) != m.w) {
		m.stepLife()
	}
	if m.h == 0 || m.w == 0 {
		return
	}
	for x := range m.life[m.h-1] {
		if l := m.mix[x*m.bands/m.w]; rand.Float32() < l*l {
			m.life[m.h-1][x] = true
		}
	}
	// ponytail: fixed rate of ~6 generations/s in silence up to ~20 at full
	// energy. Make it an option if the soup boils too fast on the panel.
	m.lifeAcc += 0.15 + 0.35*energy
	for m.lifeAcc >= 1 {
		m.lifeAcc--
		m.stepLife()
	}
}

// drawLife paints live cells in the palette's colour for their column and row.
func (m *Model) drawLife() {
	for y, row := range m.life {
		for x, alive := range row {
			if alive {
				m.frame[y][x] = m.cellColour(x*m.bands/m.w, m.h-1-y, m.h)
			}
		}
	}
}

// cellColour is the palette's bar colour for a band and row, rolled over time
// for animated palettes, or its peak colour for a palette that hides bars.
func (m *Model) cellColour(band, y, h int) color.RGBA {
	if m.palette.Animate {
		band = (band + m.tick/8) % m.bands
	}
	c, peak := m.palette.At(band, m.bands, y, h)
	if c.A == 0 {
		c = peak
	}
	return c
}

// star is a point in front of the viewer: x, y in -1..1 at depth z in 0..1.
type star struct{ x, y, z float32 }

// warp is how many blocks of hyperspace are left after a drop.
func (m Model) warp() int {
	if m.mode == Starfield {
		return m.burst
	}
	return 0
}

// stepStars flies the stars at the viewer at a speed that follows the energy,
// four times faster in warp, and respawns the ones that passed by.
func (m *Model) stepStars(energy float32) {
	if n := max(20, m.w*m.h/16); len(m.stars) != n {
		m.stars = make([]star, n)
		for i := range m.stars {
			m.stars[i] = star{rand.Float32()*2 - 1, rand.Float32()*2 - 1, rand.Float32()}
		}
	}
	// ponytail: fixed speeds, 1% of the depth per block idle up to 6% at full energy
	speed := 0.01 + 0.05*energy
	if m.warp() > 0 {
		speed *= 4
	}
	for i := range m.stars {
		s := &m.stars[i]
		s.z -= speed
		if s.z <= 0.02 || max(s.x, -s.x, s.y, -s.y)/s.z > 1 {
			*s = star{rand.Float32()*2 - 1, rand.Float32()*2 - 1, 1}
		}
	}
}

// drawStars projects every star, as a radial streak in warp, brighter the nearer.
func (m *Model) drawStars() {
	steps := 1
	if m.warp() > 0 {
		steps = 6
	}
	cx, cy := float32(m.w)/2, float32(m.h)/2
	for i, s := range m.stars {
		for k := range steps {
			z := s.z + float32(k)*0.02
			x, y := int(cx+s.x/z*cx), int(cy+s.y/z*cy)
			if z > 1 || x < 0 || x >= m.w || y < 0 || y >= m.h {
				continue
			}
			m.frame[y][x] = dim(m.cellColour(i%m.bands, int((1-z)*float32(m.h-1)), m.h), 1-0.7*z)
		}
	}
}

// dim scales a colour's brightness by l in 0..1.
func dim(c color.RGBA, l float32) color.RGBA {
	return color.RGBA{uint8(float32(c.R) * l), uint8(float32(c.G) * l), uint8(float32(c.B) * l), 255}
}

// particle is a firework rocket or spark: x in 0..1 across, y in 0..1 up,
// speeds per block. A spark fades as bright falls to 0.
type particle struct {
	x, y, vx, vy float32
	band         int
	rocket       bool
	bright       float32
}

// ponytail: fixed fireworks physics, after maaslalani/confetty. Rockets fly
// under the flying peaks' gravity so a full-level one reaches the top; sparks
// fall at a quarter of that and fade over ~1 s. Pool capped at 2000.
const (
	rocketGravity  = 0.004
	rocketSpeed    = 0.0894 // sqrt(2 * rocketGravity): reaches the top
	sparkGravity   = 0.001
	sparkSpeed     = 0.02
	sparksPerBurst = 30
)

// stepFireworks launches at most one rocket per block, from a band with its
// level squared as the chance and a height set by the level, fires a volley
// on a drop, flies everything and bursts rockets near their apex.
func (m *Model) stepFireworks(dropped bool) {
	launch := func(b int, l float32) {
		if len(m.parts) < 2000 {
			m.parts = append(m.parts, particle{x: (float32(b) + rand.Float32()) / float32(m.bands),
				vy: rocketSpeed * (0.5 + 0.5*l), band: b, rocket: true, bright: 1})
		}
	}
	off := rand.IntN(m.bands)
	for i := range m.mix {
		if b := (i + off) % m.bands; rand.Float32() < m.mix[b]*m.mix[b]*0.1 {
			launch(b, m.mix[b])
			break
		}
	}
	if dropped {
		for range 5 {
			launch(rand.IntN(m.bands), 1)
		}
	}
	aspect := float32(m.h) / float32(max(m.w, 1)) // round bursts on a wide frame
	kept, born := m.parts[:0], []particle(nil)
	for _, p := range m.parts {
		switch {
		case p.rocket && p.vy <= 0.1*rocketSpeed: // near the apex: burst
			for range sparksPerBurst {
				if len(m.parts)+len(born) >= 2000 {
					break
				}
				a, v := rand.Float64()*2*math.Pi, sparkSpeed*(0.5+0.5*rand.Float32())
				born = append(born, particle{x: p.x, y: p.y, vx: float32(math.Cos(a)) * v * aspect,
					vy: float32(math.Sin(a)) * v, band: p.band, bright: 1})
			}
		case p.rocket:
			p.vy -= rocketGravity
			p.y += p.vy
			kept = append(kept, p)
		default:
			p.vy -= sparkGravity
			p.x += p.vx
			p.y += p.vy
			p.bright -= 1.0 / 45
			if p.bright > 0 && p.y >= 0 && p.x >= 0 && p.x < 1 {
				kept = append(kept, p)
			}
		}
	}
	m.parts = append(kept, born...)
}

// drawFireworks paints rockets with a tail as long as they are fast and
// sparks fading out, all in the palette's colour for their band and height.
func (m *Model) drawFireworks() {
	for _, p := range m.parts {
		x, y := int(p.x*float32(m.w)), int(p.y*float32(m.h-1)+0.5)
		if x < 0 || x >= m.w || y < 0 || y >= m.h {
			continue
		}
		c := m.cellColour(p.band, y, m.h)
		if !p.rocket {
			m.frame[m.h-1-y][x] = dim(c, p.bright)
			continue
		}
		m.frame[m.h-1-y][x] = c
		for t := 1; t <= int(p.vy/rocketSpeed*8) && y-t >= 0; t++ {
			m.frame[m.h-1-(y-t)][x] = dim(c, 0.4)
		}
	}
}

// parrotFS holds the party parrot frames, 50x18 characters each, from
// github.com/caarlos0/parttysh (MIT, see parrot/LICENSE).
//
//go:embed parrot/*.txt
var parrotFS embed.FS

// parrotFrames are the frames in order, each a slice of rows.
var parrotFrames = func() [][]string {
	entries, _ := parrotFS.ReadDir("parrot")
	var frames [][]string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		b, _ := parrotFS.ReadFile("parrot/" + e.Name())
		frames = append(frames, strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
	}
	return frames
}()

// stepParrot advances the animation on an energy budget: the louder the
// music, the faster the parrot dances.
func (m *Model) stepParrot(energy float32) {
	// ponytail: fixed rate of ~2 frames/s in silence up to ~28 at full energy
	// (parttysh runs at 15). Make it an option if the panel wants it calmer.
	m.parrotAcc += 0.05 + 0.6*energy
	for m.parrotAcc >= 1 {
		m.parrotAcc--
		m.parrotFrame = (m.parrotFrame + 1) % len(parrotFrames)
	}
}

// parrotMask scales every frame to the current size once, a character being
// one pixel wide and two tall: 0 is background, 1 a stroke, 2 a light stroke
// (punctuation) drawn dim. It is dropped on resize, so it never outlives the
// frame it was built for.
func (m *Model) parrotMask() [][][]uint8 {
	if m.parrot != nil {
		return m.parrot
	}
	s := min(float32(m.w)/50, float32(m.h)/36)
	ox, oy := (float32(m.w)-50*s)/2, (float32(m.h)-36*s)/2
	m.parrot = make([][][]uint8, len(parrotFrames))
	for f, art := range parrotFrames {
		mask := make([][]uint8, m.h)
		for y := range mask {
			mask[y] = make([]uint8, m.w)
			ay := int((float32(y) - oy) / (2 * s))
			if ay < 0 || ay >= len(art) {
				continue
			}
			for x := range mask[y] {
				ax := int((float32(x) - ox) / s)
				switch {
				case ax < 0 || ax >= len(art[ay]) || art[ay][ax] == ' ':
				case strings.IndexByte(".,':;", art[ay][ax]) >= 0:
					mask[y][x] = 2
				default:
					mask[y][x] = 1
				}
			}
		}
		m.parrot[f] = mask
	}
	return m.parrot
}

// drawParrot paints the current frame's mask in the palette's colour for the
// frame so the parrot cycles through the palette like the real one cycles
// through the rainbow.
func (m *Model) drawParrot() {
	mask := m.parrotMask()[m.parrotFrame]
	for y, row := range mask {
		c := m.cellColour(m.parrotFrame*m.bands/len(parrotFrames), m.h-1-y, m.h)
		d := dim(c, 0.5)
		for x, k := range row {
			switch k {
			case 1:
				m.frame[y][x] = c
			case 2:
				m.frame[y][x] = d
			}
		}
	}
}
