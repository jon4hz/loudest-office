# Panel Streaming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream the spectrum visualizer's pixel frame to a 64x32 HUB75 panel through an ESP32 over USB serial.

**Architecture:** A PlatformIO firmware parses the Appendix A packet protocol and blits FRAME payloads into the HUB75 DMA back buffer. On the host, `proto` encodes/decodes packets, `serial` owns the port (handshake, latest-frame-wins writer, reconnect), and `cmd/spectrum` gets a `--serial` flag that sends `Model.Frame()` after every audio block.

**Tech Stack:** Go 1.27, `golang.org/x/sys/unix` (termios2), bubbletea v2; PlatformIO, Arduino-ESP32, ESP32-HUB75-MatrixPanel-DMA.

**Spec:** `docs/superpowers/specs/2026-09-18-panel-streaming-design.md` (protocol: Appendix A of `esp_visualizer_plan.md`)

## Global Constraints

- Never `git commit`, merge or push: the user owns git history. Tasks end with passing checks, not commits.
- No new Go module dependencies; `golang.org/x/sys` is already in the module graph (`go mod tidy` promotes it to direct).
- Host code is Linux only.
- Packet layout: `A5 5A`, `u8 type`, `u8 seq`, `u16 len` LE, payload, `u16 crc` LE = CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF) over bytes 2..end of payload.
- Golden packets (used by both the Go and the C++ test): HELLO seq 0 = `a5 5a 01 00 00 00 74 f2`; CONFIG seq 7 payload `40 16 00` = `a5 5a 04 07 03 00 40 16 00 e3 a2`. CRC check value: `CRC16("123456789") == 0x29B1`.
- Pixels are sent as RGB565 little-endian, row-major, top-left origin; alpha is ignored (as in `Model.View`).
- Firmware: the HUB75 library is the only `lib_deps` entry (`-DNO_GFX` keeps Adafruit GFX out). Library default pins, 64x32, no chain.
- Match the surrounding code's comment density and naming.

## File Structure

| File | Responsibility |
|---|---|
| `visualizer/proto/proto.go` | message types, CRC, `Encode`, `Decoder`, typed payload helpers, RGB565 |
| `visualizer/proto/proto_test.go` | golden bytes, decoder resync, payload helpers |
| `visualizer/serial/serial.go` | raw port open, handshake, writer/reader goroutines, reconnect |
| `visualizer/serial/serial_test.go` | fake ESP on a pty |
| `visualizer/cmd/spectrum/main.go` | `--serial`, `--baud`, `--brightness`, headless run |
| `firmware/platformio.ini` | envs `esp32dev`, `esp32s3` |
| `firmware/src/proto.h` | Arduino-free packet parser + CRC |
| `firmware/src/main.cpp` | panel setup, message handling, STATUS, no-signal |
| `firmware/test/parser_test.cpp` | host-compiled parser check |
| `Taskfile.yml`, `.gitignore`, `README.md` | `firmware:*` tasks, `.pio`, docs |

---

### Task 1: `proto` package

**Files:**
- Create: `visualizer/proto/proto.go`
- Test: `visualizer/proto/proto_test.go`

**Interfaces:**
- Produces:
  - `const Hello, Info, Frame, Config, Status, Blank byte = 1..6`
  - `func CRC16(p []byte) uint16`
  - `func Encode(dst []byte, typ, seq byte, payload []byte) []byte` (appends one packet)
  - `type Packet struct{ Type, Seq byte; Payload []byte }`
  - `type Decoder struct{ Max int; CRCErrors int }`, `func (d *Decoder) Feed(p []byte) []Packet` (payloads are copies)
  - `type InfoMsg struct{ W, H uint16; PixFmts, MaxFPS byte; FW uint16 }`, `func ParseInfo(p []byte) (InfoMsg, bool)`
  - `type StatusMsg struct{ FramesOK, CRCErr, SeqGaps uint16; FPS, TempC byte }`, `func ParseStatus(p []byte) (StatusMsg, bool)`
  - `func ConfigPayload(brightness byte) []byte`
  - `func FramePayload(dst []byte, frame [][]color.RGBA) []byte`

- [ ] **Step 1: Write the failing tests**

