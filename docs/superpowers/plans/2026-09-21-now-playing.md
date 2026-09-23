# Now Playing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On a track change the panel shows cover, title and artist from Music Assistant for a few seconds; `POST /api/v1/bubbles/{name}/show` shows any bubble for a few seconds.

**Architecture:** A new bubble `nowplaying` of a new kind `track` polls MA's HTTP RPC (`POST /api`, `players/get`) every 2 s, also while hidden. On a new track it sends the controller a new message, `bubble.Interlude`, and the controller shows it between pin and music in its priority. The API route uses the same controller method, `Show`.

**Tech Stack:** Go, stdlib `net/http` + `image/*`, bubbletea v2, `golang.org/x/text/unicode/norm` (already in the module graph), Ansible + ansible-vault.

**Spec:** `docs/superpowers/specs/2026-09-21-now-playing-design.md`

## Global Constraints

- **No git commits, merges or pushes.** The user owns the history. Tasks end with passing tests, not with a commit.
- All Go commands run in `visualizer/`. `CGO_ENABLED=0 go build ./...`, `go vet ./...` and `go test ./...` must pass after every task.
- No new Go dependency except `golang.org/x/text` moving from indirect to direct.
- The MA token is a secret and the repo is public: it never appears in a file that git tracks unencrypted, in a default, in a test fixture that looks real, or in the agent's output. The user runs `ansible-vault encrypt_string` themselves.
- `ansible/vault.txt` is the vault password, gitignored already; never read it, never print it.
- Match the surrounding code: short doc comments that say why, lowercase error strings, `bubble.Patch` for settings, no logging from bubbles.
- Exact values: poll every 2 s, poll timeout 5 s / 1 MiB, cover timeout 10 s / 4 MiB, cover request `?size=80&fmt=png`, default card 10 s, `seconds` 0..600, scroll 12 px/s after a 2 s pause with a gap of 3 glyphs, title white, artist `#1db954`, placeholder colour `{153,153,153}`.

## Files

| File | What |
|---|---|
| `visualizer/bubble/font.go` | `fold`, used by `DrawText` and `TextWidth` |
| `visualizer/bubble/bubble.go` | `Track` kind, `Interlude` message |
| `visualizer/controller/controller.go` | interlude state, `decide`, `wantBright`, `Update` case |
| `visualizer/controller/api.go` | `Show` |
| `visualizer/api/api.go` | the show route |
| `visualizer/config/config.go` | `ma_url`, `ma_token`, `ma_player` |
| `visualizer/nowplaying/nowplaying.go` | the bubble: poll, announce, settings |
| `visualizer/nowplaying/cover.go` | cover URL, fetch, `shrink` |
| `visualizer/nowplaying/draw.go` | layout and scroll |
| `visualizer/cmd/spectrum/main.go` | registry line |
| `addon/config.yaml`, `ansible/...`, `Taskfile.yml`, `README.md` | deployment |

---

### Task 1: fold diacritics in the font

**Files:**
- Modify: `visualizer/bubble/font.go`
- Test: `visualizer/bubble/bubble_test.go`

**Interfaces:**
- Produces: `bubble.DrawText` and `bubble.TextWidth` treat `Züri` as `Zuri`, `ß` as `SS`. Signatures unchanged.

- [ ] **Step 1: Write the failing test** (append to `bubble_test.go`; `reflect` and `image/color` are imported there already)

```go
func TestDrawTextFoldsDiacritics(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	got, want := NewFrame(80, 7), NewFrame(80, 7)
	DrawText(got, 0, 0, "Züri Wést ßø", white, 1)
	DrawText(want, 0, 0, "Zuri West SSo", white, 1)
	if !reflect.DeepEqual(got, want) {
		t.Error("folded text draws other pixels than its ASCII spelling")
	}
	if got, want := TextWidth("ß", 1), TextWidth("SS", 1); got != want {
		t.Errorf("TextWidth(ß) = %d, want %d", got, want)
	}
}
```

- [ ] **Step 2: Run it, expect FAIL** — `go test ./bubble -run TestDrawTextFoldsDiacritics` (unknown glyph boxes differ from the letters).

- [ ] **Step 3: Implement** in `font.go`. Imports become `image/color`, `strings`, `unicode`, `unicode/utf8`, `golang.org/x/text/unicode/norm`. Replace the `ponytail:` comment above `glyphs` with:

```go
// ponytail: ASCII only; fold maps accented letters onto it. Add glyphs when
// a script without a Latin base needs them.
```

Add below `unknownGlyph`:

```go
// folds are the letters NFD leaves whole.
var folds = strings.NewReplacer("ß", "SS", "ø", "o", "Ø", "O", "æ", "ae", "Æ", "AE",
	"œ", "oe", "Œ", "OE", "ł", "l", "Ł", "L", "’", "'")

// fold spells s with the letters the font has: decomposed, without the
// combining marks.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(folds.Replace(s)))
}
```

In `DrawText` change `for _, r := range s {` to `for _, r := range fold(s) {` and add to its doc comment: `Accented letters fold to their base letter.` In `TextWidth` change the count to `n := utf8.RuneCountInString(fold(s))`.

- [ ] **Step 4:** `go mod tidy` (moves `golang.org/x/text` into the direct `require` block), then `go test ./bubble ./alert ./clock` — PASS.

---

### Task 2: controller: track kind, Interlude, Show

**Files:**
- Modify: `visualizer/bubble/bubble.go`, `visualizer/controller/controller.go`, `visualizer/controller/api.go`
- Test: `visualizer/controller/controller_test.go`

**Interfaces:**
- Produces: `bubble.Track Kind = "track"`; `bubble.Interlude{Name string; For time.Duration}`; `(*Controller).Show(name string, d time.Duration) error`.

- [ ] **Step 1: Write the failing tests** (append to `controller_test.go`; helpers `newFake`, `newTest`, `opts`, `tickAt`, `at`, `patch` exist; `-20` dB is music, `-90` is silence)

