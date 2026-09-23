# Home Tidbyt (WiFi panel, Sendspin audio) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream the visualizer to a Tidbyt v1 over WiFi and take the audio from Music Assistant through Sendspin, without touching how the office setup works.

**Architecture:** The wire protocol stays as it is. `serial` learns to dial `tcp://host:port`; an ESPHome external component, `panel_stream`, accepts that connection, runs the shared `proto.h` parser and paints the frames through ESPHome's `hub75` display. The audio comes from `sendspin-pipe`, a Sendspin player in its own Go module that writes S16LE to stdout at play time, so `spectrum` reads it like `arecord`.

**Tech Stack:** Go 1.27 (`visualizer/`), `github.com/Sendspin/sendspin-go` v1.8.2 (cgo: miniaudio, libopus) in the nested module only, ESPHome 2026.9.0 on esp-idf, lwIP BSD sockets, PlatformIO for the existing firmware.

**Spec:** `docs/superpowers/specs/2026-09-19-home-tidbyt-design.md`. Read it first; it is normative for behaviour, this plan for order and names.

## Global Constraints

- **Never `git commit`, merge or push: the user owns git history.** A task ends with a green build, not a commit.
- Module `github.com/jon4hz/loudest-office/visualizer`; Go paths in tasks 1, 4 are relative to `visualizer/`. **No new dependencies in that module**, and `CGO_ENABLED=0 go build ./...` must keep working there. Tests use stdlib `testing` only.
- `sendspin-go` is pinned to `v1.8.2`, ESPHome to `2026.9.0`. Do not float either.
- The wire protocol does not change: `A5 5A | type | seq | len u16 | payload | crc u16`, FRAME payload `pixfmt 0, flags 0, 64*32*2 bytes` little-endian RGB565.
- TCP port `7090`. Tidbyt brightness cap `100`. Sample format on the pipe: S16LE, 44100 Hz, 2 channels.
- Match the surrounding code's comment density and naming. Deliberate shortcuts get a `ponytail:` comment naming the ceiling and the upgrade path.
- Build exactly the names listed under **Produces**, nothing more.
- After every Go task: `cd visualizer && go build ./... && go vet ./... && go test ./...` is green.

## File map

| File | Responsibility |
|---|---|
| `visualizer/serial/serial.go` | + TCP dial, write deadline, STATUS timeout |
| `visualizer/serial/serial_test.go` | + fake panel on a TCP listener |
| `firmware/src/proto.h` | + `Parser::reset()` |
| `esphome/tidbyt.yaml` | the Tidbyt's ESPHome config |
| `esphome/components/panel_stream/__init__.py` | config schema and codegen |
| `esphome/components/panel_stream/panel_stream.{h,cpp}` | TCP server, parser, painting |
| `esphome/components/panel_stream/proto.h` | symlink to `firmware/src/proto.h` |
| `visualizer/config/config.go`, `cmd/spectrum/main.go` | + `args` key |
| `visualizer/cmd/sendspin-pipe/` | nested module: `main.go`, `pcmout/` |
| `ansible/roles/visualizer/files/build/Dockerfile`, `Taskfile.yml` | image with both binaries |
| `README.md` | home setup section |

---

### Task 1: TCP transport in `serial`

**Files:** Modify `serial/serial.go`, `serial/serial_test.go`.

**Produces:** `serial.Open("tcp://host:port", baud, brightness)` dials TCP (baud ignored). Package vars `writeTimeout`, `statusTimeout`, `retry` (tests shorten them). No exported name changes.

- [ ] **Step 1: Write the failing test.** Append to `serial/serial_test.go` (add `net` and `sync/atomic` to the imports):

```go
// A TCP listener stands in for the ESPHome firmware: INFO on HELLO, a STATUS
// every 50 ms until told to go silent.
func TestTCPReconnect(t *testing.T) {
	statusTimeout, retry = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { statusTimeout, retry = 5*time.Second, time.Second })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	var silent atomic.Bool
	conns := make(chan net.Conn, 8)
	pkts := make(chan proto.Packet, 64)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
			go func() {
				for !silent.Load() {
					if _, err := c.Write(proto.Encode(nil, proto.Status, 1, []byte{9, 0, 0, 0, 0, 0, 30, 0xff})); err != nil {
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
			}()
			go func() {
				var d proto.Decoder
				buf := make([]byte, 8192)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					for _, p := range d.Feed(buf[:n]) {
						if p.Type == proto.Hello {
							c.Write(proto.Encode(nil, proto.Info, 0, []byte{4, 0, 2, 0, 1, 60, 1, 0}))
						}
						pkts <- p
					}
				}
			}()
		}
	}()
	next := func(want byte) proto.Packet {
		t.Helper()
		select {
		case p := <-pkts:
			if p.Type != want {
				t.Fatalf("got type %#x, want %#x", p.Type, want)
			}
			return p
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for type %#x", want)
		}
		panic("unreachable")
	}
	conn := func() net.Conn {
		t.Helper()
		select {
		case c := <-conns:
			return c
		case <-time.After(5 * time.Second):
			t.Fatal("no connection")
		}
		panic("unreachable")
	}

	p, info, err := Open("tcp://"+ln.Addr().String(), 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if info.W != 4 || info.H != 2 {
		t.Fatalf("info = %+v", info)
	}
	first := conn()
	next(proto.Hello) // no boot wait and no flush on TCP: the first HELLO is answered
	if c := next(proto.Config); c.Payload[0] != 64 {
		t.Fatalf("brightness = %d", c.Payload[0])
	}
	p.Send([][]color.RGBA{make([]color.RGBA, 4), make([]color.RGBA, 4)})
	next(proto.Frame)

	first.Close() // the panel rebooted: dial again, and send the brightness again
	conn()
	next(proto.Hello)
	next(proto.Config)

	silent.Store(true) // WiFi is gone without a FIN: only the STATUS timeout notices
	conn()
}
```

