// Package controller is the root tea.Model. Once per frame tick it decides
// which bubble is shown (event > pin > interlude > music > idle > blank),
// loops within a kind by weight, and owns the event queue, the panels and
// the settings the state file persists. Rendering happens per render group
// (group.go): every panel of one size and frame rate shares one set of
// bubble instances, ticked at that rate. The HTTP API calls its methods from
// inside Update through a Call, so nothing here needs a lock and nothing
// holds the *tea.Program.
package controller

import (
	"fmt"
	"image/color"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/proto"
)

// maxEvents is how many live events the queue holds.
const maxEvents = 32

// maxDt caps the frame delta, so a stalled process does not jump the physics.
const maxDt = 250 * time.Millisecond

// toggleLoop is what the a key turns a loop of 0 into.
const toggleLoop = 10

// defaultFPS is the controller's tick rate without a render group and the
// terminal's without an fps option.
const defaultFPS = 30

// Loop is how one kind cycles through its bubbles.
type Loop struct {
	Seconds float64 `json:"loop"`  // 0 = stay
	Order   string  `json:"order"` // "random" | "sequence"
}

// Orders are the loop orders a Loop accepts.
var Orders = []string{"random", "sequence"}

// Settings is the runtime configuration; the state file holds exactly this.
// Brightness is per serial panel, see panels.go.
type Settings struct {
	Music        Loop    `json:"music"`
	Idle         Loop    `json:"idle"`
	Show         string  `json:"show"`
	MusicDB      float64 `json:"music_db"`
	SilenceDB    float64 `json:"silence_db"`
	SilenceAfter float64 `json:"silence_after"`
}

// Entry is one registered bubble: a factory, called once for the template
// that holds the settings and once per render group, and its weight; 0
// disables it.
type Entry struct {
	New    func() bubble.Bubble
	Weight int
	tmpl   bubble.Bubble // the template, made by New
}

// Options is everything the controller takes from main.
type Options struct {
	Analyzer *dsp.Analyzer
	Blocks   <-chan [][]float32 // nil in tests
	Wait     func() error       // capture's Wait, nil in tests
	// Dial opens a serial panel; main passes serial.Open. nil = serial panels never connect.
	Dial      func(address string, baud int, brightness byte) (Port, proto.InfoMsg, error)
	OnFrame   func(panel string, frame [][]color.RGBA) // every panel's frame; nil = none
	Clients   func(panel string) int                   // a virtual panel's websocket clients; nil = 0
	Terminal  bool                                     // stdout is a tty: the terminal panel exists
	FPS       int                                      // the terminal panel's frame rate
	Seed      *PanelSpec                               // the first panel of a state file without any; nil = none
	StatePath string                                   // "" = do not persist
	Show      string                                   // pin at start, not saved
}

// Call is a tea.Msg the API sends: it runs fn inside Update.
type Call func(*Controller)

type frameMsg time.Time
type blockMsg [][]float32
type captureDone struct{}

// Controller is the root model.
type Controller struct {
	o        Options
	entries  []Entry
	index    map[string]int
	set      Settings
	show     string                  // unsaved pin from --show or the m key
	prevLoop map[bubble.Kind]float64 // loop the a key switched off

	preset   string // name of the last loaded or saved preset, "" = none
	modified bool   // something changed since preset was loaded or saved
	presets  map[string]presetDoc

	panels  []*panel // declared, in declaration order
	term    *panel   // the terminal, nil when headless
	groups  map[groupKey]*group
	gen     int       // groups made, see group.gen
	pending []tea.Cmd // what group building and dialing produced, returned by the next Update
	view    string    // the terminal's rendered frame

	active  bubble.Bubble // the shown bubble's template, nil = blank
	kind    bubble.Kind   // kind of active, "" = blank
	since   time.Time     // when active was activated
	repick  bool          // Next: pick again on the next tick
	recheck bool          // a patch: re-arm the loop on the next tick
	seed    uint64        // of the current activation: every group's instance rolls from it

	interlude      string        // bubble shown for a while, "" = none
	interludeFor   time.Duration // its length
	interludeUntil time.Time     // zero until the first tick after Show

	sig     dsp.Signal // the last taken signal, what group ticks render
	play    bool
	quiet   time.Time // when the level first fell below silence_db
	db, bpm float64

	events []bubble.Event
	nextID int
	alert  bubble.Event // what the event bubbles were last told

	err     error
	sigHook func() dsp.Signal // tests: used instead of Analyzer.Take
}

var _ tea.Model = (*Controller)(nil)

