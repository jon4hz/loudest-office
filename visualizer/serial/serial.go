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

// settle is how long stale input gets to arrive before it is flushed.
var settle = 50 * time.Millisecond

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
func (p *Port) connect() (f *os.File, info proto.InfoMsg, err error) {
	if f, err = openRaw(p.path, p.baud); err != nil {
		return nil, info, err
	}
	defer func() {
		if err != nil {
			f.Close()
			f = nil
		}
	}()
	time.Sleep(bootWait)
	hello := proto.Encode(nil, proto.Hello, 0, nil)

	// A CP2102 whose receive buffer overflowed while the port was closed (the
	// ESP sends a STATUS every second) replays stale bytes at full USB speed
	// until the host writes something. Write, let the junk land, drop it.
	if _, err = f.Write(hello); err != nil {
		return
	}
	time.Sleep(settle)
	if err = flushInput(f); err != nil {
		return
	}

	d := proto.Decoder{Max: 64}
	buf := make([]byte, 4096)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		if _, err = f.Write(hello); err != nil {
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
				var ok bool
				if info, ok = proto.ParseInfo(pkt.Payload); ok && pkt.Type == proto.Info {
					f.SetReadDeadline(time.Time{})
					_, err = f.Write(proto.Encode(nil, proto.Config, 1, proto.ConfigPayload(p.brightness)))
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