- [ ] **Step 2: Run it.** `cd visualizer && go test ./serial/ -run TestTCPReconnect` → FAIL: `undefined: statusTimeout`, `undefined: retry`.

- [ ] **Step 3: Implement.** In `serial/serial.go`:

Imports: add `io`, `net`, `strings`.

Below `settle`:

```go
// writeTimeout bounds one packet: a WiFi peer that vanished blocks Write forever otherwise.
var writeTimeout = 2 * time.Second

// statusTimeout is how long the panel may stay quiet; it sends a STATUS every second.
var statusTimeout = 5 * time.Second

// retry is the pause between two attempts to reach a lost panel.
var retry = time.Second

// conn is how the panel is reached: a tty, or TCP for the ESPHome firmware.
type conn interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// dial opens path: tcp://host:port, or a tty at the port's baud rate.
func (p *Port) dial() (conn, error) {
	addr, ok := strings.CutPrefix(p.path, "tcp://")
	if !ok {
		f, err := openRaw(p.path, p.baud)
		if err != nil {
			return nil, err // not f: a nil *os.File in a conn is not nil
		}
		return f, nil
	}
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	// Two frames. The writer replaces a pending frame instead of queueing it;
	// a default socket buffer would queue dozens during a WiFi stall.
	c.(*net.TCPConn).SetWriteBuffer(8192)
	return c, nil
}

func write(f conn, b []byte) error {
	f.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := f.Write(b)
	return err
}
```

`connect`: change the signature to `func (p *Port) connect() (f conn, info proto.InfoMsg, err error)`, open with `if f, err = p.dial(); err != nil`, and make the tty-only parts conditional. The start of the function becomes:

```go
	if f, err = p.dial(); err != nil {
		return nil, info, err
	}
	defer func() {
		if err != nil {
			f.Close()
			f = nil
		}
	}()
	hello := proto.Encode(nil, proto.Hello, 0, nil)
	if tty, ok := f.(*os.File); ok {
		time.Sleep(bootWait)
		// A CP2102 whose receive buffer overflowed while the port was closed (the
		// ESP sends a STATUS every second) replays stale bytes at full USB speed
		// until the host writes something. Write, let the junk land, drop it.
		if err = write(f, hello); err != nil {
			return
		}
		time.Sleep(settle)
		if err = flushInput(tty); err != nil {
			return
		}
	}
```

In the rest of `connect` replace both `f.Write(x)` calls by `write(f, x)` (`if err = write(f, hello); err != nil`, and `err = write(f, proto.Encode(nil, proto.Config, 1, proto.ConfigPayload(b)))`).

`read`: take `f conn`, and arm the timeout at the start and on every STATUS:

```go
func (p *Port) read(f conn, errc chan<- error) {
	d := proto.Decoder{Max: 64}
	buf := make([]byte, 256)
	f.SetReadDeadline(time.Now().Add(statusTimeout))
	for {
		n, err := f.Read(buf)
		if err != nil {
			errc <- err
			return
		}
		for _, pkt := range d.Feed(buf[:n]) {
			if s, ok := proto.ParseStatus(pkt.Payload); ok && pkt.Type == proto.Status {
				f.SetReadDeadline(time.Now().Add(statusTimeout))
				p.mu.Lock()
				p.status = s
				p.mu.Unlock()
			}
		}
	}
}
```

`run`: take `f conn`; replace the two `_, err = f.Write(pkt)` by `err = write(f, pkt)`, the BLANK write by `write(f, proto.Encode(nil, proto.Blank, seq, nil))`, and `time.After(time.Second)` by `time.After(retry)`. `Open` compiles unchanged (`go p.run(f)`).

Update the package comment's first line to `// Package serial streams frames to the ESP32 behind a USB serial port or a TCP socket.` and the `--serial` flag help in `config/config.go` to `"panel: serial port of the ESP32, e.g. /dev/ttyUSB0, or tcp://host:7090 for the ESPHome firmware (the frame then has the panel's size)"`.

- [ ] **Step 4: Run all tests.** `cd visualizer && go build ./... && go vet ./... && go test -race ./serial/ && go test ./...` → PASS, including the unchanged `TestHandshakeFrameBlank`.

---

### Task 2: ESPHome config and `panel_stream`

**Files:** Create `esphome/tidbyt.yaml`, `esphome/secrets.yaml.example`, `esphome/components/panel_stream/{__init__.py,panel_stream.h,panel_stream.cpp}`, symlink `esphome/components/panel_stream/proto.h`. Modify `firmware/src/proto.h`, `firmware/test/parser_test.cpp`, `.gitignore`, `Taskfile.yml`.

**Consumes:** `Parser`, `crc16`, `MSG_*`, `PANEL_W`, `PANEL_H` from `firmware/src/proto.h`. ESPHome's `hub75::HUB75Display` (`set_brightness(uint8_t)`, `update()`) and `display::Display::draw_pixels_at`.

