// sendspin-pipe: a Sendspin player whose loudspeaker is stdout. Music Assistant
// sees a player; whoever reads the pipe gets S16LE, 44100 Hz, stereo, in step
// with the other players of the group, and silence while nothing plays.
//
//	sendspin-pipe --server ma.home:8927 | spectrum-like-reader
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"reflect"
	"runtime/pprof"
	"syscall"
	"time"
	"unsafe"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	"github.com/Sendspin/sendspin-go/pkg/sendspin"

	"github.com/jon4hz/loudest-office/visualizer/cmd/sendspin-pipe/pcmout"
)

// artworkDrain reads the album art that sendspin-go v1.8.2 asks the server for and never reads
// itself: Receiver.Connect advertises artwork@v1, nothing consumes Client.ArtworkChunks (buffer 10),
// so from the eleventh chunk on the read loop blocks on it for good: no audio, no clock sync, and
// Music Assistant drops the player as "client too slow" (goroutine dump of 2026-09-21 22:09). The
// client sits behind two unexported fields, hence reflect; a reconnect makes a new one, hence the
// poll. ponytail: the fix belongs upstream; the pointer read is unlocked and a goroutine per client
// stays behind after a reconnect.
func artworkDrain(ctx context.Context, p *sendspin.Player) {
	var last *protocol.Client
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		c := clientOf(p)
		if c == nil || c == last {
			continue
		}
		last = c
		go func(ch <-chan protocol.ArtworkChunk) {
			for range ch {
			}
		}(c.ArtworkChunks)
	}
}

// clientOf is p.receiver.client, nil before the first connection.
func clientOf(p *sendspin.Player) *protocol.Client {
	v := reflect.ValueOf(p).Elem().FieldByName("receiver")
	if !v.IsValid() || v.IsNil() {
		return nil
	}
	v = v.Elem().FieldByName("client")
	if !v.IsValid() || v.IsNil() {
		return nil
	}
	return *(**protocol.Client)(unsafe.Pointer(v.UnsafeAddr()))
}

// syncWatch is the log's writer and a watchdog: three failed sync rounds (30 s) in a row mean the
// connection is wedged and the library would sit on it silently for hours (seen 2026-09-21; the
// artwork deadlock above was the cause then). Dump the goroutines for the bug report and exit;
// whoever runs the visualizer restarts it.
type syncWatch struct {
	fails int
	exit  func()
}

func (w *syncWatch) Write(p []byte) (int, error) {
	switch {
	case bytes.Contains(p, []byte("Sync #")):
		w.fails = 0
	case bytes.Contains(p, []byte("Burst produced 0 valid samples")):
		if w.fails++; w.fails >= 3 {
			os.Stderr.Write(p)
			w.exit()
		}
	}
	return os.Stderr.Write(p)
}

func main() {
	log.SetOutput(&syncWatch{exit: func() {
		pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
		os.Exit(1)
	}})
	server := flag.String("server", "", "Sendspin server as host:port (Music Assistant: port 8927)")
	name := flag.String("name", "Visualizer", "player name")
	id := flag.String("id", "visualizer", "client id, keep it stable or the server sees a new player")
	delay := flag.Int("delay-ms", 0, "static delay, 0-5000: plays later by this much")
	flag.Parse()
	if *server == "" {
		log.Fatal("--server is required")
	}

	// A closed pipe must reach Fill as EPIPE, not kill us: the reader going away is the normal end,
	// and the player should leave the group with a clean Close.
	signal.Ignore(syscall.SIGPIPE)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := pcmout.New(os.Stdout)
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if err := out.Fill(now); err != nil { // the reader is gone
					log.Print(err)
					stop()
					return
				}
			}
		}
	}()

	p, err := sendspin.NewPlayer(sendspin.PlayerConfig{
		ServerAddr:     *server,
		PlayerName:     *name,
		ClientID:       *id,
		Volume:         100,
		StaticDelayMs:  *delay,
		PreferredCodec: "pcm",
		MaxSampleRate:  pcmout.Rate,
		MaxBitDepth:    16,
		DeviceInfo:     sendspin.DeviceInfo{ProductName: "loudest-office visualizer", Manufacturer: "jon4hz", SoftwareVersion: "1"},
		Output:         out,
		Reconnect:      sendspin.ReconnectConfig{Enabled: true},
		OnError: func(err error) {
			log.Print(err)
			if errors.Is(err, pcmout.ErrFormat) {
				os.Exit(1) // a format the pipe cannot carry: better loud than wrong
			}
			if errors.Is(err, syscall.EPIPE) { // the reader is gone; Fill's path is quiet while music flows
				stop()
			}
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()
	go artworkDrain(ctx, p)

	// Reconnect covers a lost connection, not a server that is not up yet.
	for err := p.Connect(); err != nil; err = p.Connect() {
		log.Printf("%s: %v, again in 5 s", *server, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	<-ctx.Done()
}
