package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	t.Setenv("SPECTRUM_STATE", "/env/state.json")
	c, err := load(t, "rate: 48000\nbands: 8\ngain: 8\nmono: true\nbrightness: 200\nfps: 20\nlisten: :8099\nstate: /yaml/state.json\n", "--gain", "6")
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{Cmd: "parec", Device: "@DEFAULT_SINK@.monitor", Rate: 48000, Bands: 16, Gain: 6, Mono: true,
		FPS: 20, Listen: ":8099", State: "/env/state.json", Baud: 921600, Brightness: 200}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
}

func TestBrightnessRange(t *testing.T) {
	if _, err := load(t, "brightness: 300\n"); err == nil {
		t.Error("brightness 300 accepted")
	}
}

func TestFPSRange(t *testing.T) {
	if _, err := load(t, "fps: 0\n"); err == nil {
		t.Error("fps 0 accepted")
	}
	if _, err := load(t, "fps: 121\n"); err == nil {
		t.Error("fps 121 accepted")
	}
}

func TestBandsRange(t *testing.T) {
	if _, err := load(t, "bands: 0\n"); err == nil {
		t.Error("bands 0 accepted")
	}
	if _, err := load(t, "bands: 65\n"); err == nil {
		t.Error("bands 65 accepted")
	}
}

func TestUnknownKey(t *testing.T) {
	if _, err := load(t, "loop_modes: [bars]\n"); err == nil || !strings.Contains(err.Error(), "loop_modes") {
		t.Errorf("err = %v, want unknown key loop_modes", err)
	}
}

func TestArgs(t *testing.T) {
	c, err := load(t, "cmd: sendspin-pipe\nargs: [--server, \"ma.home:8927\", --delay-ms, \"40\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--server", "ma.home:8927", "--delay-ms", "40"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args = %q, want %q", c.Args, want)
	}
	if c, _ = load(t, ""); c.Args != nil { // nil, not empty: audio.Config then builds the parec arguments
		t.Errorf("args = %#v, want nil", c.Args)
	}
}

func TestSendspinPipeNeedsArgs(t *testing.T) {
	if _, err := load(t, "cmd: sendspin-pipe\n"); err == nil || !strings.Contains(err.Error(), "needs args") {
		t.Errorf("err = %v, want a needs-args error", err)
	}
}

func TestSendspinPipeRateMustBe44100(t *testing.T) {
	yaml := "cmd: sendspin-pipe\nargs: [--server, \"ma.home:8927\"]\nrate: 48000\n"
	if _, err := load(t, yaml); err == nil || !strings.Contains(err.Error(), "44100") {
		t.Errorf("err = %v, want a rate error", err)
	}
}

func TestSendspinPipeMustNotBeMono(t *testing.T) {
	yaml := "cmd: sendspin-pipe\nargs: [--server, \"ma.home:8927\"]\nmono: true\n"
	if _, err := load(t, yaml); err == nil || !strings.Contains(err.Error(), "mono") {
		t.Errorf("err = %v, want a mono error", err)
	}
}

func TestSendspinPipeAccepted(t *testing.T) {
	yaml := "cmd: /usr/local/bin/sendspin-pipe\nargs: [--server, \"ma.home:8927\"]\n"
	c, err := load(t, yaml)
	if err != nil {
		t.Fatal(err)
	}
	if c.Rate != 44100 || c.Mono {
		t.Errorf("got rate=%d mono=%v, want defaults", c.Rate, c.Mono)
	}
}

func TestMusicAssistant(t *testing.T) {
	t.Setenv("SPECTRUM_MA_TOKEN", "from-env")
	c, err := load(t, "ma_url: http://ma.home:8095/\nma_player: visualizer\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.MAURL != "http://ma.home:8095" || c.MAToken != "from-env" || c.MAPlayer != "visualizer" {
		t.Errorf("got %q %q %q", c.MAURL, c.MAToken, c.MAPlayer)
	}
}

func TestMusicAssistantNeedsTokenAndPlayer(t *testing.T) {
	if _, err := load(t, "ma_url: http://ma.home:8095\nma_player: visualizer\n"); err == nil || !strings.Contains(err.Error(), "ma_token") {
		t.Errorf("no token: %v, want an error naming ma_token", err)
	}
	if _, err := load(t, "ma_url: http://ma.home:8095\nma_token: x\n"); err == nil || !strings.Contains(err.Error(), "ma_player") {
		t.Errorf("no player: %v, want an error naming ma_player", err)
	}
	if _, err := load(t, "ma_url: ma.home:8095\nma_token: x\nma_player: y\n"); err == nil {
		t.Error("a URL without http:// was accepted")
	}
}

func TestMusicAssistantTrimsTokenAndPlayer(t *testing.T) {
	c, err := load(t, "ma_url: http://ma.home:8095\nma_token: \"tok\\n\"\nma_player: \" visualizer \"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.MAToken != "tok" || c.MAPlayer != "visualizer" {
		t.Errorf("got token %q player %q, want trimmed", c.MAToken, c.MAPlayer)
	}
	if _, err := load(t, "ma_url: http://ma.home:8095\nma_token: \"   \"\nma_player: visualizer\n"); err == nil || !strings.Contains(err.Error(), "ma_token") {
		t.Errorf("whitespace-only token: %v, want an error naming ma_token", err)
	}
	vault := "ma_url: http://ma.home:8095\nma_token: |\n  $ANSIBLE_VAULT;1.1;AES256\n  3334\nma_player: visualizer\n"
	if _, err := load(t, vault); err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Errorf("undecrypted vault block as token: %v, want an error naming whitespace", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "options.json") // the Home Assistant app's options file: JSON is YAML
	if err := os.WriteFile(env, []byte(`{"bands": 12, "listen": ":8099"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPECTRUM_CONFIG", env)
	f := pflag.NewFlagSet("spectrum", pflag.ContinueOnError)
	Flags(f)
	if err := f.Parse(nil); err != nil {
		t.Fatal(err)
	}
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Bands != 12 || c.Listen != ":8099" {
		t.Errorf("got bands %d, listen %q: the file named by SPECTRUM_CONFIG was not read", c.Bands, c.Listen)
	}

	if c, err = load(t, "bands: 7\n"); err != nil { // load passes --config: the flag wins
		t.Fatal(err)
	} else if c.Bands != 7 {
		t.Errorf("bands = %d, want the --config file's 7", c.Bands)
	}
}