**Produces:** a TCP server on port 7090 that behaves like `firmware/src/main.cpp` does on the UART. YAML keys `panel_stream: {id, display_id, port, max_brightness}`; C++ `void PanelStream::draw(display::Display &it)` for the display lambda.

- [ ] **Step 1: `Parser::reset()`, test first.** In `firmware/test/parser_test.cpp` add, right before `puts("ok");`:

```cpp
  // a new TCP client must not inherit half a packet from the last one
  static Parser q;
  assert(feed(q, config, sizeof config / 2) == 0);
  q.reset();
  assert(feed(q, hello, sizeof hello) == 1 && q.crcErr == 0);
```

Run `task firmware:test` → FAIL to compile: no member `reset`. Then add to `struct Parser` in `firmware/src/proto.h`, after `feed`:

```cpp
  // reset forgets a half-received packet, e.g. when the peer changed.
  void reset() { state = MAGIC1; }
```

Run `task firmware:test` → PASS. Run `cd firmware && pio run` → all envs SUCCESS.

- [ ] **Step 2: Scaffolding.**

```bash
mkdir -p esphome/components/panel_stream
ln -s ../../../firmware/src/proto.h esphome/components/panel_stream/proto.h
```

Append to `.gitignore`:

```
esphome/.esphome/
esphome/secrets.yaml
```

`esphome/secrets.yaml.example`:

```yaml
wifi_ssid: "..."
wifi_password: "..."
api_key: "..."       # openssl rand -base64 32
ota_password: "..."
```

Add to `Taskfile.yml`, after `firmware:bin`:

```yaml
  esphome:compile:
    desc: Build the Tidbyt's ESPHome firmware
    dir: esphome
    cmds:
      - uvx esphome@2026.9.0 compile tidbyt.yaml

  esphome:run:
    desc: Build and flash the Tidbyt (the first time over USB, then over the air) and show its log
    dir: esphome
    cmds:
      - uvx esphome@2026.9.0 run tidbyt.yaml {{.CLI_ARGS}}
```

- [ ] **Step 3: `esphome/tidbyt.yaml`.**

```yaml
# Tidbyt v1 as a WiFi panel for the visualizer: `spectrum --serial tcp://<ip>:7090`.
# Copy secrets.yaml.example to secrets.yaml first. See ../README.md.
esphome:
  name: tidbyt
  friendly_name: Tidbyt

esp32:
  board: esp32dev
  flash_size: 8MB
  framework:
    type: esp-idf

logger:

api:
  encryption:
    key: !secret api_key

ota:
  - platform: esphome
    password: !secret ota_password

wifi:
  ssid: !secret wifi_ssid
  password: !secret wifi_password
  # modem sleep delays frames by up to a beacon interval (100 ms)
  power_save_mode: none

external_components:
  - source:
      type: local
      path: components

display:
  - platform: hub75
    id: panel
    panel_width: 64
    panel_height: 32
    double_buffer: true
    brightness: 64
    update_interval: never
    auto_clear_enabled: false # panel_stream paints every pixel
    # Pin map from tronbyt/firmware-esp32. If the boot bars are not red, green,
    # blue, the unit is the colour-swapped variant:
    #   r1_pin: 21, g1_pin: 2, b1_pin: 22, r2_pin: 23, g2_pin: 4, b2_pin: 27
    r1_pin: 2
    g1_pin: 22
    b1_pin: 21
    r2_pin: 4
    g2_pin: 27
    b2_pin: 23
    a_pin: 26
    b_pin: 5
    c_pin: 25
    d_pin: 18
    lat_pin: 19
    oe_pin: 32
    clk_pin: 33
    lambda: id(stream).draw(it);

panel_stream:
  id: stream
  display_id: panel
  port: 7090
  # the panel is fed from the USB port; Tronbyt uses the same cap
  max_brightness: 100
```

- [ ] **Step 4: `esphome/components/panel_stream/__init__.py`.**

```python
"""TCP endpoint of the visualizer's panel protocol, painting into a hub75 display."""

import esphome.codegen as cg
from esphome.components import socket
from esphome.components.hub75.display import HUB75Display
import esphome.config_validation as cv
from esphome.const import CONF_ID, CONF_PORT

DEPENDENCIES = ["network", "display"]

CONF_DISPLAY_ID = "display_id"
CONF_MAX_BRIGHTNESS = "max_brightness"

panel_stream_ns = cg.esphome_ns.namespace("panel_stream")
PanelStream = panel_stream_ns.class_("PanelStream", cg.Component)


def _sockets(config):
    # one listener and one client; lwIP's socket pool is sized from these counts
    socket.consume_sockets(1, "panel_stream", socket.SocketType.TCP_LISTEN)(config)
    socket.consume_sockets(1, "panel_stream")(config)
    return config


CONFIG_SCHEMA = cv.All(
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(PanelStream),
            cv.Required(CONF_DISPLAY_ID): cv.use_id(HUB75Display),
            cv.Optional(CONF_PORT, default=7090): cv.port,
            cv.Optional(CONF_MAX_BRIGHTNESS, default=255): cv.int_range(min=1, max=255),
        }
    ).extend(cv.COMPONENT_SCHEMA),
    _sockets,
)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
    cg.add(var.set_display(await cg.get_variable(config[CONF_DISPLAY_ID])))
    cg.add(var.set_port(config[CONF_PORT]))
    cg.add(var.set_max_brightness(config[CONF_MAX_BRIGHTNESS]))
