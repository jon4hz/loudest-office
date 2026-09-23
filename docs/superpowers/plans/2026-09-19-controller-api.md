# Controller, Bubbles and Config API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the bloated `spectrum.Model` into an audio analyzer, peer bubbles and a controller that picks the bubble (event > pin > music > idle), configurable at runtime through an HTTP API that persists to a state file.

**Architecture:** `dsp.Analyzer` runs per audio block and hands a `Signal` snapshot to the controller once per `tea.Tick` frame. Every bubble implements `bubble.Bubble`; music bubbles run their legacy per-block physics on a fixed timestep so no constant is retuned. API handlers run closures inside the controller's `Update` via `p.Send`, so there are no locks.

**Tech Stack:** Go 1.27, `charm.land/bubbletea/v2`, stdlib `net/http` and `encoding/json`, cobra/viper (existing).

**Spec:** `docs/superpowers/specs/2026-09-19-controller-api-design.md`. Read it first; it is normative for behaviour, this plan for order and names.

## Global Constraints

- Module `github.com/jon4hz/loudest-office/visualizer`, all Go paths below are relative to `visualizer/`.
- **No new Go module dependencies.** Tests use stdlib `testing` only.
- **Never `git commit`, merge or push: the user owns git history.** A task ends with a green build, not a commit.
- After every task: `cd visualizer && go build ./... && go vet ./... && go test ./...` is green.
- Frames are `[][]color.RGBA`, rows top to bottom, zero value = off.
- Match the surrounding code's comment density and naming. Deliberate shortcuts get a `ponytail:` comment naming the ceiling and the upgrade path.
- No speculative abstraction: build exactly the names listed under **Produces**, nothing more.
- Durations in JSON are seconds as numbers.

## File map

| File | Responsibility |
|---|---|
| `dsp/analyzer.go` | `Signal`, `Analyzer`: all per-block audio analysis |
| `bubble/bubble.go` | `Bubble`, `Kind`, msgs, `Event`, `Levels`, `NewFrame`, `Render`, `Patch` |
| `bubble/font.go` | 5x7 font, `DrawText`, `TextWidth` |
| `spectrum/base.go` | embedded base of the music bubbles, `stepper`, `Settings` |
| `spectrum/{bars,fire,life,stars,fireworks,parrot}.go` | one music bubble each |
| `clock/clock.go`, `alert/alert.go` | idle and event bubble |
| `serial/serial.go` | + `SetBrightness` |
| `controller/controller.go` | root tea.Model: kind choice, loop, events, brightness, API methods |
| `controller/state.go` | state document load/save |
| `api/api.go` | HTTP handlers |
| `config/config.go`, `cmd/spectrum/main.go` | flags, wiring |

---

### Task 1: Analyzer

**Files:** Create `dsp/analyzer.go`, `dsp/analyzer_test.go`. Modify `spectrum/model.go`, `spectrum/modes.go`, `spectrum/model_test.go`.

**Produces:**

```go
type Signal struct {
	Levels  [][]float32 // per channel, max since the last Take; valid until the next Add
	Mix     []float32   // channel-mixed levels; len(Mix) is the band count
	Energy  float32
	Drop    bool    // latched until Take
	BigDrop bool    // latched until Take
	Beat    float32 // phase 0..1, meaningful while BPM > 0
	Beats   int
	BPM     float64 // 0 = no tempo
	DB      float64 // RMS dBFS before gain, clamped to >= -120
}
func NewAnalyzer(channels, bands, rate, fftSize int, gain float64, autoGain bool) *Analyzer
func (a *Analyzer) Add(block [][]float32)
func (a *Analyzer) SetBands(n int)
func (a *Analyzer) Bands() int
func (a *Analyzer) Take() Signal
```

- [ ] **Step 1: tests first** in `dsp/analyzer_test.go` (package `dsp`, copy `sine` from `spectrum/model_test.go:12`):
  - `TestAutoGain`: port of `spectrum/model_test.go:75-101`; read `maxLevel(a.Take().Levels[0])` instead of `m.bars[0]`, poke `a.agc`.
  - `TestDropLatchesUntilTake`: 300 blocks of `0.32*sine(256,1000)` with a `Take` after each, then one full-scale block: `Take().Drop` is true, the next `Take().Drop` false. Then assert no second `Drop` within the next 29 blocks of alternating quiet/loud.
  - `TestTakeHoldsLevelsWithoutNewBlock`: `Add(loud)`, `Take`, `Take` again: second `Mix` equals the first and is not all zero.
  - `TestTakeMaxMerges`: `Add(loud)`, `Add(silence)`, `Take`: `Mix` still carries the loud levels; then `Add(silence)`, `Take`: zero.
  - `TestDBClampedAndMarshals`: all-zero block gives `DB == -120` and `json.Marshal(sig)` succeeds; full-scale sine gives `DB` in `-4..-2`.
  - `TestTempo`: the 21-block beat pattern of `spectrum/model_test.go:734-760`, assert `118 < BPM < 128`.
