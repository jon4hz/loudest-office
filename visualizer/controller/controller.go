// Package controller is the root tea.Model. Once per frame tick it decides
// which bubble is shown (event > pin > interlude > music > idle > blank),
// loops within a kind by weight, and owns the event queue, the panel
// brightness and the settings the state file persists. The HTTP API calls
// its methods from inside Update through a Call, so nothing here needs a
// lock and nothing holds the *tea.Program.
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

// Loop is how one kind cycles through its bubbles.
type Loop struct {
	Seconds float64 `json:"loop"`  // 0 = stay
	Order   string  `json:"order"` // "random" | "sequence"
}

// Orders are the loop orders a Loop accepts.
var Orders = []string{"random", "sequence"}

// Settings is the runtime configuration; the state file holds exactly this.
type Settings struct {
	Music          Loop    `json:"music"`
	Idle           Loop    `json:"idle"`
	Show           string  `json:"show"`
	MusicDB        float64 `json:"music_db"`
	SilenceDB      float64 `json:"silence_db"`
	SilenceAfter   float64 `json:"silence_after"`
	Brightness     uint8   `json:"brightness"`
	IdleBrightness uint8   `json:"idle_brightness"`
}

// Entry is one registered bubble and its weight; 0 disables it.
type Entry struct {
	Bubble bubble.Bubble
	Weight int
}

// Panel is the part of serial.Port the controller uses.
type Panel interface {
	Send([][]color.RGBA)
	SetBrightness(byte)
	Status() proto.StatusMsg
}

// Options is everything the controller takes from main.
type Options struct {
	Analyzer   *dsp.Analyzer
	Blocks     <-chan [][]float32   // nil in tests
	Wait       func() error         // capture's Wait, nil in tests
	Panel      Panel                // nil = terminal only
	OnFrame    func([][]color.RGBA) // every rendered frame, e.g. the websocket hub; nil = none
	W, H       int                  // fixed size from the panel, 0 = follow the window
	FPS        int
	Brightness uint8  // default for both brightness settings
	StatePath  string // "" = do not persist
	Show       string // pin at start, not saved
	Headless   bool   // skip rendering the view
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

	w, h  int
	blank [][]color.RGBA
	view  string // the rendered frame, empty while headless

	active  bubble.Bubble // nil = blank
	kind    bubble.Kind   // kind of active, "" = blank
	since   time.Time     // when active was activated
	repick  bool          // Next: pick again on the next tick
	recheck bool          // a patch: re-arm the loop on the next tick

	interlude      string        // bubble shown for a while, "" = none
	interludeFor   time.Duration // its length
	interludeUntil time.Time     // zero until the first tick after Show

	last    time.Time // previous frame, zero before the first
	play    bool
	quiet   time.Time // when the level first fell below silence_db
	db, bpm float64

	events []bubble.Event
	nextID int
	alert  bubble.Event // what the event bubbles were last told

	bright     byte
	brightSent bool

	initCmd tea.Cmd // what the bubbles answered to the first Resize, handed to Init

	err     error
	sigHook func() dsp.Signal // tests: used instead of Analyzer.Take
}

var _ tea.Model = (*Controller)(nil)

// New registers the bubbles and loads the state file. Duplicate names, an
// unknown Options.Show and unparseable state are errors.
func New(entries []Entry, o Options) (*Controller, error) {
	c := &Controller{o: o, entries: slices.Clone(entries), index: make(map[string]int, len(entries)),
		show: o.Show, prevLoop: map[bubble.Kind]float64{},
		set: Settings{Music: Loop{60, "random"}, Idle: Loop{60, "random"}, MusicDB: -50,
			SilenceDB: -60, SilenceAfter: 12, Brightness: o.Brightness, IdleBrightness: o.Brightness}}
	for i, e := range c.entries {
		name := e.Bubble.Name()
		if _, dup := c.index[name]; dup {
			return nil, fmt.Errorf("duplicate bubble %q", name)
		}
		c.index[name] = i
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	if err := c.checkShow(c.show); err != nil {
		return nil, err
	}
	if o.W != 0 || o.H != 0 {
		// A bubble may answer its first size with a cmd, e.g. to start
		// polling; New has no *tea.Program to run it, Init does.
		c.initCmd = c.resize(o.W, o.H)
	}
	return c, nil
}

// Err is the capture's error, set when the block channel closed.
func (c *Controller) Err() error { return c.err }

func (c *Controller) Init() tea.Cmd { return tea.Batch(c.wait(), c.frame(), c.initCmd) }

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

// frame arms the next frame tick.
func (c *Controller) frame() tea.Cmd {
	fps := c.o.FPS
	if fps <= 0 {
		fps = 30
	}
	return tea.Tick(time.Second/time.Duration(fps), func(t time.Time) tea.Msg { return frameMsg(t) })
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
	case Call:
		msg(c)
		return c, nil
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
	case tea.WindowSizeMsg:
		if c.o.W == 0 { // with a panel the size is fixed from its INFO
			return c, c.resize(msg.Width, 2*msg.Height)
		}
		return c, nil
	case bubble.Resize: // a websocket client's terminal, in pixels
		if c.o.W == 0 && msg.W > 0 && msg.H > 0 {
			return c, c.resize(msg.W, msg.H)
		}
		return c, nil
	case tea.KeyPressMsg:
		return c, c.key(msg)
	}
	// anything else goes to every bubble, so a bubble can poll on its own
	cmds := make([]tea.Cmd, 0, len(c.entries))
	for _, e := range c.entries {
		cmds = append(cmds, e.Bubble.Update(msg))
	}
	return c, tea.Batch(cmds...)
}

