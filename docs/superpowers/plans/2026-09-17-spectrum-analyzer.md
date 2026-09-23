# Spectrum Analyzer Bubble Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A bubbletea v2 spectrum-analyzer component that renders audio into a pixel frame, plus a CLI that runs it on the laptop's default sink monitor.

**Architecture:** `audio` shells out to `parec` and yields per-channel float blocks; `dsp` turns a block into per-band 0..1 levels (Hann, radix-2 FFT, log bands, dB); `spectrum` is the bubble that keeps bar/peak dynamics, draws into an RGB pixel frame and paints it with half-block glyphs; `cmd/spectrum` wires them together.

**Tech Stack:** Go 1.27, `charm.land/bubbletea/v2` (only dependency), `parec` at runtime.

**Spec:** `docs/superpowers/specs/2026-09-17-spectrum-analyzer-design.md`

## Global Constraints

- Module: `github.com/jon4hz/loudest-office/visualizer`, go 1.27.0 (existing `visualizer/go.mod`).
- Only external dependency: `charm.land/bubbletea/v2`. No cgo.
- Component `View()` returns `string` (bubbles convention); the CLI wraps it in `tea.View` with `AltScreen = true`.
- Pixel frame is `[][]color.RGBA`, rows top to bottom; a zero `color.RGBA{}` means "off".
- Tests use only `testing`, run with `go test ./...` from `visualizer/`.

---

### Task 1: dsp — Hann window and FFT

**Files:**
- Create: `visualizer/dsp/fft.go`
- Test: `visualizer/dsp/fft_test.go`

**Interfaces:**
- Produces: `func Hann(n int) []float32`, `func FFT(re, im []float32)` (in-place, len power of two).

- [ ] **Step 1: Write the failing test**

```go
package dsp

import (
	"math"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	const n = 64
	re := make([]float32, n)
	im := make([]float32, n)
	for i := range re {
		re[i] = float32(math.Sin(2*math.Pi*3*float64(i)/n) + 0.5*math.Cos(2*math.Pi*10*float64(i)/n))
	}
	want := make([]complex128, n)
	for k := range want {
		for i := range re {
			a := -2 * math.Pi * float64(k*i) / n
			want[k] += complex(float64(re[i])*math.Cos(a), float64(re[i])*math.Sin(a))
		}
	}
	FFT(re, im)
	for k := range want {
		if math.Abs(float64(re[k])-real(want[k])) > 1e-3 || math.Abs(float64(im[k])-imag(want[k])) > 1e-3 {
			t.Fatalf("bin %d: got (%v,%v) want %v", k, re[k], im[k], want[k])
		}
	}
}

func TestHann(t *testing.T) {
	w := Hann(8)
	if w[0] != 0 || w[7] != 0 || math.Abs(float64(w[3])-0.95048) > 1e-3 {
		t.Fatalf("unexpected window %v", w)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd visualizer && go test ./dsp/`
Expected: FAIL, `undefined: FFT`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package dsp holds the pure signal-processing helpers for the visualizer.
package dsp

import "math"

