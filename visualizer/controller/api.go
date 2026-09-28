// The API surface: the documents GET /state and GET /bubbles return, the
// patches their PATCH siblings apply and the event queue POST /events feeds.
// Every method here runs inside Update, sent as a Call, so none of them locks.
package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

var (
	ErrNotFound = errors.New("not found")
	ErrFull     = errors.New("event queue full")
	// ErrSave wraps a save() failure: the settings change is already live in
	// memory, so the caller (api) must not report it as a bad request.
	ErrSave  = errors.New("state not saved")
	ErrInUse = errors.New("in use")
)

// State is the whole runtime state, as GET /state returns it.
type State struct {
	Active   string         `json:"active"` // "" = blank
	Kind     bubble.Kind    `json:"kind"`
	Playing  bool           `json:"playing"`
	DB       float64        `json:"db"`
	BPM      float64        `json:"bpm"`
	Settings Settings       `json:"settings"`
	Events   []bubble.Event `json:"events"`
	Panels   []PanelState   `json:"panels"`
	Preset   string         `json:"preset"`   // last loaded or saved preset, "" = none
	Modified bool           `json:"modified"` // something changed since Preset
}

// State reports what is shown right now and on which panels.
func (c *Controller) State() State {
	s := State{Playing: c.play, Kind: c.kind, DB: c.db, BPM: c.bpm, Settings: c.set,
		Events: append([]bubble.Event{}, c.events...), Panels: c.Panels(), Preset: c.preset, Modified: c.modified}
	s.Settings.Show = c.pin()
	if c.active != nil {
		s.Active = c.active.Name()
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
	c.modified = true
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
	if e.tmpl.Kind() == bubble.EventKind {
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
		out[i] = BubbleInfo{e.tmpl.Name(), e.tmpl.Kind(), e.Weight, e.tmpl.Settings()}
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
		if err := e.tmpl.Configure(settings); err != nil {
			return err
		}
		c.configureAll(c.index[name], settings)
	}
	if weight != nil {
		e.Weight = *weight
	}
	// no recheck: choose() catches a bubble that just lost its weight on the
	// next tick anyway, and enabling one waits for the loop like every pick
	c.modified = true
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

// PaletteInfo is one palette as GET /palettes returns it: a 16-colour
// preview, and the stops for a custom one.
type PaletteInfo struct {
	Name    string          `json:"name"`
	Preview []string        `json:"preview"`
	Custom  *palette.Custom `json:"custom"`
}

// Palettes lists every palette, shipped first.
func (c *Controller) Palettes() []PaletteInfo {
	out := make([]PaletteInfo, len(palette.Palettes))
	for i, p := range palette.Palettes {
		out[i] = PaletteInfo{p.Name, palette.Preview(p, 16), p.Custom}
	}
	return out
}

// PutPalette validates, registers (replacing a custom palette of the same
// name) and saves a custom palette.
func (c *Controller) PutPalette(cp palette.Custom) (PaletteInfo, error) {
	p, err := cp.Palette()
	if err != nil {
		return PaletteInfo{}, err
	}
	if _, ok := palette.ByName(p.Name); !ok && customCount() >= maxCustom {
		return PaletteInfo{}, fmt.Errorf("too many palettes, max %d", maxCustom)
	}
	if err := palette.Register(p); err != nil {
		return PaletteInfo{}, err
	}
	c.modified = true
	if err := c.save(); err != nil {
		return PaletteInfo{}, fmt.Errorf("%w: %v", ErrSave, err)
	}
	return PaletteInfo{p.Name, palette.Preview(p, 16), p.Custom}, nil
}

// maxCustom caps the custom palettes and the presets alike: both live in the
// state file and every GET lists them all.
const maxCustom = 64

func customCount() int {
	n := 0
	for _, p := range palette.Palettes {
		if p.Custom != nil {
			n++
		}
	}
	return n
}

// DeletePalette removes a custom palette. A shipped name is ErrShipped, an
// unknown one ErrNotFound, and one that a bubble's settings or a preset
// names is ErrInUse, listing the users.
func (c *Controller) DeletePalette(name string) error {
	p, ok := palette.ByName(name)
	if !ok {
		return fmt.Errorf("unknown palette %q: %w", name, ErrNotFound)
	}
	if p.Custom == nil {
		return fmt.Errorf("%q: %w", name, palette.ErrShipped)
	}
	if users := c.paletteUsers(name); len(users) > 0 {
		return fmt.Errorf("palette %q %w by %s", name, ErrInUse, strings.Join(users, ", "))
	}
	palette.Remove(name)
	c.modified = true
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}

// paletteUsers names every bubble whose settings and every preset whose
// bubble sections list the palette, as "bubble x" and "preset y".
func (c *Controller) paletteUsers(name string) []string {
	var users []string
	for _, e := range c.entries {
		buf, _ := json.Marshal(e.tmpl.Settings())
		if namesPalette(buf, name) {
			users = append(users, "bubble "+e.tmpl.Name())
		}
	}
	for _, pname := range slices.Sorted(maps.Keys(c.presets)) {
		for _, bs := range c.presets[pname].Bubbles {
			if namesPalette(bs.Settings, name) {
				users = append(users, "preset "+pname)
				break
			}
		}
	}
	return users
}

// namesPalette reports whether a settings document's "palettes" list holds
// name. ponytail: by convention every palette list is called "palettes";
// look at Hints instead if a bubble ever names one differently.
func namesPalette(settings json.RawMessage, name string) bool {
	var doc struct {
		Palettes []string `json:"palettes"`
	}
	if json.Unmarshal(settings, &doc) != nil {
		return false
	}
	return slices.ContainsFunc(doc.Palettes, func(s string) bool { return strings.EqualFold(s, name) })
}

// checkName validates a preset name.
func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 32 || strings.Contains(name, "/") {
		return "", fmt.Errorf("name must be 1..32 characters without '/', got %q", name)
	}
	return name, nil
}

// Presets lists the preset names, sorted.
func (c *Controller) Presets() []string {
	if names := slices.Sorted(maps.Keys(c.presets)); names != nil {
		return names
	}
	return []string{} // GET /presets answers [], not null
}

// SavePreset stores the current controller and bubble sections under name,
// or raw when given: a {controller, bubbles} document with no unknown
// fields, checked for shape only. Loading validates the content.
func (c *Controller) SavePreset(name string, raw json.RawMessage) error {
	name, err := checkName(name)
	if err != nil {
		return err
	}
	var doc presetDoc
	if len(bytes.TrimSpace(raw)) == 0 {
		if doc, err = c.snapshot(); err != nil {
			return err
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&doc); err != nil {
			return err
		}
		if dec.More() {
			return fmt.Errorf("unexpected data after JSON value")
		}
	}
	if _, ok := c.presets[name]; !ok && len(c.presets) >= maxCustom {
		return fmt.Errorf("too many presets, max %d", maxCustom)
	}
	c.presets[name] = doc
	if len(bytes.TrimSpace(raw)) == 0 { // a body is not what runs now
		c.preset, c.modified = name, false
	}
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}

// LoadPreset applies a preset: the controller section like a patch (a stale
// pin is dropped, as at load), then every bubble's weight and settings.
// Unknown bubbles are skipped. ponytail: not atomic, a bubble that rejects
// its section leaves the ones before it applied; a preset saved from a
// snapshot is always valid, so make it a dry run first only if hand-written
// presets ever matter.
func (c *Controller) LoadPreset(name string) error {
	name = strings.TrimSpace(name)
	doc, ok := c.presets[name]
	if !ok {
		return fmt.Errorf("unknown preset %q: %w", name, ErrNotFound)
	}
	controller, _ := stripKeys(doc.Controller, brightnessKeys...) // an old preset may still carry the brightness keys
	set, err := bubble.Patch(c.set, controller)
	if err != nil {
		return fmt.Errorf("controller section: %w", err)
	}
	if err := c.check(set); err != nil {
		set.Show = ""
		if err := c.check(set); err != nil {
			return fmt.Errorf("controller section: %w", err)
		}
	}
	for bname, bs := range doc.Bubbles {
		e := c.entry(bname)
		if e == nil {
			continue
		}
		if len(bs.Settings) > 0 {
			if err := e.tmpl.Configure(bs.Settings); err != nil {
				c.modified = true // the bubbles before this one are applied
				return fmt.Errorf("bubble %q: %w", bname, err)
			}
			c.configureAll(c.index[bname], bs.Settings)
		}
		if bs.Weight >= 0 {
			e.Weight = bs.Weight
		}
	}
	c.recheck = true
	c.set = set
	c.show = ""
	c.preset, c.modified = name, false
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}

// DeletePreset drops a preset by name; deleting the active one leaves no
// preset active.
func (c *Controller) DeletePreset(name string) error {
	name = strings.TrimSpace(name)
	if _, ok := c.presets[name]; !ok {
		return fmt.Errorf("unknown preset %q: %w", name, ErrNotFound)
	}
	delete(c.presets, name)
	if c.preset == name {
		c.preset, c.modified = "", false
	}
	if err := c.save(); err != nil {
		return fmt.Errorf("%w: %v", ErrSave, err)
	}
	return nil
}
