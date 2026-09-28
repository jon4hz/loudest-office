package main

import (
	"reflect"
	"testing"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	"github.com/Sendspin/sendspin-go/pkg/sendspin"
)

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

// TestClientOf fails when the library's layout moves: then artworkDrain drains nothing.
func TestClientOf(t *testing.T) {
	p, err := sendspin.NewPlayer(sendspin.PlayerConfig{ServerAddr: "127.0.0.1:1", ClientID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if clientOf(p) != nil {
		t.Fatal("a client before any connection")
	}
	f, ok := reflect.TypeOf(p).Elem().FieldByName("receiver")
	if !ok {
		t.Fatal("Player has no field receiver")
	}
	c, ok := f.Type.Elem().FieldByName("client")
	if !ok || c.Type != reflect.TypeOf((*protocol.Client)(nil)) {
		t.Fatalf("Receiver.client is %v, want *protocol.Client", c.Type)
	}
}
