package audio

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestDeinterleave(t *testing.T) {
	// two frames, two channels, s16le: L=32767 R=-32768, L=0 R=16384
	b := []byte{0xff, 0x7f, 0x00, 0x80, 0x00, 0x00, 0x00, 0x40}
	got := Deinterleave(b, 2)
	if len(got) != 2 || len(got[0]) != 2 {
		t.Fatalf("shape: %v", got)
	}
	if got[0][0] < 0.999 || got[1][0] != -1 || got[0][1] != 0 || got[1][1] != 0.5 {
		t.Fatalf("values: %v", got)
	}
}

func TestStartReadsBlocks(t *testing.T) {
	// "cat" of /dev/zero stands in for parec: endless silence.
	c, err := Start(context.Background(), Config{Command: "cat", Args: []string{"/dev/zero"}, Channels: 2, Frames: 4})
	if err != nil {
		t.Fatal(err)
	}
	blk := <-c.Blocks()
	if len(blk) != 2 || len(blk[0]) != 4 || blk[0][0] != 0 {
		t.Fatalf("block: %v", blk)
	}
	c.Stop()
	for range c.Blocks() {
	}
	c.Wait() // must not hang
}

func TestStartMissingCommand(t *testing.T) {
	if _, err := Start(context.Background(), Config{Command: "definitely-not-a-binary"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestParecArgsRequestLowLatency(t *testing.T) {
	c := Config{}
	c.defaults()
	if !slices.Contains(c.Args, "--latency-msec=20") {
		t.Fatalf("parec args lack a latency hint (default fragments are ~380 ms): %v", c.Args)
	}
}

func TestTailWriterKeepsOnlyLastBytes(t *testing.T) {
	tw := &tailWriter{n: 4096}
	// write far more than the cap, in chunks, and check it never grows beyond it.
	chunk := bytes.Repeat([]byte("x"), 1000)
	for i := 0; i < 10; i++ {
		tw.Write(chunk)
		if len(tw.buf) > 4096 {
			t.Fatalf("grew beyond cap: %d bytes", len(tw.buf))
		}
	}
	if len(tw.buf) != 4096 {
		t.Fatalf("want exactly 4096 bytes kept, got %d", len(tw.buf))
	}
	// last write is "y"*50; the tail must end with it.
	tw.Write([]byte(strings.Repeat("y", 50)))
	if !strings.HasSuffix(string(tw.buf), strings.Repeat("y", 50)) {
		t.Fatalf("tail doesn't end with the most recent write: %q", tw.buf[len(tw.buf)-60:])
	}
}

func TestStartDeliversStderrToWriterAndError(t *testing.T) {
	var buf bytes.Buffer
	c, err := Start(context.Background(), Config{Command: "sh", Args: []string{"-c", "echo oops >&2; exit 3"}, Stderr: &buf})
	if err != nil {
		t.Fatal(err)
	}
	for range c.Blocks() {
	}
	werr := c.Wait()
	if werr == nil || !strings.Contains(werr.Error(), "oops") {
		t.Fatalf("Wait() error should contain child stderr: %v", werr)
	}
	if !strings.Contains(buf.String(), "oops") {
		t.Fatalf("stderr should also have been copied to the configured writer: %q", buf.String())
	}
}
