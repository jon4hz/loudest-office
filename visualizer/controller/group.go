package controller

import (
	"encoding/json"
	"image/color"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// groupKey names a render group: every panel of one size and frame rate
// shares one set of bubble instances, so a picture is rendered once.
type groupKey struct{ W, H, FPS int }

// group is one set of bubble instances and the tick state they share.
type group struct {
	key     groupKey
	gen     int             // creation number; a tick of an older generation is ignored
	bubbles []bubble.Bubble // registry order, same indices as the templates
	blank   [][]color.RGBA
	last    time.Time // previous group tick, zero before the first
	armed   bool      // its tick chain runs, started by the controller tick
	drop    bool      // Drop latched from controller ticks until the next group tick
	bigDrop bool
}

// groupTick is one group's frame.
type groupTick struct {
	key groupKey
	gen int
	at  time.Time
}

// tickCmd arms the group's next tick.
func (g *group) tickCmd() tea.Cmd {
	key, gen := g.key, g.gen
	return tea.Tick(time.Second/time.Duration(key.FPS), func(t time.Time) tea.Msg { return groupTick{key, gen, t} })
}

// newGroup builds the group for key: one instance per registered bubble,
// configured like its template, sized, told the live event, and, while a
// bubble is shown, activated from the same seed as everywhere else. What
// the instances answer goes into c.pending.
func (c *Controller) newGroup(key groupKey) *group {
	c.gen++
	g := &group{key: key, gen: c.gen, blank: bubble.NewFrame(key.W, key.H), bubbles: make([]bubble.Bubble, len(c.entries))}
	for i, e := range c.entries {
		b := e.New()
		if buf, err := json.Marshal(e.tmpl.Settings()); err == nil {
			_ = b.Configure(buf) // a template's own settings always configure
		}
		c.pending = append(c.pending, b.Update(bubble.Resize{W: key.W, H: key.H}))
		if b.Kind() == bubble.EventKind && c.alert != (bubble.Event{}) {
			c.pending = append(c.pending, b.Update(c.alert))
		}
		g.bubbles[i] = b
	}
	if c.active != nil {
		c.pending = append(c.pending, g.bubbles[c.index[c.active.Name()]].Update(bubble.Activate{Rand: c.newRand()}))
	}
	c.groups[key] = g
	return g
}

// join puts p into the group of its size and fps, building it when it is
// the first; a panel without a size waits.
func (c *Controller) join(p *panel) {
	if key, ok := p.key(); ok && c.groups[key] == nil {
		c.newGroup(key)
	}
}

// leave takes p out of its group, dropping the group when p was its last panel.
func (c *Controller) leave(p *panel) {
	key, ok := p.key()
	if !ok {
		return
	}
	for _, q := range c.allPanels() {
		if k, ok := q.key(); q != p && ok && k == key {
			return
		}
	}
	delete(c.groups, key)
}

// fps is the controller's own tick rate: the fastest group's, so no group
// waits more than one of its frames for a decision.
func (c *Controller) fps() int {
	fps := 0
	for key := range c.groups {
		fps = max(fps, key.FPS)
	}
	if fps == 0 {
		return defaultFPS
	}
	return fps
}

// newRand is a source from the current activation's seed: every group's
// instance gets its own, and they all roll the same.
func (c *Controller) newRand() *rand.Rand { return rand.New(rand.NewPCG(c.seed, 0)) }

// drain hands out what group building and dialing queued.
func (c *Controller) drain() tea.Cmd {
	if len(c.pending) == 0 {
		return nil
	}
	cmd := tea.Batch(c.pending...)
	c.pending = nil
	return cmd
}

// groupTick renders one frame of a group: the shown bubble's instance ticks
// with the last taken signal and the group's drop latch, and the frame goes
// to every panel of the group. A tick for a gone group or generation is ignored.
func (c *Controller) groupTick(key groupKey, gen int, now time.Time) tea.Cmd {
	g := c.groups[key]
	if g == nil || g.gen != gen {
		return nil
	}
	var dt time.Duration
	if !g.last.IsZero() {
		dt = min(now.Sub(g.last), maxDt)
	}
	g.last = now
	sig := c.sig
	sig.Drop, sig.BigDrop = g.drop, g.bigDrop
	g.drop, g.bigDrop = false, false
	frame := g.blank
	var cmd tea.Cmd
	if c.active != nil {
		b := g.bubbles[c.index[c.active.Name()]]
		cmd = b.Update(bubble.Tick{Signal: sig, Dt: dt, Now: now})
		frame = b.Frame()
	}
	for _, p := range c.allPanels() {
		if k, ok := p.key(); ok && k == key {
			c.deliver(p, frame)
		}
	}
	return tea.Batch(cmd, g.tickCmd())
}

// deliver sends a frame to one panel, and a serial panel the brightness
// when it changed; every panel's frame also goes to its hub.
func (c *Controller) deliver(p *panel, frame [][]color.RGBA) {
	switch p.Kind {
	case KindSerial:
		if p.port != nil {
			p.port.Send(frame)
			if want := c.wantBright(p); !p.brightSent || want != p.bright {
				p.bright, p.brightSent = want, true
				p.port.SetBrightness(want)
			}
		}
	case KindTerminal:
		c.view = bubble.Render(frame)
	}
	if c.o.OnFrame != nil {
		c.o.OnFrame(p.Name, frame)
	}
}

// instances runs fn on every group's instance of the bubble at registry
// index i, once per object: a factory that hands out one instance (tests)
// still gets it once.
func (c *Controller) instances(i int, fn func(bubble.Bubble) tea.Cmd) []tea.Cmd {
	var cmds []tea.Cmd
	seen := map[bubble.Bubble]bool{}
	for _, g := range c.groups {
		if b := g.bubbles[i]; !seen[b] {
			seen[b] = true
			cmds = append(cmds, fn(b))
		}
	}
	return cmds
}

// configureAll applies raw, which the template accepted, to every instance
// of the bubble at registry index i other than the template itself: same
// type, same patch, so they stay equal to the template.
func (c *Controller) configureAll(i int, raw json.RawMessage) {
	tmpl := c.entries[i].tmpl
	c.instances(i, func(b bubble.Bubble) tea.Cmd {
		if b != tmpl {
			_ = b.Configure(raw)
		}
		return nil
	})
}