```go
package proto

import (
	"bytes"
	"image/color"
	"testing"
)

func TestCRC16CheckValue(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0x29B1 {
		t.Fatalf("CRC16 = %#x, want 0x29b1", got)
	}
}

func TestEncodeGolden(t *testing.T) {
	hello := []byte{0xa5, 0x5a, 0x01, 0x00, 0x00, 0x00, 0x74, 0xf2}
	if got := Encode(nil, Hello, 0, nil); !bytes.Equal(got, hello) {
		t.Fatalf("hello = % x", got)
	}
	config := []byte{0xa5, 0x5a, 0x04, 0x07, 0x03, 0x00, 0x40, 0x16, 0x00, 0xe3, 0xa2}
	if got := Encode(nil, Config, 7, ConfigPayload(64)); !bytes.Equal(got, config) {
		t.Fatalf("config = % x", got)
	}
}

func TestDecoderResyncs(t *testing.T) {
	var stream []byte
	stream = append(stream, 0x00, 0xa5, 0xa5, 0x13) // boot garbage, including a lone magic byte
	stream = Encode(stream, Info, 1, []byte{64, 0, 32, 0, 1, 60, 1, 0})
	bad := Encode(nil, Status, 2, make([]byte, 8))
	bad[7] ^= 0xff // corrupt payload
	stream = append(stream, bad...)
	stream = Encode(stream, Blank, 3, nil)

	var d Decoder
	var got []Packet
	for _, b := range stream { // byte at a time: packets may straddle reads
		got = append(got, d.Feed([]byte{b})...)
	}
	if len(got) != 2 || got[0].Type != Info || got[1].Type != Blank || got[1].Seq != 3 {
		t.Fatalf("packets = %+v", got)
	}
	if d.CRCErrors != 1 {
		t.Fatalf("CRCErrors = %d, want 1", d.CRCErrors)
	}
	info, ok := ParseInfo(got[0].Payload)
	if !ok || info != (InfoMsg{W: 64, H: 32, PixFmts: 1, MaxFPS: 60, FW: 1}) {
		t.Fatalf("info = %+v %v", info, ok)
	}
}

func TestDecoderDropsOversized(t *testing.T) {
	d := Decoder{Max: 8}
	if got := d.Feed(Encode(nil, Frame, 0, make([]byte, 9))); len(got) != 0 {
		t.Fatalf("oversized packet accepted: %+v", got)
	}
	if got := d.Feed(Encode(nil, Blank, 1, nil)); len(got) != 1 {
		t.Fatalf("decoder did not recover: %+v", got)
	}
}

func TestParseStatus(t *testing.T) {
	s, ok := ParseStatus([]byte{0x2b, 0x00, 0x01, 0x00, 0x02, 0x00, 43, 0xff})
	if !ok || s != (StatusMsg{FramesOK: 43, CRCErr: 1, SeqGaps: 2, FPS: 43, TempC: 0xff}) {
		t.Fatalf("status = %+v %v", s, ok)
	}
	if _, ok := ParseStatus([]byte{1, 2, 3}); ok {
		t.Fatal("short status accepted")
	}
}

func TestFramePayloadRGB565(t *testing.T) {
	frame := [][]color.RGBA{{{R: 255, A: 255}, {G: 255}}, {{B: 255}, {R: 255, G: 255, B: 255}}}
	want := []byte{0, 0, 0x00, 0xf8, 0xe0, 0x07, 0x1f, 0x00, 0xff, 0xff}
	if got := FramePayload(nil, frame); !bytes.Equal(got, want) {
		t.Fatalf("payload = % x", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd visualizer && go test ./proto/`
Expected: build failure, `undefined: CRC16` etc.

- [ ] **Step 3: Implement**

