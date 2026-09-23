# Home Assistant app for the visualizer

The home setup (`2026-09-19-home-tidbyt-design.md`) runs from a laptop today.
This puts it on the Home Assistant Pi as a local app (Home Assistant's new name
for add-ons), deployed over SSH with one task.

## What is known

- Home Assistant OS and Music Assistant run on the same host (192.168.16.253;
  8123, 8927 and SSH on 22 are open). SSH wants a key; the user sets that up.
- `spectrum --config /data/options.json` works as it is: the options file is
  JSON, which the YAML parser reads, and the unknown-key check still applies.
  So the app's options are spectrum's config keys, one to one, and no wrapper
  script, `bashio` or `jq` is needed.
- The Supervisor builds a local app on the device, from the `Dockerfile` in
  the app's folder under `/addons`, with that folder as the build context.
  `build.yaml` is no longer used; base images are plain `FROM` lines.
- `task visualizer:build` already produces a complete arm64 build context in
  `ansible/roles/visualizer/files/build/`: the `Dockerfile`, the
  cross-compiled `spectrum`, and the `sendspin-pipe` sources that are compiled
  on the target.

## Decisions

- One image for both deployments. The app's folder is the office build context
  plus `config.yaml`; there is no second Dockerfile. `alsa-utils` rides along
  unused at home.
- The image's entrypoint takes no arguments, and the Supervisor cannot pass
  any. So the config path comes from the environment: `SPECTRUM_CONFIG`, which
  the README's "SPECTRUM_* env vars work too" already promises for every other
  flag. `config.Load` reads the path through viper instead of from the flag
  alone. The app sets `SPECTRUM_CONFIG: /data/options.json`.
- `host_network: true`: Music Assistant is reached at `127.0.0.1:8927`
  whatever the Pi's address is, and the API listens on the host's 8099 as it
  does at the office. No devices, no audio, no privileges.
- Local, over SSH: the code does not have to be committed or pushed to try it.
  `tar` through `ssh`, not `rsync`: the SSH app is not known to ship rsync.

## Files

`addon/config.yaml`:

```yaml
name: Loudest Office Visualizer
version: "0.1.0"
slug: loudest_visualizer
description: Audio visualizer on a HUB75 panel over WiFi, fed by Music Assistant through Sendspin
url: https://github.com/jon4hz/loudest-office
arch: [aarch64]
init: false
host_network: true
environment:
  SPECTRUM_CONFIG: /data/options.json
watchdog: http://[HOST]:8099/api/v1/state
options:
  cmd: sendspin-pipe
  args: [--server, "127.0.0.1:8927", --name, Visualizer]
  serial: tcp://192.168.16.70:7090
  rate: 44100
  brightness: 60
  fps: 30
  listen: ":8099"
  state: /data/state.json
schema:
  cmd: str
  args: [str]
  serial: str
  rate: int
  brightness: int(0,255)
  fps: int(1,120)
  listen: str
  state: str
```

`/data` is the app's persistent volume, so `state.json` survives rebuilds.
The Supervisor sets `TZ`; the image has `tzdata`. The first run checks that
the clock bubble shows local time.

`Taskfile.yml`, `addon:deploy` (`deps: [visualizer:build]`, `HA=user@host`
required): stream `addon/config.yaml` and the build context (`Dockerfile`,
`spectrum`, `sendspin-pipe/`) as one tar into `/addons/loudest_visualizer` on
the Pi, replacing what is there; then `ha store reload`, and, depending on
whether `ha apps info local_loudest_visualizer` knows the app, `ha apps
rebuild` or `ha apps install`. No ignored errors: a failed rebuild fails the
task. An interrupted copy leaves a half-filled folder; running the task again
repairs it. Checked on the Pi (2026-09-19): aarch64, 8 GB; the SSH app logs
in as a normal user with passwordless `sudo`; `/addons` belongs to root, so
the copy runs under `sudo`; the CLI was renamed with the UI (`ha apps`,
`ha store`). The Supervisor's API token only exists in that user's login
shell: `ha` runs as `bash -lc "ha …"` and never under `sudo`, where it answers
"unauthorized". After the first install the options are set and the app is
started in the Home Assistant UI.

`visualizer/config/config.go`: the config path is `v.GetString("config")`
(flag, else `SPECTRUM_CONFIG`). Test: a file named by the environment is
loaded; the flag wins over it.

README: a short "As a Home Assistant app" part under "At home": the SSH app
with a key, `task addon:deploy HA=<user>@<pi>`, install and start in the UI,
where the log is.

## Not built

- A GitHub app repository (`repository.yaml`, tagged builds): worth it once
  someone else wants to install this.
- Ingress or a web UI, Home Assistant entities, the Tronbyt idle bubble.
- A second architecture: `task visualizer:build` targets arm64.

## Verification

- `go test ./config/`, `go vet ./...`, `go test ./...`.
- On the laptop: `SPECTRUM_CONFIG=options.json go run ./cmd/spectrum` starts
  with that file (the image itself holds an arm64 `spectrum` and only runs on
  the Pi; its Dockerfile does not change).
- On the Pi: the app installs and builds, its log shows the pipe connecting
  to `127.0.0.1:8927` and the panel's INFO, the Tidbyt shows the clock, then
  bars when the group plays; `curl <pi>:8099/api/v1/state` answers; a restart
  of the app keeps `state.json`; the watchdog does not restart a healthy app.
