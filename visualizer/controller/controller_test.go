package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
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
	Speed float64 `json:"speed"`
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
}

var _ Panel = (*fakePanel)(nil)

func (p *fakePanel) Send(f [][]color.RGBA)   { p.frames = append(p.frames, f) }
func (p *fakePanel) SetBrightness(b byte)    { p.bright = append(p.bright, b) }
func (p *fakePanel) Status() proto.StatusMsg { return p.status }

// opts is the usual setup: a fixed 4x2 canvas so no window size is needed.
func opts() Options { return Options{W: 4, H: 2, FPS: 30, Brightness: 64} }

func newTest(t *testing.T, o Options, entries ...Entry) *Controller {
	t.Helper()
	c, err := New(entries, o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// tickAt runs one frame at time tm with an RMS level of db.
func tickAt(c *Controller, tm time.Time, db float64) {
	c.sigHook = func() dsp.Signal { return dsp.Signal{DB: db} }
	c.Update(frameMsg(tm))
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
	c := newTest(t, opts(), Entry{music, 1}, Entry{clock, 1})
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
	c := newTest(t, opts(), Entry{music, 1}, Entry{clock, 1})
	tickAt(c, t0, -50) // exactly music_db
	if s := c.State(); s.Active != "bars" || s.Kind != bubble.Music || !s.Playing || s.DB != -50 {
		t.Fatalf("state = %+v, want bars/music, playing, db -50", s)
	}
}

func TestQuietKeepsMusicUntilSilenceAfter(t *testing.T) {
	music, clock := newFake("bars", bubble.Music), newFake("clock", bubble.Idle)
	c := newTest(t, opts(), Entry{music, 1}, Entry{clock, 1})
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
	c := newTest(t, opts(), Entry{music, 1}, Entry{clock, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, opts(), Entry{off, 0}, Entry{on, 1})
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
	c := newTest(t, opts(), Entry{heavy, 3}, Entry{light, 1}, Entry{clock, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1}, Entry{d, 1})
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
	o := opts()
	o.Panel = p
	c := newTest(t, o, Entry{music, 0})
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
	c := newTest(t, o, Entry{music, 1}, Entry{clock, 1}, Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{clock, 1}, Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{music, 1})
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
	c := newTest(t, Options{FPS: 30}, Entry{a, 1})
	c.Update(tea.WindowSizeMsg{Width: 8, Height: 3})
	if got := msgsOf[bubble.Resize](a); len(got) != 1 || got[0] != (bubble.Resize{W: 8, H: 6}) {
		t.Fatalf("resizes = %+v, want one {8 6}", got)
	}
	if s := c.State(); s.W != 8 || s.H != 6 {
		t.Fatalf("size = %dx%d, want 8x6", s.W, s.H)
	}
}

func TestWindowSizeIgnoredWithAFixedSize(t *testing.T) {
	a := newFake("a", bubble.Music)
	c := newTest(t, opts(), Entry{a, 1})
	c.Update(tea.WindowSizeMsg{Width: 8, Height: 3})
	if got := msgsOf[bubble.Resize](a); len(got) != 1 || got[0] != (bubble.Resize{W: 4, H: 2}) {
		t.Fatalf("resizes = %+v, want only the fixed {4 2} from New", got)
	}
	if s := c.State(); s.W != 4 || s.H != 2 {
		t.Fatalf("size = %dx%d, want 4x2", s.W, s.H)
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

// TestInitRunsResizeCmds is the regression for a panel with a fixed size: New
// is the only Resize a bubble ever gets, so a cmd it answers with (e.g. a
// bubble.Bubble that starts polling on its first size) must run, and only
// Init has a *tea.Program to run it on.
func TestInitRunsResizeCmds(t *testing.T) {
	a := newFake("a", bubble.Music)
	a.onResize = func() tea.Msg { return markerMsg{} }
	c := newTest(t, opts(), Entry{a, 1}) // opts(): a fixed panel size, like every deployed config
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
	o := opts()
	o.Panel = p
	c := newTest(t, o, Entry{music, 1}, Entry{clock, 1})
	patch(t, c, `{"idle_brightness":16,"silence_after":0}`)
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
	c := newTest(t, opts(), Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, o, Entry{music, 1}, Entry{clock, 1}, Entry{alert, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
	c.Update("hello")
	for _, f := range []*fake{a, b} {
		if n := len(msgsOf[string](f)); n != 1 {
			t.Errorf("%s got %d strings, want 1", f.name, n)
		}
	}
}

// A remote size resizes the canvas like the local window, unless a panel fixes it.
func TestRemoteResize(t *testing.T) {
	c := newTest(t, Options{FPS: 30}, Entry{newFake("a", bubble.Music), 1})
	c.Update(bubble.Resize{W: 6, H: 4})
	if c.w != 6 || c.h != 4 || len(c.blank) != 4 || len(c.blank[0]) != 6 {
		t.Fatalf("canvas %dx%d", c.w, c.h)
	}
	c = newTest(t, opts(), Entry{newFake("a", bubble.Music), 1})
	c.Update(bubble.Resize{W: 6, H: 4})
	if c.w != 4 || c.h != 2 {
		t.Fatalf("panel canvas changed to %dx%d", c.w, c.h)
	}
}

func TestHeadlessSkipsRendering(t *testing.T) {
	a := newFake("a", bubble.Music)
	o := opts()
	o.Headless = true
	c := newTest(t, o, Entry{a, 1})
	tickAt(c, t0, -40)
	if c.view != "" {
		t.Errorf("view = %q, want nothing rendered", c.view)
	}
	o.Headless = false
	c = newTest(t, o, Entry{newFake("a", bubble.Music), 1})
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
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1})
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
	if _, err := New([]Entry{{a, 1}, {newFake("a", bubble.Idle), 1}}, opts()); err == nil {
		t.Error("New with duplicate names: want an error")
	}
	o := opts()
	o.Show = "nope"
	if _, err := New([]Entry{{a, 1}}, o); err == nil {
		t.Error("New with an unknown show: want an error")
	}
	o.Show = "alert"
	if _, err := New([]Entry{{a, 1}, {newFake("alert", bubble.EventKind), 1}}, o); err == nil {
		t.Error("New pinning an event bubble: want an error")
	}
}

func TestPatchValidates(t *testing.T) {
	a, alert := newFake("a", bubble.Music), newFake("alert", bubble.EventKind)
	c := newTest(t, opts(), Entry{a, 1}, Entry{alert, 1})
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
	patch(t, c, `{"brightness":10,"music":{"loop":5}}`)
	if c.set.Brightness != 10 || c.set.Music.Seconds != 5 || c.set.Music.Order != "random" {
		t.Errorf("settings = %+v, want a merged patch", c.set)
	}
}

func TestBubblesAndPatchBubble(t *testing.T) {
	a := newFake("a", bubble.Music)
	c := newTest(t, opts(), Entry{a, 2})
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
	if got := c.Bubbles()[0]; got.Weight != 3 || got.Settings != (fakeSettings{Speed: 9}) {
		t.Errorf("bubble = %+v, want weight 3 and speed 9", got)
	}
}

func TestStateJSONPanelIsSnakeCase(t *testing.T) {
	p := &fakePanel{status: proto.StatusMsg{CRCErr: 3}}
	o := opts()
	o.Panel = p
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1})
	buf, err := json.Marshal(c.State())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"crc_err":3`) {
		t.Fatalf("state json = %s, want panel.crc_err", buf)
	}
}

func TestStatePanelStatus(t *testing.T) {
	p := &fakePanel{status: proto.StatusMsg{FramesOK: 7, FPS: 20}}
	o := opts()
	o.Panel = p
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1})
	if s := c.State(); s.Panel == nil || s.Panel.FramesOK != 7 {
		t.Fatalf("panel status = %+v, want the port's", s.Panel)
	}
	c = newTest(t, opts(), Entry{newFake("b", bubble.Music), 1})
	if s := c.State(); s.Panel != nil {
		t.Errorf("panel status = %+v without a panel, want nil", s.Panel)
	}
}

func TestUnrelatedPatchDoesNotPostponeTheLoop(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1}) // the default 60 s music loop
	tickAt(c, t0, -40)
	first := c.State().Active
	for i := 1; i <= 6; i++ { // a web UI patching the brightness every 10 s
		patch(t, c, fmt.Sprintf(`{"brightness":%d}`, 60+i))
		tickAt(c, at(time.Duration(i)*10*time.Second), -40)
	}
	if got := c.State().Active; got == first {
		t.Fatalf("active = %q at 60 s, want the other bubble: a brightness patch must not postpone the loop", got)
	}
}

func TestPatchBubbleDoesNotPostponeTheLoop(t *testing.T) {
	a, b := newFake("a", bubble.Music), newFake("b", bubble.Music)
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, opts(), Entry{a, 1}, Entry{b, 1})
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
	c := newTest(t, opts(), Entry{bars, 1}, Entry{fire, 0}, Entry{clock, 1})
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
	one, zero := 1, 0
	if err := c.PatchBubble("fire", &one, nil); err != nil {
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
	c := newTest(t, opts(), Entry{newFake("bars", bubble.Music), 1}, Entry{newFake("clock", bubble.Idle), 1},
		Entry{np, 1}, Entry{newFake("alert", bubble.EventKind), 1})
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
	c := newTest(t, opts(), Entry{newFake("clock", bubble.Idle), 1}, Entry{np, 1})
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
	c := newTest(t, opts(), Entry{newFake("nowplaying", bubble.Track), 1})
	tickAt(c, at(0), -20)
	tickAt(c, at(time.Second), -90)
	if got := c.State().Active; got != "" {
		t.Errorf("active = %q, want blank: a track bubble is in no loop", got)
	}
}

func TestInterludeBrightness(t *testing.T) {
	p := &fakePanel{}
	o := opts()
	o.Panel = p
	c := newTest(t, o, Entry{newFake("clock", bubble.Idle), 1}, Entry{newFake("nowplaying", bubble.Track), 1})
	patch(t, c, `{"brightness":200,"idle_brightness":10}`)
	tickAt(c, at(0), -90)
	if err := c.Show("nowplaying", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(time.Second), -90)
	if got := p.bright[len(p.bright)-1]; got != 200 {
		t.Errorf("brightness = %d, want 200 as for music", got)
	}
}