```go
// interludeSetup is bars playing, with a clock, a track bubble and an alert.
func interludeSetup(t *testing.T) (*Controller, *fake) {
	t.Helper()
	np := newFake("nowplaying", bubble.Track)
	c := newTest(t, opts(), Entry{newFake("bars", bubble.Music), 1}, Entry{newFake("clock", bubble.Idle), 1},
		Entry{np, 1}, Entry{newFake("alert", bubble.EventKind), 1})
	tickAt(c, at(0), -20)
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q, want bars", got)
	}
	return c, np
}

func TestInterludeShowsThenReturns(t *testing.T) {
	c, np := interludeSetup(t)
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20) // its time starts with this tick
	if s := c.State(); s.Active != "nowplaying" || s.Kind != bubble.Track {
		t.Fatalf("active = %q kind = %q, want nowplaying/track", s.Active, s.Kind)
	}
	if n := len(msgsOf[bubble.Activate](np)); n != 1 {
		t.Errorf("Activate sent %d times, want 1", n)
	}
	tickAt(c, at(10*time.Second), -20)
	if got := c.State().Active; got != "nowplaying" {
		t.Fatalf("active = %q after 9 s, want nowplaying", got)
	}
	tickAt(c, at(11*time.Second), -20)
	if got := c.State().Active; got != "bars" {
		t.Fatalf("active = %q after 10 s, want bars", got)
	}
}

func TestInterludeBeatsIdle(t *testing.T) {
	np := newFake("nowplaying", bubble.Track)
	c := newTest(t, opts(), Entry{newFake("clock", bubble.Idle), 1}, Entry{np, 1})
	tickAt(c, at(0), -90)
	if err := c.Show("nowplaying", time.Second); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(time.Second), -90)
	if got := c.State().Active; got != "nowplaying" {
		t.Fatalf("active = %q, want nowplaying", got)
	}
}

func TestInterludeLosesToEventAndReturns(t *testing.T) {
	c, _ := interludeSetup(t)
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20)
	if _, err := c.AddEvent(bubble.Event{Text: "x", Level: "info", Expires: at(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(2*time.Second), -20)
	if got := c.State().Active; got != "alert" {
		t.Fatalf("active = %q during the event, want alert", got)
	}
	tickAt(c, at(4*time.Second), -20)
	if got := c.State().Active; got != "nowplaying" {
		t.Fatalf("active = %q after the event, want nowplaying", got)
	}
}

func TestShowErrors(t *testing.T) {
	c, _ := interludeSetup(t)
	if err := c.Show("nope", time.Second); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown bubble: %v, want ErrNotFound", err)
	}
	if err := c.Show("alert", time.Second); err == nil {
		t.Error("an event bubble was accepted")
	}
	if err := c.Show("nowplaying", 0); err == nil {
		t.Error("a duration of 0 was accepted")
	}
	patch(t, c, `{"show":"bars"}`)
	if err := c.Show("nowplaying", time.Second); err == nil || !strings.Contains(err.Error(), "bars") {
		t.Errorf("under a pin: %v, want an error naming bars", err)
	}
}

func TestPinEndsInterlude(t *testing.T) {
	c, _ := interludeSetup(t)
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20)
	patch(t, c, `{"show":"clock"}`)
	tickAt(c, at(2*time.Second), -20)
	patch(t, c, `{"show":""}`)
	tickAt(c, at(3*time.Second), -20)
	if got := c.State().Active; got == "nowplaying" {
		t.Error("the interlude came back after the pin")
	}
}

func TestInterludeFromDisabledBubbleIgnored(t *testing.T) {
	c, _ := interludeSetup(t)
	zero := 0
	if err := c.PatchBubble("nowplaying", &zero, nil); err != nil {
		t.Fatal(err)
	}
	c.Update(bubble.Interlude{Name: "nowplaying", For: 10 * time.Second})
	tickAt(c, at(time.Second), -20)
	if got := c.State().Active; got != "bars" {
		t.Errorf("active = %q, want bars: weight 0 switches the automatic card off", got)
	}
	if err := c.Show("nowplaying", time.Second); err != nil {
		t.Errorf("Show of a weight 0 bubble: %v, want nil like a pin", err)
	}
}

func TestTrackBubbleNeverLooped(t *testing.T) {
	c := newTest(t, opts(), Entry{newFake("nowplaying", bubble.Track), 1})
	tickAt(c, at(0), -20)
	tickAt(c, at(time.Second), -90)
	if got := c.State().Active; got != "" {
		t.Errorf("active = %q, want blank: a track bubble is in no loop", got)
	}
}

func TestInterludeBrightness(t *testing.T) {
	p := &fakePanel{}
	o := opts()
	o.Panel = p
	c := newTest(t, o, Entry{newFake("clock", bubble.Idle), 1}, Entry{newFake("nowplaying", bubble.Track), 1})
	patch(t, c, `{"brightness":200,"idle_brightness":10}`)
	tickAt(c, at(0), -90)
	if err := c.Show("nowplaying", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tickAt(c, at(time.Second), -90)
	if got := p.bright[len(p.bright)-1]; got != 200 {
		t.Errorf("brightness = %d, want 200 as for music", got)
	}
}
```

- [ ] **Step 2: Run, expect a compile FAIL** — `go test ./controller` (`bubble.Track`, `bubble.Interlude`, `c.Show` undefined).

- [ ] **Step 3: `bubble.go`.** In the package comment's tag list and the `Kind` doc nothing else changes; add the kind and the message:

```go
const (
	Music     Kind = "music"
	Idle      Kind = "idle"
	EventKind Kind = "event"
	Track     Kind = "track" // in no loop: shown as an interlude or through the pin
)
```

```go
// Interlude is a bubble's request to the controller: show the bubble called
// Name for this long, then carry on. The controller ignores it under a pin
// and from a bubble of weight 0.
type Interlude struct {
	Name string
	For  time.Duration
}
```

Change the priority in the `Kind` doc comment to `(event > pin > interlude > music > idle > blank)`.

- [ ] **Step 4: `controller.go`.**

Package comment, line 2: `(event > pin > interlude > music > idle > blank)`.

Fields, after `recheck`:

```go
	interlude      string        // bubble shown for a while, "" = none
	interludeFor   time.Duration // its length
	interludeUntil time.Time     // zero until the first tick after Show
```

`Update`, a case before `captureDone`:

