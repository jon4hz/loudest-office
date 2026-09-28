package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/proto"
)

// t0 is the base of every synthetic test time; tests never sleep.
var t0 = time.Unix(1_700_000_000, 0)

// at is t0 plus d.
func at(d time.Duration) time.Time { return t0.Add(d) }

// fakeSettings is a settings shape with something to reject, so Configure
// covers both an unknown field and a bad value.
type fakeSettings struct {
	Speed    float64  `json:"speed"`
	Palettes []string `json:"palettes,omitempty"`
}

// fake records every message it gets and draws a 1x1 frame in its own colour.
type fake struct {
	name string
	kind bubble.Kind
	col  color.RGBA
	msgs []tea.Msg
	set  fakeSettings

	onResize tea.Cmd // returned from Update on a bubble.Resize, nil = none
}

var _ bubble.Bubble = (*fake)(nil)

// colours gives every fake a distinct pixel, so a frame names its bubble.
var colours int

func newFake(name string, kind bubble.Kind) *fake {
	colours++
	return &fake{name: name, kind: kind, col: color.RGBA{uint8(colours), 0, 0, 255},
		set: fakeSettings{Speed: 1}}
}

func (f *fake) Name() string          { return f.name }
func (f *fake) Kind() bubble.Kind     { return f.kind }
func (f *fake) Frame() [][]color.RGBA { return [][]color.RGBA{{f.col}} }
func (f *fake) Settings() any         { return f.set }
func (f *fake) Update(msg tea.Msg) tea.Cmd {
	f.msgs = append(f.msgs, msg)
	if _, ok := msg.(bubble.Resize); ok {
		return f.onResize
	}
	return nil
}
func (f *fake) Configure(raw json.RawMessage) error {
	out, err := bubble.Patch(f.set, raw)
	if err != nil {
		return err
	}
	if out.Speed < 0 {
		return fmt.Errorf("speed must be >= 0, got %v", out.Speed)
	}
	f.set = out
	return nil
}