```

- [ ] **Step 5: `panel_stream.h`.**

```cpp
// TCP endpoint of the panel protocol (see proto.h): what firmware/src/main.cpp
// does on the UART, as an ESPHome component in front of a hub75 display.
#pragma once
#ifdef USE_ESP32

#include "esphome/components/display/display.h"
#include "esphome/components/hub75/hub75_component.h"
#include "esphome/core/component.h"
#include "esphome/core/helpers.h"

#include "proto.h"

namespace esphome::panel_stream {

class PanelStream : public Component {
 public:
  void set_display(hub75::HUB75Display *display) { this->display_ = display; }
  void set_port(uint16_t port) { this->port_ = port; }
  void set_max_brightness(uint8_t b) { this->max_brightness_ = b; }

  void setup() override;
  void loop() override;
  void dump_config() override;
  float get_setup_priority() const override { return setup_priority::AFTER_WIFI; }

  // For the display lambda: paints what the panel shows right now.
  void draw(display::Display &it);

 protected:
  enum What : uint8_t { BOOT, LIVE, NO_SIGNAL, BLANK };

  void show_(What what);
  void handle_();
  void send_(uint8_t type, const uint8_t *payload, uint16_t n);
  void drop_client_();

  hub75::HUB75Display *display_{nullptr};
  uint16_t port_{7090};
  uint8_t max_brightness_{255};

  int listen_fd_{-1};
  int client_fd_{-1};
  HighFrequencyLoopRequester high_freq_;

  Parser parser_;
  uint8_t frame_[PANEL_W * PANEL_H * 2];
  What what_{BOOT};
  uint16_t frames_ok_{0}, seq_gaps_{0};
  uint8_t last_seq_{0}, tx_seq_{0}, fps_{0};
  uint32_t last_frame_{0}, last_status_{0};
};

}  // namespace esphome::panel_stream

#endif
```

- [ ] **Step 6: `panel_stream.cpp`.**

```cpp
#ifdef USE_ESP32

#include "panel_stream.h"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <lwip/sockets.h>

#include "esphome/core/hal.h"
#include "esphome/core/log.h"

namespace esphome::panel_stream {

static const char *const TAG = "panel_stream";
static const uint16_t FW_VERSION = 1;
static const uint32_t NO_SIGNAL_MS = 2000;

void PanelStream::setup() {
  this->listen_fd_ = ::socket(AF_INET, SOCK_STREAM, 0);
  int on = 1;
  struct sockaddr_in addr {};
  addr.sin_family = AF_INET;
  addr.sin_port = htons(this->port_);
  addr.sin_addr.s_addr = htonl(INADDR_ANY);
  if (this->listen_fd_ < 0 || ::setsockopt(this->listen_fd_, SOL_SOCKET, SO_REUSEADDR, &on, sizeof on) < 0 ||
      ::bind(this->listen_fd_, (struct sockaddr *) &addr, sizeof addr) < 0 || ::listen(this->listen_fd_, 1) < 0 ||
      ::fcntl(this->listen_fd_, F_SETFL, O_NONBLOCK) < 0) {
    ESP_LOGE(TAG, "listen on port %u: errno %d", this->port_, errno);
    this->mark_failed();
    return;
  }
  this->display_->set_brightness(std::min<uint8_t>(64, this->max_brightness_));
  this->show_(BOOT);
}

void PanelStream::dump_config() {
  ESP_LOGCONFIG(TAG, "Panel stream:\n  Port: %u\n  Max brightness: %u", this->port_, this->max_brightness_);
}

void PanelStream::loop() {
  // A new connection replaces the old one: a restarted host is never locked
  // out by a socket that is half open on this side.
  int fd = ::accept(this->listen_fd_, nullptr, nullptr);
  if (fd >= 0) {
    this->drop_client_();
    int on = 1;
    ::fcntl(fd, F_SETFL, O_NONBLOCK);
    ::setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &on, sizeof on);
    this->client_fd_ = fd;
    this->parser_.reset();
    this->parser_.crcErr = 0;
    this->frames_ok_ = this->seq_gaps_ = 0;
    this->high_freq_.start();  // the default 16 ms loop would cap the frame rate
    ESP_LOGI(TAG, "client connected");
  }

  if (this->client_fd_ >= 0) {
    uint8_t buf[1024];
    for (int i = 0; i < 8; i++) {  // at most two frames per loop, the other components want to run too
      int n = ::recv(this->client_fd_, buf, sizeof buf, 0);
      if (n > 0) {
        for (int j = 0; j < n; j++)
          if (this->parser_.feed(buf[j]))
            this->handle_();
        continue;
      }
      if (n == 0 || (errno != EWOULDBLOCK && errno != EAGAIN))
        this->drop_client_();
      break;
    }
  }

