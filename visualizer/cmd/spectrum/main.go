// spectrum: audio visualizer of the default audio output, on the HUB75 panel
// and/or the terminal.
//
//	spectrum                       # default sink monitor, 32 bands, terminal only
//	spectrum -bands 16 -gain 6
//	spectrum -mono                 # one spectrum, channels mixed down
//	spectrum -autogain             # loudest band always fills the display
//	spectrum -serial /dev/ttyUSB0  # also stream to the HUB75 panel behind an ESP32
//	spectrum -cmd arecord -device hw:0   # on the Pi
//	spectrum -listen :8099 -state state.json  # HTTP API, settings persisted there
//	spectrum watch <pi>:8099       # mirror a running spectrum's frames in this terminal
//	spectrum -show fire            # pin a bubble at start, without saving it
//	spectrum -ma-url http://ma:8095 -ma-player visualizer  # new tracks show cover, title, artist; token: SPECTRUM_MA_TOKEN
//	spectrum -config spectrum.yaml # flags as YAML keys, - as _; SPECTRUM_* env vars work too
//
// Keys: q quit, m pin the next bubble, a clear the pin / toggle the auto loop, +/- bands;
// the shown bubble: c/C palette, bars: l layout, p peak style, t trails.
package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"net"
	"net/http"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/jon4hz/loudest-office/visualizer/alert"
	"github.com/jon4hz/loudest-office/visualizer/api"
	"github.com/jon4hz/loudest-office/visualizer/audio"
	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/clock"
	"github.com/jon4hz/loudest-office/visualizer/config"
	"github.com/jon4hz/loudest-office/visualizer/controller"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/bars"
	"github.com/jon4hz/loudest-office/visualizer/music/fire"
	"github.com/jon4hz/loudest-office/visualizer/music/fireworks"
	"github.com/jon4hz/loudest-office/visualizer/music/invaders"
	"github.com/jon4hz/loudest-office/visualizer/music/life"
	"github.com/jon4hz/loudest-office/visualizer/music/parrot"
	"github.com/jon4hz/loudest-office/visualizer/music/plasma"
	"github.com/jon4hz/loudest-office/visualizer/music/polygon"
	"github.com/jon4hz/loudest-office/visualizer/music/ripples"
	"github.com/jon4hz/loudest-office/visualizer/music/stars"
	"github.com/jon4hz/loudest-office/visualizer/nowplaying"
	"github.com/jon4hz/loudest-office/visualizer/serial"
	"github.com/jon4hz/loudest-office/visualizer/tronbyt"
	"github.com/jon4hz/loudest-office/visualizer/ws"
)