- [ ] **Step 2:** `go test ./dsp/` fails to compile.
- [ ] **Step 3: implement.** Move `spectrum/model.go:224-234` (levels), `:256-294` (drop, flux, tempo, beat phase) and `:318-327` (AGC) into `Add` with every constant unchanged. Details that matter:
  - Keep `cur []float32`, this block's own mix. Flux and `prevMix` use `cur`, never the merged `mix`.
  - `fresh := a.taken; a.taken = false`. When `fresh`, `levels`/`mix` are overwritten by this block, otherwise element-wise `max`. `Take` sets `a.taken = true`, returns the slices (no copy) and clears `drop`/`bigDrop`.
  - The legacy `m.burst` refractory logic becomes `a.refractory` with the same order: `a.refractory = max(a.refractory-1, 0)`, then `if a.refractory == 0 && a.energyAvg > 0.02 && energy > 1.5*a.energyAvg { a.refractory, a.drop, a.bigDrop = 30, true, a.bigDrop || energy > 1.7*a.energyAvg }`.
  - `DB = max(10*log10(meanSquare over all channels' samples), -120)`; an empty block is -120.
  - A channel shorter than `fftSize` is skipped as today.
  - `SetBands` recomputes `edges` with `Bands(n, fftSize, rate, 40, 16000)` and reallocates `levels`, `mix`, `cur`, `prevMix`.
- [ ] **Step 4: use it in `Model`.** `Model` gets `an *dsp.Analyzer` (built in `New` after the options, `SetBands` forwards). `Update(SamplesMsg)` becomes `m.an.Add(msg); sig := m.an.Take()`; the bar/peak loop reads `l := sig.Levels[ch][b]`; `m.mix = sig.Mix`; `energy := sig.Energy`; after the peak loop `m.burst = max(m.burst-1, 0); if sig.Drop { m.burst = 30 }`; `dropped, big := sig.Drop, sig.BigDrop`. `period()` becomes `sig.BPM > 0` via stored `m.sig`; `pulse()` uses `m.sig.Beat`; `stepParrot` reads `m.sig.Beats`/`m.sig.Beat`. Delete the moved fields (`gain agc autoGain energyAvg prevMix fluxAvg tempo beat beats window edges`). `BPM()` returns `m.sig.BPM`.
- [ ] **Step 5:** delete `TestAutoGain` from `spectrum/model_test.go`; in `TestDropDetectorRunsInEveryMode` nothing changes (`m.burst` still exists). Run the full suite: the legacy tests validate the extraction bit for bit.

---

### Task 2: `bubble` package

**Files:** Create `bubble/bubble.go`, `bubble/font.go`, `bubble/bubble_test.go`. Modify `spectrum/model.go` (`View` delegates).

**Produces:**

```go
type Kind string
const ( Music Kind = "music"; Idle Kind = "idle"; EventKind Kind = "event" )
type Bubble interface {
	Name() string
	Kind() Kind
	Update(tea.Msg) tea.Cmd
	Frame() [][]color.RGBA
	Settings() any
	Configure(json.RawMessage) error
}
type Tick struct { Signal dsp.Signal; Dt time.Duration; Now time.Time }
type Resize struct{ W, H int }
type Activate struct{}
type Event struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Level   string    `json:"level"`
	Expires time.Time `json:"expires"`
}
var Levels = []string{"info", "warning", "critical"} // rank = index
func NewFrame(w, h int) [][]color.RGBA
func Render(frame [][]color.RGBA) string
func Patch[T any](cur T, raw json.RawMessage) (T, error)
func DrawText(frame [][]color.RGBA, x, y int, s string, c color.RGBA, scale int)
func TextWidth(s string, scale int) int // 6*scale per rune, minus the trailing gap
```

- [ ] **Step 1: tests.** `Render(NewFrame(40, 20))` has 9 newlines. `Patch`: `type s struct{ L []string `json:"l"` }`; patching `s{[]string{"a","b"}}` with `{"l":["zzz"]}` returns `["zzz"]` and leaves the original `["a","b"]` untouched (this is the bug the deep copy prevents); `{"nope":1}` is an error; empty/`null` raw returns `cur`. `DrawText`: "1" at scale 1 lights exactly the pixels of its glyph; drawing at `x=-3`, past the right edge, or an unknown rune (`'é'`) does not panic; scale 2 lights 4x the pixels; `TextWidth("AB", 1) == 11`.
- [ ] **Step 2: implement.** `Render` is `spectrum/model.go:442-468` with `w, h` taken from the frame. `Patch`: marshal `cur`, unmarshal into a fresh `T` (deep copy), then `json.NewDecoder` with `DisallowUnknownFields` onto the copy. Font: `var glyphs = map[rune][7]uint8{...}`, 5 bits per row, MSB left; cover `A-Z 0-9` space and `.,:;!?-+/%'"()=<>#*_`; lowercase folds to uppercase; unknown runes draw a filled 5x7 box outline. `// ponytail: ASCII only, no umlauts; add glyphs when an alert needs them.`
- [ ] **Step 3:** `Model.View` becomes `return bubble.Render(m.frame)`. Full suite green.

