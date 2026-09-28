// spectrum: audio visualizer of the default audio output, on the HUB75 panel
// and/or the terminal.
//
//	spectrum                       # default sink monitor, 32 bands, terminal only
//	spectrum -bands 16 -gain 6
//	spectrum -mono                 # one spectrum, channels mixed down
//	spectrum -autogain             # loudest band always fills the display
//	spectrum -serial /dev/ttyUSB0  # seeds a panel named "panel", the HUB75 board behind an ESP32
//	spectrum -cmd arecord -device hw:0   # on the Pi
//	spectrum -listen :8099 -state state.json  # HTTP API, settings persisted there
//	spectrum watch <pi>:8099[/desk] # mirror a panel's frames in this terminal, or the first without one
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
	"net/netip"
	neturl "net/url"
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
	"github.com/jon4hz/loudest-office/visualizer/music/aurora"
	"github.com/jon4hz/loudest-office/visualizer/music/bars"
	"github.com/jon4hz/loudest-office/visualizer/music/fire"
	"github.com/jon4hz/loudest-office/visualizer/music/fireworks"
	"github.com/jon4hz/loudest-office/visualizer/music/invaders"
	"github.com/jon4hz/loudest-office/visualizer/music/life"
	"github.com/jon4hz/loudest-office/visualizer/music/metaballs"
	"github.com/jon4hz/loudest-office/visualizer/music/parrot"
	"github.com/jon4hz/loudest-office/visualizer/music/pitch"
	"github.com/jon4hz/loudest-office/visualizer/music/plasma"
	"github.com/jon4hz/loudest-office/visualizer/music/polygon"
	"github.com/jon4hz/loudest-office/visualizer/music/ripples"
	"github.com/jon4hz/loudest-office/visualizer/music/spray"
	"github.com/jon4hz/loudest-office/visualizer/music/stars"
	"github.com/jon4hz/loudest-office/visualizer/music/waterfall"
	"github.com/jon4hz/loudest-office/visualizer/nowplaying"
	"github.com/jon4hz/loudest-office/visualizer/proto"
	"github.com/jon4hz/loudest-office/visualizer/serial"
	"github.com/jon4hz/loudest-office/visualizer/tronbyt"
	"github.com/jon4hz/loudest-office/visualizer/web"
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
		Use:   "watch host:port[/name]",
		Short: "Mirror the frames of a running spectrum in this terminal",
		Long: `Mirror the frames of a running spectrum in this terminal, over the websocket
at GET /api/v1/panels/{name}/frames of its --listen port. host:port, or a full
ws:// URL; without /name, GET /api/v1/frames mirrors the first panel.`,
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
		bub(bars.New, 8), bub(fire.New, 1),
		bub(life.New, 2), bub(stars.New, 2),
		bub(fireworks.New, 2), bub(parrot.New, 1),
		bub(invaders.New, 2), bub(polygon.New, 2),
		bub(plasma.New, 2), bub(ripples.New, 2),
		bub(waterfall.New, 2), bub(pitch.New, 2),
		bub(spray.New, 2), bub(aurora.New, 1),
		bub(metaballs.New, 2),
		bub(alert.New, 1),
	}
	if c.Tronbyt != "" { // Tronbyt is the idle loop then, with clock apps of its own; a weight in the state file still wins
		entries = append(entries, bub(clock.New, 0),
			controller.Entry{New: tronbyt.Factory(c.Tronbyt), Weight: 1})
	} else {
		entries = append(entries, bub(clock.New, 1))
	}
	if c.MAURL != "" { // a track bubble is in no loop: it shows itself on a new track
		entries = append(entries, controller.Entry{New: nowplaying.Factory(c.MAURL, c.MAToken, c.MAPlayer), Weight: 1})
	}

	var seed *controller.PanelSpec
	if c.Serial != "" {
		seed = &controller.PanelSpec{Name: "panel", Kind: controller.KindSerial, Address: c.Serial, Baud: c.Baud,
			FPS: c.FPS, Brightness: c.Brightness, IdleBrightness: c.Brightness}
	}
	hubs := &ws.Hubs{}
	ctrl, err := controller.New(entries, controller.Options{
		Analyzer: an, Blocks: cap.Blocks(), Wait: cap.Wait,
		Dial: func(addr string, baud int, bright byte) (controller.Port, proto.InfoMsg, error) {
			p, info, err := serial.Open(addr, baud, bright)
			if err != nil {
				return nil, info, err // not p: a nil *serial.Port in a Port is not nil
			}
			return p, info, nil
		},
		OnFrame: hubs.Send, Clients: hubs.Clients, Terminal: !headless, FPS: c.FPS, Seed: seed,
		StatePath: c.State, Show: c.Show,
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
		mux.Handle("/", web.Handler())
		mux.Handle("/api/", api.New(do)) // its patterns carry the full /api/v1 prefix
		mux.HandleFunc("GET /api/v1/panels/{name}/frames", func(w http.ResponseWriter, r *http.Request) {
			name, ok := r.PathValue("name"), false
			if err := do(func(c *controller.Controller) { ok = c.HasPanel(name) }); err != nil || !ok {
				http.NotFound(w, r)
				return
			}
			hubs.ServeHTTP(w, r)
		})
		mux.HandleFunc("GET /api/v1/frames", func(w http.ResponseWriter, r *http.Request) { // the first panel, for old clients
			var first string
			if err := do(func(c *controller.Controller) { first = c.FirstPanel() }); err != nil || first == "" {
				http.NotFound(w, r)
				return
			}
			r.SetPathValue("name", first)
			hubs.ServeHTTP(w, r)
		})
		var handler http.Handler = mux
		if c.TrustedProxy != "" { // validated by config.Load
			handler = api.TrustedProxy(netip.MustParsePrefix(c.TrustedProxy), mux)
		}
		srv = &http.Server{Handler: handler}
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
	for _, ps := range ctrl.Panels() {
		if ps.Status != nil {
			fmt.Fprintf(os.Stderr, "%s: %+v\n", ps.Name, *ps.Status)
		}
	}
	ctrl.Close()
	if err == nil {
		err = ctrl.Err()
	}
	return err
}

// bub wraps a constructor as a factory for the registry.
func bub[T bubble.Bubble](f func() T, weight int) controller.Entry {
	return controller.Entry{New: func() bubble.Bubble { return f() }, Weight: weight}
}

// watch mirrors the frame stream of another spectrum in this terminal.
// target is host:port[/name], or a full ws:// URL; without /name it mirrors
// the first panel.
func watch(ctx context.Context, target string) error {
	url := target
	if !strings.Contains(url, "://") {
		url = "ws://" + url
	}
	scheme, rest, _ := strings.Cut(url, "://")
	host, path, _ := strings.Cut(rest, "/")
	switch {
	case path == "":
		url = scheme + "://" + host + "/api/v1/frames"
	case !strings.HasPrefix(path, "api/"):
		url = scheme + "://" + host + "/api/v1/panels/" + neturl.PathEscape(path) + "/frames"
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
		return m, nil
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
