# Spectrum analyzer bubble — design

Date: 2026-09-17

## Goal

A bubbletea v2 component that turns audio samples into a spectrum-analyzer
picture, plus a small CLI that runs it against the laptop's default sink
monitor. The picture is a pixel frame so the same component can later drive
the 64x32 HUB75 panel through the ESP32 without a second renderer.

Out of scope for now: sending anything to the ESP, auto gain, demo/fire
mode, automatic pattern cycling, mirrored or side-by-side channel layouts.

## Module layout (`visualizer/`, module `github.com/jon4hz/loudest-office/visualizer`)

- `audio/` — `Start(ctx, Config) (<-chan [][]float32, error)`. Runs
  `parec --raw --format=s16le --rate=R --channels=C -d DEVICE` as a
  subprocess, deinterleaves blocks of `Frames` samples into one `[]float32`
  per channel scaled to [-1, 1], and sends them on the channel. Closes the
  channel when the process exits; the exit error (with stderr) is returned
  from `Wait`. Default device `@DEFAULT_SINK@.monitor`, rate 44100, 2
  channels, 1024 frames per block. The command name is configurable so the
  Pi can use `arecord -t raw -f S16_LE`.
- `dsp/` — pure functions, no state:
  - `Hann(n) []float32`, `FFT(re, im []float32)` in-place radix-2 (n must be
    a power of two).
  - `Bands(n, fftSize, rate, lo, hi) []int` — bin edges for `n` log-spaced
    bands between `lo` Hz and `hi` Hz (defaults 40 and 16000), each band at
    least one bin wide and never overlapping.
  - `Levels(samples, window, edges, gain, floorDB) []float32` — window,
    FFT, peak magnitude per band, dB, then `(dB + gain - floorDB) /
    -floorDB` clamped to [0, 1]. `floorDB` defaults to -60.
- `spectrum/` — the bubble.
  - `Model` created by `New(opts ...Option)`. Options: `Bands(n)`,
    `Channels(n)`, `Palette(p)`, `FallSpeed(f)`, `PeakHold(frames)`,
    `PeakFall(f)`, `Gain(dB)`, `Size(w, h px)`.
  - Messages: `SamplesMsg [][]float32` (one slice per channel) and
    `tea.WindowSizeMsg`. On a window size the frame becomes
    `width` x `2*height` pixels unless a fixed `Size` was given.
  - State per channel: `bars []float32` (0..1, fast attack, falls by
    `FallSpeed` per block), `peaks []float32` and `peakTimer []int`
    (peak sits at the max, holds `PeakHold` blocks, then falls by
    `PeakFall`).
  - `Frame() [][]color.RGBA` — the pixel grid, rows top to bottom.
    Channels are stacked vertically and each gets an equal slice of rows.
    Bars are as wide as `frameWidth / bands` pixels with a one-pixel gap
    when the bar is at least three pixels wide.
  - `View() string` paints the frame with `▀` per cell (the CLI wraps it in
    a `tea.View` with the alt screen on): foreground is the
    upper pixel, background the lower pixel, 24-bit color escapes.
  - `Palette` is `func(band, nBands, y, height int) (bar, peak color.RGBA,
    drawBar, drawPeak bool)` where y is the pixel row from the bottom.
    Shipped palettes, ported from FFT_ESP32_Analyzer: `Rainbow` (hue per
    band, white peak), `TriBar` (green/yellow/red by height, peak same
    color as its row), `Red` (red bars, blue peak), `Blue` (blue bars, red
    peak), `Purple` (blue→purple gradient by height, white peak),
    `Outrun` (purple→yellow→blue gradient by height, no peak), `PeaksOnly`
    (blue peaks, no bars). `Palettes` is the ordered list with names for
    cycling and the CLI flag.
- `cmd/spectrum/main.go` — flags `-device`, `-bands` (32), `-palette`
  (rainbow), `-gain` (0 dB), `-rate` (44100), `-cmd` (parec). Starts
  capture, runs the bubble in the alt screen, forwards each block as a
  `SamplesMsg`. Keys: `q`/ctrl+c quit, `c` next palette, `+`/`-` bands
  (8..64 in steps of 8). If capture ends the program exits nonzero and
  prints the capture error.

## Data flow

parec → audio goroutine (1024 frames per block ≈ 43 blocks/s) → channel →
`waitForSamples` tea.Cmd → `SamplesMsg` → `Update` (levels per channel, bar
and peak dynamics, redraw frame) → `View`.

## Testing

- `dsp`: FFT of a known signal against a naive DFT; a 1 kHz sine at 44.1 kHz
  produces its maximum level in the band whose edges contain 1 kHz.
- `spectrum`: Update with a synthetic block gives nonzero bars and a peak
  that holds then falls over successive silent blocks; frame size follows
  the window size; every shipped palette returns a color for every row.
- `audio`: deinterleave of a known byte slice.