  uint32_t now = millis();
  if (this->what_ == LIVE && now - this->last_frame_ > NO_SIGNAL_MS)
    this->show_(NO_SIGNAL);
  if (this->client_fd_ >= 0 && now - this->last_status_ >= 1000) {
    const uint16_t crc_err = this->parser_.crcErr;
    const uint8_t status[8] = {(uint8_t) this->frames_ok_, (uint8_t) (this->frames_ok_ >> 8),
                               (uint8_t) crc_err,          (uint8_t) (crc_err >> 8),
                               (uint8_t) this->seq_gaps_,  (uint8_t) (this->seq_gaps_ >> 8),
                               this->fps_,                 0xFF};
    this->send_(MSG_STATUS, status, sizeof status);
    ESP_LOGD(TAG, "frames %u, crc errors %u, seq gaps %u, %u fps", this->frames_ok_, crc_err, this->seq_gaps_,
             this->fps_);
    this->fps_ = 0;
    this->last_status_ = now;
  }
}

void PanelStream::handle_() {
  if (this->parser_.type != MSG_HELLO)
    this->seq_gaps_ += (uint8_t) (this->parser_.seq - this->last_seq_ - 1);
  this->last_seq_ = this->parser_.seq;

  switch (this->parser_.type) {
    case MSG_HELLO: {
      const uint8_t info[8] = {PANEL_W & 0xff, PANEL_W >> 8, PANEL_H & 0xff, PANEL_H >> 8,
                               1 /* RGB565 */, 60,           FW_VERSION & 0xff, FW_VERSION >> 8};
      this->send_(MSG_INFO, info, sizeof info);
      break;
    }
    case MSG_FRAME:
      if (this->parser_.len != Parser::MAX || this->parser_.payload[0] != 0)
        break;
      memcpy(this->frame_, this->parser_.payload + 2, sizeof this->frame_);
      this->frames_ok_++, this->fps_++;
      this->last_frame_ = millis();
      this->show_(LIVE);
      break;
    case MSG_CONFIG:  // gamma and rotation are not used yet
      if (this->parser_.len >= 1)
        this->display_->set_brightness(std::min(this->parser_.payload[0], this->max_brightness_));
      break;
    case MSG_BLANK:
      this->show_(BLANK);
      break;
  }
}

void PanelStream::show_(What what) {
  this->what_ = what;
  this->display_->update();  // runs the lambda, which calls draw(), then flips the buffers
}

void PanelStream::draw(display::Display &it) {
  switch (this->what_) {
    case BOOT:  // red, green and blue bars: wrong colours or a garbled picture mean a wrong pin map
      it.filled_rectangle(0, 0, PANEL_W / 3, PANEL_H, Color(255, 0, 0));
      it.filled_rectangle(PANEL_W / 3, 0, PANEL_W / 3, PANEL_H, Color(0, 255, 0));
      it.filled_rectangle(2 * (PANEL_W / 3), 0, PANEL_W - 2 * (PANEL_W / 3), PANEL_H, Color(0, 0, 255));
      break;
    case LIVE:
      it.draw_pixels_at(0, 0, PANEL_W, PANEL_H, this->frame_, display::COLOR_ORDER_RGB, display::COLOR_BITNESS_565,
                        false /* the wire is little-endian */, 0, 0, 0);
      break;
    case NO_SIGNAL:  // a small red cross in the corner
      it.fill(Color::BLACK);
      for (int i = 0; i < 5; i++) {
        it.draw_pixel_at(1 + i, 1 + i, Color(96, 0, 0));
        it.draw_pixel_at(5 - i, 1 + i, Color(96, 0, 0));
      }
      break;
    case BLANK:
      it.fill(Color::BLACK);
      break;
  }
}

void PanelStream::send_(uint8_t type, const uint8_t *payload, uint16_t n) {
  uint8_t pkt[6 + 8 + 2];  // INFO and STATUS carry 8 bytes
  if (this->client_fd_ < 0 || n > 8)
    return;
  pkt[0] = 0xA5, pkt[1] = 0x5A, pkt[2] = type, pkt[3] = this->tx_seq_++, pkt[4] = (uint8_t) n, pkt[5] = n >> 8;
  memcpy(pkt + 6, payload, n);
  uint16_t crc = 0xFFFF;
  for (int i = 2; i < 6 + n; i++)
    crc = crc16(crc, pkt[i]);
  pkt[6 + n] = (uint8_t) crc, pkt[7 + n] = crc >> 8;
  // 16 bytes into an empty send buffer: a short or failed write means the peer is gone
  if (::send(this->client_fd_, pkt, 8 + n, 0) != 8 + n)
    this->drop_client_();
}

void PanelStream::drop_client_() {
  if (this->client_fd_ < 0)
    return;
  ::close(this->client_fd_);
  this->client_fd_ = -1;
  this->high_freq_.stop();
  ESP_LOGI(TAG, "client gone");
  if (this->what_ == LIVE)
    this->show_(NO_SIGNAL);
}

}  // namespace esphome::panel_stream

