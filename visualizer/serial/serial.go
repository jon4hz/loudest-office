// Package serial streams frames to the ESP32 behind a USB serial port or a TCP socket.
//
// ponytail: Linux only (termios2 for arbitrary baud rates), like the capture
// commands. go.bug.st/serial if this ever has to run elsewhere.
package serial

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jon4hz/loudest-office/visualizer/proto"
)

// bootWait covers the reboot that opening the port triggers on boards that
// wire DTR/RTS to reset.
var bootWait = time.Second

// settle is how long stale input gets to arrive before it is flushed.
var settle = 50 * time.Millisecond

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

// Port is an open panel. Frames are sent by a writer goroutine that always
// takes the newest one; a lost port is reopened in the background.
type Port struct {
	path       string
	baud       int
	brightness byte

	frames chan []byte // depth 1: a pending frame is replaced, never queued
	bright chan byte   // depth 1: same replace-pending pattern as frames
	quit   chan struct{}
	done   chan struct{}

	mu     sync.Mutex
	status proto.StatusMsg
}

// Open opens the port and waits for the ESP to introduce itself.
func Open(path string, baud int, brightness byte) (*Port, proto.InfoMsg, error) {
	p := &Port{path: path, baud: baud, brightness: brightness,
		frames: make(chan []byte, 1), bright: make(chan byte, 1),
		quit: make(chan struct{}), done: make(chan struct{})}
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

// SetBrightness queues a new brightness for the panel, replacing one that is
// still waiting. It never blocks the caller.
func (p *Port) SetBrightness(b byte) {
	select {
	case <-p.bright:
	default:
	}
	select {
	case p.bright <- b:
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
	// Not f.Fd(): that puts the file into blocking mode, and then neither read
	// deadlines nor Close interrupt a Read.
	rc, err := f.SyscallConn()
	if err == nil {
		var ioErr error
		err = rc.Control(func(fd uintptr) {
			var t *unix.Termios
			if t, ioErr = unix.IoctlGetTermios(int(fd), unix.TCGETS2); ioErr != nil {
				return
			}
			t.Iflag, t.Oflag, t.Lflag = 0, 0, 0
			t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL | unix.BOTHER
			t.Ispeed, t.Ospeed = uint32(baud), uint32(baud)
			t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
			ioErr = unix.IoctlSetTermios(int(fd), unix.TCSETS2, t)
		})
		err = errors.Join(err, ioErr)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// connect opens the port, says HELLO until INFO comes back and sends CONFIG.
func (p *Port) connect() (f conn, info proto.InfoMsg, err error) {
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

	d := proto.Decoder{Max: 64}
	buf := make([]byte, 4096)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		if err = write(f, hello); err != nil {
			return
		}
		f.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		for { // until the deadline: a chatty line must not eat the retries
			n, rerr := f.Read(buf)
			if errors.Is(rerr, os.ErrDeadlineExceeded) {
				break
			}
			if rerr != nil {
				return f, info, rerr
			}
			for _, pkt := range d.Feed(buf[:n]) {
				if pkt.Type != proto.Info {
					continue
				}
				var ok bool
				if info, ok = proto.ParseInfo(pkt.Payload); ok {
					f.SetReadDeadline(time.Time{})
					p.mu.Lock()
					b := p.brightness
					p.mu.Unlock()
					err = write(f, proto.Encode(nil, proto.Config, 1, proto.ConfigPayload(b)))
					return
				}
			}
		}
	}
	return f, info, fmt.Errorf("%s: no INFO from the panel", p.path)
}

// flushInput drops what the kernel has received but nobody read yet.
func flushInput(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioErr error
	err = rc.Control(func(fd uintptr) { ioErr = unix.IoctlSetInt(int(fd), unix.TCFLSH, unix.TCIFLUSH) })
	return errors.Join(err, ioErr)
}

// read keeps the latest STATUS until the port fails.
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

func (p *Port) run(f conn) {
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
				err = write(f, pkt)
			case v := <-p.bright:
				p.mu.Lock()
				p.brightness = v
				p.mu.Unlock()
				pkt = proto.Encode(pkt[:0], proto.Config, seq, proto.ConfigPayload(v))
				seq++
				err = write(f, pkt)
			case err = <-errc:
			case <-p.quit:
				write(f, proto.Encode(nil, proto.Blank, seq, nil))
				f.Close()
				<-errc // wait for the reader to notice and stop touching p.status/statusTimeout
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
			case <-time.After(retry):
			}
			f, _, _ = p.connect()
		}
		seq = 2
	}
}
