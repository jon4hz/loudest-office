# Home setup: Tidbyt over WiFi, audio from Sendspin

The visualizer at home: a Tidbyt v1 as the panel, fed over WiFi by ESPHome
firmware, with the audio taken from Music Assistant instead of an ADC. The
office setup (HiFiBerry ADC, devkit on USB serial) keeps working unchanged.

Three independent pieces, in build order:

1. `serial`: a TCP transport next to the tty one.
2. `esphome/`: an ESPHome config for the Tidbyt plus the external component
   `panel_stream`, which speaks the existing wire protocol on a TCP port.
3. `cmd/sendspin-pipe`: a Sendspin player that plays into stdout, used as the
   capture command.

The Tronbyt idle bubble and the home deployment get their own specs. The
deployment will be a local Home Assistant add-on; see "Add-on direction" for
what that asks of the pieces built here.

## What is known

- Tidbyt v1: ESP32-D0WD-V3, 8 MB flash, no PSRAM, CP2102N bridge (2 Mbaud
  works), auto-reset wired, no secure boot. Checked with esptool on 2026-09-19.
- Pin map (from `tronbyt/firmware-esp32`, `main/display.cpp`):
  R1 2, G1 22, B1 21, R2 4, G2 27, B2 23, A 26, B 5, C 25, D 18, no E, LAT 19,
  OE 32, CLK 33. A colour-swapped hardware variant exists; the boot bars show
  it. The panel is fed from the USB port, so the brightness is capped at 100
  of 255, as Tronbyt does.
- The panel needs an 8 MHz pixel clock. ESPHome's `hub75` defaults to 20 MHz,
  which shows one garbled row; `clock_speed: 8MHZ` fixes it (the default clock
  phase is fine). Over WiFi the ESPHome firmware then runs at 30 fps with no
  CRC errors and no sequence gaps (1133 frames in 40 s, 2026-09-19).
- The PlatformIO env `tidbyt` runs on it today: 43 fps at 2 Mbaud with about
  5 % CRC errors, most likely the link at 88 % load. The 30 fps frame clock of
  the controller spec brings that to 60 %; measure again then. This env stays
  as the wired fallback.
- The stock firmware is gone (overwritten before a backup finished). The way
  back to a normal Tidbyt is the Tronbyt Gen1 firmware.
- Home audio: Music Assistant plays Spotify to Sonos (a Playbase with two One
  as surrounds, a Play:5 in another room). Since MA 2.8 the Sendspin bridges
  put AirPlay-capable Sonos players into a synced Sendspin group; this works
  here including the Play:5. `music-assistant/support#6403` (several Sonos
  plus a Sendspin-only member, only the coordinator plays) is open and has to
  be tried with the real group.
- Sendspin is a preview in MA, its spec is at RC1, and `sendspin-go` announces
  a hard cutover to spec v2. Expect to pin versions.

## Decisions

- ESPHome owns the Tidbyt firmware: WiFi, OTA, logs, the HUB75 driver
  (`display: platform: hub75`). We only add the frame receiver.
- TCP, not UDP: a frame is 4 KB, above the MTU, and lwIP reassembly is off by
  default in ESP-IDF. TCP also gives a connection to hang HELLO/INFO on.
- The wire protocol does not change. `proto.h` is shared by both firmwares.
- The host stays the master of the brightness (CONFIG). No brightness entity
  in Home Assistant: two masters for one value, and the controller API already
  has it.
- `spectrum` stays free of cgo. `sendspin-go` needs miniaudio and libopus
  through cgo, so the Sendspin client is its own small binary and `spectrum`
  reads it like it reads `arecord`. No change to `audio`.

## 1. TCP transport (`visualizer/serial`)

`--serial tcp://host:7090` dials TCP; anything else is a tty as before. The
flag and the package keep their names.

- `connect` works on a small interface (`io.ReadWriteCloser`,
  `SetReadDeadline`, `SetWriteDeadline`) that `*os.File` and `*net.TCPConn`
  both satisfy. `openRaw`, `bootWait`, `settle` and `flushInput` apply to the
  tty only.
- TCP: `net.DialTimeout` 3 s, `SetWriteBuffer(8192)`. The depth-1 frame
  channel means "never queue a frame"; a default socket buffer would queue
  dozens of them during a WiFi stall and show them late.