#endif
```

- [ ] **Step 7: Compile.** `cp esphome/secrets.yaml.example esphome/secrets.yaml`, fill in throwaway values if the real ones are not at hand (`api_key` must be valid base64 of 32 bytes: `openssl rand -base64 32`), then `task esphome:compile` → `INFO Successfully compiled program.` The first run downloads the toolchain and takes several minutes. If ESPHome reports that `socket.SocketType` does not exist, the installed version is not 2026.9.0: fix the version, not the code.

- [ ] **Step 8: Regression.** `task firmware:test` and `cd firmware && pio run` still pass (the symlink must not have disturbed the PlatformIO build).

---

### Task 3: Transport on hardware (with the user)

No code. Needs the Tidbyt on USB and the user's WiFi secrets in `esphome/secrets.yaml`. **Stop and ask the user before flashing**; never flash while anything else holds `/dev/ttyUSB0`.

- [ ] `task esphome:run -- --device /dev/ttyUSB0`. Expected in the log: WiFi connected with an IP, `Panel stream: Port: 7090`. On the panel: red, green, blue bars, left to right. Other colours → switch to the commented pin block in `tidbyt.yaml`, flash again (now over the air).
- [ ] `cd visualizer && go run ./cmd/spectrum --serial tcp://<ip>:7090 --brightness 60 --listen :8099` with music playing on the PC. The picture matches the terminal view, colours included. Red and blue swapped or noisy colours while the boot bars were right → the endianness flag in `draw()` is wrong: set `big_endian` to `true`, flash, check again.
- [ ] After ten minutes: `curl -s localhost:8099/api/v1/state` shows about 30 fps and `CRCErr` 0, `SeqGaps` 0. Anything else over TCP is a bug in `panel_stream`, not noise.
- [ ] Quit `spectrum`: the panel goes dark (BLANK). Kill it with `-9` instead: the red cross appears within about 2 s. Start it again: the picture is back within 2 s.
- [ ] Unplug and replug the Tidbyt while `spectrum` runs: the picture comes back by itself (STATUS timeout, then the reconnect loop).
- [ ] Write the measured fps and counters into the spec's "What is known" section.

---

### Task 4: `args` config key

**Files:** Modify `config/config.go`, `config/config_test.go`, `cmd/spectrum/main.go`.

**Produces:** `Config.Args []string`, flag `--args`, YAML key `args`. `nil` when not given, because `audio.Config` generates the `parec`/`arecord` arguments only while `Args == nil`.

- [ ] **Step 1: Failing test.** Append to `config/config_test.go`:

```go
func TestArgs(t *testing.T) {
	c, err := load(t, "cmd: sendspin-pipe\nargs: [--server, \"ma.home:8927\", --delay-ms, \"40\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--server", "ma.home:8927", "--delay-ms", "40"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args = %q, want %q", c.Args, want)
	}
	if c, _ = load(t, ""); c.Args != nil { // nil, not empty: audio.Config then builds the parec arguments
		t.Errorf("args = %#v, want nil", c.Args)
	}
}
```

Run `go test ./config/` → FAIL: `c.Args undefined`.

- [ ] **Step 2: Implement.** In `config/config.go` add the field after `Device`:

```go
	Args       []string `mapstructure:"args"`
```

the flag after `device` (and extend the `cmd` help to `"capture command: parec, arecord, or any command that writes S16LE to stdout (then set args)"`):

```go
	f.StringSlice("args", nil, "arguments of the capture command, instead of the generated ones")
```

and at the end of `Load`, before `return &c, nil`:

```go
	if len(c.Args) == 0 {
		c.Args = nil // audio.Config generates the arguments only for nil
	}
```

In `cmd/spectrum/main.go` pass it on: `audio.Config{Command: c.Cmd, Args: c.Args, Device: c.Device, Rate: c.Rate, Channels: channels}`.

- [ ] **Step 3:** `go build ./... && go vet ./... && go test ./...` → PASS (`TestPrecedence` still expects `Args` nil).

---

### Task 5: `sendspin-pipe`

**Files:** Create `visualizer/cmd/sendspin-pipe/{go.mod,main.go}`, `visualizer/cmd/sendspin-pipe/pcmout/{pcmout.go,pcmout_test.go}`.

A nested module: `go build ./...` in `visualizer/` skips it, which is the point. `pcmout` imports nothing outside the stdlib, so its tests need no cgo and no libopus.

**Produces:**

```go
package pcmout
const Rate, Channels = 44100, 2
var ErrFormat error
type Output struct{ Now func() time.Time /* defaults to time.Now */ }
func New(w io.Writer) *Output
func (o *Output) Open(sampleRate, channels, bitDepth int) error // ErrFormat unless 44100 Hz stereo
func (o *Output) Write(samples []int32) error                   // 24-bit-justified int32 → S16LE
func (o *Output) Fill(now time.Time) error                      // silence, once nothing was written for 100 ms
func (o *Output) Close() error
func (o *Output) SetVolume(int)
func (o *Output) SetMuted(bool)
```

`*Output` satisfies `sendspin-go`'s `output.Output` interface structurally.

- [ ] **Step 1: Module.**

```bash
mkdir -p visualizer/cmd/sendspin-pipe/pcmout && cd visualizer/cmd/sendspin-pipe
go mod init github.com/jon4hz/loudest-office/visualizer/cmd/sendspin-pipe
```

- [ ] **Step 2: Failing tests.** `pcmout/pcmout_test.go`:

```go
package pcmout

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestWriteConverts(t *testing.T) {
	var b bytes.Buffer
	o := New(&b)
	// sendspin-go hands out 24-bit-justified samples: int16 << 8
	if err := o.Write([]int32{0, 1 << 8, -1 << 8, 32767 << 8, -32768 << 8}); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 1, 0, 0xff, 0xff, 0xff, 0x7f, 0, 0x80}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("got % x, want % x", b.Bytes(), want)
	}
}

func TestOpen(t *testing.T) {
	o := New(&bytes.Buffer{})
	if err := o.Open(44100, 2, 16); err != nil {
		t.Error(err)
	}
	if err := o.Open(48000, 2, 16); !errors.Is(err, ErrFormat) {
		t.Errorf("48 kHz: %v", err)
	}
	if err := o.Open(44100, 1, 16); !errors.Is(err, ErrFormat) {
		t.Errorf("mono: %v", err)
	}
}

// Silence flows in real time while nothing plays, and never on top of audio.
func TestFill(t *testing.T) {
	var b bytes.Buffer
	now := time.Unix(1000, 0)
	o := New(&b)
	o.Now = func() time.Time { return now }
	const frame = Channels * 2 // bytes

	o.Fill(now) // the first call only starts the clock
	now = now.Add(50 * time.Millisecond)
	o.Fill(now)
	if b.Len() != 0 {
		t.Fatalf("%d bytes after 50 ms, want none yet", b.Len())
	}
	now = now.Add(50 * time.Millisecond)
	o.Fill(now)
	if want := Rate / 10 * frame; b.Len() != want {
		t.Fatalf("%d bytes after 100 ms, want %d", b.Len(), want)
	}

	b.Reset()
	now = now.Add(90 * time.Millisecond)
	o.Write(make([]int32, 2)) // music: the idle clock starts over
	b.Reset()
	now = now.Add(90 * time.Millisecond)
	o.Fill(now)
	if b.Len() != 0 {
		t.Fatalf("%d bytes of silence 90 ms after audio", b.Len())
	}

	now = now.Add(time.Hour) // no drift: an hour of silence is an hour of frames
	o.Fill(now)
	if want := (3600*Rate + Rate*90/1000) * frame; b.Len() != want {
		t.Fatalf("%d bytes, want %d", b.Len(), want)
	}
}
```

Run `go test ./pcmout/` → FAIL: `undefined: New`.

- [ ] **Step 3: `pcmout/pcmout.go`.**

```go
// Package pcmout is a Sendspin audio output that writes S16LE to a writer
// instead of a sound card, and silence while nothing plays.
package pcmout

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	Rate     = 44100
	Channels = 2
	idle     = 100 * time.Millisecond
)

// ErrFormat is returned by Open for anything but 44100 Hz stereo: the reader
// of the pipe cannot be told about another format.
var ErrFormat = errors.New("unsupported stream format")

type Output struct {
	Now func() time.Time

	mu   sync.Mutex // Write and Fill come from two goroutines
	w    io.Writer
	buf  []byte
	last time.Time // until when w has been fed
}

func New(w io.Writer) *Output { return &Output{Now: time.Now, w: w} }

func (o *Output) Open(sampleRate, channels, bitDepth int) error {
	if sampleRate != Rate || channels != Channels {
		return fmt.Errorf("%w: %d Hz, %d channels", ErrFormat, sampleRate, channels)
	}
	return nil
}

// Write gets the samples at their play time. They are 24-bit-justified.
func (o *Output) Write(samples []int32) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf = o.buf[:0]
	for _, s := range samples {
		o.buf = binary.LittleEndian.AppendUint16(o.buf, uint16(int16(s>>8)))
	}
	o.last = o.Now()
	_, err := o.w.Write(o.buf)
	return err
}

// Fill writes the silence that is due once nothing was written for idle. Call
// it from a ticker. Without it the reader sees no blocks while nothing plays
// and cannot tell silence from a stalled capture.
func (o *Output) Fill(now time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.last.IsZero() {
		o.last = now
		return nil
	}
	d := now.Sub(o.last)
	if d < idle {
		return nil
	}
	frames := int(d * Rate / time.Second)
	o.last = o.last.Add(time.Duration(frames) * time.Second / Rate) // not now: the remainder carries over
	_, err := o.w.Write(make([]byte, frames*Channels*2))
	return err
}

func (o *Output) Close() error  { return nil }
func (o *Output) SetVolume(int) {}
func (o *Output) SetMuted(bool) {}
```

Run `go test -race ./pcmout/` → PASS. If `TestFill`'s last assertion is off by one frame, the carry-over in `Fill` is wrong, not the test.

- [ ] **Step 4: `main.go`.**

```go
// sendspin-pipe: a Sendspin player whose loudspeaker is stdout. Music Assistant
// sees a player; whoever reads the pipe gets S16LE, 44100 Hz, stereo, in step
// with the other players of the group, and silence while nothing plays.
//
//	sendspin-pipe --server ma.home:8927 | spectrum-like-reader
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/sendspin"

	"github.com/jon4hz/loudest-office/visualizer/cmd/sendspin-pipe/pcmout"
)

func main() {
	server := flag.String("server", "", "Sendspin server as host:port (Music Assistant: port 8927)")
	name := flag.String("name", "Visualizer", "player name")
	id := flag.String("id", "visualizer", "client id, keep it stable or the server sees a new player")
	delay := flag.Int("delay-ms", 0, "static delay, 0-5000: plays later by this much")
	flag.Parse()
	if *server == "" {
		log.Fatal("--server is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := pcmout.New(os.Stdout)
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if err := out.Fill(now); err != nil { // the reader is gone
					log.Print(err)
					stop()
					return
				}
			}
		}
	}()

	p, err := sendspin.NewPlayer(sendspin.PlayerConfig{
		ServerAddr:     *server,
		PlayerName:     *name,
		ClientID:       *id,
		Volume:         100,
		StaticDelayMs:  *delay,
		PreferredCodec: "pcm",
		MaxSampleRate:  pcmout.Rate,
		MaxBitDepth:    16,
		DeviceInfo:     sendspin.DeviceInfo{ProductName: "loudest-office visualizer", Manufacturer: "jon4hz", SoftwareVersion: "1"},
		Output:         out,
		Reconnect:      sendspin.ReconnectConfig{Enabled: true},
		OnError: func(err error) {
			log.Print(err)
			if errors.Is(err, pcmout.ErrFormat) {
				os.Exit(1) // a format the pipe cannot carry: better loud than wrong
			}
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	// Reconnect covers a lost connection, not a server that is not up yet.
	for err := p.Connect(); err != nil; err = p.Connect() {
		log.Printf("%s: %v, again in 5 s", *server, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	<-ctx.Done()
}
```

