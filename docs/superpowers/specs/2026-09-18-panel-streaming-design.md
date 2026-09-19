# Panel streaming: ESP32 firmware + serial transport

Covers phases 1 and 2 of `esp_visualizer_plan.md`, plus wiring the existing
`spectrum` visualizer to the transport. The scene stack, alert API, Tronbyt and
deployment (phases 3+) are out of scope and get their own spec.

## Hardware

- Panel: Waveshare RGB-Matrix-P3-64x32 (HUB75, 5 V / 2.5 A max), one panel, no chain.
- Controller: generic ESP32. Default target is a classic ESP32 with a UART
  bridge (`esp32dev`); an `esp32s3` env exists for native USB CDC. The chip is
  confirmed with esptool before the first flash.
- Wiring: the default GPIO map of ESP32-HUB75-MatrixPanel-DMA, documented in
  the README. No `E` line (32 rows).

## Wire protocol

Appendix A of `esp_visualizer_plan.md` is normative: `A5 5A` magic, `type`,
`seq`, `u16 len`, payload, CRC-16/CCITT-FALSE over bytes 2..end of payload,
little-endian, six message types (HELLO, INFO, FRAME, CONFIG, STATUS, BLANK).
Pi → ESP is fire-and-forget; the ESP sends only INFO and a 1 Hz STATUS.

FRAME payload is `u8 pixfmt (0 = RGB565)`, `u8 flags (0)`, then
`width*height*2` bytes, row-major, top-left origin.

## Firmware (`firmware/`)

PlatformIO, Arduino framework, the HUB75 DMA library as the only `lib_deps`.

- Envs `esp32dev` (default) and `esp32s3` (`-DARDUINO_USB_MODE=1
  -DARDUINO_USB_CDC_ON_BOOT=1`). Baud is a build flag, default 921 600: the
  board at hand has a classic CP2102 (10c4:ea60, bcdDevice 1.00), which cannot
  go faster.
- `double_buff = true`. RGB colour bars are drawn at boot, before any serial
  traffic; they double as the pin-map / colour-order check.
- Byte-at-a-time parser `MAGIC1 → MAGIC2 → HEADER → PAYLOAD → CRC`; a bad byte
  or a `len` above the frame size resets to `MAGIC1`. Unknown types are
  skipped by `len`.
- Good FRAME → back buffer → `flipDMABuffer()`. Bad CRC → discard, count, keep
  the last good frame. `seq` gaps are counted only.
- HELLO → INFO (width, height, pixfmt mask, max fps, fw version).
  CONFIG → brightness (gamma and rotation are parsed and ignored for now).
  BLANK → clear.
- STATUS once a second: frames_ok, crc_err, seq_gaps, fps, temp (0xFF).
- 2 s without a FRAME → small "no signal" glyph.
- `setRxBufferSize(16384)` on the UART env.
- The parser is a plain C++ file without Arduino includes so it can be reasoned
  about (and reused) separately from `main.cpp`.
- `task firmware:flash` runs `pio run -t upload`; `task firmware:bin` produces
  the merged `fw.bin` (offset 0x1000 classic, 0x0 S3).

## Host (`visualizer/`)

- `proto`: `Encode(type, seq, payload)`, a streaming `Decoder`, CRC-16, typed
  helpers for INFO/STATUS/FRAME/CONFIG, RGB565 conversion from
  `[][]color.RGBA`. Unit tests against known byte sequences (CRC check value
  `0x29B1` for `"123456789"`), and a decoder test with garbage between packets.
- `serial`: opens the port raw at an arbitrary baud with termios2 through
  `golang.org/x/sys/unix` (already in the module graph; Linux only, like the
  capture commands). On connect it waits ~1 s for the DTR reboot, sends HELLO
  until INFO arrives, then sends CONFIG. One writer goroutine with a depth-1
  channel: a pending frame is replaced, never queued. A reader goroutine keeps
  the latest STATUS. Any I/O error closes the port and re-enters the connect
  loop. `Close` sends BLANK.
- `cmd/spectrum`: `--serial <port>`, `--baud` (921600), `--brightness` (0–255).
  With `--serial` the model is fixed to the INFO size via `spectrum.Size` and
  each `SamplesMsg` update sends `Model.Frame()`. When stdout is not a terminal
  the program runs with `tea.WithoutRenderer()` and no input. The last STATUS
  is printed on exit.

Frame pacing stays audio-driven (~43 blocks/s); the depth-1 writer caps it at
what the link carries (≈22 fps at 921600 baud, full rate at 2 Mbaud or on S3).
A fixed `tea.Tick` pacing arrives with the scene stack in phase 3.

## Not built

- `cmd/paneltest`: the boot colour bars verify wiring, and the visualizer plus
  STATUS counters are the bandwidth check. Add it for audio-free soak tests.
- STATUS sidebar in the TUI (phase 3).
- Gamma/rotation handling in the firmware, chained panels.

## Verification

- `go test ./...` in `visualizer/`.
- `pio run` for both envs.
- On hardware: colour bars at boot, STATUS lines in `pio device monitor`, then
  `spectrum --serial` with music; crc_err and seq_gaps stay 0 at the chosen baud.