// msgsOf returns every message of type T that f recorded, in order.
func msgsOf[T tea.Msg](f *fake) []T {
	var out []T
	for _, m := range f.msgs {
		if v, ok := m.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// fakePanel records what the controller sent it.
type fakePanel struct {
	frames [][][]color.RGBA
	bright []byte
	status proto.StatusMsg
	closed bool
}

var _ Port = (*fakePanel)(nil)

func (p *fakePanel) Send(f [][]color.RGBA)   { p.frames = append(p.frames, f) }
func (p *fakePanel) SetBrightness(b byte)    { p.bright = append(p.bright, b) }
func (p *fakePanel) Status() proto.StatusMsg { return p.status }
func (p *fakePanel) Close()                  { p.closed = true }

// one registers f as its own template and instance: a single-group test
// then sees every message on f, as before render groups.
func one(f *fake, weight int) Entry {
	return Entry{New: func() bubble.Bubble { return f }, Weight: weight}
}

// factory registers a bubble made fresh per group; made lists the instances
// in creation order, the template first.
func factory(name string, kind bubble.Kind, weight int) (Entry, func() []*fake) {
	var made []*fake
	e := Entry{New: func() bubble.Bubble { f := newFake(name, kind); made = append(made, f); return f }, Weight: weight}
	return e, func() []*fake { return made }
}

// virtual is the usual panel: 4x2 at 30 fps, named v.
var virtual = PanelSpec{Name: "v", Kind: KindVirtual, W: 4, H: 2, FPS: 30}

func opts() Options { return Options{FPS: 30} }

// newBare is New without a panel: no group, nothing renders.
func newBare(t *testing.T, o Options, entries ...Entry) *Controller {
	t.Helper()
	c, err := New(entries, o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// newTest is the usual setup: one virtual 4x2 panel, so one group exists.
func newTest(t *testing.T, o Options, entries ...Entry) *Controller {
	t.Helper()
	c := newBare(t, o, entries...)
	mustPut(t, c, virtual)
	return c
}

func mustPut(t *testing.T, c *Controller, spec PanelSpec) PanelState {
	t.Helper()
	ps, err := c.PutPanel(spec)
	if err != nil {
		t.Fatalf("PutPanel(%+v): %v", spec, err)
	}
	return ps
}

// serialOpts dials p for every serial panel, reporting a 4x2 panel.
func serialOpts(p *fakePanel) Options {
	o := opts()
	o.Dial = func(string, int, byte) (Port, proto.InfoMsg, error) { return p, proto.InfoMsg{W: 4, H: 2}, nil }
	return o
}

var serialSpec = PanelSpec{Name: "p", Kind: KindSerial, Address: "/dev/x", Baud: 1, FPS: 30, Brightness: 64, IdleBrightness: 64}

// newSerial is newTest with a serial panel on p instead of the virtual one,
// connected.
func newSerial(t *testing.T, p *fakePanel, entries ...Entry) *Controller {
	t.Helper()
	c := newBare(t, serialOpts(p), entries...)
	mustPut(t, c, serialSpec)
	run(c, c.drain())
	return c
}

// run executes cmd like the runtime would and feeds its messages back.
func run(c *Controller, cmd tea.Cmd) {
	for _, m := range flatten(cmd) {
		c.Update(m)
	}
}

// tickAt runs one controller frame at time tm with an RMS level of db and
// then one tick of every group.
func tickAt(c *Controller, tm time.Time, db float64) {
	c.sigHook = func() dsp.Signal { return dsp.Signal{DB: db} }
	c.Update(frameMsg(tm))
	for key, g := range c.groups {
		c.Update(groupTick{key: key, gen: g.gen, at: tm})
	}
}

// patch applies a settings patch and fails the test if it is rejected.
func patch(t *testing.T, c *Controller, raw string) {
	t.Helper()
	if err := c.Patch(json.RawMessage(raw)); err != nil {
		t.Fatalf("Patch(%s): %v", raw, err)
	}
}

func TestIdleAtStart(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	c := newTest(t, opts(), one(music, 1), one(clock, 1))
	tickAt(c, t0, -100)
	if s := c.State(); s.Active != "clock" || s.Kind != bubble.Idle || s.Playing {
		t.Fatalf("state = %+v, want clock/idle and not playing", s)
	}
	if n := len(msgsOf[bubble.Activate](clock)); n != 1 {
		t.Errorf("clock activations = %d, want 1", n)
	}
	if n := len(msgsOf[bubble.Tick](music)); n != 0 {
		t.Errorf("music got %d ticks, want 0", n)
	}
}

func TestMusicAtThreshold(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	c := newTest(t, opts(), one(music, 1), one(clock, 1))
	tickAt(c, t0, -50) // exactly music_db
	if s := c.State(); s.Active != "bars" || s.Kind != bubble.Music || !s.Playing || s.DB != -50 {
		t.Fatalf("state = %+v, want bars/music, playing, db -50", s)
	}
}

func TestQuietKeepsMusicUntilSilenceAfter(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	c := newTest(t, opts(), one(music, 1), one(clock, 1))
	patch(t, c, `{"silence_after":5}`) // not the default, which is free to change
	tickAt(c, t0, -40)
	for d := time.Second; d <= 5*time.Second; d += time.Second { // quiet starts at t0+1s
		tickAt(c, at(d), -100)
		if got := c.State().Active; got != "bars" {
			t.Fatalf("after %s of quiet: active = %q, want bars", d-time.Second, got)
		}
	}
	tickAt(c, at(6*time.Second), -100) // 5 s of quiet
	if got := c.State().Active; got != "clock" {
		t.Fatalf("after 5s of quiet: active = %q, want clock", got)
	}
}

func TestLevelBetweenThresholdsResetsTheTimer(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	c := newTest(t, opts(), one(music, 1), one(clock, 1))
	patch(t, c, `{"silence_after":5}`) // not the default, which is free to change
	tickAt(c, t0, -40)
	tickAt(c, at(time.Second), -100) // quiet starts
	tickAt(c, at(2*time.Second), -55)
	tickAt(c, at(6*time.Second), -100) // quiet starts again here
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q, want bars: the -55 dB tick reset the timer", got)
	}
	tickAt(c, at(11*time.Second), -100)
	if got := c.State().Active; got != "clock" {
		t.Fatalf("active = %q, want clock", got)
	}
}

func TestLoopDeadlineActivatesAnother(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	patch(t, c, `{"music":{"loop":1}}`)
	tickAt(c, t0, -40)
	first := c.State().Active
	tickAt(c, at(1500*time.Millisecond), -40)
	second := c.State().Active
	if second == first {
		t.Fatalf("active = %q after the loop deadline, want the other bubble", second)
	}
	for _, f := range []*fake{a, b} {
		if n := len(msgsOf[bubble.Activate](f)); n != 1 {
			t.Errorf("%s activations = %d, want 1", f.name, n)
		}
	}
}

func TestLoopZeroNeverRepicks(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	patch(t, c, `{"music":{"loop":0}}`)
	tickAt(c, t0, -40)
	first := c.State().Active
	for i := 1; i < 100; i++ {
		tickAt(c, at(time.Duration(i)*time.Second), -40)
	}
	if got := c.State().Active; got != first {
		t.Fatalf("active = %q, want %q: loop 0 stays", got, first)
	}
	if n := len(msgsOf[bubble.Activate](a)) + len(msgsOf[bubble.Activate](b)); n != 1 {
		t.Errorf("activations = %d, want 1", n)
	}
}

func TestWeightZeroNeverPicked(t *testing.T) {
	off, on := newFake("off", bubble.Music), newFake("on", bubble.Music)
	c := newTest(t, opts(), one(off, 0), one(on, 1))
	patch(t, c, `{"music":{"loop":1}}`)
	for i := range 20 {
		tickAt(c, at(time.Duration(i)*time.Second), -40)
		if got := c.State().Active; got != "on" {
			t.Fatalf("active = %q, want on", got)
		}
	}
	if n := len(msgsOf[bubble.Activate](off)); n != 0 {
		t.Errorf("disabled bubble activated %d times, want 0", n)
	}
}

func TestWeightedPicksFollowTheWeights(t *testing.T) {
	heavy, light := newFake("heavy", bubble.Music), newFake("light", bubble.Music)
	clock := newFake("clock", bubble.Idle)
	c := newTest(t, opts(), one(heavy, 3), one(light, 1), one(clock, 1))
	patch(t, c, `{"silence_after":0}`)
	// Alternating loud and quiet ticks make the clock the shown bubble before
	// every music pick, so no candidate is excluded and the draw is a plain
	// weighted one.
	const picks = 4000
	for i := range picks {
		tickAt(c, at(time.Duration(2*i)*time.Millisecond), -40)
		tickAt(c, at(time.Duration(2*i+1)*time.Millisecond), -100)
	}
	got := len(msgsOf[bubble.Activate](light))
	if share := float64(got) / picks; share < 0.20 || share > 0.30 {
		t.Fatalf("light share = %.3f (%d of %d), want 0.20..0.30", share, got, picks)
	}
	if n := len(msgsOf[bubble.Activate](heavy)) + got; n != picks {
		t.Errorf("music activations = %d, want %d", n, picks)
	}
}

func TestSequenceWalksRegistryOrder(t *testing.T) {
	a, b, d := newFake("a", bubble.Music), newFake("b", bubble.Music), newFake("d", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1), one(d, 1))
	patch(t, c, `{"music":{"loop":1,"order":"sequence"}}`)
	var got []string
	for i := range 4 {
		tickAt(c, at(time.Duration(i)*time.Second), -40)
		got = append(got, c.State().Active)
	}
	want := []string{"a", "b", "d", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", got, want)
		}
	}
}

func TestBlankFrameWhenNothingIsEnabled(t *testing.T) {
	music := newFake("bars", bubble.Music)
	p := &fakePanel{}
	c := newSerial(t, p, one(music, 0))
	tickAt(c, t0, -40)
	if s := c.State(); s.Active != "" || s.Kind != "" {
		t.Fatalf("state = %+v, want blank", s)
	}
	if len(p.frames) != 1 {
		t.Fatalf("panel got %d frames, want 1", len(p.frames))
	}
	f := p.frames[0]
	if len(f) != 2 || len(f[0]) != 4 {
		t.Fatalf("frame is %dx%d, want 4x2", len(f[0]), len(f))
	}
	for _, row := range f {
		for _, px := range row {
			if px != (color.RGBA{}) {
				t.Fatalf("blank frame has a lit pixel %v", px)
			}
		}
	}
}

func TestShowBeatsMusicAndLosesToAnEvent(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	alert := newFake("alert", bubble.EventKind)
	o := opts()
	o.Show = "clock"
	c := newTest(t, o, one(music, 1), one(clock, 1), one(alert, 1))
	tickAt(c, t0, -40)
	if s := c.State(); s.Active != "clock" || !s.Playing || s.Settings.Show != "clock" {
		t.Fatalf("state = %+v, want the pinned clock while playing", s)
	}
	if _, err := c.AddEvent(bubble.Event{Text: "boom", Level: "info", Expires: at(time.Minute)}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	tickAt(c, at(time.Second), -40)
	if got := c.State().Active; got != "alert" {
		t.Fatalf("active = %q, want alert: an event beats the pin", got)
	}
	if err := c.RemoveEvent(c.State().Events[0].ID); err != nil {
		t.Fatalf("RemoveEvent: %v", err)
	}
	tickAt(c, at(2*time.Second), -40)
	if got := c.State().Active; got != "clock" {
		t.Fatalf("active = %q, want the pin back", got)
	}
	if n := len(msgsOf[bubble.Activate](clock)); n != 2 {
		t.Errorf("clock activations = %d, want 2 (once per time the pin took effect)", n)
	}
}

func TestEventExpiresAtExpires(t *testing.T) {
	clock, alert := newFake("clock", bubble.Idle), newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(clock, 1), one(alert, 1))
	if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(2 * time.Second)}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	tickAt(c, at(time.Second), -100)
	if got := c.State().Active; got != "alert" {
		t.Fatalf("active = %q, want alert", got)
	}
	tickAt(c, at(2*time.Second), -100)
	if s := c.State(); s.Active != "clock" || len(s.Events) != 0 {
		t.Fatalf("state = %+v, want the event gone at Expires", s)
	}
}

func TestCriticalOutranksWarning(t *testing.T) {
	alert := newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(alert, 1))
	for _, e := range []bubble.Event{
		{Text: "careful", Level: "warning", Expires: at(time.Minute)},
		{Text: "on fire", Level: "critical", Expires: at(time.Minute)},
		{Text: "hello", Level: "info", Expires: at(time.Minute)},
	} {
		if _, err := c.AddEvent(e); err != nil {
			t.Fatalf("AddEvent: %v", err)
		}
	}
	tickAt(c, t0, -100)
	got := msgsOf[bubble.Event](alert)
	if len(got) != 1 || got[0].Text != "on fire" || got[0].Level != "critical" {
		t.Fatalf("alert events = %+v, want one critical \"on fire\"", got)
	}
}

