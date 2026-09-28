package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/palette"
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
	c := newTest(t, o, one(newFake("a", bubble.Music), 1), one(newFake("clock", bubble.Idle), 1))
	patch(t, c, `{"music":{"loop":5,"order":"sequence"},"music_db":-40,"show":"clock"}`)
	four := 4
	if err := c.PatchBubble("a", &four, json.RawMessage(`{"speed":7}`)); err != nil {
		t.Fatalf("PatchBubble: %v", err)
	}

	a := newFake("a", bubble.Music)
	c2 := newTest(t, o, one(a, 1), one(newFake("clock", bubble.Idle), 1))
	want := c.set
	if got := c2.set; got != want {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
	if got := c2.Bubbles()[0]; got.Weight != 4 || !reflect.DeepEqual(got.Settings, fakeSettings{Speed: 7}) {
		t.Errorf("bubble = %+v, want weight 4 and speed 7", got)
	}
	if entries, err := os.ReadDir(filepath.Dir(o.StatePath)); err != nil || len(entries) != 1 {
		t.Errorf("dir = %v (err %v), want only state.json: no .tmp left behind", entries, err)
	}
}

func TestMissingStateFileKeepsDefaults(t *testing.T) {
	o := stateOpts(t)
	c := newBare(t, o, one(newFake("a", bubble.Music), 2))
	if c.set.Music.Seconds != 60 || c.Bubbles()[0].Weight != 2 {
		t.Errorf("settings = %+v, weight %d, want the defaults", c.set, c.Bubbles()[0].Weight)
	}
	if _, err := os.Stat(o.StatePath); err == nil {
		t.Error("New wrote the state file; only a patch does")
	}
}

func TestCorruptStateIsAnError(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":`)
	if _, err := New([]Entry{one(newFake("a", bubble.Music), 1)}, o); err == nil {
		t.Error("New with unparseable state: want an error")
	}
}

func TestInvalidControllerSectionFallsBackToDefaults(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":{"music":{"loop":5,"order":"nope"},"music_db":-40}}`)
	c := newTest(t, o, one(newFake("a", bubble.Music), 1))
	if c.set.Music.Order != "random" || c.set.Music.Seconds != 60 || c.set.MusicDB != -50 {
		t.Errorf("settings = %+v, want the defaults", c.set)
	}
}

func TestStaleShowKeepsRestOfControllerSection(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":{"show":"gone","music_db":-40}}`)
	c := newTest(t, o, one(newFake("a", bubble.Music), 1))
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
	c := newTest(t, o, one(newFake("a", bubble.Music), 1))
	if got := c.Bubbles(); len(got) != 1 || got[0].Weight != 4 {
		t.Errorf("bubbles = %+v, want just a with weight 4", got)
	}
}

