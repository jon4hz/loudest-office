# Loudest Office

This repo holds all the code required for the audio setup in our office. Our office already had the nickname "loud office" because we aren't exactly quiet and like to listen to music.

We've now decided to live up to our name and go the extra mile to become the *loudest* office. And because we are an office full of nerds and engineers, we don't want to do anything half-baked.

The idea is to have a Raspberry Pi running Music Assistant, so that everyone in the office can control the music.

We will then use the HiFiBerry DAC output to feed the signal to the CB10 speaker. We will also listen on the ADC input so that we can do fancy visualizations on the matrix panel.


## Hardware

* 1x [QSC CB10](https://www.qscaudio.com/products-solutions/loudspeakers/portable/powered/portable-pa/cb-battery-powered-loundspeaker/cb10/) speaker
* 2x cheap microphones to torture our coworkers with extra bad karaoke sessions
* 1x [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/)
* 1x [HiFiBerry Studio DAC/ADC XLR](https://www.hifiberry.com/shop/boards/hifiberry-studio-dacadc-xlr/)
* 1x [Hub75 Matrix Panel](https://www.bastelgarage.ch/rgb-p3-matrix-panel-64x32-hub75)
* 1x [ESP32](https://www.espressif.com/en/products/socs/esp32) to control the matrix panel


## Software

* Music Assistant

### Visualizer

`visualizer/` holds the Go spectrum analyzer; with `--serial` it also feeds the matrix panel.
Try it locally: `cd visualizer && go run ./cmd/spectrum` (needs `parec`; keys: `q`, `c` palette, `m` mode, `t` trails, `+`/`-` bands).

### Panel

`firmware/` is the PlatformIO project for the ESP32 that drives the HUB75 panel
([ESP32-HUB75-MatrixPanel-DMA](https://github.com/mrcodetastic/ESP32-HUB75-MatrixPanel-DMA)).
The visualizer streams frames to it over USB serial (protocol: appendix A of
`esp_visualizer_plan.md`).

Wiring (classic ESP32, the library's defaults; the 64x32 panel has no `E` line):

| HUB75 | GPIO | HUB75 | GPIO |
|-------|------|-------|------|
| R1    | 25   | A     | 23   |
| G1    | 26   | B     | 19   |
| B1    | 27   | C     | 5    |
| R2    | 14   | D     | 17   |
| G2    | 12   | LAT   | 4    |
| B2    | 13   | OE    | 15   |
| GND   | GND  | CLK   | 16   |

Power the panel from its own 5 V supply (up to 2.5 A) and tie the grounds.

```
uv tool install platformio
task firmware:flash                  # ENV=esp32s3 for an S3 with native USB
cd visualizer && go run ./cmd/spectrum --serial /dev/ttyUSB0 --brightness 64
```

The panel shows red/green/blue bars after boot and a small red cross when no
frames arrive for 2 s. Serial port access needs the `uucp` (Arch) or `dialout`
(Debian) group. The link runs at 921600 baud, the most a classic CP2102 bridge
does, which gives about 22 fps. With a CP2102N or CH340 set
`-DSERIAL_BAUD=2000000` in `firmware/platformio.ini` and pass `--baud 2000000`
for the full ~43 fps. On exit the visualizer prints the ESP's
counters; `CRCErr` and `SeqGaps` should stay 0. Without a terminal (systemd)
the visualizer runs headless.

#### On the Pi

The `visualizer` role runs it as a rootless podman container under its own
`visualizer` user: `task run -- --tags visualizer` cross-compiles the binary
(`task visualizer:build`), copies it with a Dockerfile to the Pi and lets
podman-compose build `localhost/visualizer` there; nothing is published.
It captures the HiFiBerry ADC with `arecord` and streams to `/dev/hub75`, a udev
symlink for the ESP32's CP2102. The user unit is bound to that device, so
unplugging the ESP32 stops the container and plugging it back in starts a fresh
one. Plug the ESP32 in before deploying. Logs:
`sudo -u visualizer XDG_RUNTIME_DIR=/run/user/$(id -u visualizer) podman logs -f visualizer`.
Flags live in `ansible/roles/visualizer/defaults/main.yml`.

Flashing the ESP32 while it hangs on the Pi: `task firmware:bin`, copy
`firmware/fw-esp32dev.bin` over, stop the visualizer (it holds the port), then
`esptool --chip esp32 --port /dev/hub75 --baud 115200 --no-stub write-flash 0x0 fw-esp32dev.bin`
(`python3 -m venv` + `pip install esptool`). The stub flasher and higher baud
rates fail on this CP2102.

## Configuring the Pi

### Requirements
* podman
* uv
* working avahi (mDNS) setup



https://www.hifiberry.com/docs/data-sheets/datasheet-studio-dac-adc/
