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

	now = now.Add(250 * time.Millisecond)
	o.Fill(now)
	if want := 340 * Rate / 1000 * frame; b.Len() != want { // 250ms here + the 90ms carried from before: exact, no drift
		t.Fatalf("%d bytes, want %d", b.Len(), want)
	}

	b.Reset()
	now = now.Add(time.Hour) // a long stall (e.g. host suspend): clamped, not one huge allocation
	o.Fill(now)
	if want := Rate * Channels * 2; b.Len() != want {
		t.Fatalf("%d bytes after a 1-hour stall, want exactly 1s of silence: %d", b.Len(), want)
	}

	b.Reset()
	now = now.Add(50 * time.Millisecond) // clamp reset the clock to now, so this is well within idle
	o.Fill(now)
	if b.Len() != 0 {
		t.Fatalf("%d bytes 50 ms after the clamped fill, want none yet", b.Len())
	}
}
