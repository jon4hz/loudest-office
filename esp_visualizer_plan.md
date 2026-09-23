# LoudestOffice — Display Controller Build Plan

Go/Bubble Tea display controller on the Pi 5 driving a HUB75 panel via an ESP32 over USB.
Scenes by priority: **alert (API) > audio visualizer > Tronbyt idle**.

Phases are ordered so each one verifies the previous one. Audio comes late on purpose:
the ADC cable issue is still open and everything before it works without a signal.

---

## Phase 0 — Decisions before code

- [ ] Waveshare board model and ESP32 variant (classic UART-bridge vs S3 native USB)
- [ ] Panel size and chain count → logical canvas size, fps budget
- [ ] What Tronbyt can serve to a device: animated WebP only, or GIF too → decoder choice in phase 6
- [ ] Repo layout:

```
loudestoffice-display/
├── cmd/
│   ├── display-controller/   # the Bubble Tea service
│   ├── paneltest/            # test patterns, bandwidth check
│   └── psu-monitor/          # GPIO 16/26 watcher → alert API
├── internal/
│   ├── proto/                # frame protocol encode/decode, CRC-16
│   ├── serial/               # port handling, HELLO/INFO, latest-frame-wins writer
│   ├── scene/                # Scene interface + implementations
│   ├── audio/                # ALSA capture, RMS, FFT bands
│   ├── api/                  # unix-socket HTTP
│   └── tronbyt/              # device polling, decode
├── firmware/                 # PlatformIO project for the ESP32
└── ansible/
```

---

## Phase 1 — ESP32 firmware

**Done when:** panel shows a boot pattern on power-up and `pio device monitor` prints STATUS once a second.

- [ ] PlatformIO project, `mrfaptastic/ESP32 HUB75 LED MATRIX PANEL DMA Display` as the only lib_dep
- [ ] Pin map from the Waveshare wiki in `HUB75_I2S_CFG::i2s_pins`
- [ ] `double_buff = true`, boot pattern drawn before any serial traffic
- [ ] Byte-at-a-time protocol parser (see appendix), FRAME → back buffer → `flipDMABuffer()`
- [ ] CRC / seq-gap counters, 1 Hz STATUS out
- [ ] 2 s no-signal watchdog → small "no signal" glyph
- [ ] RX buffer ≥ 2 frames (`setRxBufferSize(16384)` on UART-bridged boards)
- [ ] S3 only: `-DARDUINO_USB_MODE=1 -DARDUINO_USB_CDC_ON_BOOT=1`
- [ ] Flash from laptop with `pio run -t upload`
- [ ] Produce merged `fw.bin` as a build artifact (`esptool.py merge_bin`, offset 0x1000 classic / 0x0 S3)

Linux flashing prerequisites: `usermod -aG dialout`, mask ModemManager, remove brltty on Ubuntu if CH340.

---

## Phase 2 — Go transport + test tool

**Done when:** a moving test pattern runs at target fps for 10 minutes with zero CRC errors.

- [ ] `internal/proto`: encode/decode all six message types, CRC-16/CCITT-FALSE, unit tests against known byte sequences
- [ ] `internal/serial`: open port, tolerate DTR-triggered reboot (wait ~1 s, then HELLO), parse INFO, depth-1 channel writer (latest frame wins), STATUS reader
- [ ] `cmd/paneltest`: colour bars, gradient, bouncing pixel, fps flag
- [ ] Record measured fps / CRC rate for the chosen board — this is the bandwidth ceiling for everything after

---

## Phase 3 — Bubble Tea core

**Done when:** the service runs under systemd and survives unplugging and replugging the ESP32.

- [ ] Model with scene stack, single `tea.Tick` at target fps (frame pacing never driven by audio arrival)
- [ ] `Scene` interface: `Render(fb *image.RGBA, now time.Time)`, `Priority()`, `Expired()`
- [ ] Framebuffer sized from INFO, not from config
- [ ] Terminal preview: framebuffer as `▀` half-block cells + sidebar (scene, dB, API queue, STATUS counters)
- [ ] `--headless` → `tea.WithoutRenderer()`, no input
- [ ] Idle placeholder scene (a clock is enough)
- [ ] Serial reconnect loop; BLANK on clean shutdown
- [ ] systemd unit: `Restart=always`, journal logging, `After=dev-hub75.device` once the udev rule exists

Keep FFT and serial writes in their own goroutines. `Update` is single-threaded and only receives results.

---

## Phase 4 — Alert API + PSU GPIO

**Done when:** `curl --unix-socket … /notify` shows text on the panel and a jumper on GPIO 26 triggers "battery low".

- [ ] Unix-socket HTTP server: `POST /notify {text, icon, level, ttl_s}`, `POST /scene`, `GET /status`
- [ ] TTL expiry on every alert — a crashed sender must not wedge the display
- [ ] Priority ordering within alerts (critical > warning > info)
- [ ] 3×5 or 4×6 bitmap font; horizontal scroll for text wider than the panel
- [ ] `cmd/psu-monitor`: libgpiod on GPIO 16 (AC OK) and GPIO 26 (BAT LOW), debounce, calls the API; own systemd unit
- [ ] Socket permissions / group so other services can post without root