- [ ] **Step 5: Dependencies and build.** `go get github.com/Sendspin/sendspin-go@v1.8.2 && go mod tidy`. Building needs cgo and the opus headers (Arch: `sudo pacman -S opus opusfile`; ask the user, do not sudo yourself). With them: `go build . && go vet ./... && go test ./...`. Without them on this machine, `go vet ./pcmout/ && go test ./pcmout/` must pass and the full build is proven by Task 6's image build.

- [ ] **Step 6: Check the parent module is untouched.** `cd visualizer && CGO_ENABLED=0 go build ./... && go test ./... && git status --short go.mod go.sum` → builds, and `go.mod`/`go.sum` show no change from this task.

---

### Task 6: One image with both binaries

**Files:** Modify `ansible/roles/visualizer/files/build/Dockerfile`, `Taskfile.yml`, `.gitignore`.

The build context is `ansible/roles/visualizer/files/build/`. `spectrum` keeps being cross-compiled into it; the pipe's sources are copied into it and compiled by a build stage on the target, where cgo has the right architecture for free.

- [ ] **Step 1: Taskfile.** In `visualizer:build`, add a second command after the `go build` line:

```yaml
      - rm -rf ../ansible/roles/visualizer/files/build/sendspin-pipe && cp -r cmd/sendspin-pipe ../ansible/roles/visualizer/files/build/sendspin-pipe
```

and to `.gitignore`: `ansible/roles/visualizer/files/build/sendspin-pipe/`.

- [ ] **Step 2: Dockerfile.** Replace the file with:

```dockerfile
# Built on the raspi by podman-compose; spectrum is cross-compiled by `task visualizer:build`,
# which also copies the sendspin-pipe sources next to it.
# sendspin-pipe needs cgo (miniaudio, libopus), so it is compiled here, on the target.
FROM docker.io/golang:1.27-alpine AS pipe
RUN apk add --no-cache build-base opus-dev opusfile-dev
WORKDIR /src
COPY sendspin-pipe/ .
RUN go build -trimpath -ldflags "-s -w" -o /sendspin-pipe .

FROM docker.io/alpine:3.22
# spectrum captures through arecord, or through sendspin-pipe (opus at run time)
RUN apk add --no-cache alsa-utils opus opusfile
COPY --from=pipe /sendspin-pipe /usr/local/bin/sendspin-pipe
COPY spectrum /spectrum
RUN chmod 0755 /spectrum
ENTRYPOINT ["/spectrum"]
```

- [ ] **Step 3: Build it.** `task visualizer:build`, then on this machine (amd64, so `spectrum` inside is the wrong architecture, but the pipe stage is what is being proven):
`podman build -t spectrum-test ansible/roles/visualizer/files/build && podman run --rm --entrypoint sendspin-pipe spectrum-test --help` → the flag list. If the link step reports missing symbols from `opusfile`, add the package it names to the first `apk add`; do not switch the pipe to a static build.

---

### Task 7: Audio on hardware, README (with the user)

Needs the user's Music Assistant address and their hands in the MA UI.

- [ ] Build the pipe locally (Task 5, step 5) or use the image. Run
`cd visualizer && go run ./cmd/spectrum --cmd /path/to/sendspin-pipe --args=--server,<ma-host>:8927 --serial tcp://<tidbyt-ip>:7090 --listen :8099`. `GET /api/v1/state` shows `playing: false` and a `db` near -120: the silence fill works. The idle bubble shows.
- [ ] The user adds the player "Visualizer" to a sync group with the Playbase and plays a track. The bars move; pause → the idle bubble after `silence_after`.
- [ ] The user adds the Play:5 to the group. Both rooms play (this is `music-assistant/support#6403`; it worked for the user over Sendspin AirPlay before, confirm it still does with the third member).
- [ ] Sync by eye, on a track with a bare kick drum. Note the offset. Visuals early → raise `--delay-ms`. Visuals late → nothing to turn yet; write the number into the spec's Risks section and stop.
- [ ] README: add a "Home setup" section after the panel section: what the three parts are, `task esphome:run`, the `spectrum` config for home

```yaml
cmd: sendspin-pipe
args: [--server, "<ma-host>:8927", --name, Visualizer]
serial: tcp://<tidbyt-ip>:7090   # an IP or a DNS name: .local does not resolve in the container
brightness: 60                   # the firmware caps it at 100
listen: ":8099"
state: /data/state.json
```

a warning that `mono: true` must stay off with `sendspin-pipe` (the pipe is always stereo, `spectrum` would read it misaligned), the group step in Music Assistant, and the wired fallback (`pio run -e tidbyt -t upload`, `--serial /dev/ttyUSB0 --baud 2000000`). Match the README's existing tone and heading levels.