```go
	case bubble.Interlude:
		if e := c.entry(msg.Name); e != nil && e.Weight > 0 { // weight 0 switches a bubble's own requests off
			_ = c.Show(msg.Name, msg.For) // refused under a pin, which is fine
		}
		return c, nil
```

`choose`: `kind, pinned := c.decide(now)` and change the comment on the pinned branch to `// a pin or an interlude is activated once and never looped`.

`decide` becomes:

```go
// decide is the priority: a live event, else the pin, else an interlude, else
// music while playing, else idle, else blank. Events, music and idle need an
// enabled bubble of their own; the pin and the interlude name one bubble.
func (c *Controller) decide(now time.Time) (bubble.Kind, bubble.Bubble) {
	if len(c.events) > 0 && len(c.candidates(bubble.EventKind)) > 0 {
		return bubble.EventKind, nil
	}
	if e := c.entry(c.pin()); e != nil {
		c.interlude = "" // a pin means only this
		return e.Bubble.Kind(), e.Bubble
	}
	if b := c.interluding(now); b != nil {
		return b.Kind(), b
	}
	if c.play && len(c.candidates(bubble.Music)) > 0 {
		return bubble.Music, nil
	}
	if len(c.candidates(bubble.Idle)) > 0 {
		return bubble.Idle, nil
	}
	return "", nil
}

// interluding is the bubble of the live interlude, nil without one. Show has
// no clock, so the first tick after it starts the time.
func (c *Controller) interluding(now time.Time) bubble.Bubble {
	e := c.entry(c.interlude)
	if e == nil {
		return nil
	}
	if c.interludeUntil.IsZero() {
		c.interludeUntil = now.Add(c.interludeFor)
	}
	if !now.Before(c.interludeUntil) {
		c.interlude = ""
		return nil
	}
	return e.Bubble
}
```

`wantBright`:

```go
// wantBright is the brightness for the shown kind: idle and blank dim.
func (c *Controller) wantBright() byte {
	switch c.kind {
	case bubble.Music, bubble.EventKind, bubble.Track:
		return c.set.Brightness
	}
	return c.set.IdleBrightness
}
```

- [ ] **Step 5: `controller/api.go`**, after `Next` (add `"time"` to the imports):

```go
// Show shows a bubble for d, then the loop carries on: an interlude. Like
// the pin it takes any bubble but an event bubble, whatever its weight;
// under a pin it is refused, since a pin means only this.
func (c *Controller) Show(name string, d time.Duration) error {
	if err := c.checkShow(name); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("no bubble named")
	}
	if d <= 0 {
		return fmt.Errorf("duration must be > 0, got %v", d)
	}
	if pin := c.pin(); pin != "" {
		return fmt.Errorf("pinned to %q", pin)
	}
	c.interlude, c.interludeFor, c.interludeUntil = name, d, time.Time{}
	return nil
}
```

- [ ] **Step 6:** `go test ./controller` — PASS, all old tests included.

---

### Task 3: the show route

**Files:**
- Modify: `visualizer/api/api.go`
- Test: `visualizer/api/api_test.go`

**Interfaces:**
- Consumes: `(*controller.Controller).Show(name string, d time.Duration) error`, `controller.ErrNotFound`.
- Produces: `POST /api/v1/bubbles/{name}/show`, body `{"seconds": N}` optional.

- [ ] **Step 1: Write the failing test** (append; the test server registers `bars`, `clock`, `alert`)

```go
func TestPostShow(t *testing.T) {
	h := newTestServer(t)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", nil), http.StatusNoContent)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"seconds":5}`), http.StatusNoContent)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/nope/show", nil), http.StatusNotFound)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/alert/show", nil), http.StatusBadRequest)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"seconds":601}`), http.StatusBadRequest)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"seconds":-1}`), http.StatusBadRequest)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"secs":5}`), http.StatusBadRequest)

	wantStatus(t, doReq(t, h, "PATCH", "/api/v1/controller", `{"show":"bars"}`), http.StatusOK)
	rec := doReq(t, h, "POST", "/api/v1/bubbles/clock/show", nil)
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "bars") {
		t.Errorf("body %s does not name the pin", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./api -run TestPostShow` (404/405 instead of 204).

- [ ] **Step 3: Implement.** Package comment: `eight routes` -> `nine routes`. Route, after the `PATCH /api/v1/bubbles/{name}` line:

```go
	mux.HandleFunc("POST /api/v1/bubbles/{name}/show", s.postShow)
```

Handler, after `bubbleInfo`:

```go
// defaultShow is how long postShow shows a bubble when the body names no time.
const defaultShow = 10

// postShow shows a bubble for {seconds}, 10 without a body or with 0.
func (s *server) postShow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Seconds float64 `json:"seconds"`
	}
	if err := decode(w, r, &body); err != nil && !errors.Is(err, io.EOF) { // EOF: no body at all
		fail(w, http.StatusBadRequest, err)
		return
	}
	if body.Seconds == 0 {
		body.Seconds = defaultShow
	}
	if body.Seconds < 0 || body.Seconds > 600 {
		fail(w, http.StatusBadRequest, fmt.Errorf("seconds must be 0..600, got %v", body.Seconds))
		return
	}
	name, d := r.PathValue("name"), time.Duration(body.Seconds*float64(time.Second))
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) {
		return nil, c.Show(name, d)
	})
}
```

- [ ] **Step 4:** `go test ./api` — PASS.

---

### Task 4: config keys

**Files:**
- Modify: `visualizer/config/config.go`
- Test: `visualizer/config/config_test.go`

**Interfaces:**
- Produces: `Config.MAURL`, `Config.MAToken`, `Config.MAPlayer` (strings); flags `--ma-url`, `--ma-token`, `--ma-player`; keys `ma_url`, `ma_token`, `ma_player`; `MAURL` has no trailing slash.

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./config -run TestMusicAssistant` (`unknown key: ma_url`).

- [ ] **Step 3: Implement.** Struct fields after `Tronbyt`:

```go
	MAURL      string   `mapstructure:"ma_url"`
	MAToken    string   `mapstructure:"ma_token"`
	MAPlayer   string   `mapstructure:"ma_player"`
```

Flags after `tronbyt`:

```go
	f.String("ma-url", "", "now playing: Music Assistant, e.g. http://127.0.0.1:8095 (empty = off)")
	f.String("ma-token", "", "now playing: a long-lived Music Assistant token; better as SPECTRUM_MA_TOKEN than as a flag")
	f.String("ma-player", "", "now playing: the ID of the player whose track is shown")