```go
// Package proto is the packet protocol between the Pi and the ESP32 that
// drives the panel, see docs/superpowers/specs/2026-09-18-panel-streaming-design.md.
//
//	A5 5A | type | seq | len u16 | payload | crc u16
//
// Little-endian; the CRC is CRC-16/CCITT-FALSE over type..payload.
package proto

import (
	"encoding/binary"
	"image/color"
)

const (
	Hello  byte = 0x01 // Pi→ESP, empty; the ESP answers Info
	Info   byte = 0x02 // ESP→Pi, see InfoMsg
	Frame  byte = 0x03 // Pi→ESP, see FramePayload
	Config byte = 0x04 // Pi→ESP, see ConfigPayload
	Status byte = 0x05 // ESP→Pi, once a second, see StatusMsg
	Blank  byte = 0x06 // Pi→ESP, empty; clears the panel
)

// CRC16 is CRC-16/CCITT-FALSE: poly 0x1021, init 0xFFFF.
func CRC16(p []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range p {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// Encode appends one packet to dst.
func Encode(dst []byte, typ, seq byte, payload []byte) []byte {
	start := len(dst)
	dst = append(dst, 0xA5, 0x5A, typ, seq)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(payload)))
	dst = append(dst, payload...)
	return binary.LittleEndian.AppendUint16(dst, CRC16(dst[start+2:]))
}

type Packet struct {
	Type, Seq byte
	Payload   []byte
}

// Decoder finds packets in a byte stream. Anything that is not a valid packet
// (boot messages, a corrupt packet) is skipped.
type Decoder struct {
	Max       int // longest payload accepted, 0 = any
	CRCErrors int

	magic int    // magic bytes seen, 0..2
	buf   []byte // type, seq, len, payload, crc
}

// Feed consumes p and returns the packets completed by it.
func (d *Decoder) Feed(p []byte) []Packet {
	var out []Packet
	for _, b := range p {
		switch {
		case d.magic == 0:
			if b == 0xA5 {
				d.magic = 1
			}
			continue
		case d.magic == 1:
			switch b {
			case 0x5A:
				d.magic, d.buf = 2, d.buf[:0]
			case 0xA5:
			default:
				d.magic = 0
			}
			continue
		}
		d.buf = append(d.buf, b)
		if len(d.buf) < 4 {
			continue
		}
		n := int(binary.LittleEndian.Uint16(d.buf[2:]))
		if d.Max > 0 && n > d.Max {
			d.magic = 0
			continue
		}
		if len(d.buf) < 4+n+2 {
			continue
		}
		if CRC16(d.buf[:4+n]) == binary.LittleEndian.Uint16(d.buf[4+n:]) {
			out = append(out, Packet{d.buf[0], d.buf[1], append([]byte(nil), d.buf[4:4+n]...)})
		} else {
			d.CRCErrors++
		}
		d.magic = 0
	}
	return out
}

// InfoMsg is what the ESP reports about itself.
type InfoMsg struct {
	W, H    uint16 // logical canvas
	PixFmts byte   // bit 0 = RGB565
	MaxFPS  byte
	FW      uint16
}

func ParseInfo(p []byte) (InfoMsg, bool) {
	if len(p) < 8 {
		return InfoMsg{}, false
	}
	le := binary.LittleEndian
	return InfoMsg{le.Uint16(p), le.Uint16(p[2:]), p[4], p[5], le.Uint16(p[6:])}, true
}

// StatusMsg holds the ESP's counters; they wrap at 65535.
type StatusMsg struct {
	FramesOK, CRCErr, SeqGaps uint16
	FPS                       byte
	TempC                     byte // 0xFF = no sensor
}

func ParseStatus(p []byte) (StatusMsg, bool) {
	if len(p) < 8 {
		return StatusMsg{}, false
	}
	le := binary.LittleEndian
	return StatusMsg{le.Uint16(p), le.Uint16(p[2:]), le.Uint16(p[4:]), p[6], p[7]}, true
}

// ConfigPayload sets the brightness; gamma stays 2.2 and rotation 0.
func ConfigPayload(brightness byte) []byte { return []byte{brightness, 22, 0} }

// FramePayload appends a Frame payload: pixfmt RGB565, no flags, then the
// pixels row by row. Alpha is ignored, like in the terminal view.
func FramePayload(dst []byte, frame [][]color.RGBA) []byte {
	dst = append(dst, 0, 0)
	for _, row := range frame {
		for _, c := range row {
			dst = binary.LittleEndian.AppendUint16(dst, uint16(c.R>>3)<<11|uint16(c.G>>2)<<5|uint16(c.B>>3))
		}
	}
	return dst
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd visualizer && go test ./proto/ && go vet ./proto/`
Expected: `ok`

---

### Task 2: `serial` package

**Files:**
- Create: `visualizer/serial/serial.go`
- Test: `visualizer/serial/serial_test.go`
- Modify: `visualizer/go.mod` (via `go mod tidy`)

**Interfaces:**
- Consumes: everything from Task 1.
- Produces:
  - `func Open(path string, baud int, brightness byte) (*Port, proto.InfoMsg, error)` — blocks until the ESP answered INFO (or ~6 s passed)
  - `func (p *Port) Send(frame [][]color.RGBA)` — never blocks; replaces a pending frame
  - `func (p *Port) Status() proto.StatusMsg` — latest STATUS
  - `func (p *Port) Close()` — sends BLANK, closes the port, waits for the goroutines

- [ ] **Step 1: Write the failing test**

A pty stands in for the USB port; the test plays the ESP on the master side.

```go
package serial

import (
	"fmt"
	"image/color"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jon4hz/loudest-office/visualizer/proto"
)

// pty returns the master and the path of its slave.
func pty(t *testing.T) (*os.File, string) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no pty:", err)
	}
	t.Cleanup(func() { m.Close() })
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	return m, fmt.Sprintf("/dev/pts/%d", n)
}

func TestHandshakeFrameBlank(t *testing.T) {
	bootWait = 0
	m, path := pty(t)
	pkts := make(chan proto.Packet, 16)
	go func() { // the fake ESP: answers HELLO, reports everything it gets
		var d proto.Decoder
		buf := make([]byte, 8192)
		for {
			n, err := m.Read(buf)
			if err != nil {
				close(pkts)
				return
			}
			for _, p := range d.Feed(buf[:n]) {
				if p.Type == proto.Hello {
					m.Write(proto.Encode(nil, proto.Info, 0, []byte{4, 0, 2, 0, 1, 60, 1, 0}))
					m.Write(proto.Encode(nil, proto.Status, 1, []byte{9, 0, 0, 0, 0, 0, 30, 0xff}))
				}
				pkts <- p
			}
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

	p, info, err := Open(path, 2000000, 64)
	if err != nil {
		t.Fatal(err)
	}
	if info.W != 4 || info.H != 2 {
		t.Fatalf("info = %+v", info)
	}
	next(proto.Hello)
	if c := next(proto.Config); c.Payload[0] != 64 {
		t.Fatalf("brightness = %d", c.Payload[0])
	}

	frame := [][]color.RGBA{make([]color.RGBA, 4), make([]color.RGBA, 4)}
	frame[1][3] = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	p.Send(frame)
	f := next(proto.Frame)
	if len(f.Payload) != 2+4*2*2 || f.Payload[len(f.Payload)-1] != 0xff {
		t.Fatalf("frame payload = % x", f.Payload)
	}

	for p.Status().FPS != 30 { // the reader goroutine picks the STATUS up
		time.Sleep(10 * time.Millisecond)
	}
	p.Close()
	next(proto.Blank)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd visualizer && go test ./serial/`
