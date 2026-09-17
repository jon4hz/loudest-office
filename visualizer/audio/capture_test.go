package audio

import (
	"context"
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
