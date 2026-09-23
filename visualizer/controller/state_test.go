package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// stateOpts is opts() persisting to a fresh file in the test's temp dir.
func stateOpts(t *testing.T) Options {
	t.Helper()
	o := opts()
	o.StatePath = filepath.Join(t.TempDir(), "state.json")
	return o
}

// read unmarshals the state file.
func read(t *testing.T, path string) stateDoc {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var doc stateDoc
	if err := json.Unmarshal(buf, &doc); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return doc
}

// write puts raw in the state file.
func write(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func TestStateRoundTrip(t *testing.T) {
	o := stateOpts(t)
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1}, Entry{newFake("clock", bubble.Idle), 1})
	patch(t, c, `{"music":{"loop":5,"order":"sequence"},"brightness":10,"idle_brightness":3,"show":"clock"}`)
	four := 4
	if err := c.PatchBubble("a", &four, json.RawMessage(`{"speed":7}`)); err != nil {
		t.Fatalf("PatchBubble: %v", err)
	}

	a := newFake("a", bubble.Music)
	c2 := newTest(t, o, Entry{a, 1}, Entry{newFake("clock", bubble.Idle), 1})
	want := c.set
	if got := c2.set; got != want {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
	if got := c2.Bubbles()[0]; got.Weight != 4 || got.Settings != (fakeSettings{Speed: 7}) {
		t.Errorf("bubble = %+v, want weight 4 and speed 7", got)
	}
	if entries, err := os.ReadDir(filepath.Dir(o.StatePath)); err != nil || len(entries) != 1 {
		t.Errorf("dir = %v (err %v), want only state.json: no .tmp left behind", entries, err)
	}
}

func TestMissingStateFileKeepsDefaults(t *testing.T) {
	o := stateOpts(t)
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 2})
	if c.set.Music.Seconds != 60 || c.set.Brightness != 64 || c.Bubbles()[0].Weight != 2 {
		t.Errorf("settings = %+v, weight %d, want the defaults", c.set, c.Bubbles()[0].Weight)
	}
	if _, err := os.Stat(o.StatePath); err == nil {
		t.Error("New wrote the state file; only a patch does")
	}
}

func TestCorruptStateIsAnError(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":`)
	if _, err := New([]Entry{{newFake("a", bubble.Music), 1}}, o); err == nil {
		t.Error("New with unparseable state: want an error")
	}
}

func TestInvalidControllerSectionFallsBackToDefaults(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":{"music":{"loop":5,"order":"nope"},"brightness":9}}`)
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1})
	if c.set.Music.Order != "random" || c.set.Music.Seconds != 60 || c.set.Brightness != 64 {
		t.Errorf("settings = %+v, want the defaults", c.set)
	}
}

func TestStaleShowKeepsRestOfControllerSection(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":{"show":"gone","music_db":-40}}`)
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1})
	if c.set.MusicDB != -40 {
		t.Errorf("music_db = %v, want -40: a stale show must not drop the rest of the section", c.set.MusicDB)
	}
	if c.set.Show != "" {
		t.Errorf("show = %q, want cleared", c.set.Show)
	}
}

func TestUnknownBubbleIgnored(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"bubbles":{"nope":{"weight":5},"a":{"weight":4}}}`)
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1})
	if got := c.Bubbles(); len(got) != 1 || got[0].Weight != 4 {
		t.Errorf("bubbles = %+v, want just a with weight 4", got)
	}
}

func TestBadBubbleSettingsKeepDefaults(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"bubbles":{"a":{"weight":2,"settings":{"speed":-1}}}}`)
	a := newFake("a", bubble.Music)
	c := newTest(t, o, Entry{a, 1})
	if got := c.Bubbles()[0]; got.Weight != 2 || got.Settings != (fakeSettings{Speed: 1}) {
		t.Errorf("bubble = %+v, want weight 2 and the default speed 1", got)
	}
}

func TestShowOptionIsNotPersisted(t *testing.T) {
	o := stateOpts(t)
	o.Show = "clock"
	c := newTest(t, o, Entry{newFake("a", bubble.Music), 1}, Entry{newFake("clock", bubble.Idle), 1})
	patch(t, c, `{"brightness":1}`)
	if got := c.State().Settings.Show; got != "clock" {
		t.Errorf("State().Settings.Show = %q, want the pin", got)
	}
	var set Settings
	if err := json.Unmarshal(read(t, o.StatePath).Controller, &set); err != nil {
		t.Fatalf("unmarshal controller: %v", err)
	}
	if set.Show != "" {
		t.Errorf("saved show = %q, want empty: --show is never written", set.Show)
	}
	// an explicit patch clears the unsaved pin too
	patch(t, c, `{"show":""}`)
	if got := c.State().Settings.Show; got != "" {
		t.Errorf("State().Settings.Show = %q after clearing, want empty", got)
	}
}

func TestPatchWithoutAStatePathDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	c := newTest(t, opts(), Entry{newFake("a", bubble.Music), 1})
	patch(t, c, `{"brightness":7}`)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("dir = %v, want nothing written", entries)
	}
	if c.set.Brightness != 7 {
		t.Errorf("brightness = %d, want the patch applied in memory", c.set.Brightness)
	}
}