func TestSameIDReplaces(t *testing.T) {
	alert := newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(alert, 1))
	id, err := c.AddEvent(bubble.Event{ID: "x", Text: "a", Level: "info", Expires: at(time.Minute)})
	if err != nil || id != "x" {
		t.Fatalf("AddEvent = %q, %v", id, err)
	}
	if _, err := c.AddEvent(bubble.Event{ID: "x", Text: "b", Level: "info", Expires: at(time.Minute)}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if s := c.State(); len(s.Events) != 1 || s.Events[0].Text != "b" {
		t.Fatalf("events = %+v, want one event \"b\"", s.Events)
	}
	if err := c.RemoveEvent("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveEvent(nope) = %v, want ErrNotFound", err)
	}
}

func TestTopLevelTextsJoinAndRefreshOnExpiry(t *testing.T) {
	alert := newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(alert, 1))
	if _, err := c.AddEvent(bubble.Event{Text: "a", Level: "info", Expires: at(2 * time.Second)}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if _, err := c.AddEvent(bubble.Event{Text: "b", Level: "info", Expires: at(time.Minute)}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	tickAt(c, t0, -100)
	tickAt(c, at(time.Second), -100) // unchanged: no second Event
	if got := msgsOf[bubble.Event](alert); len(got) != 1 || got[0].Text != "a +++ b" {
		t.Fatalf("alert events = %+v, want one \"a +++ b\"", got)
	}
	tickAt(c, at(2*time.Second), -100)
	got := msgsOf[bubble.Event](alert)
	if len(got) != 2 || got[1].Text != "b" {
		t.Fatalf("alert events = %+v, want a second one with just \"b\"", got)
	}
}

func TestQueueFullPast32(t *testing.T) {
	alert := newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(alert, 1))
	for i := range maxEvents {
		if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(time.Minute)}); err != nil {
			t.Fatalf("AddEvent %d: %v", i, err)
		}
	}
	if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(time.Minute)}); !errors.Is(err, ErrFull) {
		t.Fatalf("AddEvent 33 = %v, want ErrFull", err)
	}
	id := c.State().Events[0].ID // replacing is not growth
	if _, err := c.AddEvent(bubble.Event{ID: id, Text: "y", Level: "info", Expires: at(time.Minute)}); err != nil {
		t.Errorf("replacing at the limit: %v", err)
	}
	if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "nope", Expires: at(time.Minute)}); err == nil {
		t.Error("AddEvent with an unknown level: want an error")
	}
}

func TestDtIsZeroThenClamped(t *testing.T) {
	music := newFake("bars", bubble.Music)
	c := newTest(t, opts(), one(music, 1))
	tickAt(c, t0, -40)
	tickAt(c, at(10*time.Second), -40)
	got := msgsOf[bubble.Tick](music)
	if len(got) != 2 || got[0].Dt != 0 || got[1].Dt != 250*time.Millisecond {
		t.Fatalf("ticks = %+v, want dt 0 then 250ms", got)
	}
	if got[1].Now != at(10*time.Second) {
		t.Errorf("Tick.Now = %v, want the tick's time", got[1].Now)
	}
}

func TestWindowSizeFansOut(t *testing.T) {
	a := newFake("a", bubble.Music)
	c := newBare(t, Options{FPS: 30, Terminal: true}, one(a, 1))
	c.Update(tea.WindowSizeMsg{Width: 8, Height: 3})
	if got := msgsOf[bubble.Resize](a); len(got) != 1 || got[0] != (bubble.Resize{W: 8, H: 6}) {
		t.Fatalf("resizes = %+v, want one {8 6}", got)
	}
	c.Update(tea.WindowSizeMsg{Width: 8, Height: 3}) // same size: same group
	if got := msgsOf[bubble.Resize](a); len(got) != 1 {
		t.Fatalf("resizes = %+v after the same size again, want still one", got)
	}
	ps := c.Panels()
	if len(ps) != 1 || ps[0].Name != "terminal" || ps[0].W != 8 || ps[0].H != 6 || ps[0].FPS != 30 {
		t.Fatalf("panels = %+v, want the terminal at 8x6", ps)
	}
	c.Update(tea.WindowSizeMsg{Width: 4, Height: 1}) // moves to another group
	if got := msgsOf[bubble.Resize](a); len(got) != 2 || got[1] != (bubble.Resize{W: 4, H: 2}) {
		t.Fatalf("resizes = %+v, want a second {4 2}", got)
	}
	if len(c.groups) != 1 {
		t.Fatalf("groups = %d, want the old one gone", len(c.groups))
	}
}

func TestWindowSizeIgnoredWithoutATerminal(t *testing.T) {
	a := newFake("a", bubble.Music)
	c := newTest(t, opts(), one(a, 1))
	c.Update(tea.WindowSizeMsg{Width: 8, Height: 3})
	if got := msgsOf[bubble.Resize](a); len(got) != 1 || got[0] != (bubble.Resize{W: 4, H: 2}) {
		t.Fatalf("resizes = %+v, want only the virtual panel's {4 2}", got)
	}
}

// flatten runs cmd and, recursively for a tea.BatchMsg, every cmd it yields,
// collecting every message produced.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flatten(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// markerMsg is what a bubble's first Resize answers with, to prove Init runs it.
type markerMsg struct{}

// TestInitRunsResizeCmds is the regression for a panel with a fixed size: the
// virtual panel's group build queues the only Resize a bubble ever gets, so a
// cmd it answers with (e.g. a bubble.Bubble that starts polling on its first
// size) must run, and only Init has a *tea.Program to run it on.
func TestInitRunsResizeCmds(t *testing.T) {
	a := newFake("a", bubble.Music)
	a.onResize = func() tea.Msg { return markerMsg{} }
	c := newTest(t, opts(), one(a, 1)) // opts(): a fixed panel size, like every deployed config
	got := flatten(c.Init())
	var found bool
	for _, m := range got {
		if _, ok := m.(markerMsg); ok {
			found = true
		}
	}
	if !found {
		t.Fatalf("Init()'s cmds = %+v, want the marker the first Resize answered with", got)
	}
}

func TestIdleBrightnessSentOnce(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	p := &fakePanel{}
	c := newSerial(t, p, one(music, 1), one(clock, 1))
	if _, err := c.PatchPanel("p", json.RawMessage(`{"idle_brightness":16}`)); err != nil {
		t.Fatal(err)
	}
	patch(t, c, `{"silence_after":0}`)
	tickAt(c, t0, -100)
	tickAt(c, at(time.Second), -100)
	if want := []byte{16}; string(p.bright) != string(want) {
		t.Fatalf("brightness = %v, want %v", p.bright, want)
	}
	tickAt(c, at(2*time.Second), -40)
	tickAt(c, at(3*time.Second), -40)
	if want := []byte{16, 64}; string(p.bright) != string(want) {
		t.Fatalf("brightness = %v, want %v", p.bright, want)
	}
}

