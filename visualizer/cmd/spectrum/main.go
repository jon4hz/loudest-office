// spectrum: terminal spectrum analyzer of the default audio output.
//
//	spectrum                       # default sink monitor, 32 bands, rainbow
//	spectrum -bands 16 -palette tribar -gain 6
//	spectrum -mono                 # one spectrum, channels mixed down
//	spectrum -autogain             # loudest band always fills the display
//	spectrum -layout mirror        # stacked (default), side, mirror or hmirror
//	spectrum -mode life            # bars (default), fire, life, stars, fireworks or parrot
//	spectrum -trails               # bars fade out instead of vanishing
//	spectrum -cmd arecord -device hw:0   # on the Pi
//
// Keys: q quit, c/C next/previous palette, l next layout, m next mode, t trails, +/- bands.
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/jon4hz/loudest-office/visualizer/audio"
	"github.com/jon4hz/loudest-office/visualizer/spectrum"
)

type captureDone struct{}

type loopTick struct{ gen int } // gen guards against ticks from a loop that was toggled off and on

// flavor is one look the auto loop can pick.
type flavor struct {
	pal    int
	lay    spectrum.Layout
	peaks  spectrum.PeakStyle
	mode   spectrum.Mode
	trails bool
}

// nextFlavor draws a random flavor, with the layout and mode taken from the
// lists (a mode listed twice is twice as likely) and, for bars, trails on one
// time in twenty, that differs from cur in at least one part. A palette that
// hides the bars is never paired with hidden peaks: that would draw nothing.
func nextFlavor(cur flavor, layouts []spectrum.Layout, modes []spectrum.Mode) flavor {
	for {
		f := flavor{rand.IntN(len(spectrum.Palettes)), layouts[rand.IntN(len(layouts))],
			spectrum.PeakStyle(rand.IntN(int(spectrum.NoPeaks) + 1)), modes[rand.IntN(len(modes))], false}
		f.trails = f.mode == spectrum.Bars && rand.IntN(20) == 0
		bar, _ := spectrum.Palettes[f.pal].At(0, 1, 0, 1)
		if f != cur && (bar.A != 0 || f.peaks != spectrum.NoPeaks) {
			return f
		}
	}
}

type app struct {
	spec     spectrum.Model
	cap      *audio.Capture
	pal      int
	loop     time.Duration // 0 = off
	interval time.Duration
	layouts  []spectrum.Layout // the auto loop picks from these
	modes    []spectrum.Mode   // and from these, repeats weigh
	gen      int
	err      error
}

func (a *app) apply(f flavor) {
	a.pal = f.pal
	a.spec.SetPalette(spectrum.Palettes[f.pal])
	a.spec.SetLayout(f.lay)
	a.spec.SetPeakStyle(f.peaks)
	a.spec.SetMode(f.mode)
	a.spec.SetTrails(f.trails)
}

func (a app) flavor() flavor {
	return flavor{a.pal, a.spec.Layout(), a.spec.PeakStyle(), a.spec.Mode(), a.spec.Trails()}
}

