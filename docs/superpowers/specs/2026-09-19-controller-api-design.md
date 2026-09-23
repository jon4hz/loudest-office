# Controller, bubbles and config API

Covers phases 3 to 5 of `esp_visualizer_plan.md`, reshaped: bubbles instead of
the `Scene` interface, a TCP API instead of the unix socket, and the config
surface a web UI needs. The web UI, Tronbyt, auth and `psu-monitor` are out of
scope and get their own spec.

## Problem

`spectrum.Model` does the audio analysis, holds the state of all six modes,
dispatches them with two `switch m.mode` statements and renders the terminal
view. The loop (`flavor`, `nextFlavor`, `loopTick`) sits in `cmd/spectrum`.
Frames only exist while audio blocks arrive, nothing knows whether music is
playing, and nothing can be configured at runtime.

## Decisions

- Flat: every mode is a peer **bubble** behind one interface, tagged `music`,
  `idle` or `event`. The controller picks the kind (event > pin > music > idle
  > blank) and loops within it by weight.
- Fixed `tea.Tick` frame clock at `--fps`. The analysis stays per audio block.
- TCP HTTP API, stdlib `net/http`, no auth yet, `--listen` (empty = off).
- Runtime settings persist to a JSON state file (`--state`); `config.yaml`
  stays Ansible-owned and holds hardware settings only.
- Music detection is RMS silence detection with two thresholds and a timer.
- This round ships a 5x7 bitmap font, a scrolling-text alert bubble, a clock
  idle bubble and runtime brightness with idle dimming.

## Packages (`visualizer/`)

- `dsp`: new `Analyzer`. Everything audio-derived moves here from
  `Model.Update` with its per-block constants untouched (levels, AGC, drop
  detector with its 30-block refractory counter, onset flux, tempo, beat
  phase), plus RMS dBFS clamped to >= -120 (JSON cannot carry `-Inf`).
  `Add(block)` per audio block, `Take() Signal` per frame. `Drop`/`BigDrop`
  are latched until `Take`. `Take` never returns zeroed levels when no block
  arrived: `Add` overwrites levels that were already taken and max-merges
  otherwise.
- `bubble`: the `Bubble` interface (`Name`, `Kind`, `Update(tea.Msg) tea.Cmd`,
  `Frame`, `Settings`, `Configure(json.RawMessage)`), msgs `Tick{Signal, Dt,
  Now}`, `Resize`, `Activate`, `Event`; `Levels`; `NewFrame`; `Render` (the
  half-block renderer); generic `Patch` (deep copy, unknown fields rejected,
  so a failed patch never touches live settings); the font and `DrawText`.
- `palette`: the palettes, moved out of `spectrum` so modes and the API share
  them.
- `music`: the exported `Base` every music bubble embeds (frame, size,
  stepper, palette pick, `CellColour`), plus `music/musictest` with the shared
  test helpers. One package per mode below it: `music/bars`, `music/fire`,
  `music/life`, `music/stars`, `music/fireworks`, `music/parrot`, each with
  `New()`. Package `spectrum`, `Model`, the `Mode` enum and both switches go
  away.
- `clock`, `alert`: HH:MM with a blinking colon; scrolling text coloured by
  level.
- `controller`: the root `*Controller` tea.Model and the state file.
- `api`: the handlers.

Adding a bubble or a mode later is one package plus one registry line in
`main.go`.

## Fixed timestep

`step = time.Second * 1024 / 44100`. Each music bubble runs its existing
per-block step `stepper.steps(dt)` times per `Tick` (integer `time.Duration`
accumulator), so no physics constant is retuned and a test sending
`Tick{Dt: step}` gets exactly one step.

- Latched fields are handled once per Tick, outside the step loop, so a drop
  survives a Tick with zero steps. Fireworks volley and stars warp before the
  loop, bars hold clearing and scatter after it (the legacy order).
- Bars `burst` and stars `warp` are bubble-local timers started by
  `Signal.Drop`.
- Bars fade trails once per step and do not redraw on zero steps.
- Bubbles take the band count from `len(sig.Mix)`.
- The controller clamps `dt` to 250 ms, 0 on the first tick.
- Known change: at 48 kHz capture the physics go from 46.9 to 43.07 steps/s.

## Controller

Per frame tick, using the tick's time so tests use synthetic times:

1. `sig := analyzer.Take()`.
2. Playing: `DB >= music_db` sets it and resets the quiet timer; `DB <
   silence_db` for `silence_after` seconds clears it; in between resets the
   timer.
3. Expire events. The queue holds at most 32; the alert bubble gets one
   `Event` with the texts of the highest live level joined by `" +++ "`,
   re-sent on any queue change.
4. Kind: events, else `show`, else music while playing, else idle. The first
   kind with an enabled bubble wins, else a zeroed panel-sized frame.
