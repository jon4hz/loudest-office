package main

import "testing"

func TestSyncWatch(t *testing.T) {
	exits := 0
	w := &syncWatch{exit: func() { exits++ }}
	fail, ok := []byte("Burst produced 0 valid samples; filter not updated\n"), []byte("Sync #7: rtt=143us\n")
	for _, p := range [][]byte{fail, fail, ok, fail, fail} {
		w.Write(p)
	}
	if exits != 0 {
		t.Fatal("exited although a good sync came in between")
	}
	if w.Write(fail); exits != 1 {
		t.Fatalf("exits = %d after three failed rounds in a row, want 1", exits)
	}
}