func (a app) tickLoop() tea.Cmd {
	if a.loop == 0 {
		return nil
	}
	gen := a.gen
	return tea.Tick(a.loop, func(time.Time) tea.Msg { return loopTick{gen} })
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

func (a app) Init() tea.Cmd { return tea.Batch(a.wait(), a.tickLoop()) }

func (a app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "c":
			a.pal = (a.pal + 1) % len(spectrum.Palettes)
			a.spec.SetPalette(spectrum.Palettes[a.pal])
		case "C":
			a.pal = (a.pal + len(spectrum.Palettes) - 1) % len(spectrum.Palettes)
			a.spec.SetPalette(spectrum.Palettes[a.pal])
		case "l":
			a.spec.SetLayout((a.spec.Layout() + 1) % (spectrum.HMirrored + 1))
		case "p":
			a.spec.SetPeakStyle((a.spec.PeakStyle() + 1) % (spectrum.NoPeaks + 1))
		case "m":
			a.spec.SetMode((a.spec.Mode() + 1) % (spectrum.Parrot + 1))
		case "t":
			a.spec.SetTrails(!a.spec.Trails())
		case "a":
			a.gen++
			if a.loop != 0 {
				a.loop = 0
				return a, nil
			}
			a.loop = a.interval
			a.apply(nextFlavor(a.flavor(), a.layouts, a.modes))
			return a, a.tickLoop()
		case "+", "=":
			a.spec.SetBands(min(64, a.spec.NumBands()+8))
		case "-":
			a.spec.SetBands(max(8, a.spec.NumBands()-8))
		}
		return a, nil
	case loopTick:
		if a.loop == 0 || msg.gen != a.gen {
			return a, nil
		}
		a.apply(nextFlavor(a.flavor(), a.layouts, a.modes))
		return a, a.tickLoop()
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
	var (
		cfg                                         audio.Config
		bands                                       int
		gain                                        float64
		mono, autoGain, trails                      bool
		palette, layoutName, peaksName, loopLayouts string
		modeName, loopModes                         string
		loop                                        time.Duration
	)
	names := make([]string, len(spectrum.Palettes))
	for i, p := range spectrum.Palettes {
		names[i] = p.Name
	}
	cmd := &cobra.Command{
		Use:   "spectrum",
		Short: "Terminal spectrum analyzer of the default audio output",
		Long: `Terminal spectrum analyzer of the default audio output.

Keys: q quit, c/C next/previous palette, l next layout, p next peak style, m next mode, t toggle trails, a toggle auto loop, +/- bands.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), cfg, bands, gain, mono, autoGain, trails, palette, layoutName, peaksName, modeName, loopLayouts, loopModes, loop)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Command, "cmd", "parec", "capture command: parec or arecord")
	f.StringVar(&cfg.Device, "device", "@DEFAULT_SINK@.monitor", "capture device")
	f.IntVar(&cfg.Rate, "rate", 44100, "sample rate")
	f.IntVar(&bands, "bands", 32, "number of bands")
	f.Float64Var(&gain, "gain", 0, "gain in dB")
	f.BoolVar(&mono, "mono", false, "mix down to one spectrum")
	f.BoolVar(&autoGain, "autogain", false, "adapt gain so the loudest band fills the display")
	f.StringVar(&palette, "palette", "rainbow", "palette: "+strings.Join(names, ", "))
	f.StringVar(&layoutName, "layout", "stacked", "channel layout: stacked, side, mirror or hmirror")
	f.StringVar(&peaksName, "peaks", "fall", "peak style: fall, fly, beat or none")
	f.StringVar(&modeName, "mode", "bars", "visualization: bars, fire, life, stars, fireworks or parrot")
	f.BoolVar(&trails, "trails", false, "fade bars out instead of clearing them")
	f.DurationVar(&loop, "loop", 0, "auto loop: pick a random palette/layout/peak style at this interval (0 = off; the a key toggles it at 10s)")
	f.StringVar(&loopLayouts, "loop-layouts", "mirror,hmirror", "comma-separated layouts the auto loop picks from")
	f.StringVar(&loopModes, "loop-modes", "bars,bars,bars,fire,life,stars,fireworks,parrot", "comma-separated modes the auto loop picks from; repeat one to make it likelier")
	if err := fang.Execute(context.Background(), cmd); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg audio.Config, bands int, gain float64, mono, autoGain, trails bool, palette, layoutName, peaksName, modeName, loopLayouts, loopModes string, loop time.Duration) error {
	layout, ok := spectrum.ParseLayout(layoutName)
	if !ok {
		return fmt.Errorf("unknown layout: %s", layoutName)
	}
	mode, ok := spectrum.ParseMode(modeName)
	if !ok {
		return fmt.Errorf("unknown mode: %s", modeName)
	}
	var modes []spectrum.Mode
	for name := range strings.SplitSeq(loopModes, ",") {
		m, ok := spectrum.ParseMode(strings.TrimSpace(name))
		if !ok {
			return fmt.Errorf("unknown mode in --loop-modes: %s", name)
		}
		modes = append(modes, m)
	}
	var layouts []spectrum.Layout
	for name := range strings.SplitSeq(loopLayouts, ",") {
		l, ok := spectrum.ParseLayout(strings.TrimSpace(name))
		if !ok {
			return fmt.Errorf("unknown layout in --loop-layouts: %s", name)
		}
		layouts = append(layouts, l)
	}
	peaks, ok := spectrum.ParsePeakStyle(peaksName)
	if !ok {
		return fmt.Errorf("unknown peak style: %s", peaksName)
	}
	pal := -1
	for i, p := range spectrum.Palettes {
		if strings.EqualFold(p.Name, palette) {
			pal = i
		}
	}
	if pal < 0 {
		return fmt.Errorf("unknown palette: %s", palette)
	}
	cfg.Channels = 2
	if mono {
		cfg.Channels = 1 // parec/arecord do the downmix
	}
	cap, err := audio.Start(ctx, cfg)
	if err != nil {
		return err
	}
	interval := loop
	if interval == 0 {
		interval = 10 * time.Second
	}
	a := app{cap: cap, pal: pal, loop: loop, interval: interval, layouts: layouts, modes: modes, spec: spectrum.New(
		spectrum.Bands(bands), spectrum.Channels(cfg.Channels), spectrum.Rate(cfg.Rate), spectrum.Gain(gain),
		spectrum.AutoGain(autoGain), spectrum.WithPalette(spectrum.Palettes[pal]), spectrum.WithLayout(layout),
		spectrum.WithPeakStyle(peaks), spectrum.WithMode(mode), spectrum.WithTrails(trails))}
	if loop != 0 { // start on a loop flavor instead of waiting for the first tick
		a.apply(nextFlavor(flavor{pal, layout, peaks, mode, trails}, layouts, modes))
	}
	final, err := tea.NewProgram(a, tea.WithContext(ctx)).Run()
	cap.Stop()
	for range cap.Blocks() {
	}
	if err == nil {
		err = final.(app).err
	}
	return err
}