---

### Task 3: five music bubbles

**Files:** Create `spectrum/base.go`, `fire.go`, `life.go`, `stars.go`, `fireworks.go`, `parrot.go`, `helpers_test.go`, `base_test.go`, and one `_test.go` per bubble. Modify `spectrum/model.go`, `modes.go`, `model_test.go`.

**Consumes:** Task 1 and 2 names. **Produces:**

```go
type Settings struct{ Palettes []string `json:"palettes"` } // empty = all
func NewFire() *Fire; NewLife() *Life; NewStars() *Stars; NewFireworks() *Fireworks; NewParrot() *Parrot
// names: "fire", "life", "stars", "fireworks", "parrot"
```

`base.go`:

```go
const step = time.Second * 1024 / 44100 // one legacy audio block

// stepper turns frame time into whole physics steps.
type stepper struct{ acc time.Duration }

func (s *stepper) take(dt time.Duration) int {
	s.acc += dt
	n := int(s.acc / step)
	s.acc -= time.Duration(n) * step
	return n
}

type base struct {
	name        string
	frame       [][]color.RGBA
	w, h, bands int
	steps       int // physics steps run, drives animated palettes (was Model.tick)
	stepper
	set     Settings
	palette Palette
}
```

`base` methods: `Name`, `Kind` (always `bubble.Music`), `Frame`, `resize(w, h)` (`bubble.NewFrame`), `clear()`, `cellColour` and `dim` moved from `modes.go:153-162,223-225` (using `b.steps/8`), `pickPalette()` (uniform from `set.Palettes` resolved with `PaletteByName`, all of `Palettes` when empty), `paletteKey(tea.KeyPressMsg) bool` (`c`/`C` walk `Palettes` by index of the current name), `Settings() any`, `Configure(raw)` (`bubble.Patch`, every name must pass `PaletteByName` else `fmt.Errorf("unknown palette %q, have: %s", ...)`). `newBase(name)` starts with `Palettes[0]`.

Every bubble's `Update` has this shape (fire shown; fire overrides `Settings`/`Configure` with `struct{}` because it ignores the palette):

```go
func (f *Fire) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		f.resize(msg.W, msg.H)
	case bubble.Activate:
		f.palette = f.pickPalette()
	case tea.KeyPressMsg:
		f.paletteKey(msg)
	case bubble.Tick:
		f.bands = len(msg.Signal.Mix)
		for range f.take(msg.Dt) {
			f.steps++
			f.step(msg.Signal)
		}
		f.draw()
	}
	return nil
}
```

Rules (spec "Fixed timestep"):
- `step`/`draw` bodies are the legacy `stepX`/`drawX` from `modes.go` with `m.mix` → `sig.Mix`, `energy` → `sig.Energy`, `m.period() > 0` → `sig.BPM > 0`, `m.pulse()` → `(1-sig.Beat)*(1-sig.Beat)` when `sig.BPM > 0`. `draw` starts with `clear()` and returns when `w == 0 || h == 0 || bands == 0`.
- `resize` on life nils the grid, on parrot nils the mask (legacy `model.go:204-205`).
- **Latched fields outside the loop.** Fireworks: `if sig.Drop { launch 5 }` before the loop (split `stepFireworks` so the volley is its own method). Stars: `if sig.Drop { s.warp = 30 }` before the loop; inside `step`, use `s.warp > 0` then `s.warp = max(s.warp-1, 0)` at the end. `drawStars` uses `s.warp > 0`. The `warp()` method and its mode check disappear.
- Parrot with a tempo computes the frame index once per Tick from `sig.Beats`/`sig.Beat`; without one it runs the energy budget per step.

`helpers_test.go`: move `sine`, `noise` from `model_test.go`; add

```go
// feed plays blocks through the analyzer and gives the bubble one step each.
func feed(a *dsp.Analyzer, b bubble.Bubble, blocks ...[]float32) {
	for _, blk := range blocks {
		a.Add([][]float32{blk})
		b.Update(bubble.Tick{Signal: a.Take(), Dt: step})
	}
}
func sized(b bubble.Bubble, w, h int) { b.Update(bubble.Resize{W: w, H: h}) }
func lit(frame [][]color.RGBA) int
func litColumns(frame [][]color.RGBA) []string // body of model_test.go:112
func column(frame [][]color.RGBA, x int) string // body of model_test.go:387
// dropped feeds b 300 quiet blocks and one loud one: a detected drop.
func dropped(b bubble.Bubble) *dsp.Analyzer
```

