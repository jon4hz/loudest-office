package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// warn writes one tolerant-load stderr line. load runs before the TUI
// starts, so stderr is safe.
func (c *Controller) warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "state: %s: "+format+"\n", append([]any{c.o.StatePath}, args...)...)
}

// stateDoc is the state file: the controller settings, one section per
// bubble, the custom palettes and the saved presets. config.yaml stays
// Ansible-owned and holds the hardware settings.
type stateDoc struct {
	Controller json.RawMessage        `json:"controller,omitempty"`
	Bubbles    map[string]bubbleState `json:"bubbles,omitempty"`
	Palettes   []palette.Custom       `json:"palettes,omitempty"`
	Presets    map[string]presetDoc   `json:"presets,omitempty"`
	Panels     []PanelSpec            `json:"panels"` // never omitempty: an empty list must round-trip, or the seed returns
}

// bubbleState is one bubble's persisted weight and settings.
type bubbleState struct {
	Weight   int             `json:"weight"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// presetDoc is what a preset stores: the same two sections as the file.
type presetDoc struct {
	Controller json.RawMessage        `json:"controller,omitempty"`
	Bubbles    map[string]bubbleState `json:"bubbles,omitempty"`
}

// load reads the state file. A missing file or an empty StatePath leaves the
// defaults and unparseable JSON is an error; everything else is tolerant: an
// invalid controller section retries once with its pin cleared (see
// loadController), unknown bubbles are ignored and settings a bubble rejects
// keep its defaults. Whatever gets dropped along the way is logged to
// stderr. The panel step below runs regardless, so a seed still applies
// without a file or a state path.
func (c *Controller) load() error {
	var doc stateDoc
	if c.o.StatePath != "" {
		buf, err := os.ReadFile(c.o.StatePath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return err
		default:
			if err := json.Unmarshal(buf, &doc); err != nil {
				return fmt.Errorf("%s: %w", c.o.StatePath, err)
			}
		}
	}
	for _, cp := range doc.Palettes { // before the bubbles, whose settings may name them
		p, err := cp.Palette()
		if err == nil {
			err = palette.Register(p)
		}
		if err != nil {
			c.warn("palette %q skipped: %v", cp.Name, err)
		}
	}
	if doc.Presets != nil {
		for name, p := range doc.Presets { // an old preset may still carry the brightness keys
			p.Controller, _ = stripKeys(p.Controller, brightnessKeys...)
			doc.Presets[name] = p
		}
		c.presets = doc.Presets
	}
	controller, removed := stripKeys(doc.Controller, brightnessKeys...)
	c.loadController(controller)
	for name, bs := range doc.Bubbles {
		e := c.entry(name)
		if e == nil { // a bubble that is no longer registered
			c.warn("unknown bubble %q ignored", name)
			continue
		}
		if len(bs.Settings) > 0 {
			if err := e.tmpl.Configure(bs.Settings); err != nil { // a rejected section keeps the defaults
				c.warn("bubble %q: settings section dropped: %v", name, err)
			}
		}
		if bs.Weight >= 0 {
			e.Weight = bs.Weight
		}
	}
	stored := doc.Panels != nil
	if !stored && c.o.Seed != nil { // a file from before panels, or none: the flags seed one
		s := *c.o.Seed
		if v, ok := removed["brightness"]; ok {
			_ = json.Unmarshal(v, &s.Brightness)
		}
		if v, ok := removed["idle_brightness"]; ok {
			_ = json.Unmarshal(v, &s.IdleBrightness)
		}
		doc.Panels = []PanelSpec{s}
	}
	for _, s := range doc.Panels {
		s, err := checkPanel(s)
		if err == nil {
			_, err = c.addPanel(s)
		}
		if err != nil {
			c.warn("panel %q skipped: %v", s.Name, err)
		}
	}
	if seed := c.o.Seed; stored && seed != nil {
		if p := c.find(seed.Name); p != nil && (p.Address != seed.Address || p.Baud != seed.Baud || p.FPS != seed.FPS) {
			c.warn("panel %q: the state file wins over the serial/baud/fps flags", seed.Name)
		}
	}
	return nil
}

// stripKeys removes keys from a JSON object and returns the rest and what
// was removed; anything but an object comes back untouched. Brightness
// moved from the controller section to the panels: an old file still
// carries it, and dropping the whole section over it would lose the thresholds.
func stripKeys(raw json.RawMessage, keys ...string) (json.RawMessage, map[string]json.RawMessage) {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil || obj == nil {
		return raw, nil
	}
	removed := map[string]json.RawMessage{}
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			removed[k] = v
			delete(obj, k)
		}
	}
	if len(removed) == 0 {
		return raw, nil
	}
	out, _ := json.Marshal(obj)
	return out, removed
}

// brightnessKeys are the controller keys that moved to the panels.
var brightnessKeys = []string{"brightness", "idle_brightness"}

// loadController applies the controller section. A section that fails
// validation retries once with Show cleared, so one stale pin (a bubble
// later removed from the registry) does not throw away the rest of the
// section, including calibrated thresholds; still invalid after the retry
// keeps the defaults.
func (c *Controller) loadController(raw json.RawMessage) {
	set, err := bubble.Patch(c.set, raw)
	if err != nil {
		c.warn("controller section dropped: %v", err)
		return
	}
	checkErr := c.check(set)
	if checkErr == nil {
		c.set = set
		return
	}
	retry := set
	retry.Show = ""
	if c.check(retry) == nil {
		c.set = retry
		c.warn("controller: stale show %q ignored", set.Show)
		return
	}
	c.warn("controller section dropped: %v", checkErr)
}

// snapshot marshals the controller settings and every bubble's weight and
// settings: the state file's two sections, also what a preset stores.
func (c *Controller) snapshot() (presetDoc, error) {
	set, err := json.Marshal(c.set)
	if err != nil {
		return presetDoc{}, err
	}
	doc := presetDoc{Controller: set, Bubbles: make(map[string]bubbleState, len(c.entries))}
	for _, e := range c.entries {
		s, err := json.Marshal(e.tmpl.Settings())
		if err != nil {
			return presetDoc{}, err
		}
		doc.Bubbles[e.tmpl.Name()] = bubbleState{e.Weight, s}
	}
	return doc, nil
}

// save writes the state file through a temporary file and a rename, so a
// crash never leaves half a document behind. Only c.set is written, never the
// unsaved --show/m pin.
func (c *Controller) save() error {
	if c.o.StatePath == "" {
		return nil
	}
	snap, err := c.snapshot()
	if err != nil {
		return err
	}
	doc := stateDoc{Controller: snap.Controller, Bubbles: snap.Bubbles, Presets: c.presets, Panels: c.specs()}
	for _, p := range palette.Palettes {
		if p.Custom != nil {
			doc.Palettes = append(doc.Palettes, *p.Custom)
		}
	}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.o.StatePath + ".tmp"
	if err = writeSync(tmp, buf); err == nil {
		err = os.Rename(tmp, c.o.StatePath)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// writeSync puts buf in path, flushed to disk before the file is closed.
func writeSync(path string, buf []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