Expected: build failure, `undefined: Open`, `undefined: bootWait`.

- [ ] **Step 3: Implement**

```go
// Package serial streams frames to the ESP32 behind a USB serial port.
//
// ponytail: Linux only (termios2 for arbitrary baud rates), like the capture
// commands. go.bug.st/serial if this ever has to run elsewhere.
package serial

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jon4hz/loudest-office/visualizer/proto"
)

// bootWait covers the reboot that opening the port triggers on boards that
// wire DTR/RTS to reset.
var bootWait = time.Second

// Port is an open panel. Frames are sent by a writer goroutine that always
// takes the newest one; a lost port is reopened in the background.
type Port struct {
	path       string
	baud       int
	brightness byte

	frames chan []byte // depth 1: a pending frame is replaced, never queued
	quit   chan struct{}
	done   chan struct{}

	mu     sync.Mutex
	status proto.StatusMsg
}

// Open opens the port and waits for the ESP to introduce itself.
func Open(path string, baud int, brightness byte) (*Port, proto.InfoMsg, error) {
	p := &Port{path: path, baud: baud, brightness: brightness,
		frames: make(chan []byte, 1), quit: make(chan struct{}), done: make(chan struct{})}
	f, info, err := p.connect()
	if err != nil {
		return nil, info, err
	}
	go p.run(f)
	return p, info, nil
}

// Send queues frame for the panel, replacing one that is still waiting.
func (p *Port) Send(frame [][]color.RGBA) {
	b := proto.FramePayload(nil, frame)
	select {
	case <-p.frames:
	default:
	}
	select {
	case p.frames <- b:
	default:
	}
}

// Status returns the last STATUS the ESP sent.
func (p *Port) Status() proto.StatusMsg {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Close blanks the panel and releases the port.
func (p *Port) Close() {
	close(p.quit)
	<-p.done
}

// openRaw opens path as a raw 8N1 line at baud.
func openRaw(path string, baud int) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS2)
	if err == nil {
		t.Iflag, t.Oflag, t.Lflag = 0, 0, 0
		t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL | unix.BOTHER
		t.Ispeed, t.Ospeed = uint32(baud), uint32(baud)
		t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
		err = unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS2, t)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// connect opens the port, says HELLO until INFO comes back and sends CONFIG.
func (p *Port) connect() (*os.File, proto.InfoMsg, error) {
	f, err := openRaw(p.path, p.baud)
	if err != nil {
		return nil, proto.InfoMsg{}, err
	}
	time.Sleep(bootWait)
	d := proto.Decoder{Max: 64}
	buf := make([]byte, 256)
	for range 10 {
		if _, err := f.Write(proto.Encode(nil, proto.Hello, 0, nil)); err != nil {
			f.Close()
			return nil, proto.InfoMsg{}, err
		}
		f.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := f.Read(buf)
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			f.Close()
			return nil, proto.InfoMsg{}, err
		}
		for _, pkt := range d.Feed(buf[:n]) {
			if info, ok := proto.ParseInfo(pkt.Payload); ok && pkt.Type == proto.Info {
				f.SetReadDeadline(time.Time{})
				_, err := f.Write(proto.Encode(nil, proto.Config, 1, proto.ConfigPayload(p.brightness)))
				if err != nil {
					f.Close()
					return nil, info, err
				}
				return f, info, nil
			}
		}
	}
	f.Close()
	return nil, proto.InfoMsg{}, fmt.Errorf("%s: no INFO from the panel", p.path)
}

// read keeps the latest STATUS until the port fails.
func (p *Port) read(f *os.File, errc chan<- error) {
	d := proto.Decoder{Max: 64}
	buf := make([]byte, 256)
	for {
		n, err := f.Read(buf)
		if err != nil {
			errc <- err
			return
		}
		for _, pkt := range d.Feed(buf[:n]) {
			if s, ok := proto.ParseStatus(pkt.Payload); ok && pkt.Type == proto.Status {
				p.mu.Lock()
				p.status = s
				p.mu.Unlock()
			}
		}
	}
}

func (p *Port) run(f *os.File) {
	defer close(p.done)
	seq := byte(2) // HELLO and CONFIG took 0 and 1
	var pkt []byte
	for {
		errc := make(chan error, 1)
		go p.read(f, errc)
		var err error
		for err == nil {
			select {
			case b := <-p.frames:
				pkt = proto.Encode(pkt[:0], proto.Frame, seq, b)
				seq++
				_, err = f.Write(pkt)
			case err = <-errc:
			case <-p.quit:
				f.Write(proto.Encode(nil, proto.Blank, seq, nil))
				f.Close()
				return
			}
		}
		f.Close() // also ends the reader
		// ponytail: a panel that comes back with another size keeps the old
		// frame size; restart the visualizer after swapping panels.
		for f = nil; f == nil; {
			select {
			case <-p.quit:
				return
			case <-time.After(time.Second):
			}
			f, _, _ = p.connect()
		}
		seq = 2
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd visualizer && go mod tidy && go test -race ./serial/ && go vet ./serial/`
Expected: `ok`. `go.mod` now lists `golang.org/x/sys` as a direct requirement.