- [ ] **Step 1:** write `base_test.go`: `take(step) == 1` a thousand times; `take(step/2)` alternates 0, 1; one second in 7 ms slices totals 43; `TestDropSurvivesZeroStepTick`: a `Tick{Signal: dsp.Signal{Drop: true, Mix: make([]float32, 8)}, Dt: 0}` gives fireworks at least 3 rockets and stars `warp > 0`; `Configure` rejects `{"palettes":["nope"]}` and after `{"palettes":["outrun"]}` two hundred `Activate`s only ever pick outrun.
- [ ] **Step 2:** move the mode tests, rewriting `New(..., WithMode(X))` + `m.Update(SamplesMsg{blk})` to `a := dsp.NewAnalyzer(1, bands, 44100, fft, 0, false); b := NewX(); sized(b, w, h); feed(a, b, blk)`:
  - `fire_test.go`: `model_test.go:398`. `life_test.go`: `:457, :476, :486, :521, :675` (`stepLife`, `l.life` stay reachable in-package; the palette test sets `l.palette` directly). `stars_test.go`: `:555` (warp check becomes `dropped(s); s.warp > 0`), `:799`. `fireworks_test.go`: `:603, :645`. `parrot_test.go`: `:687, :734` (BPM read from `a.Take().BPM`), `:770`.
  - "resize must not panic" tails become `sized(b, 3, 2); a.SetBands(8); feed(a, b, blk)`. Drop the `ParseMode` assertions.
- [ ] **Step 3:** implement the five bubbles; tests green.
- [ ] **Step 4: shim.** `Model` holds `fire *Fire` … `parrot *Parrot`; `resize` forwards `bubble.Resize` to all five; `SetPalette` also sets each bubble's `palette`; `Update(SamplesMsg)` for a non-bars mode does `m.active().Update(bubble.Tick{Signal: sig, Dt: step})`; `Frame()` returns the active bubble's frame for non-bars modes. Delete the step/draw functions and per-mode fields from `modes.go`/`model.go`; keep the `Mode` enum, `ParseMode`, `SetMode`. Delete `TestDropDetectorRunsInEveryMode` (covered by Task 1). Full suite green.

---

### Task 4: bars

**Files:** Create `spectrum/bars.go`, `bars_test.go`. Modify `spectrum/model.go`, `model_test.go`.

**Produces:**

```go
type BarsSettings struct {
	Palettes []string `json:"palettes"` // empty = all
	Layouts  []string `json:"layouts"`  // empty = all; default [mirror hmirror]
	Peaks    []string `json:"peaks"`    // empty = all
	Trails   float64  `json:"trails"`   // chance per activation, 0..1, default 0.05
}
func NewBars() *Bars // name "bars"
```

`Bars` embeds `base` and owns `bars peaks hold vel vx px`, `layout peakStyle trails`, `burst int`, and the former options as plain fields `fall, peakFall float32; peakHold int` (defaults 0.04, 0.02, 20). `Layout`, `PeakStyle` and their `Parse*`/`String` stay exported in `bars.go`.

Tick order (keeps the legacy semantics, see spec):

```go
case bubble.Tick:
	b.shape(msg.Signal) // realloc when len(Levels) or len(Mix) changed
	n := b.take(msg.Dt)
	for range n {
		b.steps++
		b.physics(msg.Signal) // model.go:231-254 per channel, reads b.burst, then b.burst = max(b.burst-1, 0)
	}
	if msg.Signal.Drop { // after the loop: the "caught up" case would re-arm the hold
		b.burst = 30
		if b.peakStyle == Beat { /* model.go:296-304 with msg.Signal.BigDrop */ }
	}
	for range n - 1 {
		b.fade() // trails fade once per step, not per frame
	}
	if n > 0 {
		b.draw() // fade-or-clear, then paint: model.go:335-437
	}
```

`Activate`: `alloc()` (stale bars must not flash), palette from `pickPalette`, layout and peak style uniform from their lists, `trails = rand.Float64() < set.Trails`; then **no retry loop**: `if bar, _ := b.palette.At(0, 1, 0, 1); bar.A == 0 && b.peakStyle == NoPeaks { b.peakStyle = Falling }`. Keys: `l`, `p`, `t` as `cmd/spectrum/main.go:123-130`, plus `paletteKey`. `Configure` validates layouts with `ParseLayout`, peaks with `ParsePeakStyle`, `0 <= Trails <= 1`.

- [ ] **Step 1:** `bars_test.go`: move `model_test.go:20, 127, 149, 168, 186, 204 (b.steps = 8), 223, 237, 274, 319, 341, 427` using a helper `newBars(ch, bands, w, h) *Bars` that sizes and allocates so tests can poke `b.bars` and call `b.draw()`. Add Activate tests replacing `cmd/spectrum/main_test.go`: 5000 activations with `Layouts:["mirror","hmirror"]` only give those two; `Trails: 0.05` gives 150..350 of 5000; `Palettes:["peaks"], Peaks:["none"]` always ends with a style other than `NoPeaks`; `Configure` rejects `{"layouts":["diagonal"]}` and `{"trails":2}`.
- [ ] **Step 2:** implement; green.
- [ ] **Step 3: shim.** `Model` delegates bars too and becomes setters/getters over the six bubbles (`SetLayout` sets `m.bars.layout`, and so on). Remove the options `FallSpeed`, `PeakHold`, `PeakFall`. `model_test.go` keeps only `TestModelFollowsWindowSize`. `cmd/spectrum` still builds and its tests still pass.

---

### Task 5: clock and alert

