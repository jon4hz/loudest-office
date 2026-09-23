// Package config loads the spectrum settings from, in order of precedence,
// flags, SPECTRUM_* environment variables, a YAML file and the flag defaults.
// The config keys are the flag names, with - as _ in the file and as
// SPECTRUM_* in the environment (e.g. --autogain is autogain / SPECTRUM_AUTOGAIN).
package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	Cmd        string   `mapstructure:"cmd"`
	Device     string   `mapstructure:"device"`
	Args       []string `mapstructure:"args"`
	Rate       int      `mapstructure:"rate"`
	Bands      int      `mapstructure:"bands"`
	Gain       float64  `mapstructure:"gain"`
	Mono       bool     `mapstructure:"mono"`
	AutoGain   bool     `mapstructure:"autogain"`
	Serial     string   `mapstructure:"serial"`
	Baud       int      `mapstructure:"baud"`
	Brightness uint8    `mapstructure:"brightness"`
	Listen     string   `mapstructure:"listen"`
	State      string   `mapstructure:"state"`
	FPS        int      `mapstructure:"fps"`
	Show       string   `mapstructure:"show"`
	Tronbyt    string   `mapstructure:"tronbyt"`
	MAURL      string   `mapstructure:"ma_url"`
	MAToken    string   `mapstructure:"ma_token"`
	MAPlayer   string   `mapstructure:"ma_player"`
}

// Flags registers one flag per Config field, and --config, on f.
func Flags(f *pflag.FlagSet) {
	f.StringP("config", "c", "", "config file (default: config.yaml in ., ~/.config/spectrum or /etc/spectrum)")
	f.String("cmd", "parec", "capture command: parec, arecord, or any command that writes S16LE to stdout (then set args)")
	f.String("device", "@DEFAULT_SINK@.monitor", "capture device")
	f.StringSlice("args", nil, "arguments of the capture command, instead of the generated ones")
	f.Int("rate", 44100, "sample rate")
	f.Int("bands", 32, "number of bands")
	f.Float64("gain", 0, "gain in dB")
	f.Bool("mono", false, "mix down to one spectrum")
	f.Bool("autogain", false, "adapt gain so the loudest band fills the display")
	f.String("serial", "", "panel: serial port of the ESP32, e.g. /dev/ttyUSB0, or tcp://host:7090 for the ESPHome firmware (the frame then has the panel's size)")
	f.Int("baud", 921600, "serial baud rate, must match the firmware")
	f.Uint8("brightness", 64, "panel brightness, 0-255")
	f.String("listen", "", "HTTP API address, e.g. :8099 (empty = off, there is no auth)")
	f.String("state", "", "JSON file the API settings persist to (empty = not saved)")
	f.Int("fps", 30, "frame rate")
	f.String("show", "", "pin this bubble at start")
	f.String("tronbyt", "", "idle: play this Tronbyt device, http://<server>:8000/<device id>/next (empty = the clock)")
	f.String("ma-url", "", "now playing: Music Assistant, e.g. http://127.0.0.1:8095 (empty = off)")
	f.String("ma-token", "", "now playing: a long-lived Music Assistant token; better as SPECTRUM_MA_TOKEN than as a flag")
	f.String("ma-player", "", "now playing: the ID of the player whose track is shown")
}

// Load resolves the config from f, which must hold the flags of Flags.
func Load(f *pflag.FlagSet) (*Config, error) {
	v := viper.New()
	var err error
	f.VisitAll(func(fl *pflag.Flag) {
		if e := v.BindPFlag(strings.ReplaceAll(fl.Name, "-", "_"), fl); e != nil {
			err = e
		}
	})
	if err != nil {
		return nil, err
	}
	v.SetEnvPrefix("SPECTRUM")
	v.AutomaticEnv()

	v.SetConfigType("yaml")
	if path := v.GetString("config"); path != "" { // --config, else SPECTRUM_CONFIG
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.config/spectrum")
		v.AddConfigPath("/etc/spectrum")
	}
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}
	for _, k := range v.AllKeys() { // a typo in the file must not go unnoticed
		if f.Lookup(strings.ReplaceAll(k, "_", "-")) == nil {
			return nil, fmt.Errorf("%s: unknown key: %s", v.ConfigFileUsed(), k)
		}
	}

	if b := v.GetInt("brightness"); b < 0 || b > 255 { // Unmarshal would wrap it into the uint8
		return nil, fmt.Errorf("brightness out of range 0-255: %d", b)
	}
	if fps := v.GetInt("fps"); fps < 1 || fps > 120 {
		return nil, fmt.Errorf("fps out of range 1-120: %d", fps)
	}
	if bands := v.GetInt("bands"); bands < 1 || bands > 64 { // 0 bands panics fire/life/fireworks
		return nil, fmt.Errorf("bands out of range 1-64: %d", bands)
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	if len(c.Args) == 0 {
		c.Args = nil // audio.Config generates the arguments only for nil
	}
	if filepath.Base(c.Cmd) == "sendspin-pipe" { // it always emits 44100 Hz stereo, unlike parec/arecord
		if len(c.Args) == 0 {
			return nil, fmt.Errorf("sendspin-pipe needs args, e.g. [--server, host:8927]")
		}
		if c.Rate != 44100 {
			return nil, fmt.Errorf("sendspin-pipe emits 44100 Hz: set rate: 44100")
		}
		if c.Mono {
			return nil, fmt.Errorf("sendspin-pipe emits stereo: mono must be off")
		}
	}
	c.MAToken = strings.TrimSpace(c.MAToken)
	c.MAPlayer = strings.TrimSpace(c.MAPlayer)
	if c.MAURL != "" {
		if u, err := url.Parse(c.MAURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("ma_url must be http(s)://host:port, got %q", c.MAURL)
		}
		c.MAURL = strings.TrimRight(c.MAURL, "/")
		if c.MAToken == "" {
			return nil, fmt.Errorf("ma_url needs ma_token, a long-lived Music Assistant token")
		}
		if strings.ContainsAny(c.MAToken, " \t\r\n") { // e.g. a vault block that was not decrypted
			return nil, fmt.Errorf("ma_token contains whitespace: not a Music Assistant token")
		}
		if c.MAPlayer == "" {
			return nil, fmt.Errorf("ma_url needs ma_player, the Music Assistant player ID")
		}
	}
	return &c, nil
}