5. On kind change, loop deadline, disabled active bubble or `Next()`: pick by
   weight (`random`) or registry order (`sequence`), send `Activate`, set the
   deadline. `Activate` makes the bubble choose its own variation from its
   settings. Bars: a palette that hides bars paired with `none` peaks becomes
   `fall`.
6. Forward `Tick`, send the frame, set the brightness when the wanted value
   changed (`idle_brightness` while idle), cache the rendered view.

Unhandled messages go to every bubble and their cmds are batched, so a bubble
can poll on its own. With a panel the size is fixed from INFO; otherwise
`tea.WindowSizeMsg` becomes `Resize{W, 2H}` for all bubbles. Keys: `q`, `m` pin
the next bubble, `a` clear the pin / toggle the loop, `+`/`-` bands; the rest
goes to the active bubble (`c`/`C`; bars `l`, `p`, `t`). Nothing under
`controller/` or a bubble holds the `*tea.Program`: `Send` from inside
`Update` deadlocks.

## Settings and state file

```json
{
  "controller": {"music": {"loop": 60, "order": "random"}, "idle": {"loop": 60, "order": "random"},
                 "show": "", "music_db": -50, "silence_db": -60, "silence_after": 5,
                 "brightness": 64, "idle_brightness": 64},
  "bubbles": {
    "bars":  {"weight": 8, "settings": {"palettes": [], "layouts": ["mirror", "hmirror"], "peaks": [], "trails": 0.05}},
    "fire":  {"weight": 1, "settings": {}},
    "life":  {"weight": 2, "settings": {"palettes": []}},
    "alert": {"weight": 1, "settings": {"speed": 30}}
  }
}
```

Default weights (bars 8, fire 1, life 2, stars 2, fireworks 2, parrot 1, clock
1, alert 1) and the 60 s loop reproduce the deployed behaviour and live in Go.
Weight 0 disables, loop 0 stays, empty lists mean all. Written via tmp file,
`Sync`, rename after every successful change. Loading is tolerant: a missing
file, unknown bubbles and bad per-bubble settings fall back to defaults; only
unparseable JSON is an error.

## API

```
GET    /api/v1/state            active, kind, playing, db, bpm, settings, events, size, panel status
PATCH  /api/v1/controller       settings, merged; {"show":"fire"} pins, {"show":""} unpins
GET    /api/v1/bubbles          [{name, kind, weight, settings}]
PATCH  /api/v1/bubbles/{name}   {"weight": 2, "settings": {...}}, both optional, merged
GET    /api/v1/options          {palettes, layouts, peaks, levels, orders}; a settings field takes
                                its values from the option of the same name
POST   /api/v1/next             skip to the next pick
POST   /api/v1/events           {id?, text, level, ttl} -> 201 {id}; the same id replaces
DELETE /api/v1/events/{id}
```

JSON, durations in seconds, errors as `{"error": "..."}`. Bodies are size
limited, unknown fields rejected, text <= 256 characters, `0 < ttl <= 24h`
(mandatory: a crashed sender must not wedge the display), names checked
against the registry and the option lists, with errors naming what is allowed.

Handlers never touch state. `api.New(do func(controller.Call) error)`: in
`main.go`, `do` sends the call with `p.Send` and waits for it or for a context
cancelled right after `p.Run()` returns (503). Responses are marshalled inside
the call. `net.Listen` happens before `Run` so a busy port fails fast.

## Config, serial, shutdown

- Flags kept: `cmd device rate bands gain mono autogain serial baud
  brightness`. Added: `listen`, `state`, `fps` (30, validated 1..120), `show`
  (pins at start without saving). Removed, now in the state file: `palette
  layout peaks mode trails loop loop-layouts loop-modes`.
- `serial.Port.SetBrightness`: the writer sends CONFIG before the next frame;
  a reconnect re-sends the last value.
- No `signal.NotifyContext`: bubbletea handles SIGTERM even headless and quits
  cleanly, then the deferred `Close` blanks the panel.

## Deploy

`network_mode: host`, a `./data:/data` volume (a directory: rename over a
bind-mounted file is EBUSY), `TZ` and `tzdata` for the clock, `listen: ":8099"`,
`state: /data/state.json`, `fps: 20`, ufw allow 8099/tcp (no auth, LAN only).

## Not built

- Tronbyt, web UI, auth, SSE/websocket, `GET /frame.png`, switch on drop,
  crossfade, a PUT of the whole document.

## Verification

- `go vet ./... && go test ./...` in `visualizer/`.
- Laptop: music loops bubbles, pause shows the clock after `silence_after`,
  PATCH a palette list and see it persist across a restart, POST an event and
  see it scroll and expire, `kill -TERM` exits 0.
- Pi: brightness PATCH changes the panel, idle dims, `podman stop` blanks it,
  `crc_err`/`seq_gaps` in `/state` stay 0.