**Files:** Create `clock/clock.go`, `clock/clock_test.go`, `alert/alert.go`, `alert/alert_test.go`.

**Produces:** `clock.New() *Clock` (name `"clock"`, kind `bubble.Idle`, settings `struct{}`), `alert.New() *Alert` (name `"alert"`, kind `bubble.EventKind`, `type Settings struct{ Speed float64 `json:"speed"` }` px/s, default 30, must be > 0).

- Clock: on `Tick`, clear, `DrawText` `Now.Format("15:04")` centred, scale 2 when `TextWidth(s, 2) <= w && 14 <= h` else 1; the colon is drawn only on even seconds. White at 60% so an idle panel is not glaring.
- Alert: on `bubble.Event`, store text and level, reset the offset to 0 when the text changed. On `Tick`, `offset += Speed * Dt.Seconds()`; draw the text at `x = w - int(offset)`, vertically centred, scale 2 when `h >= 14`; when the text has fully left (`offset > w + TextWidth`), wrap to 0. A text that fits is centred and does not scroll. Colours: info `{0,160,255}`, warning `{255,160,0}`, critical `{255,0,0}`.

- [ ] **Step 1: tests.** Clock: at 64x32 and `Now` = 15:04:00 the frame equals a hand-made frame with the same `DrawText` call; at 15:04:01 fewer pixels are lit. Alert: a long text's leftmost lit column moves left by 30 px after `Tick{Dt: time.Second}` at speed 30; after enough ticks it reappears on the right; a new `Event` with other text restarts at the right edge; the same text again does not; the three levels give three different colours; `Configure({"speed":0})` fails.
- [ ] **Step 2:** implement; green.

---

### Task 6: serial brightness

**Files:** Modify `serial/serial.go`, `serial/serial_test.go`.

**Produces:** `func (p *Port) SetBrightness(b byte)`.

- [ ] **Step 1:** extend `TestHandshakeFrameBlank` after the frame check: `p.SetBrightness(10)`, expect `next(proto.Config)` with `Payload[0] == 10`.
- [ ] **Step 2:** add `bright chan byte` (depth 1, same replace-pending pattern as `Send`). In `run`'s select: `case v := <-p.bright: p.mu.Lock(); p.brightness = v; p.mu.Unlock(); pkt = proto.Encode(pkt[:0], proto.Config, seq, proto.ConfigPayload(v)); seq++; _, err = f.Write(pkt)`. `connect` already sends `p.brightness`, so a reconnect re-sends the last value; read it under `p.mu` there.

---

### Task 7: controller

**Files:** Create `controller/controller.go`, `controller/state.go`, `controller/controller_test.go`, `controller/state_test.go`.

**Produces:**

```go
type Loop struct {
	Seconds float64 `json:"loop"`  // 0 = stay
	Order   string  `json:"order"` // "random" | "sequence"
}
var Orders = []string{"random", "sequence"}
type Settings struct {
	Music          Loop    `json:"music"`
	Idle           Loop    `json:"idle"`
	Show           string  `json:"show"`
	MusicDB        float64 `json:"music_db"`
	SilenceDB      float64 `json:"silence_db"`
	SilenceAfter   float64 `json:"silence_after"`
	Brightness     uint8   `json:"brightness"`
	IdleBrightness uint8   `json:"idle_brightness"`
}
type Entry struct { Bubble bubble.Bubble; Weight int }
type Panel interface {
	Send([][]color.RGBA)
	SetBrightness(byte)
	Status() proto.StatusMsg
}
type Options struct {
	Analyzer   *dsp.Analyzer
	Blocks     <-chan [][]float32 // nil in tests
	Wait       func() error      // capture's Wait, nil in tests
	Panel      Panel             // nil = terminal only
	W, H       int               // fixed size from the panel, 0 = follow the window
	FPS        int
	Brightness uint8             // default for both brightness settings
	StatePath  string            // "" = do not persist
	Show       string            // pin at start, not saved
	Headless   bool              // skip rendering the view
}
func New(entries []Entry, o Options) (*Controller, error) // duplicate names, unknown Show and corrupt state are errors
func (c *Controller) Err() error                           // capture error after Run
type Call func(*Controller)                                // a tea.Msg; runs inside Update

type BubbleInfo struct {
	Name     string      `json:"name"`
	Kind     bubble.Kind `json:"kind"`
	Weight   int         `json:"weight"`
	Settings any         `json:"settings"`
}
type State struct {
	Active   string          `json:"active"` // "" = blank
	Kind     bubble.Kind     `json:"kind"`
	Playing  bool            `json:"playing"`
	DB       float64         `json:"db"`
	BPM      float64         `json:"bpm"`
	W        int             `json:"w"`
	H        int             `json:"h"`
	Settings Settings        `json:"settings"`
	Events   []bubble.Event  `json:"events"`
	Panel    *proto.StatusMsg `json:"panel,omitempty"`
}
var ErrNotFound = errors.New("not found")
func (c *Controller) State() State
func (c *Controller) Patch(raw json.RawMessage) error
func (c *Controller) Bubbles() []BubbleInfo
func (c *Controller) PatchBubble(name string, weight *int, settings json.RawMessage) error
func (c *Controller) Next()
func (c *Controller) AddEvent(e bubble.Event) (string, error) // fills ID when empty; same ID replaces; ErrFull past 32
func (c *Controller) RemoveEvent(id string) error
```