```

In `Load`, before `return &c, nil` (add `"net/url"` to the imports):

```go
	if c.MAURL != "" {
		if u, err := url.Parse(c.MAURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("ma_url must be http(s)://host:port, got %q", c.MAURL)
		}
		c.MAURL = strings.TrimRight(c.MAURL, "/")
		if c.MAToken == "" {
			return nil, fmt.Errorf("ma_url needs ma_token, a long-lived Music Assistant token")
		}
		if c.MAPlayer == "" {
			return nil, fmt.Errorf("ma_url needs ma_player, the Music Assistant player ID")
		}
	}
```

- [ ] **Step 4:** `go test ./config` — PASS (`TestPrecedence` still passes: the new fields are empty).

---

### Task 5: the bubble: poll and announce

**Files:**
- Create: `visualizer/nowplaying/nowplaying.go`, `visualizer/nowplaying/nowplaying_test.go`

**Interfaces:**
- Consumes: `bubble.Track`, `bubble.Interlude`, `bubble.Patch`, `bubble.Resize/Tick/Activate`.
- Produces: `nowplaying.New(url, token, player string) *NowPlaying`; `nowplaying.Settings{Seconds float64}`; private messages `pollMsg`, `polled`, `cover`; the fields `w, h, frame, title, artist, status, playing, imageURL, loading, t` that Task 6 uses; the hooks `p.wantCover(image string) tea.Cmd` and `p.draw()`, which Task 6 fills in (stubs here).

- [ ] **Step 1: Write the failing tests** — `nowplaying_test.go`:

```go
package nowplaying

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

func init() { pollEvery = time.Millisecond } // pump runs the re-arm tick too

// fakeMA is Music Assistant: /api answers player, /imageproxy/ a cover.
type fakeMA struct {
	mu       sync.Mutex
	status   int    // of /api, 0 = 200
	player   string // the JSON /api answers
	auth     string // the last Authorization header
	body     string // the last /api body
	coverReq string // the last cover request, path?query
	cover    []byte // the PNG /imageproxy/ answers, nil = 404
}

func (m *fakeMA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.URL.Path != "/api" {
		m.coverReq = r.URL.RequestURI()
		if m.cover == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(m.cover)
		return
	}
	buf, _ := io.ReadAll(r.Body)
	m.auth, m.body = r.Header.Get("Authorization"), string(buf)
	if m.status != 0 {
		http.Error(w, "nope", m.status)
		return
	}
	io.WriteString(w, m.player)
}

func (m *fakeMA) set(player string) { m.mu.Lock(); m.player = player; m.mu.Unlock() }

// playerJSON is a players/get answer.
func playerJSON(state, title, artist, image string) string {
	buf, _ := json.Marshal(map[string]any{"playback_state": state,
		"current_media": map[string]string{"title": title, "artist": artist, "image_url": image}})
	return string(buf)
}

// pump runs cmd and feeds what it yields back into p until nothing is left,
// except the re-arm tick: the test sends the next pollMsg itself. It returns
// the interludes p asked for.
func pump(p *NowPlaying, cmd tea.Cmd) []bubble.Interlude {
	if cmd == nil {
		return nil
	}
	var out []bubble.Interlude
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, pump(p, c)...)
		}
	case pollMsg:
	case bubble.Interlude:
		out = append(out, msg)
	default:
		out = append(out, pump(p, p.Update(msg))...)
	}
	return out
}

func start(t *testing.T, m *fakeMA) (*NowPlaying, []bubble.Interlude) {
	t.Helper()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	p := New(srv.URL, "secret", "visualizer")
	return p, pump(p, p.Update(bubble.Resize{W: 64, H: 32}))
}

func TestPollRequest(t *testing.T) {
	m := &fakeMA{player: playerJSON("idle", "", "", "")}
	start(t, m)
	if m.auth != "Bearer secret" {
		t.Errorf("Authorization = %q", m.auth)
	}
	var req struct {
		Command string            `json:"command"`
		Args    map[string]string `json:"args"`
	}
	if err := json.Unmarshal([]byte(m.body), &req); err != nil || req.Command != "players/get" || req.Args["player_id"] != "visualizer" {
		t.Errorf("body = %s (%v)", m.body, err)
	}
}

