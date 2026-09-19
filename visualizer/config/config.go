// Package config loads the spectrum settings from, in order of precedence,
// flags, SPECTRUM_* environment variables, a YAML file and the flag defaults.
// The config keys are the flag names with - as _: --loop-modes is loop_modes
// in the file and SPECTRUM_LOOP_MODES in the environment.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/jon4hz/loudest-office/visualizer/spectrum"
)

type Config struct {
	Cmd         string        `mapstructure:"cmd"`
	Device      string        `mapstructure:"device"`
	Rate        int           `mapstructure:"rate"`
	Bands       int           `mapstructure:"bands"`
	Gain        float64       `mapstructure:"gain"`
	Mono        bool          `mapstructure:"mono"`
	AutoGain    bool          `mapstructure:"autogain"`
	Palette     string        `mapstructure:"palette"`
	Layout      string        `mapstructure:"layout"`
	Peaks       string        `mapstructure:"peaks"`
	Mode        string        `mapstructure:"mode"`
	Trails      bool          `mapstructure:"trails"`
	Loop        time.Duration `mapstructure:"loop"`
	LoopLayouts []string      `mapstructure:"loop_layouts"`
	LoopModes   []string      `mapstructure:"loop_modes"`
	Serial      string        `mapstructure:"serial"`
	Baud        int           `mapstructure:"baud"`
	Brightness  uint8         `mapstructure:"brightness"`
}

// Flags registers one flag per Config field, and --config, on f.
func Flags(f *pflag.FlagSet) {
	names := make([]string, len(spectrum.Palettes))
	for i, p := range spectrum.Palettes {
		names[i] = p.Name
	}
	f.StringP("config", "c", "", "config file (default: config.yaml in ., ~/.config/spectrum or /etc/spectrum)")
	f.String("cmd", "parec", "capture command: parec or arecord")
	f.String("device", "@DEFAULT_SINK@.monitor", "capture device")
	f.Int("rate", 44100, "sample rate")
	f.Int("bands", 32, "number of bands")
	f.Float64("gain", 0, "gain in dB")
	f.Bool("mono", false, "mix down to one spectrum")
	f.Bool("autogain", false, "adapt gain so the loudest band fills the display")
	f.String("palette", "rainbow", "palette: "+strings.Join(names, ", "))
	f.String("layout", "stacked", "channel layout: stacked, side, mirror or hmirror")
	f.String("peaks", "fall", "peak style: fall, fly, beat or none")
	f.String("mode", "bars", "visualization: bars, fire, life, stars, fireworks or parrot")
	f.Bool("trails", false, "fade bars out instead of clearing them")
	f.Duration("loop", 0, "auto loop: pick a random palette/layout/peak style at this interval (0 = off; the a key toggles it at 10s)")
	f.StringSlice("loop-layouts", []string{"mirror", "hmirror"}, "comma-separated layouts the auto loop picks from")
	f.StringSlice("loop-modes", []string{"bars", "bars", "bars", "fire", "life", "stars", "fireworks", "parrot"}, "comma-separated modes the auto loop picks from; repeat one to make it likelier")
	f.String("serial", "", "serial port of the ESP32 driving the HUB75 panel, e.g. /dev/ttyUSB0 (the frame then has the panel's size)")
	f.Int("baud", 921600, "serial baud rate, must match the firmware")
	f.Uint8("brightness", 64, "panel brightness, 0-255")
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
	if path, _ := f.GetString("config"); path != "" {
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

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &c, nil
}