Defaults: `Music{60,"random"}`, `Idle{60,"random"}`, `MusicDB -50`, `SilenceDB -60`, `SilenceAfter 5`, brightness from `Options`.

Behaviour is the spec's "Controller" section, step by step. Implementation notes:
- `Init` = `tea.Batch(c.wait(), c.frame())`; `wait` is `cmd/spectrum/main.go:99-107` with `blocks := c.o.Blocks` captured; `frame` is `tea.Tick(time.Second/time.Duration(fps), func(t time.Time) tea.Msg { return frameMsg(t) })`. `type frameMsg time.Time`, `type blockMsg [][]float32`, `type captureDone struct{}`.
- `Update`: `blockMsg` → `Analyzer.Add`, re-arm `wait`. `frameMsg` → `c.tick(time.Time(msg))`, re-arm `frame`. `Call` → `msg(c)`. `tea.WindowSizeMsg` → when `o.W == 0`, `Resize{W, 2*H}` to all. `tea.KeyPressMsg` → `q`/`ctrl+c` quit; `m` sets `Show` to the next registry name after the active one (not saved); `a` clears `Show` if set, else toggles the active kind's loop between 0 and its previous value (10 s if it was 0); `+`/`=`/`-` `Analyzer.SetBands` in steps of 8 clamped 8..64; others to the active bubble. Anything else → every bubble, cmds batched. `captureDone` → `c.err = o.Wait()`, `tea.Quit`.
- `tick(now)`: exactly steps 1-6 of the spec. `dt = min(now.Sub(c.last), 250*time.Millisecond)`, 0 on the first tick. Weighted pick: among entries of the kind with weight > 0, excluding the current one when more than one candidate; `sequence` takes the next such entry in registry order. The pinned bubble is activated once when the pin takes effect and never looped. Blank: `c.blank` from `bubble.NewFrame(c.w, c.h)`, reallocated on resize.
- Events: level rank by index in `bubble.Levels`; the alert gets `bubble.Event{Text: strings.Join(texts, " +++ "), Level: top}` whenever the set of top-level events changes (compare the joined string and level).
- Brightness: `want := Brightness`, `IdleBrightness` when the shown kind is idle or blank; call `Panel.SetBrightness` only when `want` differs from the last sent value (first tick always sends).
- `Patch`: `bubble.Patch` onto `c.set`, validate (orders in `Orders`, loops >= 0, `SilenceAfter >= 0`, `SilenceDB <= MusicDB`, `Show` empty or a registered non-event bubble), apply, save. `PatchBubble`: unknown name → `ErrNotFound`; weight must be >= 0; settings go through `Bubble.Configure`; save.
- `state.go`: `stateDoc{Controller Settings; Bubbles map[string]bubbleState}`, `bubbleState{Weight int; Settings json.RawMessage}`. Load in `New`: missing file → defaults; unparseable → error; controller section through `Patch`-style merge and validation, falling back to defaults when invalid; per bubble: unknown name ignored, `Configure` error ignored (defaults stay), weight applied when >= 0. Save: marshal indent, write `path+".tmp"`, `f.Sync()`, close, `os.Rename`. The `--show` pin is never written: save `c.savedShow`, not the override.

- [ ] **Step 1: tests** with `fake` bubbles (record every msg, fixed name/kind, a 1x1 frame with a distinct colour), a `fakePanel`, and a helper `tickAt(c, t, db)` that stores `db` via a test hook (`c.sigHook func() dsp.Signal`, nil in production, used instead of `Analyzer.Take` when set) and calls `c.Update(frameMsg(t))`. Cases: every bullet listed in the approved plan's step 7 (idle at start; music at `MusicDB`; stays music through 4 s of quiet, idle after 5 s; a level between the thresholds resets the timer; loop deadline sends `Activate` to another bubble; loop 0 never re-picks; weight 0 never picked; 3:1 weights give 20..30% over 4000 picks; `sequence` walks registry order; no enabled bubble → the panel gets a `w x h` zero frame; `Show` beats music and idle, loses to an event; event expires at `Expires`; critical outranks warning; same ID replaces; two info events give `"a +++ b"` and the alert gets a new `Event` when one expires; 33rd event fails; dt clamp; `WindowSizeMsg` fans out, ignored with fixed size; idle brightness sent once; `Call` runs). `state_test.go`: round trip through a new controller; missing file; corrupt JSON errors; unknown bubble ignored; bad bubble settings keep defaults; no `.tmp` left; `Options.Show` not persisted.
- [ ] **Step 2:** implement; green.

---

### Task 8: API

**Files:** Create `api/api.go`, `api/api_test.go`.

**Produces:** `func New(do func(controller.Call) error) http.Handler`.

Routes and validation are the spec's "API" section. Shape of every handler:

