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
* Home Assistant

### Home Assistant

The `home_assistant` role runs Home Assistant Container rootless, like the
others, under its own `homeassistant` user, on `https://loudestoffice.local`
(and `http://<pi>:8123`). Without
the Supervisor there are no apps: what would be an app is one more rootless
role, e.g. `tronbyt`, the Tronbyt server on `https://loudestoffice.local:8443` (see "Tronbyt
as the idle loop"; at the office put the device ID, vaulted, into
`visualizer_tronbyt_device`). Integrations, HACS and custom components
work as usual. Music Assistant stays in its own role; add it in Home
Assistant with the Music Assistant integration at `http://127.0.0.1:8095`.

`configuration.yaml` is Ansible-owned (everything set in the UI lives in
`.storage`). Music Assistant's users log in to Home Assistant with their MA
credentials: a `command_line` auth provider
(`files/auth_music_assistant.py`) checks them with a login to Music
Assistant, so the passwords stay in MA (its PBKDF2 hashes cannot be copied:
Home Assistant only checks bcrypt). MA admins become admins, guests
read-only, everyone else users; the Home Assistant provider stays for the
owner.

The sidebar has three panels from
[hass_ingress](https://github.com/lovelylain/hass_ingress), which the role
installs once into `custom_components/ingress`:

* Visualizer and Music Assistant are proxied through Home Assistant after its
  login, like Supervisor ingress. hass_ingress sends no `X-Remote-User-*`
  headers, so the visualizer logs no user names and Music Assistant, whose
  single sign-on only works behind the Supervisor, asks for its own login.
* Tronbyt's links are absolute, so it cannot live under a path prefix: the
  panel frames `https://loudestoffice.local:8443` (the `caddy` role), same
  host so the cookies count as same-site. The panel has `work_mode: auth`:
  Caddy asks it (`forward_auth`) whether the browser is logged in to Home
  Assistant and sends everyone else to the Home Assistant login. Tronbyt
  gives a device one owner, so the office shares one Tronbyt account,
  `office`, which `SINGLE_USER_AUTO_LOGIN` logs in on its own; a second
  Tronbyt user turns that off. Tronbyt listens on 127.0.0.1 only, so
  nothing gets past the Home Assistant login. Opened as `http://<pi>:8123`
  instead of `https://loudestoffice.local`, the frame cannot log in.

Settings Home Assistant keeps in `.storage` rather than YAML go through its
websocket API, by the role's `home_assistant_*` modules, with a long-lived
token (`home_assistant_token`, vaulted, in
`inventory/group_vars/raspi/home_assistant.yml`):

* `home_assistant_http`: the HTTP settings. Since 2026.x the `http:` YAML is
  only migrated on the very first start and ignored afterwards; the module
  stages the change, Home Assistant restarts with it on trial, and the
  module confirms it (unconfirmed, it reverts after 5 minutes). It trusts
  the `caddy` proxy, so until the token is set `https://loudestoffice.local`
  answers 400: do the onboarding at `http://<pi>:8123`.
* `home_assistant_areas`: `home_assistant_areas` is the complete list of
  areas, so onboarding's defaults are deleted.

Check: `uv run ansible/roles/home_assistant/tests/test_modules.py`.

### Reverse proxy

The `caddy` role serves `https://loudestoffice.local` (Home Assistant),
`https://loudestoffice-ma.local` (Music Assistant) and
`https://loudestoffice.local:8443` (Tronbyt) with certificates from
Caddy's internal CA; the sites are `caddy_sites` in `site.yml`. The Pi's
avahi announces the second name as an alias (`avahi-alias@<name>.service`).
Keep such names single-label: `ma.loudestoffice.local` would not resolve on
Linux, whose nss-mdns rejects `.local` names with more than two labels. The
proxy used to be part of the `music_assistant` role; the first deploy moves its
data (and with it the CA) from `~musicassistant/caddy`, which can be deleted
afterwards. Deploy `music_assistant` and `caddy` together that first time,
so the old proxy frees ports 80 and 443.

### Visualizer

`visualizer/` holds the Go audio visualizer: fifteen music bubbles (bars, fire, life,
stars, fireworks, parrot, invaders, polygon, plasma, ripples, waterfall, pitch,
spray, aurora, metaballs), an idle clock and a scrolling event alert, driven by a
controller that decides what to show and loops within it. `--serial` seeds a
panel named `panel` on the matrix panel; without a terminal (e.g. under
systemd) it runs headless.

Try it locally: `cd visualizer && go run ./cmd/spectrum` (needs `parec`). Keys:
`q` quit, `m` pin the next bubble, `a` clear the pin or toggle the auto loop,
`+`/`-` bands; the shown bubble takes the rest: `c`/`C` palette, bars `l` layout,
`p` peak style, `t` trails, spray `r` upright/radial. `--listen :8099` turns on the HTTP API below;
`--state state.json` persists whatever it changes.

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
bun                                  # needed for the web UI build
task firmware:flash                  # ENV=esp32s3 for an S3 with native USB, ENV=office for the 128x64 chain
cd visualizer && go run ./cmd/spectrum --serial /dev/ttyUSB0 --brightness 64
```

The panel shows red/green/blue bars after boot and a small red cross when no
frames arrive for 2 s. Serial port access needs the `uucp` (Arch) or `dialout`
(Debian) group. The link runs at 921600 baud, the most a classic CP2102 bridge
does, which gives about 22 fps. With a CP2102N or CH340 set
`-DSERIAL_BAUD=2000000` in `firmware/platformio.ini` and pass `--baud 2000000`
for the full ~43 fps. On exit the visualizer prints the ESP's
counters; `CRCErr` and `SeqGaps` should stay 0.

#### On the Pi

The `visualizer` role runs it as a rootless podman container under its own
`visualizer` user: `task run -- --tags visualizer` cross-compiles the binary
(`task visualizer:build`), copies it with a Dockerfile to the Pi and lets
podman-compose build `localhost/visualizer` there; the container runs with
`network_mode: host` on port 8099, but with `trusted_proxy: 127.0.0.1/32`
it answers only Home Assistant's ingress panel (see "Home Assistant" above) and 403 to the LAN:
the web UI is "Visualizer" in the Home Assistant sidebar, and the `curl`
examples below run on the Pi itself (`127.0.0.1:8099`). Drop `trusted_proxy`
from `visualizer_config` and reopen 8099 in `site.yml` to serve the LAN
without a login again. It captures the HiFiBerry ADC with `arecord` and streams
to `/dev/hub75`, a udev symlink for the ESP32's CP2102. The user unit is bound
to that device, so unplugging the ESP32 stops the container and plugging it
back in starts a fresh one. Plug the ESP32 in before deploying. Logs:
`sudo -u visualizer XDG_RUNTIME_DIR=/run/user/$(id -u visualizer) podman logs -f visualizer`.

The capture settings (device, rate, gain, ...) stay in
`ansible/roles/visualizer/defaults/main.yml`, Ansible-owned. Its `serial`,
`brightness` and `fps`, like the `--baud` flag, are only seeds: they declare
the panel named `panel` while the state file has no `panels` key (a fresh
install) and are ignored afterwards; from then on that panel is edited in
the web UI or with `PUT /api/v1/panels/panel`. The look and feel (palettes,
weights, loops, thresholds, the pinned bubble) and the panels themselves
(including brightness and idle brightness) instead live in the state file
the API writes to, `~visualizer/data/state.json` on the Pi, and are changed
at runtime through the API below (`PATCH /api/v1/controller`,
`PATCH /api/v1/panels/{name}`) rather than redeployed.

##### Panels

Panels are declared at runtime, not in the config, and the visualizer drives
as many as are declared at once: `serial` (an ESP32 behind a tty or
`tcp://host:port`, brightness and idle brightness set per panel) or `virtual`
(a size and frame rate of its own, mirrored over the websocket below, no
hardware attached). `--serial` seeds one serial panel named `panel` so an
existing single-panel setup keeps working unchanged. Panels that share a size
and frame rate render together, once, in one group; the terminal, when there
is one, is always its own panel named `terminal` and cannot be declared,
patched or deleted. A serial panel's brightness is a range on the Now tab of
the web UI below, per panel; its idle brightness, and declaring, patching or
deleting a panel, live on the Setup tab's Panels card.

```
curl -X PUT http://<pi>:8099/api/v1/panels/desk -d '{"kind":"virtual","w":64,"h":32,"fps":60}'
curl -X PATCH http://<pi>:8099/api/v1/panels/desk -d '{"fps":30}'
curl -X DELETE http://<pi>:8099/api/v1/panels/desk
```

Each panel streams its own frames at `GET /api/v1/panels/{name}/frames` (see
below); mirror one in a terminal with `spectrum watch <pi>:8099/desk`.

##### API

No auth, LAN only. Against the Pi:

```
curl http://<pi>:8099/api/v1/state

curl -X PATCH http://<pi>:8099/api/v1/bubbles/bars \
  -d '{"settings":{"palettes":["outrun","synthwave"]}}'

curl -X PATCH http://<pi>:8099/api/v1/controller \
  -d '{"music_db":-45,"silence_db":-55,"silence_after":8}'
curl -X PATCH http://<pi>:8099/api/v1/controller -d '{"show":"parrot"}'  # pin
curl -X PATCH http://<pi>:8099/api/v1/controller -d '{"show":""}'        # unpin

curl -X PATCH http://<pi>:8099/api/v1/panels/panel -d '{"brightness":100}'

curl -X POST http://<pi>:8099/api/v1/events \
  -d '{"text":"coffee is ready","level":"info","ttl":30}'
```

Show a bubble for a few seconds, then carry on (not under a pin):

```
curl -X POST http://loudestoffice.local:8099/api/v1/bubbles/nowplaying/show -d '{"seconds":8}'
```

Each panel's frames stream over a websocket at `GET
/api/v1/panels/{name}/frames`: one binary message per frame, `w u16 | h u16 |
RGB888 pixels` row by row, little-endian, at the panel's frame rate; a slow
client misses frames rather than holding up the panel. `GET /api/v1/frames`
mirrors the first declared panel instead, for a client that does not know a
name. To mirror one in a terminal: `spectrum watch <pi>:8099/desk`, or
`spectrum watch <pi>:8099` for the first panel.

To calibrate `music_db`/`silence_db`: watch `db` in `GET /api/v1/state` with
music off, note the noise floor, then with music playing at a normal level,
note that. Put `silence_db` a few dB above the floor and `music_db` a few dB
below the playing level, with `silence_db <= music_db`; `silence_after` is how
long the level has to stay under `silence_db` before the panel falls back to
idle.

##### Web UI

The same port serves a phone-friendly UI at `http://<pi>:8099/`: a panel
picker (when there's more than one) above a live preview of the picked panel,
a brightness range per serial panel, and next (Now), weights, pins and
settings per bubble (Bubbles), a gallery and editor for gradient palettes
(Palettes), named snapshots of the whole configuration (Presets), and loops,
music detection with a live level meter, events and a Panels card listing
every panel with its status, fps, brightness and idle brightness for serial
ones, a delete button and a form to add one (Setup). Presets and custom
palettes live in the state file next to everything else.

The UI is a Svelte app in `visualizer/web`, built by `task web:build`
(bun) and embedded into the binary; `visualizer:build` runs it. `task
web:dev` starts Vite with a proxy to a local `spectrum --listen :8099`.

Flashing the ESP32 while it hangs on the Pi: `task firmware:bin`, copy
`firmware/fw-esp32dev.bin` over, stop the visualizer (it holds the port), then
`esptool --chip esp32 --port /dev/hub75 --baud 115200 --no-stub write-flash 0x0 fw-esp32dev.bin`
(`python3 -m venv` + `pip install esptool`). The stub flasher and higher baud
rates fail on this CP2102.

#### At home: a Tidbyt over WiFi, audio from Music Assistant

No ADC and no USB cable: a Tidbyt v1 (ESP32, 64x32 HUB75) runs ESPHome with the
external component `esphome/components/panel_stream`, which speaks the same
protocol as `firmware/` on TCP port 7090. The audio comes from `sendspin-pipe`
(`visualizer/cmd/sendspin-pipe`, its own Go module because it needs cgo and
libopus): a [Sendspin](https://www.sendspin-audio.com/) player that joins a
Music Assistant sync group and, instead of playing, writes the audio to stdout
at its play time, and silence while nothing plays. The visualizer reads it like
`arecord`, so the picture runs in step with the speakers of the group.

Flash the Tidbyt, the first time over USB, afterwards over the air:

```
cp esphome/secrets.yaml.example esphome/secrets.yaml   # WiFi, API key, OTA password
task esphome:run -- --device /dev/ttyUSB0              # later: --device <ip>
```

This replaces the Tidbyt's firmware; the way back to a normal Tidbyt is the
[Tronbyt](https://github.com/tronbyt/firmware-esp32) Gen1 firmware. After boot
the panel shows red, green and blue bars, left to right. Other colours mean the
colour-swapped hardware variant: use the commented pin block in
`esphome/tidbyt.yaml`. The panel is fed from the USB port, so the firmware caps
the brightness at 100 of 255. `shift_driver: FM6126A` and `clock_phase: true`
have to stay, they are how Tronbyt drives this panel: without them it shows a
single garbled row at ESPHome's default of 20 MHz, and at 8 MHz a picture that
glitches and can get stuck on two rows until a power cycle. The firmware also
turns off ESPHome's roam scans (each one is four seconds at 8 fps) and reports
its reset reason, uptime, free heap and WiFi signal to Home Assistant. Give the Tidbyt a
fixed address (a DHCP reservation, or `manual_ip` in `tidbyt.yaml`): the
visualizer reaches it by IP, `.local` names do not resolve in its container.

Then, in the visualizer's config:

```yaml
cmd: sendspin-pipe
args: [--server, "<music-assistant>:8927", --name, Visualizer]
serial: tcp://<tidbyt>:7090
rate: 44100          # the pipe is always 44100 Hz stereo; mono must stay off,
mono: false          # for one spectrum use the bars layout "single" instead
brightness: 60
listen: ":8099"
state: /data/state.json
```

In Music Assistant add the player "Visualizer" to the sync group of the
speakers. Sonos speakers join such a group through Music Assistant's Sendspin
bridge over AirPlay. `--delay-ms` on `sendspin-pipe` shifts the picture later
if it runs ahead of the sound. The pipe's log lines (connection, stream start,
dropped chunks) show up in the visualizer's log when it runs without a
terminal. sendspin-go v1.8.2 asks the server for album art and never reads
it, which wedged the connection on every art-heavy track change (no audio, no
clock sync, silently for hours); the pipe drains that channel itself. Should
the connection wedge anyway, the pipe exits after three failed clock syncs in
a row (30 s) and prints its goroutines, and the visualizer ends with it, so
whatever runs it has to restart it (the Home Assistant app: turn on its
watchdog). A panel that does not answer at start (at home a smart plug cuts
its power) is retried every 5 s instead of ending the visualizer.

The panel takes one client: a second visualizer takes the connection over.
Stop the visualizer before an over-the-air update. `GET /api/v1/state` should
show about 30 fps with `crc_err` and `seq_gaps` at 0; over TCP anything else
is a bug. The wired fallback is `pio run -e tidbyt -t upload` in `firmware/`
and `--serial /dev/ttyUSB0 --baud 2000000`.

##### As a Home Assistant app

Instead of running from a laptop, the same image (the office build context
plus `addon/config.yaml`) can run as a local Home Assistant app on the Pi that
runs Home Assistant OS and Music Assistant; spectrum reads the app's options
as its config through `SPECTRUM_CONFIG`. This needs the SSH add-on installed
on Home Assistant, with your key and a user that may `sudo`. Deploy with:

```
task addon:deploy HA=<user>@<pi>
```

The app has ingress: the web UI sits in the Home Assistant sidebar as
"Visualizer", behind the Home Assistant login, and `trusted_proxy:
172.30.32.2/32` (the Supervisor) makes that the only way in; port 8099 answers
403 to everyone else. The app log prints the peer it sees on the first ingress
request; if it is not 172.30.32.2, widen `trusted_proxy` (e.g. `172.30.32.0/23`)
and check the watchdog keeps passing. Leave `trusted_proxy` empty to serve the
LAN without a login, as at the office.

`task addon:deploy` installs the app the first time and rebuilds it on every
later run. After the first install, set its options (the panel's address in
`serial`, Music Assistant's in `args`, ...) and start it in the UI. An
interrupted deploy is fixed by running the task again. The log is
`ha apps logs local_loudest_visualizer`; run `ha` as that user directly (a
login shell, not `sudo`, is where the SSH add-on puts its Supervisor token).
The Tidbyt still takes one client, so stop any other visualizer first.

##### Tronbyt as the idle loop

While nothing plays, the Tidbyt can show the apps of a
[Tronbyt server](https://github.com/tronbyt/server): the visualizer plays the
part of the Tronbyt device. On Home Assistant the server is the app from
`https://github.com/kaffolder7/ha-app-tronbyt-server` (add the repository in
the app store, install, start; the web UI is `http://<pi>:8000`, the first
account registered is the admin). Create a device of type "Tidbyt Gen1", add
apps to it, and put its ID into the visualizer's config:

```yaml
tronbyt: http://127.0.0.1:8000/<device id>/next
```

The bubble `tronbyt` then replaces the clock as the idle bubble (the clock's
default weight becomes 0; if the state file already holds a weight for it,
`curl -X PATCH http://<pi>:8099/api/v1/bubbles/clock -d '{"weight":0}'`). It
pulls an image, plays it for the time the server names and pulls the next one,
only while it is shown. A server that is down leaves the last image up;
`tronbyt?` on the panel means it never answered. The device ID is the only
secret of that URL, so keep it out of the repo. The brightness stays
`idle_brightness`; the server's is ignored.

At the office the server is the `tronbyt` role and the device is
`visualizer_tronbyt_device` in `inventory/group_vars/raspi/visualizer.yml`.
A device created from the office LAN requires its API key by default; that
key, vaulted, is `visualizer_tronbyt_key` and goes into the URL as `?key=`.
The device type must match the panel's size, since images are drawn
unscaled: the office panel is 128x64, so "Tronbyt S3 Wide" (Tidbyt Gen1 is
64x32).

##### Now playing

With `ma_url`, `ma_token` and `ma_player` set, a new track shows its cover,
title and artist for 10 s (`PATCH /api/v1/bubbles/nowplaying
{"settings":{"seconds":10}}`, 0 = never; weight 0 does the same). The token is a
long-lived one from the Music Assistant profile settings (a guest user is
enough; it expires after a year, the panel then says `ma: 401` when
`nowplaying` is shown). The player ID is in the player's settings in Music
Assistant; a member of a sync group answers for its group. Accented letters
are drawn as their base letter.

## Configuring the Pi

### Requirements
* podman
* uv
* working avahi (mDNS) setup

### Secrets

Secrets (e.g. the Music Assistant token) are encrypted with ansible-vault.
The password is `ansible/vault.txt`, gitignored; without it `task run` fails.
Encrypt a value with, in `ansible/`:
`uv run ansible-vault encrypt_string --stdin-name <var>` (paste, Ctrl-D
twice), then paste the output into the matching `group_vars` file.

https://www.hifiberry.com/docs/data-sheets/datasheet-studio-dac-adc/