func TestCallRunsInsideUpdate(t *testing.T) {
	alert := newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(alert, 1))
	var id string
	c.Update(Call(func(c *Controller) {
		id, _ = c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(time.Minute)})
	}))
	if s := c.State(); len(s.Events) != 1 || s.Events[0].ID != id || id == "" {
		t.Fatalf("events = %+v after a Call, want one with id %q", s.Events, id)
	}
}

func TestNextPicksAnother(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	patch(t, c, `{"music":{"loop":0}}`)
	tickAt(c, t0, -40)
	first := c.State().Active
	c.Next()
	tickAt(c, at(time.Second), -40)
	if got := c.State().Active; got == first {
		t.Fatalf("active = %q after Next, want the other bubble", got)
	}
}

func TestDisabledActiveBubbleIsReplaced(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	patch(t, c, `{"music":{"loop":0}}`)
	tickAt(c, t0, -40)
	off := c.State().Active
	zero := 0
	if err := c.PatchBubble(off, &zero, nil); err != nil {
		t.Fatalf("PatchBubble: %v", err)
	}
	tickAt(c, at(time.Second), -40)
	if got := c.State().Active; got == off {
		t.Fatalf("active = %q, want the other bubble after it was disabled", got)
	}
}

func TestKeys(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	alert := newFake("alert", bubble.EventKind)
	o := opts()
	o.Analyzer = dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	c := newTest(t, o, one(music, 1), one(clock, 1), one(alert, 1))
	key := func(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }
	press := func(s string) tea.Cmd {
		_, cmd := c.Update(key(s))
		return cmd
	}

	if cmd := press("q"); cmd == nil {
		t.Error("q returned no cmd, want Quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}

	tickAt(c, t0, -40) // bars is shown
	press("m")         // pin the next registry bubble, skipping the alert
	tickAt(c, at(time.Second), -40)
	if s := c.State(); s.Active != "clock" || s.Settings.Show != "clock" {
		t.Fatalf("after m: state = %+v, want the clock pinned", s)
	}
	press("a") // clears the pin
	tickAt(c, at(2*time.Second), -40)
	if s := c.State(); s.Settings.Show != "" || s.Active != "bars" {
		t.Fatalf("after a: state = %+v, want no pin and music back", s)
	}
	press("a") // toggles the music loop off
	if got := c.set.Music.Seconds; got != 0 {
		t.Fatalf("music loop = %v, want 0", got)
	}
	press("a") // and back on
	if got := c.set.Music.Seconds; got != 60 {
		t.Fatalf("music loop = %v, want 60 again", got)
	}

	press("+")
	press("=")
	if got := o.Analyzer.Bands(); got != 24 {
		t.Errorf("bands = %d, want 24", got)
	}
	for range 4 {
		press("-")
	}
	if got := o.Analyzer.Bands(); got != 8 {
		t.Errorf("bands = %d, want 8 (clamped)", got)
	}

	press("c") // an unclaimed key goes to the shown bubble only
	if n := len(msgsOf[tea.KeyPressMsg](music)); n != 1 {
		t.Errorf("bars got %d keys, want 1", n)
	}
	if n := len(msgsOf[tea.KeyPressMsg](clock)); n != 0 {
		t.Errorf("clock got %d keys, want 0", n)
	}
}

func TestUnhandledMessagesGoToEveryBubble(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Idle)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	c.Update("hello")
	for _, f := range []*fake{a, b} {
		if n := len(msgsOf[string](f)); n != 1 {
			t.Errorf("%s got %d strings, want 1", f.name, n)
		}
	}
}

func TestHeadlessSkipsRendering(t *testing.T) {
	a := newFake("a", bubble.Music)
	c := newTest(t, opts(), one(a, 1))
	tickAt(c, t0, -40)
	if c.view != "" {
		t.Errorf("view = %q, want nothing rendered without a terminal", c.view)
	}
	c = newBare(t, Options{FPS: 30, Terminal: true}, one(newFake("a", bubble.Music), 1))
	c.Update(tea.WindowSizeMsg{Width: 4, Height: 1})
	tickAt(c, t0, -40)
	if c.view == "" {
		t.Error("view is empty, want the rendered frame")
	}
	if v := c.View(); !v.AltScreen {
		t.Error("View is not in the alt screen")
	}
}

func TestCaptureDoneQuitsWithTheCaptureError(t *testing.T) {
	want := errors.New("capture blew up")
	o := opts()
	o.Wait = func() error { return want }
	c := newTest(t, o, one(newFake("a", bubble.Music), 1))
	_, cmd := c.Update(captureDone{})
	if cmd == nil {
		t.Fatal("captureDone returned no cmd, want Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("captureDone did not quit")
	}
	if !errors.Is(c.Err(), want) {
		t.Errorf("Err() = %v, want %v", c.Err(), want)
	}
}

func TestNewRejectsDuplicatesAndUnknownShow(t *testing.T) {
	a := newFake("a", bubble.Music)
	if _, err := New([]Entry{one(a, 1), one(newFake("a", bubble.Idle), 1)}, opts()); err == nil {
		t.Error("New with duplicate names: want an error")
	}
	o := opts()
	o.Show = "nope"
	if _, err := New([]Entry{one(a, 1)}, o); err == nil {
		t.Error("New with an unknown show: want an error")
	}
	o.Show = "alert"
	if _, err := New([]Entry{one(a, 1), one(newFake("alert", bubble.EventKind), 1)}, o); err == nil {
		t.Error("New pinning an event bubble: want an error")
	}
}

func TestPatchValidates(t *testing.T) {
	a, alert := newFake("a", bubble.Music), newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), one(a, 1), one(alert, 1))
	for _, raw := range []string{
		`{"music":{"order":"nope"}}`,
		`{"idle":{"loop":-1}}`,
		`{"silence_after":-1}`,
		`{"silence_db":-40}`, // above music_db
		`{"show":"nope"}`,
		`{"show":"alert"}`,
		`{"nope":1}`,
	} {
		if err := c.Patch(json.RawMessage(raw)); err == nil {
			t.Errorf("Patch(%s): want an error", raw)
		}
	}
	if c.set.Music.Order != "random" || c.set.SilenceDB != -60 {
		t.Errorf("a rejected patch changed the settings: %+v", c.set)
	}
	patch(t, c, `{"music_db":-10,"music":{"loop":5}}`)
	if c.set.MusicDB != -10 || c.set.Music.Seconds != 5 || c.set.Music.Order != "random" {
		t.Errorf("settings = %+v, want a merged patch", c.set)
	}
}

func TestBubblesAndPatchBubble(t *testing.T) {
	a := newFake("a", bubble.Music)
	c := newTest(t, opts(), one(a, 2))
	if got := c.Bubbles(); len(got) != 1 || got[0].Name != "a" || got[0].Kind != bubble.Music || got[0].Weight != 2 {
		t.Fatalf("bubbles = %+v", got)
	}
	if err := c.PatchBubble("nope", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("PatchBubble(nope) = %v, want ErrNotFound", err)
	}
	neg := -1
	if err := c.PatchBubble("a", &neg, nil); err == nil {
		t.Error("a negative weight: want an error")
	}
	if err := c.PatchBubble("a", nil, json.RawMessage(`{"speed":-5}`)); err == nil {
		t.Error("bad settings: want an error")
	}
	three := 3
	if err := c.PatchBubble("a", &three, json.RawMessage(`{"speed":9}`)); err != nil {
		t.Fatalf("PatchBubble: %v", err)
	}
	if got := c.Bubbles()[0]; got.Weight != 3 || !reflect.DeepEqual(got.Settings, fakeSettings{Speed: 9}) {
		t.Errorf("bubble = %+v, want weight 3 and speed 9", got)
	}
}