---

### Task 3: `spectrum --serial`

**Files:**
- Modify: `visualizer/cmd/spectrum/main.go`

**Interfaces:**
- Consumes: `serial.Open`, `(*serial.Port).Send/Status/Close`, `spectrum.Size(w, h)`, `Model.Frame()`.

- [ ] **Step 1: Add the panel to the app**

In the file header comment add after the `-trails` line:

```go
//	spectrum -serial /dev/ttyUSB0  # also stream to the HUB75 panel behind an ESP32
```

Import `"github.com/jon4hz/loudest-office/visualizer/serial"`. Add the field `panel *serial.Port // nil = terminal only` to `app` and send after each block:

```go
	case spectrum.SamplesMsg:
		a.spec, _ = a.spec.Update(msg)
		if a.panel != nil {
			a.panel.Send(a.spec.Frame())
		}
		return a, a.wait()
```

- [ ] **Step 2: Flags**

```go
// panelCfg is where the HUB75 panel lives; port "" = no panel.
type panelCfg struct {
	port       string
	baud       int
	brightness uint8
}
```

In `main`: `var panel panelCfg`, pass it as the last argument of `run`, and:

```go
	f.StringVar(&panel.port, "serial", "", "serial port of the ESP32 driving the HUB75 panel, e.g. /dev/ttyUSB0 (the frame then has the panel's size)")
	f.IntVar(&panel.baud, "baud", 2000000, "serial baud rate, must match the firmware")
	f.Uint8Var(&panel.brightness, "brightness", 64, "panel brightness, 0-255")
```

- [ ] **Step 3: Open the port in `run`, go headless without a terminal**

Before `audio.Start`:

```go
	opts := []spectrum.Option{
		spectrum.Bands(bands), spectrum.Channels(cfg.Channels), spectrum.Rate(cfg.Rate), spectrum.Gain(gain),
		spectrum.AutoGain(autoGain), spectrum.WithPalette(spectrum.Palettes[pal]), spectrum.WithLayout(layout),
		spectrum.WithPeakStyle(peaks), spectrum.WithMode(mode), spectrum.WithTrails(trails)}
	var port *serial.Port
	if panel.port != "" {
		p, info, err := serial.Open(panel.port, panel.baud, panel.brightness)
		if err != nil {
			return err
		}
		defer func() {
			p.Close()
			fmt.Fprintf(os.Stderr, "panel: %+v\n", p.Status())
		}()
		port = p
		opts = append(opts, spectrum.Size(int(info.W), int(info.H)))
	}
```

(`cfg.Channels` is set above this block; move the block below the `cfg.Channels` assignment.) Build the app with `panel: port, spec: spectrum.New(opts...)`, and run the program with:

```go
	popts := []tea.ProgramOption{tea.WithContext(ctx)}
	if st, err := os.Stdout.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		popts = append(popts, tea.WithoutRenderer(), tea.WithInput(nil)) // no terminal, e.g. under systemd
	}
	final, err := tea.NewProgram(a, popts...).Run()
```

- [ ] **Step 4: Verify**

Run: `cd visualizer && go build ./... && go vet ./... && go test ./...`
Expected: all `ok`.

Run: `cd visualizer && go run ./cmd/spectrum --serial /dev/null`
Expected: exits nonzero with an error mentioning `/dev/null` (not a tty), before any audio capture starts.

---

### Task 4: Firmware

**Files:**
- Create: `firmware/platformio.ini`, `firmware/src/proto.h`, `firmware/src/main.cpp`, `firmware/test/parser_test.cpp`
- Modify: `.gitignore` (add `firmware/.pio/` and `firmware/fw-*.bin`)

**Interfaces:**
- Consumes: the packet layout and golden packets from Global Constraints.
- Produces: `pio run -e esp32dev` / `-e esp32s3` build outputs under `firmware/.pio/build/<env>/`.

- [ ] **Step 1: Write the failing parser test** (`firmware/test/parser_test.cpp`)

