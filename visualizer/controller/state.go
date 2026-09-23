package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// warn writes one tolerant-load stderr line. load runs before the TUI
// starts, so stderr is safe.
func (c *Controller) warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "state: %s: "+format+"\n", append([]any{c.o.StatePath}, args...)...)
}

// stateDoc is the state file: the controller settings and one section per
// bubble. config.yaml stays Ansible-owned and holds the hardware settings.
type stateDoc struct {
	Controller json.RawMessage        `json:"controller,omitempty"`
	Bubbles    map[string]bubbleState `json:"bubbles,omitempty"`
}

// bubbleState is one bubble's persisted weight and settings.
type bubbleState struct {
	Weight   int             `json:"weight"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// load reads the state file. A missing file leaves the defaults and
// unparseable JSON is an error; everything else is tolerant: an invalid
// controller section retries once with its pin cleared (see loadController),
// unknown bubbles are ignored and settings a bubble rejects keep its
// defaults. Whatever gets dropped along the way is logged to stderr.
func (c *Controller) load() error {
	if c.o.StatePath == "" {
		return nil
	}
	buf, err := os.ReadFile(c.o.StatePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc stateDoc
	if err := json.Unmarshal(buf, &doc); err != nil {
		return fmt.Errorf("%s: %w", c.o.StatePath, err)
	}
	c.loadController(doc.Controller)
	for name, bs := range doc.Bubbles {
		e := c.entry(name)
		if e == nil { // a bubble that is no longer registered
			c.warn("unknown bubble %q ignored", name)
			continue
		}
		if len(bs.Settings) > 0 {
			if err := e.Bubble.Configure(bs.Settings); err != nil { // a rejected section keeps the defaults
				c.warn("bubble %q: settings section dropped: %v", name, err)
			}
		}
		if bs.Weight >= 0 {
			e.Weight = bs.Weight
		}
	}
	return nil
}

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

// save writes the state file through a temporary file and a rename, so a
// crash never leaves half a document behind. Only c.set is written, never the
// unsaved --show/m pin.
func (c *Controller) save() error {
	if c.o.StatePath == "" {
		return nil
	}
	set, err := json.Marshal(c.set)
	if err != nil {
		return err
	}
	doc := stateDoc{Controller: set, Bubbles: make(map[string]bubbleState, len(c.entries))}
	for _, e := range c.entries {
		s, err := json.Marshal(e.Bubble.Settings())
		if err != nil {
			return err
		}
		doc.Bubbles[e.Bubble.Name()] = bubbleState{e.Weight, s}
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