- Both transports: a 2 s write deadline per packet, and no STATUS for 5 s is an
  error. Either one closes the connection and enters the existing reconnect
  loop. Without them a dead WiFi peer blocks `Write` forever.
- The host name is resolved by Go's resolver. `.local` names do not resolve in
  the alpine container, so the config takes an IP or a DNS name.
- Test: a fake panel on `net.Listen("tcp", "127.0.0.1:0")` answers HELLO with
  INFO; assert CONFIG and FRAME arrive, and that closing the listener side
  leads to a reconnect. The tty tests stay as they are.

## 2. ESPHome (`esphome/`)

```
esphome/
  tidbyt.yaml
  secrets.yaml                 # gitignored
  components/panel_stream/
    __init__.py
    panel_stream.h
    panel_stream.cpp
    proto.h -> ../../../firmware/src/proto.h
```

`tidbyt.yaml`: `esp32: board: esp32dev`, `flash_size: 8MB`, framework
esp-idf, `wifi`, `api`, `ota`, `logger` on USB as usual (the UART is free in
this setup), and

```yaml
external_components:
  - source: { type: local, path: components }

display:
  - platform: hub75
    id: panel
    panel_width: 64
    panel_height: 32
    double_buffer: true
    update_interval: never
    r1_pin: 2    # pin map as above
    ...
    lambda: id(stream).draw(it);

panel_stream:
  id: stream
  display_id: panel
  port: 7090
  max_brightness: 100
```

`panel_stream` is a `Component`:

- `setup`: a non-blocking lwIP listen socket. `loop`: accept, read what is
  there, feed the `Parser` from `proto.h`. One client; a new connection
  replaces the old one, so a restarted host is never locked out by a half-open
  socket. While a client is connected the component holds a
  `HighFrequencyLoopRequester`, otherwise ESPHome's 16 ms loop would cap the
  frame rate and add latency.
- Messages as in `firmware/src/main.cpp`: HELLO answers INFO; FRAME copies the
  payload into a 4096 byte buffer and calls `display->update()`; `draw()`
  hands that buffer to `it.draw_pixels_at(...)` as little-endian RGB565 in one
  call and the display flips; CONFIG sets
  `min(value, max_brightness)`; BLANK clears. STATUS goes to the client once a
  second. `seq` gaps and CRC errors are counted as before; over TCP both must
  stay 0, anything else is a bug.
- Before the first frame `draw()` paints the red, green and blue boot bars;
  2 s without a FRAME, or a closed connection, paints the red cross.
- `dump_config` logs the port; the counters are logged at DEBUG once a second.

The colour-swapped variant is a second pin block in `tidbyt.yaml`, commented
out. `task esphome:run` wraps `esphome run esphome/tidbyt.yaml`. The first
flash goes over USB, later ones over the air.

Not under ESPHome: the serial transport. The USB port is for the first flash
and the logs; the wired fallback is the PlatformIO env.

## 3. Sendspin audio (`visualizer/cmd/sendspin-pipe`)

A Sendspin player whose loudspeaker is stdout.

```
sendspin-pipe --server ma.home:8927 --name Visualizer --delay-ms 0
```

- Its own Go module (`visualizer/cmd/sendspin-pipe/go.mod`), so cgo,
  miniaudio and libopus stay out of the `spectrum` build. It is built inside
  the container image (`apk add build-base opus-dev opusfile-dev`), not
  cross-compiled.
- `sendspin.NewPlayer` with a custom `output.Output`: `Open` checks the format,
  `Write` converts the `int32` samples to S16LE and writes them to stdout,
  volume and mute are ignored. The library's scheduler releases the samples at
  their play time, so stdout runs in step with the speakers.
- `MaxSampleRate: 44100`, `MaxBitDepth: 16`, `PreferredCodec: "pcm"`. The
  AirPlay leg is 44.1 kHz/16 bit anyway and `spectrum` runs at `rate: 44100`.
  `Open` with anything but 44100 Hz stereo exits non-zero.
- Silence: when `Write` was not called for 100 ms, a ticker writes zero blocks
  in real time. Without them `spectrum` sees no blocks while nothing plays,
  the analyzer keeps its last levels and the controller never goes idle.