```cpp
// Host check of the packet parser: g++ -std=c++17 -o parser_test parser_test.cpp && ./parser_test
#include <cassert>
#include <cstdio>
#include "../src/proto.h"

static int feed(Parser &p, const uint8_t *b, size_t n) {
  int packets = 0;
  for (size_t i = 0; i < n; i++) packets += p.feed(b[i]);
  return packets;
}

int main() {
  static Parser p;
  const uint8_t hello[] = {0xa5, 0x5a, 0x01, 0x00, 0x00, 0x00, 0x74, 0xf2};
  const uint8_t config[] = {0xa5, 0x5a, 0x04, 0x07, 0x03, 0x00, 0x40, 0x16, 0x00, 0xe3, 0xa2};
  const uint8_t garbage[] = {0x00, 0xa5, 0xa5, 0x13};

  assert(feed(p, garbage, sizeof garbage) == 0);
  assert(feed(p, hello, sizeof hello) == 1 && p.type == HELLO && p.len == 0);
  assert(feed(p, config, sizeof config) == 1);
  assert(p.type == CONFIG && p.seq == 7 && p.len == 3 && p.payload[0] == 64);

  uint8_t bad[sizeof config];
  for (size_t i = 0; i < sizeof bad; i++) bad[i] = config[i];
  bad[6] ^= 0xff;
  assert(feed(p, bad, sizeof bad) == 0 && p.crcErr == 1);

  const uint8_t oversized[] = {0xa5, 0x5a, 0x03, 0x00, 0xff, 0xff}; // len 65535 > MAX
  assert(feed(p, oversized, sizeof oversized) == 0);
  assert(feed(p, hello, sizeof hello) == 1); // recovered

  puts("ok");
}
```

Run: `cd firmware/test && g++ -std=c++17 -o /tmp/parser_test parser_test.cpp`
Expected: FAIL, `../src/proto.h: No such file or directory`.

- [ ] **Step 2: Parser** (`firmware/src/proto.h`)

```cpp
// Packet parser of the Pi <-> ESP32 panel protocol. No Arduino includes, so it
// also compiles on the host (see test/parser_test.cpp).
//
//   A5 5A | type | seq | len u16 | payload | crc u16     little-endian,
//   crc = CRC-16/CCITT-FALSE over type..payload
#pragma once
#include <stddef.h>
#include <stdint.h>

#ifndef PANEL_W
#define PANEL_W 64
#endif
#ifndef PANEL_H
#define PANEL_H 32
#endif

enum : uint8_t { HELLO = 1, INFO, FRAME, CONFIG, STATUS, BLANK };

inline uint16_t crc16(uint16_t crc, uint8_t b) {
  crc ^= (uint16_t)b << 8;
  for (int i = 0; i < 8; i++) crc = crc & 0x8000 ? (crc << 1) ^ 0x1021 : crc << 1;
  return crc;
}

struct Parser {
  static const size_t MAX = 2 + PANEL_W * PANEL_H * 2; // a FRAME payload

  uint8_t type, seq;
  uint16_t len;
  uint8_t payload[MAX];
  uint32_t crcErr = 0;

  // feed returns true when b completed a valid packet; type, seq, len and
  // payload then describe it until the next call.
  bool feed(uint8_t b) {
    switch (state) {
    case MAGIC1:
      if (b == 0xA5) state = MAGIC2;
      return false;
    case MAGIC2:
      state = b == 0x5A ? HEADER : b == 0xA5 ? MAGIC2 : MAGIC1;
      n = 0;
      crc = 0xFFFF;
      return false;
    case HEADER:
      crc = crc16(crc, b);
      hdr[n++] = b;
      if (n < 4) return false;
      type = hdr[0], seq = hdr[1], len = hdr[2] | hdr[3] << 8;
      n = 0;
      state = len > MAX ? MAGIC1 : len ? PAYLOAD : CRC;
      return false;
    case PAYLOAD:
      crc = crc16(crc, b);
      payload[n++] = b;
      if (n == len) n = 0, state = CRC;
      return false;
    case CRC:
      hdr[n++] = b;
      if (n < 2) return false;
      state = MAGIC1;
      if ((hdr[0] | hdr[1] << 8) == crc) return true;
      crcErr++;
      return false;
    }
    return false;
  }

private:
  enum { MAGIC1, MAGIC2, HEADER, PAYLOAD, CRC } state = MAGIC1;
  uint8_t hdr[4];
  size_t n = 0;
  uint16_t crc = 0;
};
```

Run: `cd firmware/test && g++ -std=c++17 -Wall -o /tmp/parser_test parser_test.cpp && /tmp/parser_test`
Expected: `ok`

- [ ] **Step 3: `firmware/platformio.ini`**

```ini
; ESP32 firmware for the HUB75 panel, see ../README.md
[platformio]
default_envs = esp32dev

[env]
platform = espressif32
framework = arduino
lib_deps = mrfaptastic/ESP32 HUB75 LED MATRIX PANEL DMA Display@^3.0.12
; SERIAL_BAUD must match `spectrum --baud`; old CP2102 bridges top out at 921600
build_flags = -DNO_GFX -DSERIAL_BAUD=2000000
monitor_speed = 2000000

[env:esp32dev]
board = esp32dev
upload_speed = 921600

; native USB CDC, the baud rate is irrelevant
[env:esp32s3]
board = esp32-s3-devkitc-1
build_flags = ${env.build_flags} -DARDUINO_USB_MODE=1 -DARDUINO_USB_CDC_ON_BOOT=1
```

- [ ] **Step 4: `firmware/src/main.cpp`**

