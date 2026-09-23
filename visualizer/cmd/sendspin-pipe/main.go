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
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/sendspin"

	"github.com/jon4hz/loudest-office/visualizer/cmd/sendspin-pipe/pcmout"
)

// syncWatch is the log's writer and a watchdog. sendspin-go v1.8.2 can deadlock its message
// handling (seen 2026-09-21, joining a group that already played): its read loop blocks for good,
// so no audio, no clock sync, and the server hanging up goes unnoticed; Music Assistant's side
// of it is "Role queue full ... client too slow". The library's log is the only sign of it.
// Three failed sync rounds (30 s) in a row: dump the goroutines for the bug report and exit;
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
