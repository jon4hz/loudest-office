// spectrum: terminal spectrum analyzer of the default audio output.
//
//	spectrum                       # default sink monitor, 32 bands, rainbow
//	spectrum -bands 16 -palette tribar -gain 6
//	spectrum -mono                 # one spectrum, channels mixed down
//	spectrum -autogain             # loudest band always fills the display
//	spectrum -cmd arecord -device hw:0   # on the Pi
//
// Keys: q quit, c next palette, +/- bands.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/audio"
	"github.com/jon4hz/loudest-office/visualizer/spectrum"
)

type captureDone struct{}

type app struct {
	spec spectrum.Model
	cap  *audio.Capture
	pal  int
	err  error
}

func (a app) wait() tea.Cmd {
	return func() tea.Msg {
		blk, ok := <-a.cap.Blocks()
		if !ok {
			return captureDone{}
		}
		return spectrum.SamplesMsg(blk)
	}
}

func (a app) Init() tea.Cmd { return a.wait() }

func (a app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "c":
			a.pal = (a.pal + 1) % len(spectrum.Palettes)
			a.spec.SetPalette(spectrum.Palettes[a.pal])
		case "+", "=":
			a.spec.SetBands(min(64, a.spec.NumBands()+8))
		case "-":
			a.spec.SetBands(max(8, a.spec.NumBands()-8))
		}
		return a, nil
	case captureDone:
		a.err = a.cap.Wait()
		return a, tea.Quit
	case spectrum.SamplesMsg:
		a.spec, _ = a.spec.Update(msg)
		return a, a.wait()
	}
	a.spec, _ = a.spec.Update(msg)
	return a, nil
}

func (a app) View() tea.View {
	v := tea.NewView(a.spec.View())
	v.AltScreen = true
	return v
}

func main() {
	var cfg audio.Config
	flag.StringVar(&cfg.Command, "cmd", "parec", "capture command: parec or arecord")
	flag.StringVar(&cfg.Device, "device", "@DEFAULT_SINK@.monitor", "capture device")
	flag.IntVar(&cfg.Rate, "rate", 44100, "sample rate")
	bands := flag.Int("bands", 32, "number of bands")
	gain := flag.Float64("gain", 0, "gain in dB")
	mono := flag.Bool("mono", false, "mix down to one spectrum")
	autoGain := flag.Bool("autogain", false, "adapt gain so the loudest band fills the display")
	names := make([]string, len(spectrum.Palettes))
	for i, p := range spectrum.Palettes {
		names[i] = p.Name
	}
	palette := flag.String("palette", "rainbow", "palette: "+strings.Join(names, ", "))
	flag.Parse()

	pal := 0
	for i, p := range spectrum.Palettes {
		if strings.EqualFold(p.Name, *palette) {
			pal = i
		}
	}
	cfg.Channels = 2
	if *mono {
		cfg.Channels = 1 // parec/arecord do the downmix
	}
	cap, err := audio.Start(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	a := app{cap: cap, pal: pal, spec: spectrum.New(
		spectrum.Bands(*bands), spectrum.Channels(cfg.Channels), spectrum.Rate(cfg.Rate), spectrum.Gain(*gain), spectrum.AutoGain(*autoGain),
		spectrum.WithPalette(spectrum.Palettes[pal]))}
	final, err := tea.NewProgram(a).Run()
	cap.Stop()
	for range cap.Blocks() {
	}
	if err == nil {
		err = final.(app).err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