func main() {
	cmd := &cobra.Command{
		Use:   "spectrum",
		Short: "Audio visualizer of the default audio output",
		Long: `Audio visualizer of the default audio output, on the HUB75 panel and/or the terminal.

Every flag can also be set in a YAML config file (see --config) or as a SPECTRUM_*
environment variable, with - as _.

Keys: q quit, m pin the next bubble, a clear the pin / toggle the auto loop, +/- bands.
The shown bubble takes the rest: c/C palette, bars l layout, p peak style, t trails.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(cmd.Flags())
			if err != nil {
				return err
			}
			return run(cmd.Context(), c)
		},
	}
	config.Flags(cmd.Flags())
	cmd.AddCommand(&cobra.Command{
		Use:   "watch host:port",
		Short: "Mirror the frames of a running spectrum in this terminal",
		Long: `Mirror the frames of a running spectrum in this terminal, over the websocket
at GET /api/v1/frames of its --listen port. host:port, or a full ws:// URL.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return watch(cmd.Context(), args[0]) },
	})
	if err := fang.Execute(context.Background(), cmd); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, c *config.Config) error {
	channels := 2
	if c.Mono {
		channels = 1 // parec/arecord do the downmix
	}
	headless := !term.IsTerminal(os.Stdout.Fd()) // no terminal, e.g. under systemd

	var panel controller.Panel // nil = terminal only
	var w, h int
	if c.Serial != "" {
		p, info, err := serial.Open(c.Serial, c.Baud, c.Brightness)
		if err != nil {
			return err
		}
		defer func() {
			p.Close()
			fmt.Fprintf(os.Stderr, "panel: %+v\n", p.Status())
		}()
		panel = p
		w, h = int(info.W), int(info.H)
	}

	acfg := audio.Config{Command: c.Cmd, Args: c.Args, Device: c.Device, Rate: c.Rate, Channels: channels}
	if headless {
		acfg.Stderr = os.Stderr // in a terminal the TUI owns the screen; child output would corrupt it
	}
	cap, err := audio.Start(ctx, acfg)
	if err != nil {
		return err
	}
	drain := func() {
		cap.Stop()
		for range cap.Blocks() {
		}
	}

	an := dsp.NewAnalyzer(channels, c.Bands, c.Rate, 1024, c.Gain, c.AutoGain)
	entries := []controller.Entry{
		{Bubble: bars.New(), Weight: 8}, {Bubble: fire.New(), Weight: 1},
		{Bubble: life.New(), Weight: 2}, {Bubble: stars.New(), Weight: 2},
		{Bubble: fireworks.New(), Weight: 2}, {Bubble: parrot.New(), Weight: 1},
		{Bubble: invaders.New(), Weight: 2}, {Bubble: polygon.New(), Weight: 2},
		{Bubble: plasma.New(), Weight: 2}, {Bubble: ripples.New(), Weight: 2},
		{Bubble: alert.New(), Weight: 1},
	}
	if c.Tronbyt != "" { // Tronbyt is the idle loop then, with clock apps of its own; a weight in the state file still wins
		entries = append(entries, controller.Entry{Bubble: clock.New(), Weight: 0},
			controller.Entry{Bubble: tronbyt.New(c.Tronbyt), Weight: 1})
	} else {
		entries = append(entries, controller.Entry{Bubble: clock.New(), Weight: 1})
	}
	if c.MAURL != "" { // a track bubble is in no loop: it shows itself on a new track
		entries = append(entries, controller.Entry{Bubble: nowplaying.New(c.MAURL, c.MAToken, c.MAPlayer), Weight: 1})
	}
	var hub *ws.Hub
	var onFrame func([][]color.RGBA)
	if c.Listen != "" {
		hub = &ws.Hub{}
		onFrame = hub.Send
	}
	ctrl, err := controller.New(entries, controller.Options{
		Analyzer: an, Blocks: cap.Blocks(), Wait: cap.Wait, Panel: panel, OnFrame: onFrame, W: w, H: h,
		FPS: c.FPS, Brightness: c.Brightness, StatePath: c.State, Show: c.Show,
		Headless: headless,
	})
	if err != nil {
		drain()
		return err
	}

	popts := []tea.ProgramOption{tea.WithContext(ctx)}
	if headless {
		popts = append(popts, tea.WithoutRenderer(), tea.WithInput(nil))
	}
	p := tea.NewProgram(ctrl, popts...)
	if hub != nil { // without a panel a watcher's terminal sets the size, like the local one
		hub.OnSize = func(w, h int) { p.Send(bubble.Resize{W: w, H: h}) }
	}

	var srv *http.Server
	var cancel context.CancelFunc
	if c.Listen != "" {
		ln, err := net.Listen("tcp", c.Listen) // before Run, so a busy port fails fast
		if err != nil {
			drain()
			return err
		}
		var lctx context.Context
		lctx, cancel = context.WithCancel(ctx)
		do := func(f controller.Call) error {
			done := make(chan struct{})
			go p.Send(controller.Call(func(ct *controller.Controller) { f(ct); close(done) }))
			select {
			case <-done:
				return nil
			case <-lctx.Done():
				return lctx.Err()
			}
		}
		mux := http.NewServeMux()
		mux.Handle("/", api.New(do))
		mux.Handle("GET /api/v1/frames", hub)
		srv = &http.Server{Handler: mux}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
	}

	_, err = p.Run()
	if cancel != nil {
		cancel()
	}
	if srv != nil {
		srv.Close()
	}
	drain()
	if err == nil {
		err = ctrl.Err()
	}
	return err
}

// watch mirrors the frame stream of another spectrum in this terminal.
func watch(ctx context.Context, target string) error {
	url := target
	if !strings.Contains(url, "://") {
		url = "ws://" + url
	}
	if !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(url, "ws://"), "wss://"), "/") {
		url += "/api/v1/frames"
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cl, err := ws.Dial(ctx, url)
	if err != nil {
		return err
	}
	defer cl.Close()
	m := &mirror{cl: cl, ctx: ctx}
	p := tea.NewProgram(m, tea.WithContext(ctx))
	go func() {
		for f := range cl.Frames {
			p.Send(f)
		}
		p.Send(tea.Quit())
	}()
	if _, err := p.Run(); err != nil {
		return err
	}
	cancel()
	select {
	case err := <-cl.Err:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	default:
		return nil
	}
}

// mirror is the watch model: the last frame received, q to quit. The server
// is told the terminal size and renders at it when it has no panel; a panel's
// frame is fitted here instead.
type mirror struct {
	cl    *ws.Client
	ctx   context.Context
	frame [][]color.RGBA
	w, h  int // terminal size in pixels: cells wide, 2 pixels per row
	view  string
}

func (m *mirror) Init() tea.Cmd { return nil }

func (m *mirror) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case [][]color.RGBA:
		m.frame = msg
		m.view = bubble.Render(bubble.Fit(msg, m.w, m.h))
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, 2*msg.Height
		m.view = bubble.Render(bubble.Fit(m.frame, m.w, m.h))
		return m, func() tea.Msg { _ = m.cl.Resize(m.ctx, m.w, m.h); return nil } // a failed send ends the stream anyway
	case tea.KeyPressMsg:
		if s := msg.String(); s == "q" || s == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *mirror) View() tea.View {
	v := tea.NewView(m.view)
	v.AltScreen = true
	return v
}
