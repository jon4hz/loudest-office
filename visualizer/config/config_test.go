package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func load(t *testing.T, yaml string, args ...string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	f := pflag.NewFlagSet("spectrum", pflag.ContinueOnError)
	Flags(f)
	if err := f.Parse(append([]string{"--config", path}, args...)); err != nil {
		t.Fatal(err)
	}
	return Load(f)
}

func TestPrecedence(t *testing.T) {
	t.Setenv("SPECTRUM_BANDS", "16")
	t.Setenv("SPECTRUM_GAIN", "3")
	t.Setenv("SPECTRUM_LOOP_LAYOUTS", "side,mirror")
	c, err := load(t, "rate: 48000\nbands: 8\ngain: 8\nloop: 60s\nmono: true\nbrightness: 200\nloop_modes: [bars, fire]\n", "--gain", "6")
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{Cmd: "parec", Device: "@DEFAULT_SINK@.monitor", Rate: 48000, Bands: 16, Gain: 6, Mono: true,
		Palette: "rainbow", Layout: "stacked", Peaks: "fall", Mode: "bars", Loop: 60 * time.Second,
		LoopLayouts: []string{"side", "mirror"}, LoopModes: []string{"bars", "fire"}, Baud: 921600, Brightness: 200}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
}

func TestBrightnessRange(t *testing.T) {
	if _, err := load(t, "brightness: 300\n"); err == nil {
		t.Error("brightness 300 accepted")
	}
}

func TestUnknownKey(t *testing.T) {
	if _, err := load(t, "loop_mode: [bars]\n"); err == nil || !strings.Contains(err.Error(), "loop_mode") {
		t.Errorf("err = %v, want unknown key loop_mode", err)
	}
}