```cpp
// HUB75 panel driver: shows the FRAMEs the Pi streams over USB serial.
#include <Arduino.h>
#include <ESP32-HUB75-MatrixPanel-I2S-DMA.h>

#include "proto.h"

#ifndef SERIAL_BAUD
#define SERIAL_BAUD 2000000
#endif

static const uint16_t FW_VERSION = 1;
static const uint32_t NO_SIGNAL_MS = 2000;

static MatrixPanel_I2S_DMA *panel;
static Parser parser;
static uint16_t framesOk, seqGaps;
static uint8_t lastSeq, fps;
static uint32_t lastFrame, lastStatus;
static bool live; // a FRAME is on the panel

static void send(uint8_t type, const uint8_t *p, uint16_t n) {
  static uint8_t seq;
  const uint8_t h[6] = {0xA5, 0x5A, type, seq++, (uint8_t)n, (uint8_t)(n >> 8)};
  uint16_t crc = 0xFFFF;
  for (int i = 2; i < 6; i++) crc = crc16(crc, h[i]);
  for (int i = 0; i < n; i++) crc = crc16(crc, p[i]);
  const uint8_t t[2] = {(uint8_t)crc, (uint8_t)(crc >> 8)};
  Serial.write(h, 6);
  Serial.write(p, n);
  Serial.write(t, 2);
}

// Red, green and blue bars: wrong colours or a garbled picture mean wrong wiring.
static void bootPattern() {
  for (int y = 0; y < PANEL_H; y++)
    for (int x = 0; x < PANEL_W; x++) {
      int bar = x * 3 / PANEL_W;
      panel->drawPixelRGB888(x, y, bar == 0 ? 255 : 0, bar == 1 ? 255 : 0, bar == 2 ? 255 : 0);
    }
  panel->flipDMABuffer();
}

// A small red cross in the corner.
static void noSignal() {
  panel->clearScreen();
  for (int i = 0; i < 5; i++) {
    panel->drawPixelRGB888(1 + i, 1 + i, 96, 0, 0);
    panel->drawPixelRGB888(5 - i, 1 + i, 96, 0, 0);
  }
  panel->flipDMABuffer();
}

static void handle() {
  if (parser.type != HELLO) seqGaps += (uint8_t)(parser.seq - lastSeq - 1);
  lastSeq = parser.seq;

  switch (parser.type) {
  case HELLO: {
    const uint8_t info[8] = {PANEL_W & 0xff, PANEL_W >> 8, PANEL_H & 0xff, PANEL_H >> 8,
                             1 /* RGB565 */, 60, FW_VERSION & 0xff, FW_VERSION >> 8};
    send(INFO, info, sizeof info);
    break;
  }
  case FRAME: {
    if (parser.len != Parser::MAX || parser.payload[0] != 0) break;
    const uint8_t *px = parser.payload + 2;
    for (int y = 0; y < PANEL_H; y++)
      for (int x = 0; x < PANEL_W; x++, px += 2) panel->drawPixel(x, y, (uint16_t)(px[0] | px[1] << 8));
    panel->flipDMABuffer();
    framesOk++, fps++;
    lastFrame = millis();
    live = true;
    break;
  }
  case CONFIG: // gamma and rotation are not used yet
    if (parser.len >= 1) panel->setBrightness8(parser.payload[0]);
    break;
  case BLANK:
    panel->clearScreen();
    panel->flipDMABuffer();
    live = false;
    break;
  }
}

void setup() {
  Serial.setRxBufferSize(16384); // a bit more than two frames
  Serial.begin(SERIAL_BAUD);

  HUB75_I2S_CFG cfg(PANEL_W, PANEL_H, 1);
  cfg.double_buff = true;
  panel = new MatrixPanel_I2S_DMA(cfg);
  panel->begin();
  panel->setBrightness8(64);
  bootPattern();
}

void loop() {
  static uint8_t buf[512];
  int n = Serial.available();
  if (n > 0) {
    n = Serial.read(buf, min(n, (int)sizeof buf));
    for (int i = 0; i < n; i++)
      if (parser.feed(buf[i])) handle();
  }

  uint32_t now = millis();
  if (live && now - lastFrame > NO_SIGNAL_MS) {
    noSignal();
    live = false;
  }
  if (now - lastStatus >= 1000) {
    const uint16_t crcErr = parser.crcErr;
    const uint8_t status[8] = {(uint8_t)framesOk, (uint8_t)(framesOk >> 8), (uint8_t)crcErr, (uint8_t)(crcErr >> 8),
                               (uint8_t)seqGaps, (uint8_t)(seqGaps >> 8), fps, 0xFF};
    send(STATUS, status, sizeof status);
    fps = 0;
    lastStatus = now;
  }
}
```

- [ ] **Step 5: Build both envs**

Run: `cd firmware && pio run`  (default env) and `pio run -e esp32s3`
Expected: `SUCCESS` twice. If the registry name or version of `lib_deps` does not resolve, use what `pio pkg search "HUB75 DMA"` reports and keep it the only entry.

- [ ] **Step 6: `.gitignore`**

Append:

```
firmware/.pio/
firmware/fw-*.bin
```

---