---

## Phase 5 — Audio pipeline

**Done when:** playback from Music Assistant drives the bars and the display drops to idle 5 s after stop.

Blocked on: ADC signal fix (cable/source swap).

- [ ] Capture goroutine on `hw:sndrpihifiberry` — mono, left channel only (`arecord` pipe first, cgo ALSA later if needed)
- [ ] RMS + silence detection with hysteresis: below −50 dBFS for >5 s → idle; any signal → viz within ~100 ms
- [ ] FFT (gonum `fourier`), 16–32 log-spaced bands, ~30 updates/s, shipped as one `tea.Msg`
- [ ] First viz scene: spectrum bars with peak-hold and gamma on amplitude
- [ ] Thresholds in config, not code

---

## Phase 6 — Tronbyt idle

**Done when:** Tronbyt apps rotate during silence.

- [ ] Poll the device endpoint on its schedule; respect per-app display time
- [ ] Decoder per phase 0 answer: `x/image/webp` for stills, libwebp via cgo for animated
- [ ] Cache last good image — a Tronbyt outage shows stale content, not black
- [ ] Brightness follows CONFIG, not the image

---

## Phase 7 — Deployment

**Done when:** a fresh Pi OS Lite install reaches phase 6 state from one playbook run.

- [ ] Cross-compile arm64 binaries in CI, ship as release artifacts
- [ ] Ansible role: binaries, config file, systemd units, socket group
- [ ] udev rule → stable `/dev/hub75` symlink for the ESP32
- [ ] `esptool` flash of `fw.bin` from the Pi, guarded by a version check so it only reflashes on change
- [ ] `usb_max_current_enable=1` in `config.txt` (already known, keep it in the role)

---

## Phase 8 — Later

- More viz scenes (VU, waveform, beat flash)
- Night-time brightness schedule via CONFIG
- Config hot reload (SIGHUP)
- `/metrics` endpoint

---

## Rough effort

| Phase | Estimate | Blocked on |
|---|---|---|
| 1–2 | one evening each | board model |
| 3–4 | a weekend | — |
| 5 | one evening | ADC fix |
| 6 | one evening | Tronbyt format |
| 7 | one evening | — |

---

## Appendix A — Pi ↔ ESP32 frame protocol

USB CDC, 8N1, little-endian. Pi → ESP is fire-and-forget; ESP replies only with INFO and a 1 Hz STATUS.

### Packet layout (all messages, both directions)

```
0   u8   0xA5   magic
1   u8   0x5A   magic
2   u8   type
3   u8   seq       wraps at 255
4   u16  len       payload bytes
6   ...  payload
    u16  crc16     CRC-16/CCITT-FALSE over bytes 2..end of payload
```

### Message types

```
0x01 HELLO   Pi→ESP   empty; ESP must answer INFO
0x02 INFO    ESP→Pi   u16 width, u16 height, u8 pixfmt_mask, u8 max_fps, u16 fw_version
0x03 FRAME   Pi→ESP   u8 pixfmt (0=RGB565), u8 flags, then width*height*2 bytes row-major, top-left origin
0x04 CONFIG  Pi→ESP   u8 brightness (0–255), u8 gamma_x10 (22 = 2.2), u8 rotation (0–3)
0x05 STATUS  ESP→Pi   u16 frames_ok, u16 crc_err, u16 seq_gaps, u8 fps, u8 temp_c (0xFF if none)
0x06 BLANK   Pi→ESP   empty; clear panel
```

### Receiver rules (ESP32)

- State machine `MAGIC1 → MAGIC2 → HEADER → PAYLOAD → CRC`; any bad byte or oversized `len` resets to `MAGIC1`.
- Good FRAME → back buffer → flip. Bad CRC → discard, count, keep last good frame.
- `seq` gaps are counted only; nothing is retransmitted. Newer always supersedes older.
- Unknown types are skipped by `len`.
- `len` bound and RX buffer size follow the configured logical canvas (chain length included).

### Sender rules (Pi)

- HELLO on connect and after any serial error; no FRAMEs until INFO.
- Framebuffer sized from INFO.
- One writer goroutine, depth-1 channel: replace pending frame, never queue.
- STATUS → `tea.Msg` for the sidebar.

### Bandwidth

- 64×32 RGB565 = 4096 B/frame (+10 header). Each chained 64×32 panel adds 4 KB.
- S3 native USB: baud irrelevant, several panels at 30 fps is fine.
- Classic ESP32 via CP2102/CH340 at 921600 baud ≈ 92 KB/s → one 64×32 panel ≈ 22 fps, two ≈ 11 fps. 2 Mbaud works on CP2102, flaky on cheap CH340.

### Chained panels

Handled entirely on the ESP32 (`chain_length`, or `VirtualMatrixPanel` for 2D layouts). INFO reports the logical canvas; the Pi never knows the physical arrangement.

Power: 3–4 A per 64×32 panel at full white. Dedicated 5 V supply for panels, ESP32 on Pi USB, grounds tied. Don't chain power through more than two or three panels' pass-through connectors.