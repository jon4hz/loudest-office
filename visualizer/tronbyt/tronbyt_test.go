package tronbyt

import (
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// server serves the nyancat fixture (12 frames of 50 ms) with a dwell time of
// 2 s and counts the requests; fail switches it to 500.
type server struct {
	*httptest.Server
	hits int
	fail bool
}

func newServer(t *testing.T) *server {
	img, err := os.ReadFile("testdata/nyancat.webp")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits++
		if s.fail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Tronbyt-Dwell-Secs", "2")
		w.Write(img)
	}))
	t.Cleanup(s.Close)
	return s
}

// run executes cmd, as the tea runtime would, and hands its message back.
func run(b *Tronbyt, cmd tea.Cmd) {
	if cmd != nil {
		b.Update(cmd())
	}
}

// ticks advances b by d in 100 ms steps, running whatever fetch it starts.
func ticks(b *Tronbyt, d time.Duration) {
	for ; d > 0; d -= 100 * time.Millisecond {
		run(b, b.Update(bubble.Tick{Dt: 100 * time.Millisecond}))
	}
}

func snapshot(frame [][]color.RGBA) string { return fmt.Sprint(frame) }

func TestTronbyt(t *testing.T) {
	s := newServer(t)
	b := New(s.URL)
	b.Update(bubble.Resize{W: 64, H: 32})
	placeholder := snapshot(b.Frame())

	cmd := b.Update(bubble.Activate{})
	if cmd == nil {
		t.Fatal("Activate did not start a fetch")
	}
	if b.Update(bubble.Activate{}) != nil || b.Update(bubble.Tick{Dt: time.Hour}) != nil {
		t.Error("a second fetch started while one was in flight")
	}
	run(b, cmd)
	ticks(b, 100*time.Millisecond)
	first := snapshot(b.Frame())
	if first == placeholder {
		t.Fatal("the frame still shows the placeholder after a good fetch")
	}
	ticks(b, 100*time.Millisecond)
	if snapshot(b.Frame()) == first {
		t.Error("the animation did not advance in 100 ms")
	}

	ticks(b, 2*time.Second)
	if s.hits != 2 {
		t.Errorf("hits = %d after the dwell time, want 2", s.hits)
	}

	s.fail = true
	ticks(b, 2*time.Second) // the dwell time runs out: a failing fetch
	if s.hits != 3 {
		t.Fatalf("hits = %d, want 3", s.hits)
	}
	ticks(b, 9*time.Second)
	if s.hits != 3 {
		t.Errorf("hits = %d: retried before 10 s", s.hits)
	}
	if got := snapshot(b.Frame()); got == placeholder || got == snapshot(bubble.NewFrame(64, 32)) {
		t.Error("an error dropped the last picture")
	}
	ticks(b, 2*time.Second)
	if s.hits != 4 {
		t.Errorf("hits = %d after the retry wait, want 4", s.hits)
	}
}