// New makes the templates, the terminal panel and loads the state file.
// Duplicate names, an unknown Options.Show and unparseable state are errors.
func New(entries []Entry, o Options) (*Controller, error) {
	c := &Controller{o: o, entries: slices.Clone(entries), index: make(map[string]int, len(entries)),
		show: o.Show, prevLoop: map[bubble.Kind]float64{}, presets: map[string]presetDoc{}, groups: map[groupKey]*group{},
		set: Settings{Music: Loop{60, "random"}, Idle: Loop{60, "random"}, MusicDB: -50, SilenceDB: -60, SilenceAfter: 12}}
	for i := range c.entries {
		e := &c.entries[i]
		e.tmpl = e.New()
		name := e.tmpl.Name()
		if _, dup := c.index[name]; dup {
			return nil, fmt.Errorf("duplicate bubble %q", name)
		}
		c.index[name] = i
	}
	if o.Terminal {
		fps := o.FPS
		if fps <= 0 {
			fps = defaultFPS
		}
		c.term = &panel{PanelSpec: PanelSpec{Name: terminalName, Kind: KindTerminal, FPS: fps}}
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	if err := c.checkShow(c.show); err != nil {
		return nil, err
	}
	return c, nil
}

// Err is the capture's error, set when the block channel closed.
func (c *Controller) Err() error { return c.err }

func (c *Controller) Init() tea.Cmd { return tea.Batch(c.wait(), c.frame(), c.drain()) }

// wait blocks on the next audio block; nil when there is no capture.
func (c *Controller) wait() tea.Cmd {
	blocks := c.o.Blocks
	if blocks == nil {
		return nil
	}
	return func() tea.Msg {
		blk, ok := <-blocks
		if !ok {
			return captureDone{}
		}
		return blockMsg(blk)
	}
}

// frame arms the next controller tick at the fastest group's rate.
func (c *Controller) frame() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(c.fps()), func(t time.Time) tea.Msg { return frameMsg(t) })
}

func (c *Controller) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case blockMsg:
		if c.o.Analyzer != nil {
			c.o.Analyzer.Add(msg)
		}
		return c, c.wait()
	case frameMsg:
		return c, tea.Batch(c.tick(time.Time(msg)), c.frame())
	case groupTick:
		return c, c.groupTick(msg.key, msg.gen, msg.at)
	case Call:
		msg(c)
		return c, c.drain()
	case portUp:
		c.portUp(msg)
		return c, c.drain()
	case bubble.Interlude:
		if e := c.entry(msg.Name); e != nil && e.Weight > 0 { // weight 0 switches a bubble's own requests off
			_ = c.Show(msg.Name, msg.For) // refused under a pin, which is fine
		}
		return c, nil
	case captureDone:
		if c.o.Wait != nil {
			c.err = c.o.Wait()
		}
		return c, tea.Quit
	case tea.WindowSizeMsg: // the terminal panel follows the window, group by group
		if c.term == nil {
			return c, nil
		}
		if key, ok := c.term.key(); ok && key == (groupKey{msg.Width, 2 * msg.Height, c.term.FPS}) {
			return c, nil
		}
		c.leave(c.term)
		c.term.w, c.term.h = msg.Width, 2*msg.Height
		c.join(c.term)
		return c, c.drain()
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	// anything else goes to every instance and then every template, so a
	// bubble can poll on its own and its answer reaches all of them; the
	// instances lead, so the first to see an answer is a sized one. An
	// instance that is its own template (tests) gets it once.
	var cmds []tea.Cmd
	for i, e := range c.entries {
		cmds = append(cmds, c.instances(i, func(b bubble.Bubble) tea.Cmd {
			if b == e.tmpl {
				return nil
			}
			return b.Update(msg)
		})...)
		cmds = append(cmds, e.tmpl.Update(msg))
	}
	return c, tea.Batch(cmds...)
}

func (c *Controller) View() tea.View {
	v := tea.NewView(c.view)
	v.AltScreen = true
	return v
}

// key handles the controller's own keys and passes the rest to the shown
// bubble's instance in every group. Neither m nor a writes the state file.
func (c *Controller) key(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "m":
		c.show = c.nextName()
	case "a":
		c.toggle()
	case "+", "=":
		c.bands(8)
	case "-":
		c.bands(-8)
	default:
		if c.active != nil {
			return tea.Batch(c.instances(c.index[c.active.Name()], func(b bubble.Bubble) tea.Cmd { return b.Update(msg) })...)
		}
	}
	return nil
}