- A stable `ClientID` from `--id` (default `visualizer`), `Reconnect` enabled,
  `--delay-ms` maps to `StaticDelayMs` (0 to 5000). Logs go to stderr.
- `spectrum` side: `cmd: sendspin-pipe` and the new key `args` (a list, passed
  to `audio.Config.Args`, which exists). Because the pipe's format is fixed,
  `config.Load` rejects `cmd: sendspin-pipe` without `args`, with a `rate`
  other than 44100, or with `mono: true`: read as mono, the interleaved
  stream gives a garbage spectrum and no error. The capture command's stderr
  goes to spectrum's stderr when there is no terminal, so the pipe's log
  lines show up in the container log; only the last 4 KB are kept for the
  exit error.
- Test: the output against a `bytes.Buffer`: sample conversion, and silence
  blocks at the right pace with a fake clock.

In Music Assistant the player "Visualizer" is added once to the sync group of
the Sonos players.

## Add-on direction

Home Assistant runs as HA OS on the Pi at home, so the visualizer ships as a
local add-on (a folder under `/addons` with `config.yaml` and a `Dockerfile`,
built on the Pi). Not built here, but the pieces above are shaped for it:

- One image holds `spectrum` and `sendspin-pipe`; `spectrum` starts the pipe as
  its capture command, so the add-on runs a single process and needs no
  supervisor script beyond mapping `/data/options.json` to `SPECTRUM_*`
  variables.
- No devices, no audio, no privileges: the panel is a TCP address and the
  audio a websocket. `host_network: true` for both and for the API on 8099.
- `/data` is the add-on's persistent folder, which is where the controller
  spec already puts `state.json`.
- The image is built as section 3 says (a build stage with the opus headers
  for `sendspin-pipe`), so the same image also runs under podman at the office.

## Outcome (2026-09-19)

Built and running at home: Music Assistant (Spotify) plays to the Sonos group,
"Visualizer" is a member of that group, the Tidbyt shows the bars over WiFi.
30 fps, no CRC errors, no sequence gaps, no dropped audio chunks; the sync
looks right by eye with `--delay-ms 0`. Not yet tried by hand: the group with
the Play:5 added, and pulling the Tidbyt's power while streaming (the
reconnect paths are covered by `TestTCPReconnect`).

## Risks

- The visuals run late by the pipeline after the play time: one 23 ms block,
  up to one 33 ms frame tick, WiFi, the panel; about 60 to 80 ms. The static
  delay only shifts later. Measure first; if it shows, the fix is a lead in
  the scheduler (`Receiver` plus our own release time), not in this spec.
- Issue #6403 with the full group. If it bites, the Play:5 joins through the
  Sonos app instead of MA; untested.
- Sendspin spec v2: pin `sendspin-go` and the MA version, update both together.
- WiFi jitter: a late frame keeps the previous picture. Nothing to build.

## Not built

- Tronbyt idle bubble, home deployment, HA entities for brightness or on/off.
- UDP, frame compression, delta frames: 123 kB/s at 30 fps needs none of it.
- A Sendspin `visualizer` role client: the role is not implemented in
  `sendspin-go` yet. When it is, it replaces `sendspin-pipe` and the FFT.
- Serial under ESPHome, chained panels, gamma and rotation.

## Verification

- `go vet ./... && go test ./...` in `visualizer/` and in `cmd/sendspin-pipe/`.
- `pio run` for all envs and `task firmware:test` (the shared `proto.h`).
- `esphome compile esphome/tidbyt.yaml`.
- Hardware, transport: boot bars in the right colours, `spectrum --serial
  tcp://<ip>:7090` with `parec`, `/api/v1/state` shows 30 fps with `CRCErr` and
  `SeqGaps` at 0 for ten minutes. Restart `spectrum`: the panel picks up
  within 2 s. Power-cycle the Tidbyt: the host reconnects by itself.
- Hardware, audio: the group Playbase plus Visualizer plays, bars move with
  the music, pause shows the idle bubble after `silence_after`. Then the group
  with the Play:5. Judge the sync by eye on a track with a bare kick drum and
  note the offset.