func TestBadBubbleSettingsKeepDefaults(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"bubbles":{"a":{"weight":2,"settings":{"speed":-1}}}}`)
	a := newFake("a", bubble.Music)
	c := newTest(t, o, one(a, 1))
	if got := c.Bubbles()[0]; got.Weight != 2 || !reflect.DeepEqual(got.Settings, fakeSettings{Speed: 1}) {
		t.Errorf("bubble = %+v, want weight 2 and the default speed 1", got)
	}
}

func TestShowOptionIsNotPersisted(t *testing.T) {
	o := stateOpts(t)
	o.Show = "clock"
	c := newTest(t, o, one(newFake("a", bubble.Music), 1), one(newFake("clock", bubble.Idle), 1))
	patch(t, c, `{"music_db":-40}`)
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
	c := newTest(t, opts(), one(newFake("a", bubble.Music), 1))
	patch(t, c, `{"music_db":-7}`)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("dir = %v, want nothing written", entries)
	}
	if c.set.MusicDB != -7 {
		t.Errorf("music_db = %v, want the patch applied in memory", c.set.MusicDB)
	}
}

func lagoon() palette.Custom {
	return palette.Custom{Name: "lagoon", Axis: "bands", Stops: []palette.Stop{{Pos: 0, Color: "#000080"}, {Pos: 1, Color: "#ffffff"}}}
}

func TestPalettesRoundTripTheStateFile(t *testing.T) {
	o := stateOpts(t)
	c := newTest(t, o, one(newFake("a", bubble.Music), 1))
	t.Cleanup(func() { palette.Remove("lagoon") })
	info, err := c.PutPalette(lagoon())
	if err != nil || info.Name != "lagoon" || len(info.Preview) != 16 || info.Custom == nil {
		t.Fatalf("PutPalette = %+v, %v", info, err)
	}
	if doc := read(t, o.StatePath); len(doc.Palettes) != 1 || doc.Palettes[0].Name != "lagoon" {
		t.Fatalf("state palettes = %+v", doc.Palettes)
	}
	palette.Remove("lagoon")
	newTest(t, o, one(newFake("a", bubble.Music), 1))
	if _, ok := palette.ByName("lagoon"); !ok {
		t.Fatal("palette not registered from the state file")
	}
	if got := c.Palettes(); got[len(got)-1].Name != "lagoon" || got[0].Custom != nil {
		t.Fatalf("Palettes = %+v", got)
	}
}

func TestPutPaletteRejectsBadAndShipped(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	bad := lagoon()
	bad.Axis = "rows"
	if _, err := c.PutPalette(bad); err == nil {
		t.Fatal("bad axis accepted")
	}
	shipped := lagoon()
	shipped.Name = "Rainbow"
	if _, err := c.PutPalette(shipped); !errors.Is(err, palette.ErrShipped) {
		t.Fatalf("err = %v, want ErrShipped", err)
	}
}

func TestDeletePaletteRefusedWhileABubbleUsesIt(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	t.Cleanup(func() { palette.Remove("lagoon") })
	if _, err := c.PutPalette(lagoon()); err != nil {
		t.Fatal(err)
	}
	c.entries[0].tmpl.(*fake).set.Palettes = []string{"lagoon"}
	err := c.DeletePalette("lagoon")
	if !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "bubble a") {
		t.Fatalf("err = %v, want ErrInUse naming bubble a", err)
	}
	c.entries[0].tmpl.(*fake).set.Palettes = nil
	if err := c.DeletePalette("lagoon"); err != nil {
		t.Fatal(err)
	}
	if _, ok := palette.ByName("lagoon"); ok {
		t.Fatal("still registered")
	}
	if err := c.DeletePalette("lagoon"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := c.DeletePalette("rainbow"); !errors.Is(err, palette.ErrShipped) {
		t.Fatalf("shipped delete: %v", err)
	}
}

func TestInvalidStoredPaletteIsSkipped(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"palettes":[{"name":"bad","axis":"rows","stops":[]},{"name":"good","axis":"bands","stops":[{"pos":0,"color":"#000000"},{"pos":1,"color":"#ffffff"}]}]}`)
	t.Cleanup(func() { palette.Remove("good") })
	newTest(t, o, one(newFake("a", bubble.Music), 1))
	if _, ok := palette.ByName("good"); !ok {
		t.Fatal("the valid palette should load")
	}
	if _, ok := palette.ByName("bad"); ok {
		t.Fatal("the invalid palette should be skipped")
	}
}

func TestPaletteChangesClearThePreset(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	t.Cleanup(func() { palette.Remove("lagoon") })
	c.preset = "party"
	if _, err := c.PutPalette(lagoon()); err != nil || c.State().Preset != "party" || !c.State().Modified {
		t.Fatalf("after put: preset %q modified %v (%v)", c.State().Preset, c.State().Modified, err)
	}
	c.preset, c.modified = "party", false
	patch(t, c, `{"music_db":-40}`)
	if s := c.State(); s.Preset != "party" || !s.Modified {
		t.Fatal("a patch should mark the preset modified")
	}
}

func TestNoPresetsSectionLeavesAnEmptyMapNotNil(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"controller":{"music_db":-40}}`)
	c := newTest(t, o, one(newFake("a", bubble.Music), 1))
	if c.presets == nil || len(c.presets) != 0 {
		t.Fatalf("presets = %#v, want a non-nil empty map", c.presets)
	}
}

func TestPresetSaveLoadDelete(t *testing.T) {
	o := stateOpts(t)
	a := newFake("a", bubble.Music)
	c := newTest(t, o, one(a, 1), one(newFake("clock", bubble.Idle), 1))
	patch(t, c, `{"music_db":-10}`)
	four := 4
	if err := c.PatchBubble("a", &four, json.RawMessage(`{"speed":7}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.SavePreset(" party ", nil); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Preset != "party" || s.Modified {
		t.Fatalf("after save: preset %q modified %v", s.Preset, s.Modified)
	}
	if got := c.Presets(); len(got) != 1 || got[0] != "party" {
		t.Fatalf("Presets = %v", got)
	}
	if doc := read(t, o.StatePath); doc.Presets["party"].Bubbles["a"].Weight != 4 {
		t.Fatalf("state presets = %+v", doc.Presets)
	}
	patch(t, c, `{"music_db":-20}`)
	w1 := 1
	if err := c.PatchBubble("a", &w1, json.RawMessage(`{"speed":1}`)); err != nil {
		t.Fatal(err)
	}
	if !c.State().Modified {
		t.Fatal("a change after save should mark the preset modified")
	}
	if err := c.LoadPreset(" party "); err != nil {
		t.Fatal(err)
	}
	s := c.State()
	if s.Preset != "party" || s.Modified || s.Settings.MusicDB != -10 || c.entries[0].Weight != 4 || a.set.Speed != 7 {
		t.Fatalf("after load: preset %q music_db %v weight %d speed %v", s.Preset, s.Settings.MusicDB, c.entries[0].Weight, a.set.Speed)
	}
	patch(t, c, `{"music_db":-11}`)
	if s := c.State(); s.Preset != "party" || !s.Modified {
		t.Fatal("a change should mark the active preset modified")
	}
	c2 := newTest(t, o, one(newFake("a", bubble.Music), 1), one(newFake("clock", bubble.Idle), 1))
	if got := c2.Presets(); len(got) != 1 {
		t.Fatalf("presets after reload = %v", got)
	}
	if err := c.DeletePreset(" party "); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Preset != "" || s.Modified {
		t.Fatalf("after deleting the active preset: preset %q modified %v", s.Preset, s.Modified)
	}
	if err := c.DeletePreset("party"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := c.LoadPreset("party"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load after delete: %v", err)
	}
}