func TestAnnouncesEachTrackOnce(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "One", "A", "")}
	p, got := start(t, m)
	if len(got) != 1 || got[0] != (bubble.Interlude{Name: "nowplaying", For: 10 * time.Second}) {
		t.Fatalf("first track: %v, want one 10 s interlude", got)
	}
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("same track: %v, want none", got)
	}
	m.set(playerJSON("paused", "One", "A", ""))
	pump(p, p.Update(pollMsg{}))
	m.set(playerJSON("playing", "One", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("pause and resume: %v, want none", got)
	}
	m.set(playerJSON("paused", "Two", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("a skip while paused: %v, want none yet", got)
	}
	m.set(playerJSON("playing", "Two", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 1 {
		t.Errorf("resume on a new track: %v, want one", got)
	}
}

func TestSecondsZeroNeverAnnounces(t *testing.T) {
	m := &fakeMA{player: playerJSON("idle", "", "", "")}
	p, _ := start(t, m)
	if err := p.Configure(json.RawMessage(`{"seconds":0}`)); err != nil {
		t.Fatal(err)
	}
	m.set(playerJSON("playing", "One", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestConfigure(t *testing.T) {
	p := New("http://ma", "t", "p")
	if got := p.Settings().(Settings).Seconds; got != 10 {
		t.Errorf("default seconds = %v, want 10", got)
	}
	for _, bad := range []string{`{"seconds":-1}`, `{"seconds":601}`, `{"secs":1}`} {
		if err := p.Configure(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if got := p.Settings().(Settings).Seconds; got != 10 {
		t.Errorf("a rejected patch changed seconds to %v", got)
	}
}

func TestErrorKeepsTrack(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "One", "A", "")}
	p, _ := start(t, m)
	m.mu.Lock()
	m.status = http.StatusUnauthorized
	m.mu.Unlock()
	pump(p, p.Update(pollMsg{}))
	if p.title != "One" || p.status != "ma: 401" {
		t.Errorf("title = %q status = %q, want One / ma: 401", p.title, p.status)
	}
}

func TestUnknownPlayer(t *testing.T) {
	p, got := start(t, &fakeMA{player: "null"})
	if len(got) != 0 || p.status != "ma?" {
		t.Errorf("interludes %v status %q, want none / ma?", got, p.status)
	}
}

func TestResizeArmsOnce(t *testing.T) {
	p, _ := start(t, &fakeMA{player: "null"})
	if cmd := p.Update(bubble.Resize{W: 32, H: 16}); cmd != nil {
		t.Error("a second Resize started a second poll chain")
	}
}
```

- [ ] **Step 2: Run, expect a compile FAIL** — `go test ./nowplaying`.

- [ ] **Step 3: Implement** `nowplaying.go`:

```go
// Package nowplaying is the track bubble: cover, title and artist of what a
// Music Assistant player plays, laid out like the compact mode of Tronbyt's
// Spotify app. It polls MA's HTTP RPC, also while hidden, since that is how
// it notices a new track; it then asks the controller for an interlude.
package nowplaying

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// pollEvery is the time between two polls; a var so that tests shorten it.
var pollEvery = 2 * time.Second

const maxPoll = 1 << 20

var pollClient = &http.Client{Timeout: 5 * time.Second}

// Settings is what the API patches.
type Settings struct {
	Seconds float64 `json:"seconds"` // how long a new track is shown, 0 = never
}

// pollMsg says the next poll is due.
type pollMsg struct{}

// polled is the answer to one poll.
type polled struct {
	playing              bool
	title, artist, image string
	code                 int // HTTP status of a failed poll
	err                  error
}

// NowPlaying shows the current track of one MA player.
type NowPlaying struct {
	url, token, player string
	set                Settings

	w, h  int
	frame [][]color.RGBA
	armed bool // the poll chain runs

	playing       bool
	title, artist string
	status        string // shown instead of a track: "ma?", "ma: 401"; "" after a good poll
	shown         string // title and artist last announced

	imageURL string          // current_media.image_url as MA sent it
	loading  bool            // its fetch is in flight: the announcement waits
	src      image.Image     // the cover as fetched
	art      [][]color.RGBA  // src at (h-2)^2, nil = none
	t        time.Duration   // since Activate or the last new track: the scroll clock
}

var _ bubble.Bubble = (*NowPlaying)(nil)

// New returns the bubble for one player; url is MA's base URL without a
// trailing slash, token a long-lived token.
func New(url, token, player string) *NowPlaying {
	return &NowPlaying{url: url, token: token, player: player, set: Settings{Seconds: 10}, status: "ma?"}
}

func (p *NowPlaying) Name() string      { return "nowplaying" }
func (p *NowPlaying) Kind() bubble.Kind { return bubble.Track }

// Frame is the current picture, rows top to bottom.
func (p *NowPlaying) Frame() [][]color.RGBA { return p.frame }

func (p *NowPlaying) Settings() any { return p.set }

func (p *NowPlaying) Configure(raw json.RawMessage) error {
	set, err := bubble.Patch(p.set, raw)
	if err != nil {
		return err
	}
	if set.Seconds < 0 || set.Seconds > 600 {
		return fmt.Errorf("seconds must be 0..600, got %v", set.Seconds)
	}
	p.set = set
	return nil
}

func (p *NowPlaying) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		p.w, p.h = msg.W, msg.H
		p.frame = bubble.NewFrame(msg.W, msg.H)
		p.resized()
		p.draw()
		if !p.armed { // the first size starts the one poll chain
			p.armed = true
			return p.poll()
		}
	case bubble.Activate:
		p.t = 0
	case bubble.Tick:
		p.t += msg.Dt
		p.draw()
	case pollMsg:
		return p.poll()
	case polled:
		again := tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{} })
		if msg.err != nil { // keeps the track that is shown
			if p.status = "ma?"; msg.code != 0 {
				p.status = fmt.Sprintf("ma: %d", msg.code)
			}
			return again
		}
		p.status, p.playing = "", msg.playing
		if msg.title != p.title || msg.artist != p.artist {
			p.title, p.artist, p.t = msg.title, msg.artist, 0
		}
		return tea.Batch(again, p.wantCover(msg.image), p.announce())
	case cover:
		return p.gotCover(msg)
	}
	return nil
}

// announce asks for the interlude once per track, and only while it plays: a
// skip while paused is announced on resume. It waits for a cover in flight,
// so the card does not open with a black one.
func (p *NowPlaying) announce() tea.Cmd {
	key := p.title + "\x00" + p.artist
	if !p.playing || p.title == "" || p.loading || key == p.shown {
		return nil
	}
	p.shown = key
	if p.set.Seconds <= 0 {
		return nil
	}
	d := time.Duration(p.set.Seconds * float64(time.Second))
	return func() tea.Msg { return bubble.Interlude{Name: p.Name(), For: d} }
}

// poll asks MA off the frame loop. The chain is poll, polled, tick, poll: at
// most one is in flight.
func (p *NowPlaying) poll() tea.Cmd {
	url, token, player := p.url, p.token, p.player
	return func() tea.Msg { return get(url, token, player) }
}

// get is one players/get. MA resolves sync groups: a member answers with
// what its group plays.
func get(url, token, player string) polled {
	body, _ := json.Marshal(map[string]any{"command": "players/get", "args": map[string]string{"player_id": player}})
	req, err := http.NewRequest(http.MethodPost, url+"/api", bytes.NewReader(body))
	if err != nil {
		return polled{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := pollClient.Do(req)
	if err != nil {
		return polled{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return polled{code: resp.StatusCode, err: fmt.Errorf("music assistant: %s", resp.Status)}
	}
	var pl *struct {
		State string `json:"playback_state"`
		Media *struct {
			Title  string `json:"title"`
			Artist string `json:"artist"`
			Image  string `json:"image_url"`
		} `json:"current_media"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPoll)).Decode(&pl); err != nil {
		return polled{err: err}
	}
	if pl == nil { // MA answers null for a player it does not know
		return polled{err: fmt.Errorf("music assistant: unknown player %q", player)}
	}
	out := polled{playing: pl.State == "playing"}
	if pl.Media != nil {
		out.title, out.artist, out.image = pl.Media.Title, pl.Media.Artist, pl.Media.Image
	}
	return out
}
```

Stubs for Task 6, at the end of the same file (Task 6 moves them out):

```go
type cover struct{}

func (p *NowPlaying) wantCover(string) tea.Cmd { return nil }
func (p *NowPlaying) gotCover(cover) tea.Cmd   { return nil }
func (p *NowPlaying) resized()                 {}
func (p *NowPlaying) draw()                    {}
```

Run `gofmt -w nowplaying/` (the struct's field alignment above is approximate).

- [ ] **Step 4:** `go test ./nowplaying` — PASS. `go vet ./nowplaying` — clean.

---

### Task 6: the bubble: cover and drawing

**Files:**
- Create: `visualizer/nowplaying/cover.go`, `visualizer/nowplaying/draw.go`
- Modify: `visualizer/nowplaying/nowplaying.go` (delete the four stubs and `type cover struct{}`)
- Test: `visualizer/nowplaying/nowplaying_test.go`

**Interfaces:**
- Consumes: the `NowPlaying` fields and hooks of Task 5; `bubble.DrawText`, `bubble.TextWidth`, `bubble.NewFrame`.
- Produces: `cover{url string; img image.Image}`, `coverURL(base, raw string) string`, `shrink(img image.Image, side int) [][]color.RGBA`.

- [ ] **Step 1: Write the failing tests** (append; add `bytes`, `image`, `image/color`, `image/png`, `strings` to the test imports)

```go
var red = color.RGBA{200, 0, 0, 255}

// redPNG is a plain red square.
func redPNG(side int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = red.R, 255
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// tick draws one frame dt later.
func tick(p *NowPlaying, dt time.Duration) { p.Update(bubble.Tick{Dt: dt}) }

// lit reports whether any pixel in the columns x0..x1-1 is on.
func lit(frame [][]color.RGBA, x0, x1 int) bool {
	for _, row := range frame {
		for _, px := range row[x0:x1] {
			if px != (color.RGBA{}) {
				return true
			}
		}
	}
	return false
}

func TestCoverRequestAndPlacement(t *testing.T) {
	m := &fakeMA{cover: redPNG(80),
		player: playerJSON("playing", "One", "A", "http://ma.invalid:8095/imageproxy/abc123?size=512&fmt=jpg")}
	p, got := start(t, m)
	if len(got) != 1 {
		t.Fatalf("interludes = %v, want one, after the cover arrived", got)
	}
	if m.coverReq != "/imageproxy/abc123?size=80&fmt=png" {
		t.Errorf("cover request = %q: want our host, size 80, png", m.coverReq)
	}
	tick(p, 0)
	f := p.Frame()
	if f[1][1] != red || f[30][30] != red {
		t.Errorf("cover corners = %v %v, want red", f[1][1], f[30][30])
	}
	for _, xy := range [][2]int{{0, 0}, {31, 1}, {1, 31}, {32, 15}} {
		if f[xy[1]][xy[0]] != (color.RGBA{}) {
			t.Errorf("pixel %v is lit, want the 1 px padding dark", xy)
		}
	}
	if !lit(f, 33, 64) {
		t.Error("no text right of the cover")
	}
}

func TestCoverErrorStillAnnounces(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "One", "A", "http://ma.invalid/imageproxy/gone")}
	p, got := start(t, m)
	if len(got) != 1 {
		t.Fatalf("interludes = %v, want one although the cover is a 404", got)
	}
	tick(p, 0)
	if lit(p.Frame(), 0, 33) {
		t.Error("cover area is lit without a cover")
	}
}

func TestCoverURL(t *testing.T) {
	for raw, want := range map[string]string{
		"http://other:1/imageproxy/ab?size=512&fmt=jpg": "http://ma:8095/imageproxy/ab?size=80&fmt=png",
		"https://radio.example/logo.png":                "https://radio.example/logo.png",
		"file:///etc/passwd":                            "",
		"":                                              "",
	} {
		if got := coverURL("http://ma:8095", raw); got != want {
			t.Errorf("coverURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestShrinkAverages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4)) // left half white, right half black
	for y := 0; y < 4; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	got := shrink(img, 2)
	if got[0][0] != (color.RGBA{255, 255, 255, 255}) || got[1][1] != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("shrink = %v", got)
	}
	if one := shrink(img, 1); one[0][0].R != 127 {
		t.Errorf("1x1 = %v, want the mean 127", one[0][0])
	}
}

func TestLongTitleScrollsShortDoesNot(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "A very long title indeed", "Abc", "")}
	p, _ := start(t, m)
	snap := func() (title, artist string) {
		var a, b strings.Builder
		for y, row := range p.Frame() {
			for _, px := range row[33:] {
				c := byte('.')
				if px != (color.RGBA{}) {
					c = '#'
				}
				if y < 16 {
					a.WriteByte(c)
				} else {
					b.WriteByte(c)
				}
			}
		}
		return a.String(), b.String()
	}
	tick(p, 0)
	t0, a0 := snap()
	tick(p, time.Second) // inside the 2 s pause
	if t1, _ := snap(); t1 != t0 {
		t.Error("the title moved during the pause")
	}
	tick(p, 2*time.Second) // 1 s past the pause: 12 px
	t2, a2 := snap()
	if t2 == t0 {
		t.Error("the long title did not scroll")
	}
	if a2 != a0 {
		t.Error("the short artist moved")
	}
	if lit(p.Frame(), 0, 33) {
		t.Error("scrolled text reached the cover area")
	}
	p.Update(bubble.Activate{})
	tick(p, 0)
	if t3, _ := snap(); t3 != t0 {
		t.Error("Activate did not reset the scroll")
	}
}

func TestPlaceholder(t *testing.T) {
	p, _ := start(t, &fakeMA{status: http.StatusUnauthorized})
	tick(p, 0)
	if !lit(p.Frame(), 0, 64) {
		t.Error("nothing drawn for ma: 401")
	}
	if p.status != "ma: 401" {
		t.Errorf("status = %q", p.status)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./nowplaying` (`coverURL`, `shrink` undefined).

- [ ] **Step 3: Implement `cover.go`** and delete the five stub lines (`type cover struct{}` and the four methods) from `nowplaying.go`; its imports stay as they are:

```go
package nowplaying

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // radio logos
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

const maxCover = 4 << 20

var coverClient = &http.Client{Timeout: 10 * time.Second}

// cover is the answer to one cover fetch; img is nil when it failed.
type cover struct {
	url string // the image_url it was fetched for
	img image.Image
}

// coverURL is where to fetch MA's image_url from. MA builds its imageproxy
// URLs with its own base URL, which need not be reachable from here, and in
// 512 px: take the path, from our MA, in the smallest size MA allows. Any
// other http(s) URL (radio) is used as it is, anything else is "".
func coverURL(base, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if strings.HasPrefix(u.Path, "/imageproxy/") {
		return base + u.Path + "?size=80&fmt=png"
	}
	return raw
}

// wantCover starts the fetch when the track has another image than the one
// held; the announcement waits for it.
func (p *NowPlaying) wantCover(image string) tea.Cmd {
	if image == p.imageURL {
		return nil
	}
	p.imageURL, p.src, p.art = image, nil, nil
	from := coverURL(p.url, image)
	if from == "" {
		p.loading = false
		return nil
	}
	p.loading = true
	return func() tea.Msg {
		img, _ := fetch(from) // a failed cover is a black one
		return cover{url: image, img: img}
	}
}

// gotCover takes the cover unless the track moved on meanwhile, and makes the
// announcement that waited for it.
func (p *NowPlaying) gotCover(c cover) tea.Cmd {
	if c.url != p.imageURL {
		return nil
	}
	p.loading, p.src = false, c.img
	p.resized()
	return p.announce()
}

// resized scales the cover for the current height.
func (p *NowPlaying) resized() {
	if p.art = nil; p.src != nil {
		p.art = shrink(p.src, p.h-2)
	}
}

func fetch(from string) (image.Image, error) {
	resp, err := coverClient.Get(from)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: %s", resp.Status)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, maxCover))
	return img, err
}

// shrink scales img to side x side by averaging the source pixels under each
// target pixel; nil for nothing to draw. Covers are square: another shape is
// squashed.
func shrink(img image.Image, side int) [][]color.RGBA {
	b := img.Bounds()
	if side <= 0 || b.Empty() {
		return nil
	}
	out := bubble.NewFrame(side, side)
	for y := range side {
		y0, y1 := b.Min.Y+y*b.Dy()/side, b.Min.Y+(y+1)*b.Dy()/side
		for x := range side {
			x0, x1 := b.Min.X+x*b.Dx()/side, b.Min.X+(x+1)*b.Dx()/side
			var r, g, bl, n uint32
			for sy := y0; sy < max(y1, y0+1); sy++ { // at least one: a source smaller than side
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, _ := img.At(sx, sy).RGBA()
					r, g, bl, n = r+cr>>8, g+cg>>8, bl+cb>>8, n+1
				}
			}
			out[y][x] = color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255}
		}
	}
	return out
}
```

- [ ] **Step 4: Implement `draw.go`:**

```go
package nowplaying

import (
	"image/color"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

const (
	scrollSpeed = 12.0            // px/s
	scrollPause = 2 * time.Second // before a line starts to move
	scrollGap   = 3 * 6           // px between a line and its repeat: three glyphs
)

var (
	white = color.RGBA{255, 255, 255, 255}
	green = color.RGBA{0x1d, 0xb9, 0x54, 255}
	dim   = color.RGBA{153, 153, 153, 255} // the clock's
)

// draw lays the track out like Tronbyt's compact Spotify mode: the cover in a
// square of the panel's height with 1 px of padding, title over artist right
// of it, centred on the middle row.
func (p *NowPlaying) draw() {
	if p.w == 0 || p.h == 0 {
		return
	}
	for _, row := range p.frame {
		clear(row)
	}
	if p.title == "" { // no track: say why, so a wrong token shows
		s := p.status
		if s == "" {
			s = "-"
		}
		bubble.DrawText(p.frame, (p.w-bubble.TextWidth(s, 1))/2, (p.h-7)/2, s, dim, 1)
		return
	}
	p.line(p.title, p.h/2-8, white)
	p.line(p.artist, p.h/2+1, green)
	// the cover's square goes over the text: that is what clips the scroll
	for y, row := range p.frame {
		clear(row[:min(p.h+1, p.w)])
		if y >= 1 && y-1 < len(p.art) {
			copy(row[1:], p.art[y-1])
		}
	}
}

// line draws s from the text column on. A line that does not fit waits
// scrollPause, then moves left with a repeat of itself behind a gap, and
// waits again when the repeat has reached the start.
func (p *NowPlaying) line(s string, y int, c color.RGBA) {
	x0 := p.h + 1
	width := bubble.TextWidth(s, 1)
	if width <= p.w-x0 {
		bubble.DrawText(p.frame, x0, y, s, c, 1)
		return
	}
	period := width + scrollGap
	cycle := scrollPause + time.Duration(float64(period)/scrollSpeed*float64(time.Second))
	var off int
	if t := p.t % cycle; t > scrollPause {
		off = int((t - scrollPause).Seconds() * scrollSpeed)
	}
	bubble.DrawText(p.frame, x0-off, y, s, c, 1)
	bubble.DrawText(p.frame, x0-off+period, y, s, c, 1)
}
```

Note for `copy(row[1:], p.art[y-1])`: on a panel narrower than its height the copy is cut by `row`'s length, which is fine.

- [ ] **Step 5:** `go test ./nowplaying` — PASS. `go vet ./...` — clean.

---

### Task 7: wire it up

**Files:**
- Modify: `visualizer/cmd/spectrum/main.go`, `addon/config.yaml`, `README.md`

**Interfaces:**
- Consumes: `config.Config.MAURL/MAToken/MAPlayer`, `nowplaying.New`.

- [ ] **Step 1: `main.go`.** Import `github.com/jon4hz/loudest-office/visualizer/nowplaying` (alphabetically after `music/stars`). After the Tronbyt `if/else` that appends the clock:

```go
	if c.MAURL != "" { // a track bubble is in no loop: it shows itself on a new track
		entries = append(entries, controller.Entry{Bubble: nowplaying.New(c.MAURL, c.MAToken, c.MAPlayer), Weight: 1})
	}
```

Add a usage line to the file's header comment, after the `-show fire` line:

```go
//	spectrum -ma-url http://ma:8095 -ma-player visualizer  # new tracks show cover, title, artist; token: SPECTRUM_MA_TOKEN
```

- [ ] **Step 2: `addon/config.yaml`**, in `schema:` after the `tronbyt` line:

```yaml
  ma_url: str? # now playing: http://127.0.0.1:8095
  ma_token: password? # a long-lived token from the Music Assistant profile settings
  ma_player: str? # the player ID, e.g. visualizer
```

Bump `version` to `"0.2.0"` (Home Assistant only offers a rebuild's new schema with a new version).

- [ ] **Step 3: Try it against nothing.** In `visualizer/`:

```bash
go build -o /tmp/spectrum-np ./cmd/spectrum
SPECTRUM_MA_TOKEN=x /tmp/spectrum-np --ma-url http://127.0.0.1:1 --show nowplaying --cmd cat --args /dev/zero </dev/null >/dev/null; echo "exit $?"
SPECTRUM_MA_TOKEN=x timeout 3 /tmp/spectrum-np --ma-url http://127.0.0.1:1 --ma-player nope --show nowplaying --cmd cat --args /dev/zero </dev/null >/dev/null; echo "exit $?"
```

Expected: the first exits 1 with `ma_url needs ma_player`; the second runs headless (stdout is no terminal) against an MA that is not there until `timeout` ends it: `exit 124`, no panic. `--show nowplaying` being accepted proves the bubble is registered. Use the session's scratch directory instead of `/tmp` if there is one. In a real terminal the same command shows the dim `ma?` centred; that check is the user's.

- [ ] **Step 4: README.** Read the API section (around the `curl` examples near line 100) and add, in its style:

```markdown
Show a bubble for a few seconds, then carry on (not under a pin):

    curl -X POST loudestoffice.local:8099/api/v1/bubbles/nowplaying/show -d '{"seconds":8}'
```

and a new part after the Tronbyt part:

```markdown
#### Now playing

With `ma_url`, `ma_token` and `ma_player` set, a new track shows its cover,
title and artist for 10 s (`PATCH /api/v1/bubbles/nowplaying
{"settings":{"seconds":10}}`, 0 = never; weight 0 does the same). The token is a
long-lived one from the Music Assistant profile settings (a guest user is
enough; it expires after a year, the panel then says `ma: 401` when
`nowplaying` is shown). The player ID is in the player's settings in Music
Assistant; a member of a sync group answers for its group. Accented letters
are drawn as their base letter.
```

- [ ] **Step 5:** `CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...` — all PASS. `task visualizer:build` — builds.

---

### Task 8: Ansible and the vault

**Files:**
- Modify: `ansible/roles/visualizer/defaults/main.yml`, `ansible/roles/visualizer/templates/config.yaml.j2`, `ansible/ansible.cfg`, `README.md`
- Create: `ansible/inventory/group_vars/raspi/visualizer.yml`

- [ ] **Step 1: Role defaults**, appended to `defaults/main.yml`:

```yaml
# Now playing: Music Assistant's URL as the container sees it (host network),
# the player ID and a long-lived token. The token belongs in the vault:
# see inventory/group_vars/raspi/visualizer.yml. Empty URL = off.
visualizer_ma_url: ""
visualizer_ma_player: ""
visualizer_ma_token: ""
```

- [ ] **Step 2: Template** `config.yaml.j2` becomes:

```jinja
{{ ansible_managed | comment }}
{% set ma = {'ma_url': visualizer_ma_url, 'ma_player': visualizer_ma_player, 'ma_token': visualizer_ma_token} if visualizer_ma_url else {} %}
{{ visualizer_config | combine(ma) | to_nice_yaml }}
```

(The file is deployed `0640` to the compose user by the `podman_compose` role already.)

- [ ] **Step 3: Vault password.** `ansible/ansible.cfg`, in `[defaults]`:

```ini
vault_password_file = vault.txt
```

The path is relative to `ansible.cfg`. The playbook runs in the execution environment, which mounts `ansible/` at its own path, so `ansible/vault.txt` is there without an extra mount; `Taskfile.yml` does not change.

- [ ] **Step 4: Inventory.** Create `ansible/inventory/group_vars/raspi/visualizer.yml`:

```yaml
---
# Now playing. The token: create a long-lived one in the Music Assistant
# profile settings, then in ansible/:
#   uv run ansible-vault encrypt_string --stdin-name visualizer_ma_token
# (paste, Ctrl-D twice) and put the output below. Then uncomment the URL.
# visualizer_ma_url: http://127.0.0.1:8095
# visualizer_ma_player: <player id from the Music Assistant player settings>
```

- [ ] **Step 5: Verify the plumbing without the secret.** In `ansible/`: `uv run ansible-lint` — clean. `task run -- --tags visualizer --check --diff` — no change to `config.yaml` (the URL is still commented), and no "vault password file not found" error, which proves the execution environment sees the file. If the Pi is not reachable, `uv run ansible-navigator run playbooks/site.yml --syntax-check` proves the same, since ansible loads the vault password at start.

- [ ] **Step 6: Hand over to the user.** The token must not pass through the agent. Tell the user: create the token, run the `encrypt_string` line, paste the `visualizer_ma_token: !vault |` block into the file, fill in the player ID, uncomment both lines, `task run -- --tags visualizer`. Note for them: `--diff` prints the rendered config, token included, to their terminal.

- [ ] **Step 7: README**, in the Ansible part: one paragraph: secrets are in ansible-vault, the password is `ansible/vault.txt` (gitignored; without it `task run` fails), and the `encrypt_string` line.

---

## Verification (after all tasks)

- `cd visualizer && CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...`
- `git status --short ansible/vault.txt` prints nothing; `git check-ignore ansible/vault.txt` prints the path.
- On the hardware (user): a track change shows the card for 10 s, then the music loop continues; `curl -X POST <host>:8099/api/v1/bubbles/nowplaying/show` shows it again; under a pin the same call answers 400 naming the pin.