### Task 5: Taskfile, README, flash

**Files:**
- Modify: `Taskfile.yml`, `README.md`

- [ ] **Step 1: Taskfile tasks** (append under `tasks:`)

```yaml
  firmware:test:
    desc: Check the firmware's packet parser on the host
    dir: firmware/test
    cmds:
      - g++ -std=c++17 -Wall -o {{.TMP}}/parser_test parser_test.cpp
      - "{{.TMP}}/parser_test"
    vars:
      TMP:
        sh: mktemp -d

  firmware:flash:
    desc: Build and flash the ESP32 (ENV=esp32dev|esp32s3, extra args via CLI_ARGS)
    dir: firmware
    cmds:
      - pio run -e {{.ENV | default "esp32dev"}} -t upload {{.CLI_ARGS}}

  firmware:bin:
    desc: Build one merged image to flash with plain esptool, e.g. from the Pi
    dir: firmware
    vars:
      ENV: '{{.ENV | default "esp32dev"}}'
      CHIP: '{{if eq .ENV "esp32s3"}}esp32s3{{else}}esp32{{end}}'
      BOOT: '{{if eq .ENV "esp32s3"}}0x0{{else}}0x1000{{end}}'
    cmds:
      - pio run -e {{.ENV}}
      - >-
        pio pkg exec -p tool-esptoolpy -- esptool.py --chip {{.CHIP}} merge_bin -o fw-{{.ENV}}.bin
        {{.BOOT}} .pio/build/{{.ENV}}/bootloader.bin
        0x8000 .pio/build/{{.ENV}}/partitions.bin
        0xe000 ~/.platformio/packages/framework-arduinoespressif32/tools/partitions/boot_app0.bin
        0x10000 .pio/build/{{.ENV}}/firmware.bin
```

Run: `task firmware:test && task firmware:bin`
Expected: `ok`, then `firmware/fw-esp32dev.bin` exists.

- [ ] **Step 2: README** — replace the sentence "`visualizer/` holds the Go spectrum analyzer that will eventually feed the matrix panel." with "`visualizer/` holds the Go spectrum analyzer; with `--serial` it also feeds the matrix panel." and add after the Visualizer section:

````markdown
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
(Debian) group. Bridges that cannot do 2 Mbaud (old CP2102): set
`-DSERIAL_BAUD=921600` in `firmware/platformio.ini` and pass `--baud 921600`,
which still gives about 22 fps. On exit the visualizer prints the ESP's
counters; `CRCErr` and `SeqGaps` should stay 0.
````

- [ ] **Step 3: Flash and verify on hardware** (needs the board plugged in)

Run: `pio device list` — identify the port and bridge; `pio pkg exec -p tool-esptoolpy -- esptool.py --port <port> chip_id` — classic ESP32 or S3.
Run: `task firmware:flash` (or `ENV=esp32s3`)
Expected: upload succeeds, the panel shows the RGB bars.
Run: `cd visualizer && go run ./cmd/spectrum --serial <port>` with music playing.
Expected: the terminal preview and the panel show the same 64x32 picture; on `q` the printed status has `CRCErr:0 SeqGaps:0` and `FPS` near 43 (near 22 at 921600 baud). After quitting, the panel is blank.

---

## Deviations found during execution

The code in the repo is authoritative where it differs from the blocks above.

- **Task 2, `openRaw`:** the termios ioctls go through `f.SyscallConn().Control`, not `f.Fd()`. `Fd()` puts the file into blocking mode, after which `SetReadDeadline` returns nil but never fires and `Close` does not interrupt a `Read`; `Open` would hang forever when the first HELLO is lost to the ESP's reboot.
- **Task 2, test:** the fake ESP ignores the first HELLO (guards the deadline path above) and answers each FRAME with a STATUS instead of sending one STATUS next to INFO, which `connect` could swallow.
- **Task 3, headless check:** `term.IsTerminal(os.Stdout.Fd())` from `github.com/charmbracelet/x/term` (already in the module graph) instead of `ModeCharDevice`: `/dev/null` is a character device too, so `spectrum > /dev/null` failed with "could not open TTY".
- **Taskfile:** the binary is `go-task` on Arch, `task` elsewhere.
- **Firmware enum:** `MSG_HELLO` … `MSG_BLANK`; `STATUS` is a typedef in the ESP32 ROM headers.
- **Baud default 921600** (firmware, `--baud`, README, spec): the board has a classic CP2102 (`10c4:ea60`, bcdDevice 1.00). Measured: 20 fps, 0 CRC errors, 0 seq gaps.
- **Task 2, `connect`:** a CP2102 whose RX buffer overflowed while the port was closed (one STATUS per second, ~40 s) replays stale bytes at ~480 KB/s after open until the host writes. `connect` now writes a HELLO, waits `settle` (50 ms), flushes input (`TCFLSH`), and only then runs the handshake, which is bounded by time (5 s, HELLO every 500 ms) instead of by 10 reads.
- **Firmware knobs:** `-DPANEL_DRIVER=…` and `-DPANEL_CLKPHASE=…` build flags.