// nextName is the registry name after the shown bubble, skipping event
// bubbles: the m key never pins the alert.
func (c *Controller) nextName() string {
	start := 0
	if c.active != nil {
		start = c.index[c.active.Name()] + 1
	}
	for i := range c.entries {
		if e := c.entries[(start+i)%len(c.entries)]; e.tmpl.Kind() != bubble.EventKind {
			return e.tmpl.Name()
		}
	}
	return ""
}

// toggle is the a key: clear the pin, or switch the shown kind's loop
// between 0 and its previous value.
func (c *Controller) toggle() {
	if c.pin() != "" {
		c.show, c.set.Show = "", ""
		return
	}
	l := c.loop(c.kind)
	if l == nil {
		return
	}
	if l.Seconds != 0 {
		c.prevLoop[c.kind], l.Seconds = l.Seconds, 0
		return
	}
	if l.Seconds = c.prevLoop[c.kind]; l.Seconds == 0 {
		l.Seconds = toggleLoop
	}
	c.recheck = true
}

// bands steps the band count by d, clamped to 8..64.
func (c *Controller) bands(d int) {
	if c.o.Analyzer == nil {
		return
	}
	c.o.Analyzer.SetBands(min(64, max(8, c.o.Analyzer.Bands()+d)))
}

// tick runs one controller frame: analysis, music detection, event expiry
// and the kind and bubble choice. The groups render on their own ticks;
// this one hands them the drop flags and starts the chain of a new group.
func (c *Controller) tick(now time.Time) tea.Cmd {
	c.sig = c.take()
	c.db, c.bpm = c.sig.DB, c.sig.BPM
	c.detect(c.sig.DB, now)
	cmds := c.expire(now)
	cmds = append(cmds, c.choose(now)...)
	for _, g := range c.groups {
		g.drop, g.bigDrop = g.drop || c.sig.Drop, g.bigDrop || c.sig.BigDrop
		if !g.armed {
			g.armed = true
			cmds = append(cmds, g.tickCmd())
		}
	}
	return tea.Batch(cmds...)
}

// take is this frame's analysis.
func (c *Controller) take() dsp.Signal {
	switch {
	case c.sigHook != nil:
		return c.sigHook()
	case c.o.Analyzer != nil:
		return c.o.Analyzer.Take()
	}
	return dsp.Signal{}
}

// detect is the two-threshold RMS music detector: music_db sets playing,
// silence_db for silence_after seconds clears it, in between resets the timer.
func (c *Controller) detect(db float64, now time.Time) {
	switch {
	case db >= c.set.MusicDB:
		c.play, c.quiet = true, time.Time{}
	case db < c.set.SilenceDB:
		if c.quiet.IsZero() {
			c.quiet = now
		}
		if now.Sub(c.quiet) >= secs(c.set.SilenceAfter) {
			c.play = false
		}
	default:
		c.quiet = time.Time{}
	}
}

// expire drops the events that are due and tells every group's event
// bubbles the texts of the highest live level whenever that text or level changed.
func (c *Controller) expire(now time.Time) []tea.Cmd {
	c.events = slices.DeleteFunc(c.events, func(e bubble.Event) bool { return !now.Before(e.Expires) })
	var top string
	var texts []string
	for _, e := range c.events {
		switch {
		case rank(e.Level) > rank(top):
			top, texts = e.Level, []string{e.Text}
		case e.Level == top:
			texts = append(texts, e.Text)
		}
	}
	ev := bubble.Event{Text: strings.Join(texts, " +++ "), Level: top}
	if ev == c.alert {
		return nil
	}
	c.alert = ev
	var cmds []tea.Cmd
	for i, e := range c.entries {
		if e.tmpl.Kind() == bubble.EventKind {
			cmds = append(cmds, c.instances(i, func(b bubble.Bubble) tea.Cmd { return b.Update(ev) })...)
		}
	}
	return cmds
}

// choose settles the kind and picks a bubble within it when the kind changed,
// the shown bubble went away, the loop ran out or Next was called.
func (c *Controller) choose(now time.Time) []tea.Cmd {
	kind, pinned := c.decide(now)
	if c.recheck { // a patched loop counts from this tick
		c.recheck, c.since = false, now
	}
	if pinned != nil { // a pin or an interlude is activated once and never looped
		c.repick = false // a Next() sent while pinned must not fire later, once the pin clears
		if c.active == pinned {
			return nil
		}
		return c.activate(pinned, kind, now)
	}
	if kind != "" && !c.repick && c.kind == kind && c.active != nil &&
		c.weight(c.active) > 0 && !c.due(now) {
		return nil
	}
	c.repick = false
	next := c.pick(kind)
	if next == nil {
		c.active, c.kind = nil, ""
		return nil
	}
	return c.activate(next, kind, now)
}