func (c *Controller) View() tea.View {
	v := tea.NewView(c.view)
	v.AltScreen = true
	return v
}

// resize reallocates the blank frame and tells every bubble the new size.
func (c *Controller) resize(w, h int) tea.Cmd {
	c.w, c.h = w, h
	c.blank = bubble.NewFrame(w, h)
	cmds := make([]tea.Cmd, 0, len(c.entries))
	for _, e := range c.entries {
		cmds = append(cmds, e.Bubble.Update(bubble.Resize{W: w, H: h}))
	}
	return tea.Batch(cmds...)
}

// key handles the controller's own keys and passes the rest to the shown
// bubble. Neither m nor a writes the state file.
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
			return c.active.Update(msg)
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
		if e := c.entries[(start+i)%len(c.entries)]; e.Bubble.Kind() != bubble.EventKind {
			return e.Bubble.Name()
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

// tick runs one frame: analysis, music detection, event expiry, the kind and
// bubble choice, then the frame itself.
func (c *Controller) tick(now time.Time) tea.Cmd {
	sig := c.take()
	c.db, c.bpm = sig.DB, sig.BPM
	var dt time.Duration
	if !c.last.IsZero() {
		dt = min(now.Sub(c.last), maxDt)
	}
	c.last = now
	c.detect(sig.DB, now)
	cmds := c.expire(now)
	cmds = append(cmds, c.choose(now)...)
	frame := c.blank
	if c.active != nil {
		cmds = append(cmds, c.active.Update(bubble.Tick{Signal: sig, Dt: dt, Now: now}))
		frame = c.active.Frame()
	}
	if c.o.OnFrame != nil {
		c.o.OnFrame(frame)
	}
	if c.o.Panel != nil {
		c.o.Panel.Send(frame)
		if want := c.wantBright(); !c.brightSent || want != c.bright {
			c.bright, c.brightSent = want, true
			c.o.Panel.SetBrightness(want)
		}
	}
	if !c.o.Headless {
		c.view = bubble.Render(frame)
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

// expire drops the events that are due and tells the event bubbles the texts
// of the highest live level whenever that text or level changed.
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
	for _, e := range c.entries {
		if e.Bubble.Kind() == bubble.EventKind {
			cmds = append(cmds, e.Bubble.Update(ev))
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
		return e.Bubble.Kind(), e.Bubble
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
	return e.Bubble
}

// candidates are the registry indices of the enabled bubbles of kind.
func (c *Controller) candidates(kind bubble.Kind) []int {
	var out []int
	for i, e := range c.entries {
		if e.Bubble.Kind() == kind && e.Weight > 0 {
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
				return c.entries[i].Bubble
			}
		}
		return c.entries[cand[0]].Bubble
	}
	var total int
	for _, i := range cand {
		total += c.entries[i].Weight
	}
	n := rand.IntN(total)
	for _, i := range cand {
		if n -= c.entries[i].Weight; n < 0 {
			return c.entries[i].Bubble
		}
	}
	return c.entries[cand[len(cand)-1]].Bubble
}

// activate makes b the shown bubble and lets it choose its own variation.
func (c *Controller) activate(b bubble.Bubble, kind bubble.Kind, now time.Time) []tea.Cmd {
	c.active, c.kind, c.since = b, kind, now
	return []tea.Cmd{b.Update(bubble.Activate{})}
}

// due reports whether the shown bubble's loop ran out.
func (c *Controller) due(now time.Time) bool {
	l := c.loop(c.kind)
	return l != nil && l.Seconds > 0 && now.Sub(c.since) >= secs(l.Seconds)
}

// wantBright is the brightness for the shown kind: idle and blank dim.
func (c *Controller) wantBright() byte {
	switch c.kind {
	case bubble.Music, bubble.EventKind, bubble.Track:
		return c.set.Brightness
	}
	return c.set.IdleBrightness
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
