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
	maxFill  = time.Second // clamp a catch-up gap to this; older silence is dropped, not useful to a live reader
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
	if d > maxFill { // a stall this long (e.g. host suspend): drop the backlog, don't allocate for it
		o.last = now
		_, err := o.w.Write(make([]byte, Rate*Channels*2))
		return err
	}
	frames := int(d * Rate / time.Second)
	o.last = o.last.Add(time.Duration(frames) * time.Second / Rate) // not now: the remainder carries over
	_, err := o.w.Write(make([]byte, frames*Channels*2))
	return err
}

func (o *Output) Close() error  { return nil }
func (o *Output) SetVolume(int) {}
func (o *Output) SetMuted(bool) {}