// Hann returns an n-point Hann window.
func Hann(n int) []float32 {
	w := make([]float32, n)
	for i := range w {
		w[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

// FFT computes the in-place radix-2 FFT of re/im. len(re) must be a power of two.
func FFT(re, im []float32) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := -2 * math.Pi / float64(size)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for start := 0; start < n; start += size {
			cr, ci := 1.0, 0.0
			for k := 0; k < size/2; k++ {
				a, b := start+k, start+k+size/2
				tr := float32(cr)*re[b] - float32(ci)*im[b]
				ti := float32(ci)*re[b] + float32(cr)*im[b]
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd visualizer && go test ./dsp/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add visualizer/dsp && git commit -m "feat(visualizer): hann window and radix-2 fft"
```

---

### Task 2: dsp — log bands and levels

**Files:**
- Create: `visualizer/dsp/levels.go`
- Test: `visualizer/dsp/levels_test.go`

**Interfaces:**
- Consumes: `Hann`, `FFT` from Task 1.
- Produces: `func Bands(n, fftSize, rate int, lo, hi float64) []int` (n+1 bin edges), `func Levels(samples, window []float32, edges []int, gainDB, floorDB float64) []float32` (one 0..1 value per band).

- [ ] **Step 1: Write the failing test**

```go
package dsp

import (
	"math"
	"testing"
)

func TestBands(t *testing.T) {
	e := Bands(32, 1024, 44100, 40, 16000)
	if len(e) != 33 {
		t.Fatalf("want 33 edges, got %d", len(e))
	}
	for i := 1; i < len(e); i++ {
		if e[i] <= e[i-1] {
			t.Fatalf("edges not strictly increasing at %d: %v", i, e)
		}
	}
	if e[32] > 512 {
		t.Fatalf("top edge above nyquist: %d", e[32])
	}
}

func TestLevelsSine(t *testing.T) {
	const n, rate = 1024, 44100
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / rate))
	}
	e := Bands(32, n, rate, 40, 16000)
	lv := Levels(s, Hann(n), e, 0, -60)
	best := 0
	for b := range lv {
		if lv[b] > lv[best] {
			best = b
		}
	}
	bin := 1000.0 * n / rate
	if float64(e[best]) > bin || float64(e[best+1]) <= bin {
		t.Fatalf("loudest band %d covers bins [%d,%d), 1 kHz is bin %.1f", best, e[best], e[best+1], bin)
	}
	if lv[best] < 0.9 || lv[best] > 1 {
		t.Fatalf("full-scale sine should be near 1, got %v", lv[best])
	}
	if lv[0] > 0.2 {
		t.Fatalf("lowest band should be quiet, got %v", lv[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd visualizer && go test ./dsp/`
Expected: FAIL, `undefined: Bands`.

- [ ] **Step 3: Write minimal implementation**

```go
package dsp

import "math"

// Bands returns n+1 FFT bin edges for n log-spaced bands between lo and hi Hz.
// Band i covers bins [edges[i], edges[i+1]). Each band is at least one bin
// wide, so at low frequencies the spacing degrades to linear.
func Bands(n, fftSize, rate int, lo, hi float64) []int {
	edges := make([]int, n+1)
	binHz := float64(rate) / float64(fftSize)
	for i := range edges {
		f := lo * math.Pow(hi/lo, float64(i)/float64(n))
		b := int(math.Round(f / binHz))
		if i > 0 && b <= edges[i-1] {
			b = edges[i-1] + 1
		}
		edges[i] = min(b, fftSize/2)
	}
	return edges
}

// Levels windows and FFTs samples (len(window) samples are used) and returns
// one 0..1 level per band: the loudest bin of the band in dBFS, offset by
// gainDB, mapped so floorDB (e.g. -60) is 0 and 0 dBFS is 1.
func Levels(samples, window []float32, edges []int, gainDB, floorDB float64) []float32 {
	n := len(window)
	re := make([]float32, n)
	im := make([]float32, n)
	var wsum float64
	for i := range re {
		re[i] = samples[i] * window[i]
		wsum += float64(window[i])
	}
	FFT(re, im)
	out := make([]float32, len(edges)-1)
	for b := range out {
		var peak float64
		for k := edges[b]; k < edges[b+1]; k++ {
			if m := float64(re[k]*re[k] + im[k]*im[k]); m > peak {
				peak = m
			}
		}
		mag := 2 * math.Sqrt(peak) / wsum // full-scale sine -> 1.0
		db := 20*math.Log10(mag+1e-12) + gainDB
		out[b] = float32(max(0, min(1, (db-floorDB)/-floorDB)))
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd visualizer && go test ./dsp/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add visualizer/dsp && git commit -m "feat(visualizer): log bands and per-band levels"
```

---

### Task 3: audio — parec capture

**Files:**
- Create: `visualizer/audio/capture.go`
- Test: `visualizer/audio/capture_test.go`

**Interfaces:**
- Produces: `type Config struct{Command, Device string; Rate, Channels, Frames int}`, `func Start(ctx, Config) (*Capture, error)`, `(*Capture).Blocks() <-chan [][]float32`, `(*Capture).Wait() error`, `func Deinterleave(b []byte, channels int) [][]float32`.

- [ ] **Step 1: Write the failing test**

```go
package audio

import (
	"context"
	"testing"
)

func TestDeinterleave(t *testing.T) {
	// two frames, two channels, s16le: L=32767 R=-32768, L=0 R=16384
	b := []byte{0xff, 0x7f, 0x00, 0x80, 0x00, 0x00, 0x00, 0x40}
	got := Deinterleave(b, 2)
	if len(got) != 2 || len(got[0]) != 2 {
		t.Fatalf("shape: %v", got)
	}
	if got[0][0] < 0.999 || got[1][0] != -1 || got[0][1] != 0 || got[1][1] != 0.5 {
		t.Fatalf("values: %v", got)
	}
}

func TestStartReadsBlocks(t *testing.T) {
	// "cat" of /dev/zero stands in for parec: endless silence.
	c, err := Start(context.Background(), Config{Command: "cat", Args: []string{"/dev/zero"}, Channels: 2, Frames: 4})
	if err != nil {
		t.Fatal(err)
	}
	blk := <-c.Blocks()
	if len(blk) != 2 || len(blk[0]) != 4 || blk[0][0] != 0 {
		t.Fatalf("block: %v", blk)
	}
	c.Stop()
	for range c.Blocks() {
	}
	c.Wait() // must not hang
}

func TestStartMissingCommand(t *testing.T) {
	if _, err := Start(context.Background(), Config{Command: "definitely-not-a-binary"}); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd visualizer && go test ./audio/`
Expected: FAIL, `undefined: Deinterleave`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package audio captures PCM blocks from a subprocess such as parec or arecord.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

// Config describes the capture subprocess. Zero fields take the defaults
// noted on each field.
type Config struct {
	Command  string   // "parec" (default) or "arecord"; anything else needs Args
	Args     []string // overrides the generated arguments when set
	Device   string   // default "@DEFAULT_SINK@.monitor"
	Rate     int      // default 44100
	Channels int      // default 2
	Frames   int      // samples per channel per block, default 1024
}

func (c *Config) defaults() {
	if c.Command == "" {
		c.Command = "parec"
	}
	if c.Device == "" {
		c.Device = "@DEFAULT_SINK@.monitor"
	}
	if c.Rate == 0 {
		c.Rate = 44100
	}
	if c.Channels == 0 {
		c.Channels = 2
	}
	if c.Frames == 0 {
		c.Frames = 1024
	}
	if c.Args != nil {
		return
	}
	r, ch := strconv.Itoa(c.Rate), strconv.Itoa(c.Channels)
	switch c.Command {
	case "arecord":
		c.Args = []string{"-q", "-t", "raw", "-f", "S16_LE", "-r", r, "-c", ch, "-D", c.Device}
	default:
		c.Args = []string{"--raw", "--format=s16le", "--rate=" + r, "--channels=" + ch, "-d", c.Device}
	}
}

// Capture is a running capture subprocess.
type Capture struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr bytes.Buffer
	blocks chan [][]float32
	err    error
	done   chan struct{}
}

// Start launches the subprocess and begins reading blocks.
func Start(ctx context.Context, cfg Config) (*Capture, error) {
	cfg.defaults()
	ctx, cancel := context.WithCancel(ctx)
	c := &Capture{cancel: cancel, blocks: make(chan [][]float32, 1), done: make(chan struct{})}
	c.cmd = exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	c.cmd.Stderr = &c.stderr
	out, err := c.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := c.cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go c.read(out, cfg.Channels, cfg.Frames)
	return c, nil
}

func (c *Capture) read(r io.Reader, channels, frames int) {
	defer close(c.done)
	defer close(c.blocks)
	buf := make([]byte, frames*channels*2)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		c.blocks <- Deinterleave(buf, channels)
	}
	if err := c.cmd.Wait(); err != nil {
		c.err = fmt.Errorf("%s: %w: %s", c.cmd.Path, err, bytes.TrimSpace(c.stderr.Bytes()))
	}
}

// Blocks yields one slice per channel per block; it closes when the process exits.
func (c *Capture) Blocks() <-chan [][]float32 { return c.blocks }

// Stop kills the subprocess. Blocks closes shortly after.
func (c *Capture) Stop() { c.cancel() }

// Wait blocks until the reader has finished and returns the exit error, if any.
func (c *Capture) Wait() error {
	<-c.done
	return c.err
}

// Deinterleave converts interleaved s16le bytes into one -1..1 slice per channel.
func Deinterleave(b []byte, channels int) [][]float32 {
	frames := len(b) / 2 / channels
	out := make([][]float32, channels)
	for ch := range out {
		out[ch] = make([]float32, frames)
	}
	for i := 0; i < frames*channels; i++ {
		out[i%channels][i/channels] = float32(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768
	}
	return out
}
```

Note: the reader blocks on `c.blocks <-` while the consumer is slow; `Stop` + draining `Blocks()` (as the test does) unblocks it. The CLI always drains.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd visualizer && go test ./audio/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add visualizer/audio && git commit -m "feat(visualizer): parec/arecord capture"
```

---

### Task 4: spectrum — palettes

**Files:**
- Create: `visualizer/spectrum/palette.go`
- Test: `visualizer/spectrum/palette_test.go`

**Interfaces:**
- Produces: `type Palette struct{Name string; At func(band, nBands, y, height int) (bar, peak color.RGBA)}`, `var Palettes []Palette` (Rainbow, TriBar, Red, Blue, Purple, Outrun, PeaksOnly), `func PaletteByName(string) (Palette, bool)`.

- [ ] **Step 1: Write the failing test**

```go
package spectrum

import "testing"

func TestPalettesCoverEveryRow(t *testing.T) {
	if len(Palettes) < 7 {
		t.Fatalf("expected the shipped palettes, got %d", len(Palettes))
	}
	for _, p := range Palettes {
		for y := 0; y < 16; y++ {
			bar, peak := p.At(3, 32, y, 16)
			if bar.A == 0 && peak.A == 0 {
				t.Fatalf("%s draws nothing at row %d", p.Name, y)
			}
		}
	}
	if _, ok := PaletteByName("rainbow"); !ok {
		t.Fatal("rainbow missing")
	}
	if _, ok := PaletteByName("nope"); ok {
		t.Fatal("unknown palette found")
	}
}

func TestTriBarColorsByHeight(t *testing.T) {
	p, _ := PaletteByName("tribar")
	lo, _ := p.At(0, 8, 0, 30)
	hi, _ := p.At(0, 8, 29, 30)
	if lo.G != 255 || lo.R != 0 || hi.R != 255 || hi.G != 0 {
		t.Fatalf("bottom %v top %v", lo, hi)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd visualizer && go test ./spectrum/`
Expected: FAIL, `undefined: Palettes`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package spectrum is a bubbletea component that renders audio blocks as a
// spectrum-analyzer pixel frame.
package spectrum

import (
	"image/color"
	"math"
	"strings"
)

// Palette colours a pixel of the analyzer. At is called per band and row
// (y=0 is the bottom row); a zero color means "do not draw".
type Palette struct {
	Name string
	At   func(band, nBands, y, height int) (bar, peak color.RGBA)
}

var (
	white  = color.RGBA{255, 255, 255, 255}
	red    = color.RGBA{255, 0, 0, 255}
	green  = color.RGBA{0, 255, 0, 255}
	yellow = color.RGBA{255, 255, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	none   = color.RGBA{}
)

// Palettes are the shipped palettes, in cycling order. Ported from
// github.com/donnersm/FFT_ESP32_Analyzer colour modes.
var Palettes = []Palette{
	{"rainbow", func(band, n, _, _ int) (color.RGBA, color.RGBA) {
		return hsv(float64(band)/float64(n)), white
	}},
	{"tribar", func(_, _, y, h int) (color.RGBA, color.RGBA) {
		c := green
		switch f := float64(y) / float64(h); {
		case f >= 2.0/3:
			c = red
		case f >= 1.0/3:
			c = yellow
		}
		return c, c
	}},
	{"red", func(_, _, _, _ int) (color.RGBA, color.RGBA) { return red, blue }},
	{"blue", func(_, _, _, _ int) (color.RGBA, color.RGBA) { return blue, red }},
	{"purple", func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{0, 212, 255, 255}, color.RGBA{179, 0, 255, 255}), white
	}},
	{"outrun", func(_, _, y, h int) (color.RGBA, color.RGBA) {
		return gradient(frac(y, h), color.RGBA{141, 0, 100, 255}, color.RGBA{255, 192, 0, 255}, color.RGBA{0, 5, 255, 255}), none
	}},
	{"peaks", func(_, _, _, _ int) (color.RGBA, color.RGBA) { return none, blue }},
}

// PaletteByName looks a palette up case-insensitively.
func PaletteByName(name string) (Palette, bool) {
	for _, p := range Palettes {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Palette{}, false
}

// frac is y as a fraction of the top row, safe for h == 1.
func frac(y, h int) float64 { return float64(y) / float64(max(h-1, 1)) }

// hsv returns a fully saturated colour for hue in [0,1).
func hsv(h float64) color.RGBA {
	h = math.Mod(h, 1) * 6
	x := uint8(255 * (1 - math.Abs(math.Mod(h, 2)-1)))
	switch int(h) {
	case 0:
		return color.RGBA{255, x, 0, 255}
	case 1:
		return color.RGBA{x, 255, 0, 255}
	case 2:
		return color.RGBA{0, 255, x, 255}
	case 3:
		return color.RGBA{0, x, 255, 255}
	case 4:
		return color.RGBA{x, 0, 255, 255}
	default:
		return color.RGBA{255, 0, x, 255}
	}
}

// gradient linearly interpolates evenly spaced stops at t in [0,1].
func gradient(t float64, stops ...color.RGBA) color.RGBA {
	t = max(0, min(1, t)) * float64(len(stops)-1)
	i := min(int(t), len(stops)-2)
	f := t - float64(i)
	a, b := stops[i], stops[i+1]
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd visualizer && go test ./spectrum/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add visualizer/spectrum && git commit -m "feat(visualizer): spectrum palettes"
```

---

### Task 5: spectrum — the bubble

**Files:**
- Create: `visualizer/spectrum/model.go`
- Test: `visualizer/spectrum/model_test.go`
- Modify: `visualizer/go.mod` (add bubbletea via `go get`)

**Interfaces:**
- Consumes: `dsp.Hann`, `dsp.Bands`, `dsp.Levels`; `Palette` from Task 4.
- Produces: `type SamplesMsg [][]float32`; `func New(opts ...Option) Model`; options `Bands(int)`, `Channels(int)`, `Rate(int)`, `FFTSize(int)`, `WithPalette(Palette)`, `FallSpeed(float32)`, `PeakHold(int)`, `PeakFall(float32)`, `Gain(float64)`, `Size(w, h int)`; methods `Init() tea.Cmd`, `Update(tea.Msg) (Model, tea.Cmd)`, `View() string`, `Frame() [][]color.RGBA`, `SetBands(int)`, `SetPalette(Palette)`, `NumBands() int`.

- [ ] **Step 1: Add the dependency**

Run: `cd visualizer && go get charm.land/bubbletea/v2@v2.0.9`

- [ ] **Step 2: Write the failing test**

```go
package spectrum

import (
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sine(n int, hz float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 44100))
	}
	return s
}

func TestModelDrawsBarsAndHoldsPeaks(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), PeakHold(2), PeakFall(0.5), FallSpeed(1))
	m, _ = m.Update(SamplesMsg{sine(256, 1000)})
	f := m.Frame()
	if len(f) != 8 || len(f[0]) != 16 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	lit := 0
	for _, row := range f {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("nothing drawn")
	}
	peakBefore := append([]float32(nil), m.peaks[0]...)
	silence := make([]float32, 256)
	m, _ = m.Update(SamplesMsg{silence}) // bars drop, hold 2
	m, _ = m.Update(SamplesMsg{silence}) // hold 1
	for b := range peakBefore {
		if m.peaks[0][b] != peakBefore[b] {
			t.Fatalf("peak %d moved during hold", b)
		}
	}
	m, _ = m.Update(SamplesMsg{silence}) // hold 0
	m, _ = m.Update(SamplesMsg{silence}) // falls
	moved := false
	for b := range peakBefore {
		if m.peaks[0][b] < peakBefore[b] {
			moved = true
		}
	}
	if !moved {
		t.Fatal("peak never fell")
	}
}

func TestModelFollowsWindowSize(t *testing.T) {
	m := New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if f := m.Frame(); len(f) != 20 || len(f[0]) != 40 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	if lines := strings.Count(m.View(), "\n"); lines != 9 {
		t.Fatalf("view has %d newlines, want 9", lines)
	}
	m.SetBands(16)
	if m.NumBands() != 16 || len(m.bars[0]) != 16 {
		t.Fatal("SetBands did not resize")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd visualizer && go test ./spectrum/`
Expected: FAIL, `undefined: New`.

- [ ] **Step 4: Write minimal implementation**

```go
package spectrum

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
)

// SamplesMsg carries one block of samples per channel, each in -1..1.
type SamplesMsg [][]float32

// Model is the spectrum analyzer bubble. Create it with New.
type Model struct {
	bands, channels, rate, fftSize int
	palette                        Palette
	fall, peakFall                 float32
	peakHold                       int
	gain                           float64
	fixedW, fixedH, w, h           int

	window []float32
	edges  []int
	bars   [][]float32
	peaks  [][]float32
	hold   [][]int
	frame  [][]color.RGBA
}

// Option configures New.
type Option func(*Model)

func Bands(n int) Option             { return func(m *Model) { m.bands = n } }
func Channels(n int) Option          { return func(m *Model) { m.channels = n } }
func Rate(hz int) Option             { return func(m *Model) { m.rate = hz } }
func FFTSize(n int) Option           { return func(m *Model) { m.fftSize = n } }
func WithPalette(p Palette) Option   { return func(m *Model) { m.palette = p } }
func FallSpeed(perBlock float32) Option { return func(m *Model) { m.fall = perBlock } }
func PeakHold(blocks int) Option     { return func(m *Model) { m.peakHold = blocks } }
func PeakFall(perBlock float32) Option { return func(m *Model) { m.peakFall = perBlock } }
func Gain(db float64) Option         { return func(m *Model) { m.gain = db } }

// Size fixes the frame size in pixels instead of following the window.
func Size(w, h int) Option { return func(m *Model) { m.fixedW, m.fixedH = w, h } }

// New returns a model with 32 bands, 2 channels, 44.1 kHz, 1024-point FFT and
// the rainbow palette.
func New(opts ...Option) Model {
	m := Model{bands: 32, channels: 2, rate: 44100, fftSize: 1024, palette: Palettes[0],
		fall: 0.04, peakFall: 0.02, peakHold: 20}
	for _, o := range opts {
		o(&m)
	}
	m.window = dsp.Hann(m.fftSize)
	m.SetBands(m.bands)
	m.resize(m.fixedW, m.fixedH)
	return m
}

// SetBands changes the band count and resets bar and peak state.
func (m *Model) SetBands(n int) {
	m.bands = n
	m.edges = dsp.Bands(n, m.fftSize, m.rate, 40, 16000)
	m.bars = make([][]float32, m.channels)
	m.peaks = make([][]float32, m.channels)
	m.hold = make([][]int, m.channels)
	for ch := range m.bars {
		m.bars[ch] = make([]float32, n)
		m.peaks[ch] = make([]float32, n)
		m.hold[ch] = make([]int, n)
	}
}

func (m *Model) SetPalette(p Palette) { m.palette = p }
func (m Model) NumBands() int         { return m.bands }

// Frame is the current picture, rows top to bottom. Zero pixels are off.
func (m Model) Frame() [][]color.RGBA { return m.frame }

func (m *Model) resize(w, h int) {
	m.w, m.h = w, h
	m.frame = make([][]color.RGBA, h)
	for y := range m.frame {
		m.frame[y] = make([]color.RGBA, w)
	}
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles SamplesMsg and tea.WindowSizeMsg.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.fixedW == 0 {
			m.resize(msg.Width, 2*msg.Height)
			m.draw()
		}
	case SamplesMsg:
		for ch := 0; ch < m.channels && ch < len(msg); ch++ {
			if len(msg[ch]) < m.fftSize {
				continue
			}
			lv := dsp.Levels(msg[ch], m.window, m.edges, m.gain, -60)
			for b, l := range lv {
				m.bars[ch][b] = max(l, m.bars[ch][b]-m.fall)
				switch {
				case m.bars[ch][b] >= m.peaks[ch][b]:
					m.peaks[ch][b], m.hold[ch][b] = m.bars[ch][b], m.peakHold
				case m.hold[ch][b] > 0:
					m.hold[ch][b]--
				default:
					m.peaks[ch][b] = max(m.bars[ch][b], m.peaks[ch][b]-m.peakFall)
				}
			}
		}
		m.draw()
	}
	return m, nil
}

// draw paints bars and peaks of every channel into the frame, channels
// stacked top to bottom.
func (m *Model) draw() {
	for _, row := range m.frame {
		clear(row)
	}
	if m.w == 0 || m.h == 0 || m.bands == 0 {
		return
	}
	height := m.h / m.channels
	barW := m.w / m.bands
	if barW == 0 || height == 0 {
		return
	}
	gap := 0
	if barW >= 3 {
		gap = 1
	}
	x0 := (m.w - barW*m.bands) / 2
	for ch := range m.bars {
		top := ch * height
		for b := range m.bars[ch] {
			barH := int(m.bars[ch][b]*float32(height) + 0.5)
			peakY := int(m.peaks[ch][b]*float32(height-1) + 0.5)
			for y := 0; y < height; y++ {
				bar, peak := m.palette.At(b, m.bands, y, height)
				c := color.RGBA{}
				if y < barH {
					c = bar
				}
				if y == peakY && m.peaks[ch][b] > 0 && peak.A != 0 {
					c = peak
				}
				if c.A == 0 {
					continue
				}
				row := m.frame[top+height-1-y]
				for x := x0 + b*barW; x < x0+(b+1)*barW-gap; x++ {
					row[x] = c
				}
			}
		}
	}
}

// View paints the frame two pixels per cell using the upper half block:
// foreground is the upper pixel, background the lower one.
func (m Model) View() string {
	var sb strings.Builder
	for y := 0; y < m.h; y += 2 {
		var prevFg, prevBg color.RGBA
		first := true
		for x := 0; x < m.w; x++ {
			fg := m.frame[y][x]
			var bg color.RGBA
			if y+1 < m.h {
				bg = m.frame[y+1][x]
			}
			if first || fg != prevFg {
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
			}
			if first || bg != prevBg {
				fmt.Fprintf(&sb, "\x1b[48;2;%d;%d;%dm", bg.R, bg.G, bg.B)
			}
			sb.WriteString("▀")
			prevFg, prevBg, first = fg, bg, false
		}
		sb.WriteString("\x1b[0m")
		if y+2 < m.h {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd visualizer && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add visualizer/spectrum visualizer/go.mod visualizer/go.sum && git commit -m "feat(visualizer): spectrum analyzer bubble"
```

---

### Task 6: cmd/spectrum — the CLI

**Files:**
- Create: `visualizer/cmd/spectrum/main.go`
- Modify: `README.md` (one "Visualizer" paragraph under Software)

**Interfaces:**
- Consumes: `audio.Start/Config/Capture`, `spectrum.New/Options/Palettes/PaletteByName/SamplesMsg`.

- [ ] **Step 1: Write the CLI**

```go
// spectrum: terminal spectrum analyzer of the default audio output.
//
//	spectrum                       # default sink monitor, 32 bands, rainbow
//	spectrum -bands 16 -palette tribar -gain 6
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
	cap, err := audio.Start(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	a := app{cap: cap, pal: pal, spec: spectrum.New(
		spectrum.Bands(*bands), spectrum.Rate(cfg.Rate), spectrum.Gain(*gain),
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
```

- [ ] **Step 2: Build, check the error path, then run it for real**

Run: `cd visualizer && go vet ./... && go build ./... && go run ./cmd/spectrum -cmd definitely-missing; echo exit=$?`
Expected: exit=1 with an exec "not found" error on stderr.

Then manually: `go run ./cmd/spectrum` while music plays; `c` cycles palettes, `+`/`-` change bands, `q` quits cleanly and `pgrep parec` is empty afterwards.

- [ ] **Step 3: README**

Add under `## Software`:

```markdown
### Visualizer

`visualizer/` holds the Go spectrum analyzer that will eventually feed the matrix panel.
Try it locally: `cd visualizer && go run ./cmd/spectrum` (needs `parec`; keys: `q`, `c` palette, `+`/`-` bands).
```

- [ ] **Step 4: Commit**

```bash
git add visualizer/cmd README.md && git commit -m "feat(visualizer): spectrum cli on the default sink monitor"
```
