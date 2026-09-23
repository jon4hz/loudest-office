# Tronbyt as the idle loop

While nothing plays, the home Tidbyt shows what a Tronbyt server renders for
it. The Tidbyt runs our ESPHome firmware, not Tronbyt's, so the visualizer acts
as the Tronbyt device: an idle bubble pulls the device's images and plays them.

## What is known (checked 2026-09-20)

- Tronbyt Server (`tronbyt/server`, Go) runs on the Home Assistant Pi as the
  community app `af627ec0_tronbyt_server`
  (`https://github.com/kaffolder7/ha-app-tronbyt-server`, a wrapper around the
  official image), web UI on `:8000`, data in the app's `/data/tronbyt`. The
  first registered account is the admin; devices are created in the web UI
  only (type "Tidbyt Gen1": 64x32).
- `GET /{device_id}/next` answers `image/webp` with `Tronbyt-Dwell-Secs`
  (seconds to show it, default 15) and `Tronbyt-Brightness`. No auth by
  default: the device ID in the path is the secret. Every call advances the
  server's app rotation, so it is called once per dwell period and only while
  the picture is shown.
- The images are animated, lossless WebP. `golang.org/x/image/webp` does not
  decode animations. `github.com/gen2brain/webp` v0.6.4 (MIT, libwebp as WASM,
  no cgo) does: `DecodeAll` returns composited `*image.RGBA` frames and their
  delays in ms. Tried on `apps/nyancat/nyancat.webp` from `tronbyt/apps`:
  12 frames, 4 ms, `CGO_ENABLED=0`, arm64 builds.
- That library also imports `purego` to use a system libwebp when there is
  one. Even with `CGO_ENABLED=0` this links the binary against glibc's loader,
  which Alpine lacks (`exec /spectrum: no such file or directory`, seen on the
  Pi). Its build tag `nodynamic` leaves only the WASM path: `task
  visualizer:build` builds with `-tags nodynamic`, and `file` must say
  "statically linked".

## Decisions

- Poll `/next` over HTTP. The WebSocket only adds pushed interrupts; our own
  events API already covers alerts.
- One new config key, `tronbyt`: the full `/next` URL, e.g.
  `http://127.0.0.1:8000/<device_id>/next`. Empty (the default): the bubble is
  not registered and nothing changes, as at the office.
- With `tronbyt` set the bubble registers with weight 1 and the clock's
  default weight becomes 0: Tronbyt is the idle loop and has clock apps of its
  own. A state file that already holds a weight for the clock wins over this
  default; then `PATCH /api/v1/bubbles/clock {"weight":0}` once.
- Brightness stays the controller's `idle_brightness`; the server's
  brightness header is ignored.

## The bubble: `visualizer/tronbyt`

`New(url string) *Tronbyt`, name `tronbyt`, kind idle, no settings (like the
clock).

- `Activate`: fetch, unless a fetch is already in flight.
- The fetch is a `tea.Cmd` (off the frame loop): GET with a 10 s timeout, a
  body limit of 1 MiB, status 200 required, `webp.DecodeAll`, dwell from the
  header (missing, unparseable or < 1: 15 s). It answers with a message
  carrying frames, delays, dwell or the error. The controller already hands
  unknown messages to every bubble.
- Result, ok: replace the animation, start at frame 0, show it for `dwell`.
  Result, error: keep the last animation (an outage shows stale content, not
  black) and retry after 10 s. The error is not logged: in a terminal stderr
  belongs to the TUI, and the `tronbyt?` text below shows a server that never
  answered.
- `Tick`: advance frames by `Dt` and their delays (a delay of 0 counts as
  100 ms, a single frame stays), looping. When the dwell time or the retry
  wait is over: fetch again. Ticks only arrive while the bubble is shown, so
  it polls only then; a result that arrives after it was hidden is kept and
  shown on the next activation until its dwell runs out.
- Before the first image the frame shows the text `tronbyt?`, so a wrong URL
  is visible instead of a black panel.
- Frames are copied into the panel-sized frame top-left and clipped; nothing
  is scaled.

`cmd/spectrum/main.go`: the registry line and the clock's weight, both
depending on `c.Tronbyt`. `config`: the `tronbyt` string flag. `addon/config.yaml`:
`tronbyt: str?` in the schema only, no default: the device ID is a secret and
the repo is public. README: a short part under "At home".

## Not built

- The WebSocket path, a per-device API key, scaling for other panel sizes, the
  server's brightness, installing the Tronbyt app from the Taskfile (it is a
  store app: `ha store add …`, install, done once).

## Verification

- `tronbyt_test.go`: against an `httptest` server that serves the nyancat
  fixture with `Tronbyt-Dwell-Secs: 2`: after `Activate` and the fetch the
  frame is not blank and changes over ticks; after 2 s of ticks a second
  request was made; an error answer keeps the last picture and retries after
  10 s of ticks; no second fetch starts while one is in flight.
- `CGO_ENABLED=0 go build ./...`, `go vet ./...`, `go test ./...`.
- On the Pi: with the device ID in the app's options the Tidbyt rotates the
  Tronbyt apps while idle and switches to bars when the group plays.