func TestPresetFromBodyAndNameRules(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	if err := c.SavePreset("x", json.RawMessage(`{"controller":{"music_db":-5},"bubbles":{"a":{"weight":2}}}`)); err != nil {
		t.Fatal(err)
	}
	if c.State().Preset != "" {
		t.Fatal("a preset saved from a body is not what runs now")
	}
	if err := c.LoadPreset("x"); err != nil || c.State().Settings.MusicDB != -5 || c.entries[0].Weight != 2 {
		t.Fatalf("load from body: %v, music_db %v, weight %d", err, c.State().Settings.MusicDB, c.entries[0].Weight)
	}
	if err := c.SavePreset("y", json.RawMessage(`{"controller":{},"extra":1}`)); err == nil {
		t.Fatal("unknown top-level field accepted")
	}
	for _, name := range []string{"", "  ", "a/b", strings.Repeat("x", 33)} {
		if err := c.SavePreset(name, nil); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}

func TestPresetSkipsUnknownBubbleAndStalePin(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	if err := c.SavePreset("old", json.RawMessage(`{"controller":{"show":"gone","music_db":-7},"bubbles":{"gone":{"weight":3},"a":{"weight":5}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadPreset("old"); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Settings.MusicDB != -7 || s.Settings.Show != "" || c.entries[0].Weight != 5 {
		t.Fatalf("state = %+v weight %d", s.Settings, c.entries[0].Weight)
	}
}

func TestPresetWithRejectedBubbleSettingsFails(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	if err := c.SavePreset("bad", json.RawMessage(`{"bubbles":{"a":{"weight":1,"settings":{"nope":1}}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadPreset("bad"); err == nil || c.State().Preset == "bad" || !c.State().Modified {
		t.Fatalf("load: %v, preset %q modified %v", err, c.State().Preset, c.State().Modified)
	}
}

func TestDeletePaletteRefusedWhileAPresetUsesIt(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	t.Cleanup(func() { palette.Remove("lagoon") })
	if _, err := c.PutPalette(lagoon()); err != nil {
		t.Fatal(err)
	}
	if err := c.SavePreset("beach", json.RawMessage(`{"bubbles":{"a":{"weight":1,"settings":{"palettes":["lagoon"]}}}}`)); err != nil {
		t.Fatal(err)
	}
	err := c.DeletePalette("lagoon")
	if !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "preset beach") {
		t.Fatalf("err = %v, want ErrInUse naming preset beach", err)
	}
}

func TestPresetCap(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	for i := range 64 {
		if err := c.SavePreset(fmt.Sprint("p", i), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SavePreset("one-more", nil); err == nil {
		t.Fatal("65th preset accepted")
	}
	if err := c.SavePreset("p3", nil); err != nil {
		t.Fatalf("overwrite at the cap: %v", err)
	}
}

func TestPaletteCap(t *testing.T) {
	c := newTest(t, stateOpts(t), one(newFake("a", bubble.Music), 1))
	for i := range 64 {
		cp := lagoon()
		cp.Name = fmt.Sprint("cap", i)
		t.Cleanup(func() { palette.Remove(cp.Name) })
		if _, err := c.PutPalette(cp); err != nil {
			t.Fatal(err)
		}
	}
	cp := lagoon()
	t.Cleanup(func() { palette.Remove("lagoon") })
	if _, err := c.PutPalette(cp); err == nil {
		t.Fatal("65th palette accepted")
	}
	cp.Name = "cap3"
	if _, err := c.PutPalette(cp); err != nil {
		t.Fatalf("overwrite at the cap: %v", err)
	}
}

func TestPanelsRoundTripTheStateFile(t *testing.T) {
	o := stateOpts(t)
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	mustPut(t, c, PanelSpec{Name: "desk", Kind: KindVirtual, W: 64, H: 32, FPS: 60})
	mustPut(t, c, PanelSpec{Name: "office", Kind: KindSerial, Address: "/dev/hub75", FPS: 20, Brightness: 64, IdleBrightness: 32})
	doc := read(t, o.StatePath)
	if len(doc.Panels) != 2 || doc.Panels[1].Baud != 921600 || doc.Panels[0].W != 64 {
		t.Fatalf("panels = %+v", doc.Panels)
	}
	c2 := newBare(t, o, one(newFake("a", bubble.Music), 1))
	ps := c2.Panels()
	if len(ps) != 2 || ps[0].Name != "desk" || ps[1].Name != "office" || ps[1].IdleBrightness != 32 || ps[1].Connected {
		t.Fatalf("reloaded panels = %+v", ps)
	}
	if err := c2.DeletePanel("desk"); err != nil {
		t.Fatal(err)
	}
	if err := c2.DeletePanel("office"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readRaw(t, o.StatePath)), `"panels": []`) {
		t.Fatal("an empty panel list is not written: the seed would come back")
	}
	c3 := newBare(t, o, one(newFake("a", bubble.Music), 1))
	if n := len(c3.Panels()); n != 0 {
		t.Fatalf("panels after deleting all = %d, want 0 and no reseed", n)
	}
}

func readRaw(t *testing.T, path string) []byte {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

func TestSeedAndBrightnessMigration(t *testing.T) {
	o := stateOpts(t)
	o.Seed = &PanelSpec{Name: "panel", Kind: KindSerial, Address: "/dev/hub75", Baud: 921600, FPS: 20, Brightness: 64, IdleBrightness: 64}
	write(t, o.StatePath, `{"controller":{"music_db":-45,"brightness":100,"idle_brightness":7},
		"presets":{"p":{"controller":{"music_db":-40,"brightness":3}}}}`)
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	ps := c.Panels()
	if len(ps) != 1 || ps[0].Name != "panel" || ps[0].Brightness != 100 || ps[0].IdleBrightness != 7 {
		t.Fatalf("panels = %+v, want the seed with the file's brightness", ps)
	}
	if c.set.MusicDB != -45 {
		t.Fatalf("music_db = %v: the controller section was dropped over the old keys", c.set.MusicDB)
	}
	if err := c.LoadPreset("p"); err != nil || c.set.MusicDB != -40 {
		t.Fatalf("preset with an old brightness: %v, music_db = %v", err, c.set.MusicDB)
	}
	doc := read(t, o.StatePath)
	if strings.Contains(string(doc.Controller), "brightness") || strings.Contains(string(doc.Presets["p"].Controller), "brightness") {
		t.Fatalf("brightness keys survived the save: %s / %s", doc.Controller, doc.Presets["p"].Controller)
	}
	// no seed and no section: nothing
	o2 := stateOpts(t)
	write(t, o2.StatePath, `{}`)
	if c := newBare(t, o2, one(newFake("a", bubble.Music), 1)); len(c.Panels()) != 0 {
		t.Fatal("a panel without a seed")
	}
	// a seed without any state file
	o3 := Options{FPS: 30, Seed: o.Seed}
	if c := newBare(t, o3, one(newFake("a", bubble.Music), 1)); len(c.Panels()) != 1 {
		t.Fatal("the seed did not apply without a state file")
	}
}

// A stored panel named like the seed wins over the flags (with a warning).
func TestStoredPanelWinsOverTheSeed(t *testing.T) {
	o := stateOpts(t)
	o.Seed = &PanelSpec{Name: "panel", Kind: KindSerial, Address: "/dev/hub75", Baud: 921600, FPS: 20, Brightness: 64, IdleBrightness: 64}
	write(t, o.StatePath, `{"panels":[{"name":"panel","kind":"serial","address":"tcp://esp:4000","baud":921600,"fps":30,"brightness":10}]}`)
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	if ps := c.Panels(); len(ps) != 1 || ps[0].Address != "tcp://esp:4000" || ps[0].FPS != 30 {
		t.Fatalf("panels = %+v, want the file's panel", ps)
	}
}

func TestInvalidStoredPanelIsSkipped(t *testing.T) {
	o := stateOpts(t)
	write(t, o.StatePath, `{"panels":[{"name":"bad","kind":"virtual","w":0,"h":2,"fps":30},{"name":"ok","kind":"virtual","w":2,"h":2,"fps":30},{"name":"ok","kind":"virtual","w":3,"h":3,"fps":30}]}`)
	c := newBare(t, o, one(newFake("a", bubble.Music), 1))
	ps := c.Panels()
	if len(ps) != 1 || ps[0].Name != "ok" || ps[0].W != 2 {
		t.Fatalf("panels = %+v, want only the first ok", ps)
	}
}
