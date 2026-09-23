// The API surface: the documents GET /state and GET /bubbles return, the
// patches their PATCH siblings apply and the event queue POST /events feeds.
// Every method here runs inside Update, sent as a Call, so none of them locks.
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/proto"
)

var (
	ErrNotFound = errors.New("not found")
	ErrFull     = errors.New("event queue full")
	// ErrSave wraps a save() failure: the settings change is already live in
	// memory, so the caller (api) must not report it as a bad request.
	ErrSave = errors.New("state not saved")
)

// State is the whole runtime state, as GET /state returns it.
type State struct {
	Active   string           `json:"active"` // "" = blank
	Kind     bubble.Kind      `json:"kind"`
	Playing  bool             `json:"playing"`
	DB       float64          `json:"db"`
	BPM      float64          `json:"bpm"`
	W        int              `json:"w"`
	H        int              `json:"h"`
	Settings Settings         `json:"settings"`
	Events   []bubble.Event   `json:"events"`
	Panel    *proto.StatusMsg `json:"panel,omitempty"`
}

// State reports what is on the panel right now.
func (c *Controller) State() State {
	s := State{Playing: c.play, Kind: c.kind, DB: c.db, BPM: c.bpm, W: c.w, H: c.h,
		Settings: c.set, Events: append([]bubble.Event{}, c.events...)}
	s.Settings.Show = c.pin()
	if c.active != nil {
		s.Active = c.active.Name()
	}
	if c.o.Panel != nil {
		st := c.o.Panel.Status()
		s.Panel = &st
	}
	return s
}

// Patch merges raw onto the settings, validates the result and saves it. A
// changed pin or loop takes effect on the next frame tick; only a changed loop
// restarts the countdown, so patching anything else cannot hold off the loop.
func (c *Controller) Patch(raw json.RawMessage) error {
	set, err := bubble.Patch(c.set, raw)
	if err != nil {
		return err
	}
	if err := c.check(set); err != nil {
		return err
	}
	c.recheck = c.recheck || set.Music != c.set.Music || set.Idle != c.set.Idle
	c.set = set
	// an explicit show replaces the unsaved --show/m pin, an empty one clears it
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields) // raw parsed above; an empty body leaves a nil map
	if _, ok := fields["show"]; ok {
		c.show = ""
	}
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}

// check validates a settings document, naming what is allowed.
func (c *Controller) check(s Settings) error {
	for _, l := range []struct {
		name string
		Loop
	}{{"music", s.Music}, {"idle", s.Idle}} {
		if !slices.Contains(Orders, l.Order) {
			return fmt.Errorf("unknown %s order %q, have: %s", l.name, l.Order, strings.Join(Orders, ", "))
		}
		if l.Seconds < 0 {
			return fmt.Errorf("%s loop must be >= 0, got %v", l.name, l.Seconds)
		}
	}
	if s.SilenceAfter < 0 {
		return fmt.Errorf("silence_after must be >= 0, got %v", s.SilenceAfter)
	}
	if s.SilenceDB > s.MusicDB {
		return fmt.Errorf("silence_db (%v) must be <= music_db (%v)", s.SilenceDB, s.MusicDB)
	}
	return c.checkShow(s.Show)
}

// checkShow accepts an empty pin or a registered bubble that is not an event
// bubble: events show themselves.
func (c *Controller) checkShow(name string) error {
	if name == "" {
		return nil
	}
	e := c.entry(name)
	if e == nil {
		return fmt.Errorf("unknown bubble %q: %w", name, ErrNotFound)
	}
	if e.Bubble.Kind() == bubble.EventKind {
		return fmt.Errorf("cannot show the %s bubble %q", bubble.EventKind, name)
	}
	return nil
}

// BubbleInfo is one registered bubble, as GET /bubbles returns it.
type BubbleInfo struct {
	Name     string      `json:"name"`
	Kind     bubble.Kind `json:"kind"`
	Weight   int         `json:"weight"`
	Settings any         `json:"settings"`
}

// Bubbles lists the registered bubbles in registry order.
func (c *Controller) Bubbles() []BubbleInfo {
	out := make([]BubbleInfo, len(c.entries))
	for i, e := range c.entries {
		out[i] = BubbleInfo{e.Bubble.Name(), e.Bubble.Kind(), e.Weight, e.Bubble.Settings()}
	}
	return out
}

// PatchBubble sets a bubble's weight and patches its settings, both
// optional, and saves. An unknown name is ErrNotFound.
func (c *Controller) PatchBubble(name string, weight *int, settings json.RawMessage) error {
	e := c.entry(name)
	if e == nil {
		return fmt.Errorf("unknown bubble %q: %w", name, ErrNotFound)
	}
	if weight != nil && *weight < 0 {
		return fmt.Errorf("weight must be >= 0, got %d", *weight)
	}
	if len(settings) > 0 {
		if err := e.Bubble.Configure(settings); err != nil {
			return err
		}
	}
	if weight != nil {
		e.Weight = *weight
	}
	// no recheck: choose() catches a bubble that just lost its weight on the
	// next tick anyway, and enabling one waits for the loop like every pick
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}

// Next skips to another bubble on the next frame tick.
func (c *Controller) Next() { c.repick = true }

// Show shows a bubble for d, then the loop carries on: an interlude. Like
// the pin it takes any bubble but an event bubble, whatever its weight;
// under a pin it is refused, since a pin means only this.
func (c *Controller) Show(name string, d time.Duration) error {
	if err := c.checkShow(name); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("no bubble named")
	}
	if d <= 0 {
		return fmt.Errorf("duration must be > 0, got %v", d)
	}
	if pin := c.pin(); pin != "" {
		return fmt.Errorf("pinned to %q", pin)
	}
	c.interlude, c.interludeFor, c.interludeUntil = name, d, time.Time{}
	return nil
}

// AddEvent queues an event, filling in an ID when it has none; the same ID
// replaces. Past maxEvents live events it returns ErrFull.
func (c *Controller) AddEvent(e bubble.Event) (string, error) {
	if rank(e.Level) < 0 {
		return "", fmt.Errorf("unknown level %q, have: %s", e.Level, strings.Join(bubble.Levels, ", "))
	}
	if e.ID != "" {
		if i := slices.IndexFunc(c.events, func(q bubble.Event) bool { return q.ID == e.ID }); i >= 0 {
			c.events[i] = e // a replacement is not growth
			return e.ID, nil
		}
	}
	if len(c.events) >= maxEvents {
		return "", ErrFull
	}
	if e.ID == "" {
		c.nextID++
		e.ID = strconv.Itoa(c.nextID)
	}
	c.events = append(c.events, e)
	return e.ID, nil
}

// RemoveEvent drops the event with this ID.
func (c *Controller) RemoveEvent(id string) error {
	i := slices.IndexFunc(c.events, func(e bubble.Event) bool { return e.ID == id })
	if i < 0 {
		return fmt.Errorf("unknown event %q: %w", id, ErrNotFound)
	}
	c.events = slices.Delete(c.events, i, i+1)
	return nil
}