// decide is the priority: a live event, else the pin, else an interlude, else
// music while playing, else idle, else blank. Events, music and idle need an
// enabled bubble of their own; the pin and the interlude name one bubble.
func (c *Controller) decide(now time.Time) (bubble.Kind, bubble.Bubble) {
	if len(c.events) > 0 && len(c.candidates(bubble.EventKind)) > 0 {
		return bubble.EventKind, nil
	}
	if e := c.entry(c.pin()); e != nil {
		c.interlude = "" // a pin means only this
		return e.tmpl.Kind(), e.tmpl
	}
	if b := c.interluding(now); b != nil {
		return b.Kind(), b
	}
	if c.play && len(c.candidates(bubble.Music)) > 0 {
		return bubble.Music, nil
	}
	if len(c.candidates(bubble.Idle)) > 0 {
		return bubble.Idle, nil
	}
	return "", nil
}

// interluding is the bubble of the live interlude, nil without one. Show has
// no clock, so the first tick after it starts the time.
func (c *Controller) interluding(now time.Time) bubble.Bubble {
	e := c.entry(c.interlude)
	if e == nil {
		return nil
	}
	if c.interludeUntil.IsZero() {
		c.interludeUntil = now.Add(c.interludeFor)
	}
	if !now.Before(c.interludeUntil) {
		c.interlude = ""
		return nil
	}
	return e.tmpl
}

// candidates are the registry indices of the enabled bubbles of kind.
func (c *Controller) candidates(kind bubble.Kind) []int {
	var out []int
	for i, e := range c.entries {
		if e.tmpl.Kind() == kind && e.Weight > 0 {
			out = append(out, i)
		}
	}
	return out
}

// pick chooses the next bubble of kind, weighted at random or in registry
// order, skipping the shown one while another candidate exists.
func (c *Controller) pick(kind bubble.Kind) bubble.Bubble {
	cand := c.candidates(kind)
	cur := -1
	if c.active != nil {
		cur = c.index[c.active.Name()]
	}
	if len(cand) > 1 {
		cand = slices.DeleteFunc(cand, func(i int) bool { return i == cur })
	}
	if len(cand) == 0 {
		return nil
	}
	if l := c.loop(kind); l != nil && l.Order == "sequence" {
		for _, i := range cand {
			if i > cur {
				return c.entries[i].tmpl
			}
		}
		return c.entries[cand[0]].tmpl
	}
	var total int
	for _, i := range cand {
		total += c.entries[i].Weight
	}
	n := rand.IntN(total)
	for _, i := range cand {
		if n -= c.entries[i].Weight; n < 0 {
			return c.entries[i].tmpl
		}
	}
	return c.entries[cand[len(cand)-1]].tmpl
}

// activate makes b the shown bubble and lets every group's instance choose
// its variation from one fresh seed.
func (c *Controller) activate(b bubble.Bubble, kind bubble.Kind, now time.Time) []tea.Cmd {
	c.active, c.kind, c.since = b, kind, now
	c.seed = rand.Uint64()
	return c.instances(c.index[b.Name()], func(inst bubble.Bubble) tea.Cmd { return inst.Update(bubble.Activate{Rand: c.newRand()}) })
}

// due reports whether the shown bubble's loop ran out.
func (c *Controller) due(now time.Time) bool {
	l := c.loop(c.kind)
	return l != nil && l.Seconds > 0 && now.Sub(c.since) >= secs(l.Seconds)
}

// pin is the effective pin: the unsaved override, else the saved one.
func (c *Controller) pin() string {
	if c.show != "" {
		return c.show
	}
	return c.set.Show
}

// loop is the loop of a kind that has one, nil for events and blank.
func (c *Controller) loop(kind bubble.Kind) *Loop {
	switch kind {
	case bubble.Music:
		return &c.set.Music
	case bubble.Idle:
		return &c.set.Idle
	}
	return nil
}

// entry is the registered bubble called name, nil when there is none.
func (c *Controller) entry(name string) *Entry {
	i, ok := c.index[name]
	if !ok {
		return nil
	}
	return &c.entries[i]
}

// weight is b's weight; b is always registered.
func (c *Controller) weight(b bubble.Bubble) int { return c.entries[c.index[b.Name()]].Weight }

// secs turns settings seconds into a duration.
func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// rank is an event level's severity, -1 for no level at all.
func rank(level string) int { return slices.Index(bubble.Levels, level) }