```go
func (s *server) call(w http.ResponseWriter, status int, fn func(*controller.Controller) (any, error)) {
	var body []byte
	var err error
	if derr := s.do(func(c *controller.Controller) {
		var v any
		if v, err = fn(c); err == nil && v != nil {
			body, err = json.Marshal(v) // inside the call: the value may alias live state
		}
	}); derr != nil {
		fail(w, http.StatusServiceUnavailable, derr)
		return
	}
	// errors.Is(err, controller.ErrNotFound) → 404, other err → 400, else status + body
}
```

Request bodies: `http.MaxBytesReader(w, r.Body, 64<<10)`, decoded with `DisallowUnknownFields` where the handler owns the shape (`events`, the `{"weight","settings"}` envelope); `PATCH /controller` passes the raw body to `c.Patch`. `POST /events` body `{id, text, level, ttl}`: text 1..256 chars, level in `bubble.Levels`, `0 < ttl <= 86400`; `Expires = time.Now().Add(ttl)`. `GET /options` is static: palette names from `spectrum.Palettes`, layouts `stacked side mirror hmirror`, peaks `fall fly beat none`, `bubble.Levels`, `controller.Orders`.

- [ ] **Step 1: tests** with `httptest.NewRecorder`, a real controller over fake-free real bubbles (`spectrum.NewBars()`, `clock.New()`, `alert.New()`), `StatePath` in `t.TempDir()`, and `do := func(f controller.Call) error { f(c); return nil }`: happy path of all eight routes; 400 for bad JSON, unknown field, bad level, `ttl: 0`, unknown palette (body names the allowed ones); 404 for an unknown bubble and event id; 503 when `do` returns an error; a bars PATCH shows in the next `GET /bubbles` and in the state file.
- [ ] **Step 2:** implement with `http.NewServeMux` method patterns (`"PATCH /api/v1/bubbles/{name}"`); green.

---

### Task 9: switch over

**Files:** Modify `config/config.go`, `config/config_test.go`, `cmd/spectrum/main.go`, `ansible/roles/visualizer/defaults/main.yml`, `ansible/roles/visualizer/templates/compose.yml.j2`, `ansible/roles/visualizer/files/build/Dockerfile`, `ansible/playbooks/site.yml`, `README.md`, `Taskfile.yml`. Delete `cmd/spectrum/main_test.go`, `spectrum/model.go`, `spectrum/modes.go`, `spectrum/model_test.go`.

- [ ] **Step 1: config.** Remove the fields and flags `palette layout peaks mode trails loop loop-layouts loop-modes` and the `spectrum` import. Add `Listen string` (`--listen`, "" , "HTTP API address, e.g. :8099 (empty = off, there is no auth)"), `State string` (`--state`, "", "JSON file the API settings persist to (empty = not saved)"), `FPS int` (`--fps`, 30), `Show string` (`--show`, "", "pin this bubble at start"). Validate `1 <= fps <= 120` next to the brightness check. Rewrite `TestPrecedence` with the new keys, add `TestFPSRange`, and make `TestUnknownKey` use `loop_modes: [bars]` (a key that used to exist).
- [ ] **Step 2: main.go.** Delete `flavor`, `nextFlavor`, `app`, `loopTick`, `captureDone`. `run` builds: analyzer (`dsp.NewAnalyzer(cfg.Channels, c.Bands, c.Rate, 1024, c.Gain, c.AutoGain)`), optional port (keep the deferred `Close` + status print), capture, registry

```go
entries := []controller.Entry{
	{Bubble: spectrum.NewBars(), Weight: 8}, {Bubble: spectrum.NewFire(), Weight: 1},
	{Bubble: spectrum.NewLife(), Weight: 2}, {Bubble: spectrum.NewStars(), Weight: 2},
	{Bubble: spectrum.NewFireworks(), Weight: 2}, {Bubble: spectrum.NewParrot(), Weight: 1},
	{Bubble: clock.New(), Weight: 1}, {Bubble: alert.New(), Weight: 1},
}
```

  then `controller.New`, `tea.NewProgram(c, popts...)`. A `*serial.Port` that is nil must be passed as a nil `controller.Panel` interface, not a typed nil. With `--listen`: `ln, err := net.Listen("tcp", c.Listen)` before `Run`; `ctx, cancel := context.WithCancel(ctx)`; `do := func(f controller.Call) error { done := make(chan struct{}); go p.Send(controller.Call(func(c *controller.Controller) { f(c); close(done) })); select { case <-done: return nil; case <-ctx.Done(): return ctx.Err() } }`; `srv := &http.Server{Handler: api.New(do)}`; `go srv.Serve(ln)`; after `Run`: `cancel(); srv.Close()`. After `Run`, keep the capture stop + drain, and return `ctrl.Err()`. No `signal.NotifyContext`. Update the header comment and the cobra `Long` text (new keys, `--listen`, `--show`).
