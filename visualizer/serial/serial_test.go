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

// A pty stands in for the USB port; the test plays the ESP on the master side.
func TestHandshakeFrameBlank(t *testing.T) {
	bootWait, settle = 0, 0
	m, path := pty(t)
	pkts := make(chan proto.Packet, 16)
	go func() { // the fake ESP: reports everything it gets
		var d proto.Decoder
		buf := make([]byte, 8192)
		hellos := 0
		for {
			n, err := m.Read(buf)
			if err != nil {
				close(pkts)
				return
			}
			for _, p := range d.Feed(buf[:n]) {
				switch p.Type {
				case proto.Hello: // the first one is lost, as if the ESP were still booting
					if hellos++; hellos > 1 {
						m.Write(proto.Encode(nil, proto.Info, 0, []byte{4, 0, 2, 0, 1, 60, 1, 0}))
					}
				case proto.Frame:
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
