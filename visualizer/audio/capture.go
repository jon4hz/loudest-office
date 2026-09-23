// Package audio captures PCM blocks from a subprocess such as parec or arecord.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

// Config describes the capture subprocess. Zero fields take the defaults
// noted on each field.
type Config struct {
	Command  string    // "parec" (default) or "arecord"; anything else needs Args
	Args     []string  // overrides the generated arguments when set
	Device   string    // default "@DEFAULT_SINK@.monitor"
	Rate     int       // default 44100
	Channels int       // default 2
	Frames   int       // samples per channel per block, default 1024
	Stderr   io.Writer // where the command's stderr goes as well; nil = only kept for the exit error
}

func (c *Config) defaults() {
	if c.Command == "" {
		c.Command = "parec"
	}
	if c.Device == "" {
		c.Device = "@DEFAULT_SINK@.monitor"
	}
	if c.Rate == 0 {
		c.Rate = 44100
	}
	if c.Channels == 0 {
		c.Channels = 2
	}
	if c.Frames == 0 {
		c.Frames = 1024
	}
	if c.Args != nil {
		return
	}
	r, ch := strconv.Itoa(c.Rate), strconv.Itoa(c.Channels)
	switch c.Command {
	case "arecord":
		c.Args = []string{"-q", "-t", "raw", "-f", "S16_LE", "-r", r, "-c", ch, "-D", c.Device}
	default:
		// Without a latency hint pipewire-pulse hands parec ~380 ms fragments,
		// which makes the display step instead of flow.
		c.Args = []string{"--raw", "--format=s16le", "--rate=" + r, "--channels=" + ch, "--latency-msec=20", "-d", c.Device}
	}
}

// Capture is a running capture subprocess.
type Capture struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr tailWriter
	blocks chan [][]float32
	err    error
	done   chan struct{}
}

// tailWriter keeps only the last n bytes written, so a chatty long-running
// child (e.g. sendspin-pipe logging dropped chunks) can't grow it unbounded.
type tailWriter struct {
	buf []byte
	n   int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.n:]...)
	}
	return len(p), nil
}

const stderrTailBytes = 4096

// Start launches the subprocess and begins reading blocks.
func Start(ctx context.Context, cfg Config) (*Capture, error) {
	cfg.defaults()
	ctx, cancel := context.WithCancel(ctx)
	c := &Capture{cancel: cancel, blocks: make(chan [][]float32, 1), done: make(chan struct{})}
	c.stderr.n = stderrTailBytes
	c.cmd = exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if cfg.Stderr != nil {
		c.cmd.Stderr = io.MultiWriter(&c.stderr, cfg.Stderr)
	} else {
		c.cmd.Stderr = &c.stderr
	}
	out, err := c.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := c.cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go c.read(out, cfg.Channels, cfg.Frames)
	return c, nil
}

// read blocks on the consumer while it is slow; Stop plus draining Blocks
// unblocks it, which is what the CLI does on exit.
func (c *Capture) read(r io.Reader, channels, frames int) {
	defer close(c.done)
	defer close(c.blocks)
	buf := make([]byte, frames*channels*2)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		c.blocks <- Deinterleave(buf, channels)
	}
	if err := c.cmd.Wait(); err != nil {
		c.err = fmt.Errorf("%s: %w: %s", c.cmd.Path, err, bytes.TrimSpace(c.stderr.buf))
	}
}

// Blocks yields one slice per channel per block; it closes when the process exits.
func (c *Capture) Blocks() <-chan [][]float32 { return c.blocks }

// Stop kills the subprocess. Blocks closes shortly after.
func (c *Capture) Stop() { c.cancel() }

// Wait blocks until the reader has finished and returns the exit error, if any.
func (c *Capture) Wait() error {
	<-c.done
	return c.err
}

// Deinterleave converts interleaved s16le bytes into one -1..1 slice per channel.
func Deinterleave(b []byte, channels int) [][]float32 {
	frames := len(b) / 2 / channels
	out := make([][]float32, channels)
	for ch := range out {
		out[ch] = make([]float32, frames)
	}
	for i := 0; i < frames*channels; i++ {
		out[i%channels][i/channels] = float32(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768
	}
	return out
}