- [ ] **Step 3:** delete the four files listed above; `go build ./... && go vet ./... && go test ./...` green.
- [ ] **Step 4: deploy files.** Read each file first and follow its style. `defaults/main.yml`: drop `loop`, `loop_modes`; add `fps: 20`, `listen: ":8099"`, `state: /data/state.json` to `visualizer_config`, and `visualizer_timezone: Europe/Zurich`. `compose.yml.j2`: `network_mode: host`, volume `./data:/data`, `environment: { TZ: "{{ visualizer_timezone }}" }`. Dockerfile: `apk add --no-cache alsa-utils tzdata`. `site.yml`: ufw rule `{ port: 8099, proto: tcp }` with the comment `visualizer api (no auth, LAN only)`. If the podman_compose role does not create `./data` for a bind mount, add it the way the music_assistant role gets its `./data`.
- [ ] **Step 5: README + Taskfile.** README: new keys and flags, an "API" section with four curl examples (state, PATCH bars palettes, PATCH controller thresholds/show, POST event), where the state file lives on the Pi, how to calibrate `music_db`/`silence_db` from `db` in `/state`. Taskfile: `visualizer:test` running `go vet ./... && go test ./...` in `visualizer/`.
- [ ] **Step 6: run it.** `go run ./cmd/spectrum --listen :8099 --state "$TMPDIR/state.json"` headless (`> /dev/null`), exercise the spec's verification curls, `kill -TERM` and check exit code 0.

---

### Task 10: one package per mode

Requested by the user during execution: every music mode gets its own package, like `clock/` and `alert/`. Pure move + export, no behaviour change; the full suite is the safety net. Runs after Task 9 because the `Model` shim, which reaches into the bubbles' private fields, is gone by then.

**Files:** Create `palette/`, `music/`, `music/musictest/`, `music/{bars,fire,life,stars,fireworks,parrot}/`. Delete `spectrum/`. Modify `api/api.go`, `api/api_test.go`, `cmd/spectrum/main.go`, README paths if any.

**Produces:**

```go
// palette (from spectrum/palette.go + palette_test.go)
type Palette struct{ ... }            // unchanged
var Palettes []Palette                // unchanged
func ByName(name string) (Palette, bool) // was PaletteByName
func Names() []string
func Gradient(t float64, stops ...color.RGBA) color.RGBA // fire needs it
// export further colour vars/helpers only where a mode or its tests need them

// music (from spectrum/base.go)
const Step = time.Second * 1024 / 44100
type Settings struct{ Palettes []string `json:"palettes"` }
type Base struct {
	W, H, Bands int
	Steps       int
	Set         Settings
	Palette     palette.Palette
	// frame, name and the stepper stay unexported
}
func NewBase(name string) Base
func (b *Base) Name() string; Kind() bubble.Kind; Frame() [][]color.RGBA
func (b *Base) Resize(w, h int); Clear(); Take(dt time.Duration) int
func (b *Base) CellColour(band, y, h int) color.RGBA; PickPalette() palette.Palette
func (b *Base) PaletteKey(tea.KeyPressMsg) bool
func (b *Base) Settings() any; Configure(json.RawMessage) error
func Dim(c color.RGBA, l float32) color.RGBA

// music/musictest: the helpers of spectrum/helpers_test.go, exported
func Sine(n int, hz float64) []float32; Noise(n int, amp float32) []float32
func Feed(a *dsp.Analyzer, b bubble.Bubble, blocks ...[]float32); Sized(b bubble.Bubble, w, h int)
func Lit(frame [][]color.RGBA) int; LitColumns(frame [][]color.RGBA) []string; Column(frame [][]color.RGBA, x int) string
func Dropped(b bubble.Bubble) *dsp.Analyzer

// one package per mode, each: func New() *T  (bars.New() *Bars, fire.New() *Fire, ...)
// bars also keeps Layout, PeakStyle, ParseLayout, ParsePeakStyle and adds
var Layouts = []string{"stacked", "side", "mirror", "hmirror"}
var Peaks   = []string{"fall", "fly", "beat", "none"}
```

- [ ] **Step 1:** move `palette.go`/`palette_test.go` to `palette/`, export what is listed; suite green (spectrum imports palette).
- [ ] **Step 2:** move `base.go` to `music/` with the exported surface above, `helpers_test.go` to `music/musictest/musictest.go` (non-test package, imports only `dsp`, `bubble`, stdlib); `base_test.go` splits: stepper tests stay in `music/`, the per-bubble parts go to their bubble.
- [ ] **Step 3:** move each mode and its test file to `music/<mode>/`, package named after the mode, constructor `New`. `parrot/*.txt` moves to `music/parrot/frames/*.txt` (adjust the `go:embed` path). Tests stay in-package so they keep poking their own private state; base state goes through the exported fields.
- [ ] **Step 4:** `api` takes options from `palette.Names()`, `bars.Layouts`, `bars.Peaks`; `main.go` registry uses `bars.New()` and so on; delete `spectrum/`; update the package doc comment that lived in `spectrum/palette.go`.
- [ ] **Step 5:** `gofmt -l . && go build ./... && go vet ./... && go test ./...` green; `grep -r "visualizer/spectrum\"" .` finds nothing.