func TestStateJSONPanelIsSnakeCase(t *testing.T) {
	p := &fakePanel{status: proto.StatusMsg{CRCErr: 3}}
	c := newSerial(t, p, one(newFake("a", bubble.Music), 1))
	buf, err := json.Marshal(c.State())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"panels":[`, `"crc_err":3`, `"idle_brightness":64`, `"connected":true`, `"kind":"serial"`} {
		if !strings.Contains(string(buf), key) {
			t.Errorf("state JSON lacks %s: %s", key, buf)
		}
	}
	if strings.Contains(string(buf), `"panel":`) {
		t.Errorf("state JSON still has the old panel field: %s", buf)
	}
}

func TestStatePanelStatus(t *testing.T) {
	p := &fakePanel{status: proto.StatusMsg{FramesOK: 7, FPS: 20}}
	c := newSerial(t, p, one(newFake("a", bubble.Music), 1))
	ps := c.State().Panels
	if len(ps) != 1 || ps[0].Status == nil || ps[0].Status.FramesOK != 7 || !ps[0].Connected || ps[0].W != 4 {
		t.Fatalf("panels = %+v, want the port's status, connected, 4 wide", ps)
	}
	c = newTest(t, opts(), one(newFake("a", bubble.Music), 1))
	ps = c.State().Panels
	if len(ps) != 1 || ps[0].Status != nil || !ps[0].Connected || ps[0].Kind != KindVirtual {
		t.Fatalf("panels = %+v, want one virtual without a status", ps)
	}
}

func TestUnrelatedPatchDoesNotPostponeTheLoop(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1)) // the default 60 s music loop
	tickAt(c, t0, -40)
	first := c.State().Active
	for i := 1; i <= 6; i++ { // a web UI patching a threshold every 10 s
		patch(t, c, fmt.Sprintf(`{"music_db":%d}`, -41-i))
		tickAt(c, at(time.Duration(i)*10*time.Second), -40)
	}
	if got := c.State().Active; got == first {
		t.Fatalf("active = %q at 60 s, want the other bubble: a threshold patch must not postpone the loop", got)
	}
}

func TestPatchBubbleDoesNotPostponeTheLoop(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	tickAt(c, t0, -40)
	first := c.State().Active
	for i := 1; i <= 6; i++ {
		if err := c.PatchBubble("a", nil, json.RawMessage(fmt.Sprintf(`{"speed":%d}`, i))); err != nil {
			t.Fatalf("PatchBubble: %v", err)
		}
		tickAt(c, at(time.Duration(i)*10*time.Second), -40)
	}
	if got := c.State().Active; got == first {
		t.Fatalf("active = %q at 60 s, want the other bubble: a bubble patch must not postpone the loop", got)
	}
}

func TestPatchingTheLoopRestartsTheCountdown(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	tickAt(c, t0, -40)
	first := c.State().Active
	patch(t, c, `{"music":{"loop":30}}`)
	tickAt(c, at(31*time.Second), -40) // the countdown restarts from this tick
	if got := c.State().Active; got != first {
		t.Fatalf("active = %q, want %q: a patched loop counts from the next tick", got, first)
	}
	tickAt(c, at(61*time.Second), -40)
	if got := c.State().Active; got == first {
		t.Fatalf("active = %q 30 s after the restart, want the other bubble", got)
	}
}

// TestNextWhilePinnedDoesNotMisfireAfterUnpin covers a repick requested while
// a pin is active: it must be discarded, not queued to fire once the pin
// later clears (which could be minutes after the call).
func TestNextWhilePinnedDoesNotMisfireAfterUnpin(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), one(a, 1), one(b, 1))
	patch(t, c, `{"music":{"loop":0},"show":"a"}`)
	tickAt(c, t0, -40) // pins a
	if got := c.State().Active; got != "a" {
		t.Fatalf("active = %q, want a (pinned)", got)
	}
	c.Next()                        // sent while pinned: must not linger past the pin
	tickAt(c, at(time.Second), -40) // still pinned: this tick must consume/discard the stale Next()
	if got := c.State().Active; got != "a" {
		t.Fatalf("active = %q while still pinned, want a", got)
	}
	patch(t, c, `{"show":""}`)        // unpin
	tickAt(c, at(2*time.Second), -40) // the tick that re-enters the loop
	if got := c.State().Active; got != "a" {
		t.Fatalf("active = %q right after unpinning, want a to stay active", got)
	}
	tickAt(c, at(3*time.Second), -40) // the following tick
	if got := c.State().Active; got != "a" {
		t.Fatalf("active = %q, want a still active", got)
	}
	if n := len(msgsOf[bubble.Activate](a)); n != 1 {
		t.Errorf("a activations = %d, want 1 (only the initial pin)", n)
	}
	if n := len(msgsOf[bubble.Activate](b)); n != 0 {
		t.Errorf("b activations = %d, want 0: a stale Next() must not fire once the pin clears", n)
	}
}

func TestPatchedPinAndWeightTakeEffectOnTheNextTick(t *testing.T) {
	bars, fire := newFake("bars", bubble.Music), newFake("fire", bubble.Music)
	clock := newFake("clock", bubble.Idle)
	c := newTest(t, opts(), one(bars, 1), one(fire, 0), one(clock, 1))
	tickAt(c, t0, -40)
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q, want bars", got)
	}
	patch(t, c, `{"show":"clock"}`)
	tickAt(c, at(time.Second), -40)
	if got := c.State().Active; got != "clock" {
		t.Fatalf("active = %q, want the pin on the tick after the patch", got)
	}
	patch(t, c, `{"show":""}`)
	tickAt(c, at(2*time.Second), -40)
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q, want bars back on the tick after the pin was cleared", got)
	}
	w1, zero := 1, 0
	if err := c.PatchBubble("fire", &w1, nil); err != nil {
		t.Fatalf("PatchBubble: %v", err)
	}
	if err := c.PatchBubble("bars", &zero, nil); err != nil {
		t.Fatalf("PatchBubble: %v", err)
	}
	tickAt(c, at(3*time.Second), -40)
	if got := c.State().Active; got != "fire" {
		t.Fatalf("active = %q, want fire on the tick after bars was disabled", got)
	}
}

// interludeSetup is bars playing, with a clock, a track bubble and an alert.
func interludeSetup(t *testing.T) (*Controller, *fake) {
	t.Helper()
	np := newFake("nowplaying", bubble.Track)
	c := newTest(t, opts(), one(newFake("bars", bubble.Music), 1), one(newFake("clock", bubble.Idle), 1),
		one(np, 1), one(newFake("alert", bubble.EventKind), 1))
	tickAt(c, at(0), -20)
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q, want bars", got)
	}
	return c, np
}

func TestInterludeShowsThenReturns(t *testing.T) {
	c, np := interludeSetup(t)
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20) // its time starts with this tick
	if s := c.State(); s.Active != "nowplaying" || s.Kind != bubble.Track {
		t.Fatalf("active = %q kind = %q, want nowplaying/track", s.Active, s.Kind)
	}
	if n := len(msgsOf[bubble.Activate](np)); n != 1 {
		t.Errorf("Activate sent %d times, want 1", n)
	}
	tickAt(c, at(10*time.Second), -20)
	if got := c.State().Active; got != "nowplaying" {
		t.Fatalf("active = %q at 10 s, 9 s into the interlude, want nowplaying", got)
	}
	tickAt(c, at(11*time.Second), -20)
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q at 11 s, the interlude's end, want bars", got)
	}
}

func TestInterludeBeatsIdle(t *testing.T) {
	np := newFake("nowplaying", bubble.Track)
	c := newTest(t, opts(), one(newFake("clock", bubble.Idle), 1), one(np, 1))
	tickAt(c, at(0), -90)
	if err := c.Show("nowplaying", time.Second); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(time.Second), -90)
	if got := c.State().Active; got != "nowplaying" {
		t.Fatalf("active = %q, want nowplaying", got)
	}
}

func TestInterludeLosesToEventAndReturns(t *testing.T) {
	c, _ := interludeSetup(t)
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20)
	if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(2*time.Second), -20)
	if got := c.State().Active; got != "alert" {
		t.Fatalf("active = %q during the event, want alert", got)
	}
	tickAt(c, at(4*time.Second), -20)
	if got := c.State().Active; got != "nowplaying" {
		t.Fatalf("active = %q after the event, want nowplaying", got)
	}
}

func TestShowErrors(t *testing.T) {
	c, _ := interludeSetup(t)
	if err := c.Show("nope", time.Second); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown bubble: %v, want ErrNotFound", err)
	}
	if err := c.Show("alert", time.Second); err == nil {
		t.Error("an event bubble was accepted")
	}
	if err := c.Show("nowplaying", 0); err == nil {
		t.Error("a duration of 0 was accepted")
	}
	patch(t, c, `{"show":"bars"}`)
	if err := c.Show("nowplaying", time.Second); err == nil || !strings.Contains(err.Error(), "bars") {
		t.Errorf("under a pin: %v, want an error naming bars", err)
	}
}

func TestPinEndsInterlude(t *testing.T) {
	c, _ := interludeSetup(t)
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20)
	patch(t, c, `{"show":"clock"}`)
	tickAt(c, at(2*time.Second), -20)
	patch(t, c, `{"show":""}`)
	tickAt(c, at(3*time.Second), -20)
	if got := c.State().Active; got == "nowplaying" {
		t.Error("the interlude came back after the pin")
	}
}

func TestInterludeFromDisabledBubbleIgnored(t *testing.T) {
	c, _ := interludeSetup(t)
	zero := 0
	if err := c.PatchBubble("nowplaying", &zero, nil); err != nil {
		t.Fatal(err)
	}
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20)
	if got := c.State().Active; got != "bars" {
		t.Errorf("active = %q, want bars: weight 0 switches the automatic card off", got)
	}
	if err := c.Show("nowplaying", time.Second); err != nil {
		t.Errorf("Show of a weight 0 bubble: %v, want nil like a pin", err)
	}
}

func TestTrackBubbleNeverLooped(t *testing.T) {
	c := newTest(t, opts(), one(newFake("nowplaying", bubble.Track), 1))
	tickAt(c, at(0), -20)
	tickAt(c, at(time.Second), -90)
	if got := c.State().Active; got != "" {
		t.Errorf("active = %q, want blank: a track bubble is in no loop", got)
	}
}

func TestInterludeBrightness(t *testing.T) {
	p := &fakePanel{}
	c := newSerial(t, p, one(newFake("clock", bubble.Idle), 1), one(newFake("nowplaying", bubble.Track), 1))
	if _, err := c.PatchPanel("p", json.RawMessage(`{"brightness":200,"idle_brightness":10}`)); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(0), -90)
	if err := c.Show("nowplaying", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(time.Second), -90)
	if got := p.bright[len(p.bright)-1]; got != 200 {
		t.Errorf("brightness = %d, want 200 as for music", got)
	}
}

func TestSameKeyJoinsOneGroupDifferentFPSMakesAnother(t *testing.T) {
	e, made := factory("a", bubble.Music, 1)
	c := newTest(t, opts(), e)
	if len(made()) != 2 { // template and the v group's instance
		t.Fatalf("instances = %d, want 2", len(made()))
	}
	mustPut(t, c, PanelSpec{Name: "w", Kind: KindVirtual, W: 4, H: 2, FPS: 30})
	if len(made()) != 2 || len(c.groups) != 1 {
		t.Fatalf("instances = %d, groups = %d after a panel of the same key, want 2 and 1", len(made()), len(c.groups))
	}
	mustPut(t, c, PanelSpec{Name: "x", Kind: KindVirtual, W: 4, H: 2, FPS: 60})
	if len(made()) != 3 || len(c.groups) != 2 {
		t.Fatalf("instances = %d, groups = %d after another fps, want 3 and 2", len(made()), len(c.groups))
	}
	if _, err := c.PatchPanel("x", json.RawMessage(`{"fps":30}`)); err != nil {
		t.Fatal(err)
	}
	if len(c.groups) != 1 {
		t.Fatalf("groups = %d after patching x to 30 fps, want 1", len(c.groups))
	}
	if err := c.DeletePanel("w"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePanel("x"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePanel("v"); err != nil {
		t.Fatal(err)
	}
	if len(c.groups) != 0 {
		t.Fatalf("groups = %d after deleting every panel, want 0", len(c.groups))
	}
	if err := c.DeletePanel("v"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestGroupsActivateWithTheSameSeed(t *testing.T) {
	e, made := factory("a", bubble.Music, 1)
	c := newTest(t, opts(), e)
	mustPut(t, c, PanelSpec{Name: "x", Kind: KindVirtual, W: 8, H: 4, FPS: 60})
	tickAt(c, t0, -40)
	inst := made()[1:]
	if len(inst) != 2 {
		t.Fatalf("instances = %d, want 2", len(inst))
	}
	var first []uint64
	for _, f := range inst {
		acts := msgsOf[bubble.Activate](f)
		if len(acts) != 1 || acts[0].Rand == nil {
			t.Fatalf("%v activations, want one with a source", acts)
		}
		first = append(first, acts[0].Rand.Uint64())
	}
	if first[0] != first[1] {
		t.Fatalf("sources differ: %v", first)
	}
	// a group made while a is shown starts on a with the same seed
	mustPut(t, c, PanelSpec{Name: "y", Kind: KindVirtual, W: 2, H: 2, FPS: 30})
	late := made()[3]
	acts := msgsOf[bubble.Activate](late)
	if len(acts) != 1 || acts[0].Rand.Uint64() != first[0] {
		t.Fatalf("late group's activation = %+v, want one from the same seed", acts)
	}
	if n := len(msgsOf[bubble.Resize](late)); n != 1 {
		t.Errorf("late instance got %d resizes, want 1", n)
	}
}

func TestNewGroupLearnsTheLiveEvent(t *testing.T) {
	e, made := factory("alert", bubble.EventKind, 1)
	c := newTest(t, opts(), e)
	if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	tickAt(c, t0, -90)
	mustPut(t, c, PanelSpec{Name: "y", Kind: KindVirtual, W: 2, H: 2, FPS: 30})
	got := msgsOf[bubble.Event](made()[2])
	if len(got) != 1 || got[0].Text != "x" {
		t.Fatalf("new instance's events = %+v, want the live one", got)
	}
	if n := len(msgsOf[bubble.Event](made()[1])); n != 1 {
		t.Errorf("first instance got %d events, want 1: no empty event on group build", n)
	}
}

func TestGroupTicksAtItsOwnRateAndSeesTheDrop(t *testing.T) {
	e, made := factory("a", bubble.Music, 1)
	c := newTest(t, opts(), e)
	mustPut(t, c, PanelSpec{Name: "slow", Kind: KindVirtual, W: 8, H: 4, FPS: 10})
	fast, slow := c.groups[groupKey{4, 2, 30}], c.groups[groupKey{8, 4, 10}]
	c.sigHook = func() dsp.Signal { return dsp.Signal{DB: -40, Drop: true} }
	c.Update(frameMsg(t0))
	c.sigHook = func() dsp.Signal { return dsp.Signal{DB: -40} }
	c.Update(frameMsg(at(33 * time.Millisecond)))
	c.Update(groupTick{key: fast.key, gen: fast.gen, at: at(33 * time.Millisecond)})
	c.Update(groupTick{key: slow.key, gen: slow.gen, at: at(100 * time.Millisecond)})
	var fastF, slowF *fake
	for _, f := range made()[1:] {
		if len(msgsOf[bubble.Resize](f)) > 0 && msgsOf[bubble.Resize](f)[0].W == 4 {
			fastF = f
		} else {
			slowF = f
		}
	}
	for _, f := range []*fake{fastF, slowF} {
		ticks := msgsOf[bubble.Tick](f)
		if len(ticks) != 1 || !ticks[0].Signal.Drop {
			t.Fatalf("%v ticks, want one carrying the drop that happened between controller ticks", ticks)
		}
	}
	c.Update(groupTick{key: fast.key, gen: fast.gen, at: at(66 * time.Millisecond)})
	if ticks := msgsOf[bubble.Tick](fastF); len(ticks) != 2 || ticks[1].Signal.Drop {
		t.Fatalf("second tick = %+v, want the drop latch cleared", ticks)
	}
	if cmd := c.groupTick(groupKey{9, 9, 9}, 0, t0); cmd != nil {
		t.Error("a tick for an unknown group returned a cmd")
	}
	if cmd := c.groupTick(fast.key, fast.gen+1, t0); cmd != nil {
		t.Error("a tick of an old generation returned a cmd")
	}
}

func TestPatchBubbleReachesEveryInstanceOrNone(t *testing.T) {
	e, made := factory("a", bubble.Music, 1)
	c := newTest(t, opts(), e)
	mustPut(t, c, PanelSpec{Name: "x", Kind: KindVirtual, W: 8, H: 4, FPS: 60})
	if err := c.PatchBubble("a", nil, json.RawMessage(`{"speed":3}`)); err != nil {
		t.Fatal(err)
	}
	for i, f := range made() {
		if f.set.Speed != 3 {
			t.Errorf("instance %d speed = %v, want 3", i, f.set.Speed)
		}
	}
	if err := c.PatchBubble("a", nil, json.RawMessage(`{"speed":-1}`)); err == nil {
		t.Fatal("bad patch accepted")
	}
	for i, f := range made() {
		if f.set.Speed != 3 {
			t.Errorf("instance %d speed = %v after a rejected patch, want 3", i, f.set.Speed)
		}
	}
	// a new group is configured from the template
	mustPut(t, c, PanelSpec{Name: "y", Kind: KindVirtual, W: 2, H: 2, FPS: 30})
	if f := made()[3]; f.set.Speed != 3 {
		t.Errorf("new instance speed = %v, want the template's 3", f.set.Speed)
	}
}

func TestSerialPanelDialsJoinsAndCloses(t *testing.T) {
	p := &fakePanel{}
	a := newFake("a", bubble.Music)
	c := newBare(t, serialOpts(p), one(a, 1))
	ps := mustPut(t, c, serialSpec)
	if ps.Connected || ps.W != 0 {
		t.Fatalf("state before dialing = %+v, want not connected, no size", ps)
	}
	run(c, c.drain()) // the dial cmd, then portUp
	ps = c.Panels()[0]
	if !ps.Connected || ps.W != 4 || ps.H != 2 || len(c.groups) != 1 {
		t.Fatalf("state after portUp = %+v, groups = %d", ps, len(c.groups))
	}
	if err := c.DeletePanel("p"); err != nil {
		t.Fatal(err)
	}
	if !p.closed || len(c.groups) != 0 {
		t.Fatalf("closed = %v, groups = %d after delete, want closed and none", p.closed, len(c.groups))
	}
}

func TestLatePortUpAfterDeleteClosesThePort(t *testing.T) {
	p := &fakePanel{}
	o := opts()
	dials := make(chan struct{}, 1)
	o.Dial = func(string, int, byte) (Port, proto.InfoMsg, error) {
		dials <- struct{}{}
		return p, proto.InfoMsg{W: 4, H: 2}, nil
	}
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	mustPut(t, c, serialSpec)
	cmd := c.drain()
	if err := c.DeletePanel("p"); err != nil {
		t.Fatal(err)
	}
	run(c, cmd) // the dial finishes now
	<-dials
	if !p.closed || len(c.groups) != 0 || len(c.panels) != 0 {
		t.Fatalf("closed = %v, groups = %d, panels = %d, want the late port closed and nothing joined", p.closed, len(c.groups), len(c.panels))
	}
}

func TestDialRetriesUntilQuit(t *testing.T) {
	dialRetry = time.Millisecond
	t.Cleanup(func() { dialRetry = 5 * time.Second })
	o := opts()
	var n int
	o.Dial = func(string, int, byte) (Port, proto.InfoMsg, error) {
		n++
		return nil, proto.InfoMsg{}, errors.New("off")
	}
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	mustPut(t, c, serialSpec)
	cmd := c.drain()
	done := make(chan tea.Msg, 1)
	go func() { done <- flatten(cmd)[0] }()
	time.Sleep(20 * time.Millisecond)
	if err := c.DeletePanel("p"); err != nil {
		t.Fatal(err)
	}
	if msg := <-done; msg != nil {
		t.Fatalf("dial loop ended with %v, want nil after quit", msg)
	}
	if n < 2 {
		t.Fatalf("dialed %d times, want retries", n)
	}
}

func TestPutPanelReplacesAndRedials(t *testing.T) {
	p1, p2 := &fakePanel{}, &fakePanel{}
	o := opts()
	ports := []Port{p1, p2}
	o.Dial = func(string, int, byte) (Port, proto.InfoMsg, error) {
		p := ports[0]
		ports = ports[1:]
		return p, proto.InfoMsg{W: 4, H: 2}, nil
	}
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	mustPut(t, c, serialSpec)
	run(c, c.drain())
	same := serialSpec
	same.FPS = 60
	mustPut(t, c, same) // same address: keeps the port, moves group
	if p1.closed || c.panels[0].port != p1 || len(c.groups) != 1 || c.groups[groupKey{4, 2, 60}] == nil {
		t.Fatalf("a changed fps replaced the port or kept the old group")
	}
	moved := serialSpec
	moved.Address = "/dev/y"
	mustPut(t, c, moved)
	run(c, c.drain())
	if !p1.closed || c.panels[0].port != p2 {
		t.Fatalf("a changed address kept the old port")
	}
}

func TestCheckPanel(t *testing.T) {
	bad := []PanelSpec{
		{Name: "", Kind: KindVirtual, W: 4, H: 2, FPS: 30},
		{Name: "a/b", Kind: KindVirtual, W: 4, H: 2, FPS: 30},
		{Name: "terminal", Kind: KindVirtual, W: 4, H: 2, FPS: 30},
		{Name: "v", Kind: "tty", W: 4, H: 2, FPS: 30},
		{Name: "v", Kind: KindVirtual, W: 4, H: 2, FPS: 0},
		{Name: "v", Kind: KindVirtual, W: 4, H: 2, FPS: 121},
		{Name: "v", Kind: KindVirtual, W: 0, H: 2, FPS: 30},
		{Name: "v", Kind: KindVirtual, W: 4, H: 1025, FPS: 30},
		{Name: "s", Kind: KindSerial, Address: " ", FPS: 30},
		{Name: "s", Kind: KindSerial, Address: "/dev/x", Baud: -1, FPS: 30},
	}
	for _, s := range bad {
		if _, err := checkPanel(s); err == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	s, err := checkPanel(PanelSpec{Name: " s ", Kind: KindSerial, Address: "/dev/x", W: 9, FPS: 30, Brightness: 200})
	if err != nil || s.Name != "s" || s.Baud != 921600 || s.W != 0 || s.Brightness != 200 {
		t.Fatalf("serial = %+v, %v: want trimmed name, default baud, size dropped", s, err)
	}
	v, err := checkPanel(PanelSpec{Name: "v", Kind: KindVirtual, W: 4, H: 2, FPS: 30, Baud: 5, Brightness: 9, Address: "x"})
	if err != nil || v.Baud != 0 || v.Brightness != 0 || v.Address != "" {
		t.Fatalf("virtual = %+v, %v: want baud, brightness and address dropped", v, err)
	}
	c := newTest(t, opts(), one(newFake("a", bubble.Music), 1))
	if _, err := c.PatchPanel("v", json.RawMessage(`{"kind":"serial"}`)); err == nil {
		t.Error("a patch changed the kind")
	}
	if _, err := c.PatchPanel("v", json.RawMessage(`{"name":"z"}`)); err == nil {
		t.Error("a patch changed the name")
	}
	if _, err := c.PatchPanel("nope", json.RawMessage(`{"fps":1}`)); !errors.Is(err, ErrNotFound) {
		t.Errorf("patch of an unknown panel = %v", err)
	}
	if err := c.DeletePanel("terminal"); err == nil {
		t.Error("the terminal was deleted")
	}
	tc := newBare(t, Options{FPS: 30, Terminal: true}, one(newFake("a", bubble.Music), 1))
	if err := tc.DeletePanel("terminal"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("deleting the terminal = %v, want a refusal, not ErrNotFound", err)
	}
	if _, err := tc.PatchPanel("terminal", json.RawMessage(`{"fps":10}`)); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("patching the terminal = %v, want a refusal, not ErrNotFound", err)
	}
	for i := range maxPanels {
		if _, err := c.PutPanel(PanelSpec{Name: fmt.Sprint("n", i), Kind: KindVirtual, W: 1, H: 1, FPS: 1}); err != nil && i < maxPanels-1 {
			t.Fatalf("panel %d refused: %v", i, err)
		} else if err == nil && i == maxPanels-1 {
			t.Fatalf("panel %d accepted past the cap", i+1)
		}
	}
	if c.FirstPanel() != "v" || !c.HasPanel("v") || c.HasPanel("nope") {
		t.Errorf("FirstPanel/HasPanel wrong")
	}
	if c := newBare(t, Options{Terminal: true}, one(newFake("a", bubble.Music), 1)); c.FirstPanel() != "terminal" {
		t.Errorf("FirstPanel without declared panels = %q, want the terminal", c.FirstPanel())
	}
}

func TestKeysReachEveryGroupsActiveInstance(t *testing.T) {
	e, made := factory("a", bubble.Music, 1)
	c := newTest(t, opts(), e)
	mustPut(t, c, PanelSpec{Name: "x", Kind: KindVirtual, W: 8, H: 4, FPS: 60})
	tickAt(c, t0, -40)
	c.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if n := len(msgsOf[tea.KeyPressMsg](made()[0])); n != 0 {
		t.Errorf("template got %d keys, want 0", n)
	}
	for _, f := range made()[1:] {
		if n := len(msgsOf[tea.KeyPressMsg](f)); n != 1 {
			t.Errorf("instance got %d keys, want 1", n)
		}
	}
}

func TestVirtualFramesGoToOnFrame(t *testing.T) {
	var got []string
	o := opts()
	o.OnFrame = func(name string, f [][]color.RGBA) {
		got = append(got, fmt.Sprintf("%s %dx%d", name, len(f[0]), len(f)))
	}
	o.Clients = func(string) int { return 2 }
	c := newTest(t, o, one(newFake("a", bubble.Music), 0)) // blank: the group's own 4x2 frame, a fake draws 1x1
	tickAt(c, t0, -40)
	if len(got) != 1 || got[0] != "v 4x2" {
		t.Fatalf("OnFrame calls = %v, want [v 4x2]", got)
	}
	if ps := c.Panels(); ps[0].Clients != 2 {
		t.Fatalf("clients = %d, want 2 from Options.Clients", ps[0].Clients)
	}
}

func TestBrightnessPatchKeepsTheGroup(t *testing.T) {
	e, made := factory("a", bubble.Music, 1)
	c := newSerial(t, &fakePanel{}, e)
	n, g := len(made()), c.groups[groupKey{4, 2, 30}]
	if g == nil {
		t.Fatal("no group for the serial panel")
	}
	if _, err := c.PatchPanel("p", json.RawMessage(`{"brightness":10}`)); err != nil {
		t.Fatal(err)
	}
	if len(made()) != n || len(c.groups) != 1 || c.groups[g.key] != g {
		t.Fatalf("instances %d -> %d, groups = %d: a brightness patch rebuilt the group", n, len(made()), len(c.groups))
	}
	if _, err := c.PatchPanel("p", json.RawMessage(`{"fps":60}`)); err != nil {
		t.Fatal(err)
	}
	if len(c.groups) != 1 || c.groups[groupKey{4, 2, 60}] == nil {
		t.Fatalf("groups = %v after an fps patch, want the 4x2@60 one only", c.groups)
	}
}

// frames records OnFrame calls by panel name.
func frames(o *Options) map[string]int {
	got := map[string]int{}
	o.OnFrame = func(name string, _ [][]color.RGBA) { got[name]++ }
	return got
}

// Every panel streams to its hub, not just a virtual one: the web preview
// and spectrum watch show serial and terminal panels too.
func TestEveryPanelKindStreams(t *testing.T) {
	a := newFake("a", bubble.Music)
	port := &fakePanel{}
	o := serialOpts(port)
	got := frames(&o)
	c := newBare(t, o, one(a, 1))
	mustPut(t, c, serialSpec)
	run(c, c.drain())
	tickAt(c, t0, -100)
	if got["p"] != 1 || len(port.frames) != 1 {
		t.Fatalf("OnFrame = %v, port frames = %d, want p once and one frame", got, len(port.frames))
	}

	o = Options{FPS: 30, Terminal: true}
	got = frames(&o)
	c = newBare(t, o, one(newFake("b", bubble.Music), 1))
	c.Update(tea.WindowSizeMsg{Width: 4, Height: 1})
	tickAt(c, t0, -100)
	if got["terminal"] != 1 {
		t.Fatalf("OnFrame = %v, want terminal once", got)
	}
}
